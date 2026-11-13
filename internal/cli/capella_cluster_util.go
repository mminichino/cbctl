package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// Cluster layout rules shared by the cluster commands (port of capellactl cluster_ops.py).
const (
	// maxClusterNodes is the most nodes a cluster can have.
	maxClusterNodes = 27
	// groupLimitsText describes the service group node limits for options output.
	groupLimitsText = "first group 3-27 nodes, other groups 2-24, at most 27 per cluster, one group runs data"
	freeTierNote    = "1 node · data, index, query, search · no cost · one per organization"
	freeTierMissing = "backups, private endpoints, network peering, audit logs, alert integrations, CMEK, on/off schedule"
	freeTierBlocked = "free-tier clusters have 1 node and a fixed size"
)

var (
	supportPlans     = []string{"basic", "developer pro", "enterprise"}
	supportTimezones = []string{"ET", "GMT", "IST", "PT"}
	clusterServices  = []string{"data", "index", "query", "search", "analytics", "eventing"}
	cloudProviders   = []string{"aws", "gcp", "azure"}
	regionDefaults   = map[string]string{"aws": "us-east-1", "gcp": "us-east1", "azure": "eastus"}
	freeTierRegions  = map[string][]string{
		"aws":   {"us-east-2", "eu-west-1", "ap-southeast-1"},
		"gcp":   {"us-central1", "europe-west1", "asia-east1"},
		"azure": {"eastus", "swedencentral", "koreacentral"},
	}
)

// capellaLeaf builds a leaf command that carries the API flags. pre runs before the runtime is
// built (flag validation that needs no network); run receives the runtime and the arguments.
func capellaLeaf(use, short string, args cobra.PositionalArgs, pre func(args []string) error,
	run func(rt *runtime, args []string) error) *cobra.Command {
	var af *apiFlags
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, a []string) error {
			if pre != nil {
				if err := pre(a); err != nil {
					return err
				}
			}
			rt, err := newRuntime(cmd, af)
			if err != nil {
				return err
			}
			return run(rt, a)
		},
	}
	af = addAPIFlags(cmd)
	return cmd
}

// addProjectFlag adds the --project disambiguation flag.
func addProjectFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVar(dst, "project", "", "Project NAME; only needed when several clusters share the name")
}

// report prints v as the JSON document, or the formatted line in text mode.
func (rt *runtime) report(v any, format string, a ...any) {
	if rt.JSON {
		rt.Out.Object(v)
		return
	}
	rt.Out.Line(format, a...)
}

// errOut returns the writer for stderr messages.
func (rt *runtime) errOut() io.Writer {
	if rt.cmd != nil {
		return rt.cmd.ErrOrStderr()
	}
	return os.Stderr
}

// usageError is an invalid flag combination or value (exit 3).
func usageError(action, format string, a ...any) error {
	return Errorf(ExitConfigInvalid, action, format, a...)
}

// blockedError is a refusal caused by the resource's state (exit 4).
func blockedError(action, format string, a ...any) error {
	return Errorf(ExitBlocked, action, format, a...)
}

// conflictAsBlocked turns a 409 from Capella (the cluster is mid-operation) into an exit-4 error
// that names the state; other errors pass through.
func conflictAsBlocked(err error, name string) error {
	if errors.Is(err, capellaapi.ErrConflict) {
		return &CLIError{
			Code:    ExitBlocked,
			Message: fmt.Sprintf("Capella refused the change to cluster %s: %v", name, err),
			Action:  "wait until the cluster is healthy (see 'cbctl capella cluster get " + name + "'), then run the command again",
			Err:     err,
		}
	}
	return err
}

// isFreeCluster reports whether c is a free-tier cluster: Capella reports support plan "free".
func isFreeCluster(c *capellaapi.Cluster) bool {
	return c != nil && c.Support != nil && strings.EqualFold(c.Support.Plan, "free")
}

// freeGetter adapts the free-tier cluster endpoint to engine.ClusterGetter.
type freeGetter struct{ c *capellaapi.Client }

