package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/presets"
	"github.com/spf13/cobra"
)

// scaleResult is the outcome for one service group of cluster scale.
type scaleResult struct {
	Cluster   string `json:"cluster"`
	ClusterID string `json:"-"` // not in the output: scale.schema.json (capellactl's shape) has no id
	Group     string `json:"group"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	Status    string `json:"status"` // scaled | at_limit | unchanged | accepted | blocked | cancelled

	limit int
	step  int
}

type scaleOptions struct {
	project, group string
	nodes, step    int
	allGroups      bool
	min, max       int
	skipCapacity   bool
}

const scaleLong = `Change the node count of one service group (default: the group that runs data).

--nodes sets an absolute count; --step +1 or -1 adds or removes one node and is a successful
no-op ("at_limit") when the group is already at its limit. Limits: the first group 3-27 nodes,
other groups 2-24, a single-node cluster exactly 1 (override with --min/--max).

Examples:
  cbctl capella cluster scale prod-01 --nodes 6
  cbctl capella cluster scale prod-01 --step +1 --group index,query
  cbctl capella cluster scale prod-01 --step -1 --all-groups --no-wait`

func newCapellaClusterScaleCmd() *cobra.Command {
	var o scaleOptions
	var cmd *cobra.Command
	cmd = capellaLeaf("scale CLUSTER", "Change a service group's node count (--nodes N or --step +1|-1)", cobra.ExactArgs(1),
		func([]string) error {
			nodes, step := cmd.Flags().Changed("nodes"), cmd.Flags().Changed("step")
			switch {
			case nodes && step:
				return usageError("use either --nodes or --step", "--nodes and --step are mutually exclusive")
			case !nodes && !step:
				return usageError("pass --nodes N or --step +1|-1", "nothing to do: neither --nodes nor --step was given")
			case nodes && o.nodes < 1:
				return usageError("pass a node count of at least 1", "--nodes must be at least 1")
			case step && o.step != 1 && o.step != -1:
				return usageError("use --step +1 or --step -1", "--step must be +1 or -1, got %d", o.step)
			}
			return nil
		},
		func(rt *runtime, args []string) error { return runClusterScale(rt, args[0], o) })
	cmd.Long = scaleLong
	addProjectFlag(cmd, &o.project)
	cmd.Flags().IntVar(&o.nodes, "nodes", 0, "Set the group to exactly N nodes")
	cmd.Flags().IntVar(&o.step, "step", 0, "Add (+1) or remove (-1) one node")
	cmd.Flags().StringVar(&o.group, "group", "", "Service group to scale, as its services (for example index,query); default: the group running data")
	cmd.Flags().BoolVar(&o.allGroups, "all-groups", false, "Scale every service group")
	cmd.Flags().IntVar(&o.min, "min", 0, "Lowest node count --step may reach (default: the group's limit)")
	cmd.Flags().IntVar(&o.max, "max", 0, "Highest node count --step may reach (default: the group's limit)")
	cmd.Flags().BoolVar(&o.skipCapacity, "skip-capacity-check", false, "Scale in even when capacity cannot be verified")
	wrapManaged(cmd, addManagedFlags(cmd), managedClusterScale(&o))
	return cmd
}

func runClusterScale(rt *runtime, name string, o scaleOptions) error {
	p, listed, err := rt.resolveCluster(name, o.project)
	if err != nil {
		return err
	}
	if isFreeCluster(listed) {
		return blockedError("create a provisioned cluster for that ('cbctl capella cluster create')", "%s", freeTierBlocked)
	}
	if engine.Transitional[listed.CurrentState] {
		if rt.NoWait {
			return blockedError("wait until it is healthy (see 'cbctl capella cluster get "+listed.Name+"'), then run the command again",
				"cluster %s is %s", listed.Name, listed.CurrentState)
		}
		logging.Info("cluster %s is %s; waiting for it before scaling", listed.Name, listed.CurrentState)
		if _, err := rt.waitCluster(p.ID, listed.ID, listed.Name, engine.OpReady, false); err != nil {
			return err
		}
	}

	var results []scaleResult
	_, wrote, err := rt.putCluster(p.ID, listed.ID, func(cl *capellaapi.Cluster) (*capellaapi.UpdateClusterRequest, error) {
		req := baseUpdateRequest(cl)
		chosen, err := selectGroups(cl.ServiceGroups, o.group, o.allGroups)
		if err != nil {
			return nil, err
		}
		single := cl.ConfigurationType == "singleNode"
		results = results[:0]
		moving := false
		for _, i := range chosen {
			g := cl.ServiceGroups[i]
			lo, hi := groupLimits(i, single, o.min, o.max)
			r := scaleResult{Cluster: cl.Name, ClusterID: cl.ID, Group: groupKey(g), From: g.NumOfNodes, To: g.NumOfNodes, step: o.step}
			target := o.nodes
			if o.step != 0 {
				target = g.NumOfNodes + o.step
			}
			switch {
			case o.step != 0 && (target > hi || target < lo):
				r.Status, r.limit = "at_limit", hi
				if o.step < 0 {
					r.limit = lo
				}
			case o.step == 0 && (target < lo || target > hi):
				return nil, usageError(fmt.Sprintf("choose %d-%d nodes for group %s", lo, hi, r.Group),
					"--nodes %d is outside the limits of group %s (%d-%d)", target, r.Group, lo, hi)
			case target == g.NumOfNodes:
				r.Status = "unchanged"
			default:
				r.Status, r.To = "scaled", target
				req.ServiceGroups[i].NumOfNodes = target
				moving = true
			}
			results = append(results, r)
		}
		if !moving {
			return nil, nil
		}
		if err := rt.checkCapacity(p.ID, cl.ID, cl.ServiceGroups, req.ServiceGroups, o.skipCapacity); err != nil {
			return nil, err
		}
		return &req, nil
	})
	if err != nil {
		var ce *CLIError
		if o.step != 0 && errors.As(err, &ce) && ce.Code == ExitBlocked && len(results) > 0 {
			rt.printScale(markScale(results, "blocked"))
		}
		return err
	}
	if wrote {
		if noWait, err := rt.settle(p.ID, listed.ID, listed.Name, "scale", engine.OpUpdate, false); err != nil {
			return err
		} else if noWait {
			for i := range results {
				if results[i].Status == "scaled" {
					results[i].Status = "accepted"
				}
			}
		}
	}
	rt.printScale(results)
	return nil
}

// markScale gives every result that was about to move the status (blocked or cancelled) and
// resets its target, as capellactl reports a step that did not happen.
func markScale(results []scaleResult, status string) []scaleResult {
	for i := range results {
		if results[i].Status == "scaled" {
			results[i].Status, results[i].To = status, results[i].From
		}
	}
	return results
}

func (rt *runtime) printScale(results []scaleResult) {
	if rt.JSON {
		if len(results) == 1 {
			rt.Out.Object(results[0])
		} else {
			rt.Out.List(results)
		}
		return
	}
	for _, r := range results {
		switch r.Status {
		case "scaled":
			rt.Out.Line("scaled %s: %d → %d", r.Group, r.From, r.To)
		case "accepted":
			rt.Out.Line("scaling %s: %d → %d (--no-wait)", r.Group, r.From, r.To)
		case "at_limit":
			word := "maximum"
			if r.step < 0 {
				word = "minimum"
			}
			rt.Out.Line("%s: already at %s (%d)", r.Group, word, r.limit)
		case "blocked", "cancelled":
			// The error or the cancel message already says what happened.
		default:
			rt.Out.Line("%s: already at %d nodes", r.Group, r.From)
		}
	}
}

// ---- update ----------------------------------------------------------------------------------

type updateOptions struct {
	project, description, plan, timezone, group string
	cpu, ram, diskSize, diskIOPS                int
	diskType                                    string
	autoExpansion                               bool
}

// updateFlags lists the flags that count as "something to change".
var updateFlags = []string{"description", "support-plan", "support-timezone", "cpu", "ram", "disk-type", "disk-size", "disk-iops", "disk-auto-expansion"}

const updateLong = `Change a cluster's description, support plan, CPU/RAM or disk. The compute and disk flags
apply to one service group (--group, default: the group running data). Free-tier clusters only
accept --description.

Examples:
  cbctl capella cluster update dev-01 --support-plan enterprise
  cbctl capella cluster update dev-01 --cpu 8 --ram 32
  cbctl capella cluster update prod-01 --group data --disk-type io2 --disk-iops 20000`

type clusterUpdateResult struct {
	Cluster   string   `json:"cluster"`
	ClusterID string   `json:"clusterId"`
	Status    string   `json:"status"` // updated | accepted | unchanged
	Changes   []string `json:"changes"`
}

func newCapellaClusterUpdateCmd() *cobra.Command {
	var o updateOptions
	var cmd *cobra.Command
	cmd = capellaLeaf("update CLUSTER", "Change description, support plan, compute or disk", cobra.ExactArgs(1),
		func([]string) error {
			if !slices.ContainsFunc(updateFlags, cmd.Flags().Changed) {
				return usageError("add what to change, for example --support-plan, --cpu, --ram or --disk-size", "nothing to update: no option was given")
			}
			if cmd.Flags().Changed("support-plan") {
				o.plan = strings.ToLower(strings.TrimSpace(o.plan))
				if !slices.Contains(supportPlans, o.plan) {
					return usageError("use one of "+strings.Join(supportPlans, ", "), "unknown support plan %q", o.plan)
				}
			}
			if cmd.Flags().Changed("support-timezone") {
				o.timezone = strings.ToUpper(strings.TrimSpace(o.timezone))
				if !slices.Contains(supportTimezones, o.timezone) {
					return usageError("use one of "+strings.Join(supportTimezones, ", "), "unknown support timezone %q", o.timezone)
				}
			}
			return nil
		},
		func(rt *runtime, args []string) error { return runClusterUpdate(rt, args[0], o) })
	cmd.Long = updateLong
	addProjectFlag(cmd, &o.project)
	cmd.Flags().StringVar(&o.description, "description", "", "New description")
	cmd.Flags().StringVar(&o.plan, "support-plan", "", "Support plan: basic, developer pro, enterprise")
	cmd.Flags().StringVar(&o.timezone, "support-timezone", "", "Support timezone: ET, PT, GMT, IST")
	cmd.Flags().IntVar(&o.cpu, "cpu", 0, "vCPUs per node of the group")
	cmd.Flags().IntVar(&o.ram, "ram", 0, "RAM GB per node of the group")
	cmd.Flags().StringVar(&o.diskType, "disk-type", "", "Disk type of the group (see 'cbctl capella cluster disk-types')")
	cmd.Flags().IntVar(&o.diskSize, "disk-size", 0, "Disk GB per node of the group (cannot shrink)")
	cmd.Flags().IntVar(&o.diskIOPS, "disk-iops", 0, "Disk IOPS of the group (AWS gp3/io2, Azure Ultra)")
	cmd.Flags().BoolVar(&o.autoExpansion, "disk-auto-expansion", false, "Azure: let the disk storage expand automatically (--disk-auto-expansion=false to turn off)")
	cmd.Flags().StringVar(&o.group, "group", "", "Service group to change, as its services; default: the group running data")
	return cmd
}

func runClusterUpdate(rt *runtime, name string, o updateOptions) error {
	changed := rt.cmd.Flags().Changed
	p, listed, err := rt.resolveCluster(name, o.project)
	if err != nil {
		return err
	}
	if isFreeCluster(listed) {
		for _, f := range updateFlags[1:] {
			if changed(f) {
				return blockedError("create a provisioned cluster for that ('cbctl capella cluster create')", "%s", freeTierBlocked)
			}
		}
		return updateFreeCluster(rt, p, listed, o.description)
	}

	var changes []string
	_, wrote, err := rt.putCluster(p.ID, listed.ID, func(cl *capellaapi.Cluster) (*capellaapi.UpdateClusterRequest, error) {
		changes = changes[:0]
		req := baseUpdateRequest(cl)
		before, _ := json.Marshal(req)
		if changed("description") && o.description != cl.Description {
			req.Description = o.description
			changes = append(changes, "description")
		}
		if changed("support-plan") || changed("support-timezone") {
			s := capellaapi.Support{}
			if req.Support != nil {
				s = *req.Support
			}
			if changed("support-plan") {
				s.Plan = o.plan
			}
			if changed("support-timezone") {
				s.Timezone = o.timezone
			}
			req.Support = &s
		}
		if slices.ContainsFunc([]string{"cpu", "ram", "disk-type", "disk-size", "disk-iops", "disk-auto-expansion"}, changed) {
			idx, err := selectGroups(req.ServiceGroups, o.group, false)
			if err != nil {
				return nil, err
			}
			if err := applyGroupChanges(rt, cl, &req.ServiceGroups[idx[0]], o); err != nil {
				return nil, err
			}
		}
		after, _ := json.Marshal(req)
		if string(before) == string(after) {
			return nil, nil
		}
		// Describe the difference at the granularity of the flags that were given.
		for _, f := range updateFlags[1:] {
			if changed(f) {
				changes = append(changes, strings.TrimPrefix(f, "support-"))
			}
		}
		return &req, nil
	})
	if err != nil {
		return err
	}
	res := clusterUpdateResult{Cluster: listed.Name, ClusterID: listed.ID, Status: "unchanged", Changes: nonNil(changes)}
	if !wrote {
		rt.report(res, "cluster %s already matches; nothing to update", listed.Name)
		return nil
	}
	noWait, err := rt.settle(p.ID, listed.ID, listed.Name, "update", engine.OpUpdate, false)
	if err != nil {
		return err
	}
	res.Status = "updated"
	if noWait {
		res.Status = "accepted"
	}
	rt.report(res, "cluster %s %s (%s)", listed.Name, res.Status, strings.Join(res.Changes, ", "))
	return nil
}

// applyGroupChanges applies the compute and disk flags to one service group (port of
// cluster_ops.cluster_update and _change_disk_type).
func applyGroupChanges(rt *runtime, cl *capellaapi.Cluster, g *capellaapi.ServiceGroup, o updateOptions) error {
	changed := rt.cmd.Flags().Changed
	provider := clusterProvider(cl)
	if g.Node == nil {
		g.Node = &capellaapi.NodeConfig{}
	}
	if g.Node.Compute == nil {
		g.Node.Compute = &capellaapi.Compute{}
	}
	if g.Node.Disk == nil {
		g.Node.Disk = defaultDisk(provider)
	}
	disk := g.Node.Disk

	if changed("cpu") {
		g.Node.Compute.CPU = o.cpu
	}
	if changed("ram") {
		g.Node.Compute.RAM = o.ram
	}
	if changed("cpu") || changed("ram") {
		c := g.Node.Compute
		if offered, ok := presets.ComputeSizes()[provider]; ok && !offered.Offered(presets.Size{CPU: c.CPU, RAM: c.RAM}, cl.ConfigurationType == "singleNode") {
			logging.Warning("%d vCPU/%d GB is not a known %s size; Capella may reject it (see 'cbctl capella cluster options')", c.CPU, c.RAM, provider)
		}
	}

	if changed("disk-auto-expansion") && provider != "azure" {
		return usageError("remove --disk-auto-expansion: AWS and Google Cloud disks always grow automatically", "auto-expansion is for Azure disks only")
	}
	if changed("disk-iops") && provider == "gcp" {
		return usageError("remove --disk-iops and use --disk-size to make the disk faster", "Google Cloud pd-ssd disks get their IOPS from their size")
	}
	newType := disk.Type
	if changed("disk-type") {
		newType = o.diskType
		if types, ok := presets.DiskTypes()[provider]; ok {
			if _, known := types[newType]; !known {
				return usageError("run 'cbctl capella cluster disk-types --provider "+provider+"'", "unknown %s disk type %q", provider, newType)
			}
		}
	}
	if fixedSizeDisk(provider, newType) && (changed("disk-size") || changed("disk-iops")) {
		return usageError("choose a larger type with --disk-type (for example P20), or Ultra to set size and IOPS yourself",
			"azure disk type %s has a size and IOPS set by the type", newType)
	}

	var size, iops *int
	if changed("disk-size") {
		size = intPtr(o.diskSize)
	}
	if changed("disk-iops") {
		iops = intPtr(o.diskIOPS)
	}
	var auto *bool
	if changed("disk-auto-expansion") {
		auto = &o.autoExpansion
	}
	if changed("disk-type") && newType != disk.Type {
		// A new type keeps the size where the type allows it; IOPS become the recommendation.
		if size == nil && disk.Storage != nil && !fixedSizeDisk(provider, newType) {
			keep := *disk.Storage
			if d, ok := presetDisk(provider, newType); ok && len(d.Sizes) > 0 && !slices.Contains(d.Sizes, keep) {
				keep = 0
			}
			if keep > 0 {
				size = &keep
			}
		}
		fresh := makeDisk(provider, newType, size, iops, auto)
		if auto == nil {
			fresh.AutoExpansion = disk.AutoExpansion
		}
		g.Node.Disk = fresh
		return nil
	}
	if size != nil {
		disk.Storage = size
	}
	if iops != nil {
		disk.IOPS = iops
	}
	if auto != nil {
		disk.AutoExpansion = auto
	}
	return nil
}

// updateFreeCluster changes a free-tier cluster's description (the only field Capella accepts).
func updateFreeCluster(rt *runtime, p *capellaapi.Project, listed *capellaapi.Cluster, description string) error {
	if !rt.cmd.Flags().Changed("description") {
		return usageError("add --description TEXT", "nothing to update: free-tier clusters only allow --description")
	}
	for attempt := 0; ; attempt++ {
		obj, etag, err := rt.Client.GetFreeCluster(rt.Ctx, p.ID, listed.ID)
		if err != nil {
			return fmt.Errorf("reading cluster %s: %w", listed.Name, err)
		}
		if cur, _ := obj["description"].(string); cur == description {
			rt.report(clusterUpdateResult{Cluster: listed.Name, ClusterID: listed.ID, Status: "unchanged", Changes: []string{}},
				"cluster %s already matches; nothing to update", listed.Name)
			return nil
		}
		err = rt.Client.UpdateFreeCluster(rt.Ctx, p.ID, listed.ID, capellaapi.Object{"name": listed.Name, "description": description}, etag)
		if err == nil {
			break
		}
		if attempt == 0 && isPreconditionFailed(err) {
			continue
		}
		return err
	}
	rt.report(clusterUpdateResult{Cluster: listed.Name, ClusterID: listed.ID, Status: "updated", Changes: []string{"description"}},
		"cluster %s updated (description)", listed.Name)
	return nil
}