func (g freeGetter) GetCluster(ctx context.Context, p, id string) (*engine.ClusterWithState, error) {
	obj, _, err := g.c.GetFreeCluster(ctx, p, id)
	if err != nil {
		return nil, err
	}
	name, _ := obj["name"].(string)
	state, _ := obj["currentState"].(string)
	return &engine.ClusterWithState{Name: name, CurrentState: state}, nil
}

func (rt *runtime) clusterGetter(free bool) engine.ClusterGetter {
	if free {
		return freeGetter{c: rt.Client}
	}
	return rt.Client.AsClusterGetter()
}

// waitHooks logs state changes and prints a dot to stderr every 30s of waiting.
func (rt *runtime) waitHooks(name string) engine.WaitHooks {
	return engine.WaitHooks{
		OnState: func(state string, elapsed time.Duration) {
			logging.Info("cluster %s: %s (%s)", name, state, elapsed.Round(time.Second))
		},
		OnTick: func() { rt.Out.Progress(".") },
	}
}

// waitCluster waits for the cluster to reach op's success state with the global --timeout. On
// Ctrl-C it tells the user the operation keeps running in Capella.
func (rt *runtime) waitCluster(projectID, clusterID, name string, op engine.Op, free bool) (string, error) {
	state, err := engine.WaitForCluster(rt.Ctx, rt.clusterGetter(free), projectID, clusterID, op, rt.Timeout, rt.Clock, name, rt.waitHooks(name))
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(rt.errOut(), "the operation continues in Capella")
	}
	return state, err
}

// settle logs that Capella accepted an asynchronous change and, unless --no-wait, waits for the
// cluster to settle. It reports whether it returned without waiting.
func (rt *runtime) settle(projectID, clusterID, name, what string, op engine.Op, free bool) (bool, error) {
	logging.Info("%s accepted for cluster %s", what, name)
	if rt.NoWait {
		return true, nil
	}
	_, err := rt.waitCluster(projectID, clusterID, name, op, free)
	return false, err
}

// readCluster reads one cluster with its ETag. Free-tier clusters use the free-tier endpoint and
// are converted to the common model.
func (rt *runtime) readCluster(projectID, clusterID string, free bool) (*capellaapi.Cluster, error) {
	if !free {
		cl, err := rt.Client.GetCluster(rt.Ctx, projectID, clusterID)
		if err != nil {
			return nil, fmt.Errorf("reading cluster %s: %w", clusterID, err)
		}
		return cl, nil
	}
	obj, etag, err := rt.Client.GetFreeCluster(rt.Ctx, projectID, clusterID)
	if err != nil {
		return nil, fmt.Errorf("reading cluster %s: %w", clusterID, err)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var cl capellaapi.Cluster
	if err := json.Unmarshal(raw, &cl); err != nil {
		return nil, fmt.Errorf("decoding cluster %s: %w", clusterID, err)
	}
	cl.ETag = etag
	return &cl, nil
}

// putCluster is the read-modify-write loop for cluster updates: it reads the cluster, lets build
// produce the replacement (nil means nothing to change) and sends it with the ETag. On a 412 it
// re-reads and rebuilds once. It returns the cluster as last read and whether a write happened.
func (rt *runtime) putCluster(projectID, clusterID string,
	build func(cl *capellaapi.Cluster) (*capellaapi.UpdateClusterRequest, error)) (*capellaapi.Cluster, bool, error) {
	for attempt := 0; ; attempt++ {
		cl, err := rt.readCluster(projectID, clusterID, false)
		if err != nil {
			return nil, false, err
		}
		req, err := build(cl)
		if err != nil {
			return cl, false, err
		}
		if req == nil {
			return cl, false, nil
		}
		err = rt.Client.UpdateCluster(rt.Ctx, projectID, clusterID, *req, cl.ETag)
		if err == nil {
			return cl, true, nil
		}
		if attempt == 0 && errors.Is(err, capellaapi.ErrPreconditionFailed) {
			logging.Debug("cluster %s changed while updating; reading it again", cl.Name)
			continue
		}
		return cl, false, conflictAsBlocked(err, cl.Name)
	}
}

// baseUpdateRequest is the current cluster as a full replacement body: name, description,
// support plan and a deep copy of the service groups. Azure premium disks (anything but Ultra)
// get their size and IOPS dropped because Capella rejects them for those types.
func baseUpdateRequest(cl *capellaapi.Cluster) capellaapi.UpdateClusterRequest {
	req := capellaapi.UpdateClusterRequest{Name: cl.Name, Description: cl.Description}
	if cl.Support != nil {
		s := *cl.Support
		req.Support = &s
	}
	req.ServiceGroups = cloneGroups(cl.ServiceGroups)
	provider := clusterProvider(cl)
	for i := range req.ServiceGroups {
		n := req.ServiceGroups[i].Node
		if n != nil && n.Disk != nil && fixedSizeDisk(provider, n.Disk.Type) {
			n.Disk.Storage, n.Disk.IOPS = nil, nil
		}
	}
	return req
}

func clusterProvider(cl *capellaapi.Cluster) string {
	if cl.CloudProvider == nil {
		return ""
	}
	return cl.CloudProvider.Type
}

func clusterRegion(cl *capellaapi.Cluster) string {
	if cl.CloudProvider == nil {
		return ""
	}
	return cl.CloudProvider.Region
}

// fixedSizeDisk reports whether the disk type sets its own size and IOPS (Azure premium disks).
func fixedSizeDisk(provider, diskType string) bool {
	return provider == "azure" && diskType != "Ultra"
}

// cloneGroups deep-copies service groups.
func cloneGroups(in []capellaapi.ServiceGroup) []capellaapi.ServiceGroup {
	if in == nil {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return slices.Clone(in)
	}
	var out []capellaapi.ServiceGroup
	if err := json.Unmarshal(raw, &out); err != nil {
		return slices.Clone(in)
	}
	return out
}

// groupKey is a service group's identity: its services, sorted and comma-joined.
func groupKey(g capellaapi.ServiceGroup) string {
	s := slices.Clone(g.Services)
	sort.Strings(s)
	return strings.Join(s, ",")
}

func groupKeys(groups []capellaapi.ServiceGroup) []string {
	keys := make([]string, len(groups))
	for i, g := range groups {
		keys[i] = groupKey(g)
	}
	return keys
}

// normalizeGroupKey sorts and de-duplicates a comma-separated service list ("" stays "").
func normalizeGroupKey(text string) string {
	set := map[string]bool{}
	for _, s := range strings.Split(text, ",") {
		if s = strings.TrimSpace(s); s != "" {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// parseServices validates a comma-separated service list and returns it sorted.
func parseServices(text, where string) ([]string, error) {
	names := strings.Split(normalizeGroupKey(text), ",")
	if len(names) == 1 && names[0] == "" {
		return nil, usageError("list the services, for example data,index,query", "%s: no services", where)
	}
	for _, n := range names {
		if !slices.Contains(clusterServices, n) {
			return nil, usageError("use services from: "+strings.Join(clusterServices, ", "), "%s: unknown service %q", where, n)
		}
	}
	return names, nil
}

// selectGroups returns the indexes of the groups a command works on: all of them, the group
// whose services equal group, or (with no group) the one group that runs data.
func selectGroups(groups []capellaapi.ServiceGroup, group string, all bool) ([]int, error) {
	if all {
		idx := make([]int, len(groups))
		for i := range idx {
			idx[i] = i
		}
		return idx, nil
	}
	key := normalizeGroupKey(group)
	var chosen []int
	for i, g := range groups {
		if (key != "" && groupKey(g) == key) || (key == "" && slices.Contains(g.Services, "data")) {
			chosen = append(chosen, i)
		}
	}
	if len(chosen) != 1 {
		what := "runs data"
		if key != "" {
			what = "matches " + key
		}
		return nil, usageError("pass --group SERVICES with one of the groups listed",
			"no single service group %s (groups: %s)", what, strings.Join(groupKeys(groups), ", "))
	}
	return chosen, nil
}

// groupLimits returns the node limits of the group at index: a single-node cluster is 1..1, the
// first group 3..27 and any other 2..24. lo and hi override the defaults when above zero.
func groupLimits(index int, single bool, lo, hi int) (int, int) {
	if single {
		return 1, 1
	}
	minN, maxN := 2, 24
	if index == 0 {
		minN, maxN = 3, maxClusterNodes
	}
	if lo > 0 {
		minN = lo
	}
	if hi > 0 {
		maxN = hi
	}
	return minN, maxN
}

func totalNodes(groups []capellaapi.ServiceGroup) int {
	n := 0
	for _, g := range groups {
		n += g.NumOfNodes
	}
	return n
}

func hasService(g capellaapi.ServiceGroup, s string) bool { return slices.Contains(g.Services, s) }

// dataNodes and dataMemory are the data service's node count and node count × RAM (GB).
func dataNodes(groups []capellaapi.ServiceGroup) int {
	n := 0
	for _, g := range groups {
		if hasService(g, "data") {
			n += g.NumOfNodes
		}
	}
	return n
}

func dataMemory(groups []capellaapi.ServiceGroup) int {
	n := 0
	for _, g := range groups {
		if hasService(g, "data") && g.Node != nil && g.Node.Compute != nil {
			n += g.NumOfNodes * g.Node.Compute.RAM
		}
	}
	return n
}

// checkCapacity guards changes that lower the data service's capacity (port of capellactl's
// scale-in guard): bucket memory must fit the projected data memory and no bucket may have more
// replicas than the remaining data nodes allow. Growth needs no check. When the cluster stats
// cannot be read the change is refused unless skip is set.
func (rt *runtime) checkCapacity(projectID, clusterID string, old, updated []capellaapi.ServiceGroup, skip bool) error {
	n, m := dataNodes(old), dataNodes(updated)
	if dataMemory(updated) >= dataMemory(old) && m >= n {
		logging.Debug("capacity check skipped: the data service does not shrink")
		return nil
	}
	stats, err := rt.Client.GetClusterStats(rt.Ctx, projectID, clusterID)
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		return err
	case err != nil && !skip:
		return blockedError("pass --skip-capacity-check to proceed at your own risk",
			"cannot verify capacity (cluster stats unavailable): %v", err)
	case err != nil:
		logging.Debug("memory not checked (--skip-capacity-check): %v", err)
	default:
		allocated := float64(stats.TotalMemoryInMb - stats.FreeMemoryInMb)
		projected := float64(stats.TotalMemoryInMb) * float64(dataMemory(updated)) / float64(max(dataMemory(old), 1))
		if allocated > projected {
			return blockedError("remove buckets or choose a smaller change",
				"scale-in blocked: buckets allocate %.0f MB but the data service would have ~%.0f MB afterwards (%d -> %d data nodes)",
				allocated, projected, n, m)
		}
	}
	buckets, err := rt.Client.ListBuckets(rt.Ctx, projectID, clusterID)
	if err != nil {
		return fmt.Errorf("listing buckets: %w", err)
	}
	for _, b := range buckets {
		if b.Replicas > m-1 {
			return blockedError("lower the bucket's replicas first or keep more data nodes",
				"scale-in blocked: bucket %s has %d replicas and needs at least %d data nodes", b.Name, b.Replicas, b.Replicas+1)
		}
	}
	return nil
}

// ---- disks (port of cluster_ops.make_disk) -------------------------------------------------

// defaultDisk is the disk Capella gives a new group of each provider.
func defaultDisk(provider string) *capellaapi.Disk {
	switch provider {
	case "gcp":
		return &capellaapi.Disk{Type: "pd-ssd", Storage: intPtr(50)}
	case "azure":
		return &capellaapi.Disk{Type: "P6"}
	default:
		return &capellaapi.Disk{Type: "gp3", Storage: intPtr(50), IOPS: intPtr(3000)}
	}
}

func intPtr(n int) *int { return &n }

// makeDisk builds a group's disk: the provider default with the given values on top. Without
// IOPS, gp3, io2 and Ultra disks get Capella's recommended IOPS for their size.
func makeDisk(provider, diskType string, size, iops *int, autoExpansion *bool) *capellaapi.Disk {
	disk := defaultDisk(provider)
	if diskType != "" {
		disk = &capellaapi.Disk{Type: diskType}
		if !fixedSizeDisk(provider, diskType) {
			disk.Storage = intPtr(smallestDiskSize(provider, diskType))
		}
	}
	if size != nil {
		disk.Storage = intPtr(*size)
	}
	switch {
	case iops != nil:
		disk.IOPS = intPtr(*iops)
	case disk.Storage != nil:
		if rec := recommendedIOPS(provider, disk.Type, *disk.Storage); rec > 0 {
			disk.IOPS = intPtr(rec)
		} else {
			disk.IOPS = nil
		}
	}
	if autoExpansion != nil {
		disk.AutoExpansion = autoExpansion
	}
	return disk
}

func smallestDiskSize(provider, diskType string) int {
	d, ok := presetDisk(provider, diskType)
	switch {
	case ok && len(d.Sizes) > 0:
		return d.Sizes[0]
	case ok && len(d.Size) > 0:
		return d.Size[0]
	}
	return 50
}

func recommendedIOPS(provider, diskType string, size int) int {
	if d, ok := presetDisk(provider, diskType); ok {
		return d.DefaultIOPS(size)
	}
	return 0
}

// ---- CIDR helpers (port of suggest_cidr / _overlap) ----------------------------------------

type usedCIDR struct{ cluster, cidr string }

// suggestCIDR returns the first 10.N.M.0/24, in address order, that overlaps none of used.
func suggestCIDR(used []string) string {
	var nets []netip.Prefix
	for _, c := range used {
		if p, err := netip.ParsePrefix(strings.TrimSpace(c)); err == nil {
			nets = append(nets, p)
		}
	}
	for n := 0; n < 256; n++ {
		for m := 0; m < 256; m++ {
			cand := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(n), byte(m), 0}), 24)
			if !slices.ContainsFunc(nets, cand.Overlaps) {
				return cand.String()
			}
		}
	}
	return "10.255.255.0/24"
}

// overlapOf returns the first used network that overlaps cidr, or false.
func overlapOf(cidr string, used []usedCIDR) (usedCIDR, bool) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return usedCIDR{}, false
	}
	for _, u := range used {
		if o, err := netip.ParsePrefix(strings.TrimSpace(u.cidr)); err == nil && p.Overlaps(o) {
			return u, true
		}
	}
	return usedCIDR{}, false
}

// ---- confirmation with a typed answer ------------------------------------------------------

// confirmTyped asks the user to type want to confirm a destructive operation. --yes skips it; with
// no terminal and no --yes it fails with ExitBlocked.
func confirmTyped(rt *runtime, question, want string) (bool, error) {
	if rt.Yes {
		return true, nil
	}
	in, errOut := io.Reader(os.Stdin), io.Writer(os.Stderr)
	if rt.cmd != nil {
		in, errOut = rt.cmd.InOrStdin(), rt.cmd.ErrOrStderr()
	}
	if !ttyCheck(in) {
		return false, blockedError("re-run with --yes", "confirmation required but no terminal is attached")
	}
	fmt.Fprintf(errOut, "%s (%s): ", question, want)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return strings.TrimSpace(line) == want, nil
}

// cancelled is the common reply when the user declines a prompt.
func (rt *runtime) cancelled(v any) {
	logging.Info("cancelled; nothing was changed")
	if rt.JSON {
		rt.Out.Object(v)
	}
}
