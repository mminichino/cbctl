package resources

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/plan"
)

const kindCluster = "cluster"

// CreateFields are the file fields sent when a cluster is created.
var CreateFields = []string{
	"name", "description", "configurationType", "cloudProvider", "couchbaseServer", "serviceGroups",
	"availability", "support", "zones", "cmekId", "enablePrivateDNSResolution",
}

// Immutable are the cluster fields that cannot change once the cluster exists.
var Immutable = []string{
	"configurationType", "cloudProvider", "availability", "zones", "cmekId", "enablePrivateDNSResolution", "couchbaseServer",
}

// NotAPI are the cluster-level keys of the file that are not part of the cluster API body.
var NotAPI = map[string]bool{
	"id": true, "tier": true, "deletionProtection": true, "allowAnyIp": true, "allowedCidrs": true,
	"hibernation": true, "backupSchedule": true, "buckets": true, "databaseCredentials": true,
}

// OffStates are the cluster states that mean the cluster is (becoming) turned off.
var OffStates = map[string]bool{"turnedOff": true, "turningOff": true}

// Failed are the cluster states that need a human before anything else changes.
var Failed = map[string]bool{
	"deploymentFailed": true, "scaleFailed": true, "upgradeFailed": true, "rebalanceFailed": true,
	"peeringFailed": true, "destroyFailed": true, "turningOffFailed": true, "turningOnFailed": true,
}

// FreeServices is the service set of every free-tier cluster.
const FreeServices = "data,index,query,search"

// SettingsOnlyNote explains the cluster update job a settings-only change starts. Capella has no
// lighter call for description or support changes: the full cluster PUT starts a job that the
// Capella UI lists as "scale".
const SettingsOnlyNote = `note: Capella runs a cluster update job for this (shown as "scale" in the Capella UI); nodes do not change`

var groupFields = []string{"node", "numOfNodes", "services"}

// IsFree reports whether a cluster body is a free-tier cluster: Capella reports support.plan "free".
func IsFree(body Obj) bool {
	return strings.ToLower(strOf(objOf(body["support"]), "plan")) == "free"
}

func groupSize(g Obj) string {
	node := objOf(g["node"])
	comp, disk := objOf(node["compute"]), objOf(node["disk"])
	s := fmt.Sprintf("%s × %s vCPU/%s GB", pyStr(g["numOfNodes"]), pyStr(comp["cpu"]), pyStr(comp["ram"]))
	if truthy(disk["type"]) {
		s += " " + pyStr(disk["type"])
	}
	if truthy(disk["storage"]) {
		s += " " + pyStr(disk["storage"]) + " GB"
	}
	return s
}

// CreateSummary is the one-line description of a cluster about to be created.
func CreateSummary(d Obj) string {
	cp := objOf(d["cloudProvider"])
	if d["tier"] == "free" {
		return fmt.Sprintf("free tier · %s %s · 1 node · %s", pyStr(cp["type"]), pyStr(cp["region"]), FreeServices)
	}
	parts := []string{fmt.Sprintf("%s %s", pyStr(cp["type"]), pyStr(cp["region"]))}
	if objOf(d["availability"])["type"] == "multi" {
		parts = append(parts, "multi-AZ")
	} else {
		parts = append(parts, "single-AZ")
	}
	if d["configurationType"] == "singleNode" {
		parts = append(parts, "single node")
	}
	groups := objsOf(d["serviceGroups"])
	if len(groups) > 1 {
		var nodes int64
		for _, g := range groups {
			nodes += intOf(g["numOfNodes"])
		}
		parts = append(parts, fmt.Sprintf("%d service groups · %d nodes", len(groups), nodes))
	}
	if len(groups) == 1 {
		g := groups[0]
		comp := objOf(objOf(g["node"])["compute"])
		parts = append(parts, fmt.Sprintf("%s × %s vCPU/%s GB", pyStr(g["numOfNodes"]), pyStr(comp["cpu"]), pyStr(comp["ram"])))
		services := servicesOf(g)
		sorted := append([]string(nil), services...)
		sort.Strings(sorted)
		parts = append(parts, strings.Join(sorted, ","))
	}
	parts = append(parts, strings.ToLower(pyPlain(objOf(d["support"])["plan"])))
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// pyPlain is fmt.Sprint with an empty string for a missing value.
func pyPlain(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// CreateGroupLines are the plan lines under a new multi-dimensional cluster: each service group
// expanded with its disk.
func CreateGroupLines(d Obj) []string {
	groups := objsOf(d["serviceGroups"])
	if len(groups) < 2 {
		return nil
	}
	width := 0
	for _, g := range groups {
		width = max(width, len(plan.GroupKey(g)))
	}
	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		k := plan.GroupKey(g)
		lines = append(lines, fmt.Sprintf("+ serviceGroups[%s]%s  %s", k, strings.Repeat(" ", width-len(k)), groupSize(g)))
	}
	return lines
}

func versionMatches(desired, observed string) bool {
	if desired == "" {
		return true
	}
	return observed != "" && (observed == desired || strings.HasPrefix(observed, desired+"."))
}

// FixedSizeDisk reports whether the disk type has a size and IOPS fixed by the type. Azure premium
// disks (P6 to P60) do; Capella refuses storage and iops for them. Only Ultra takes them.
func FixedSizeDisk(provider, diskType any) bool {
	return provider == "azure" && diskType != "Ultra"
}

// APIGroup keeps the fields of a service group the API takes (node, numOfNodes, services), and
// drops storage and iops from the fixed-size Azure disks.
func APIGroup(g Obj, provider any) Obj {
	out := Obj{}
	for _, k := range groupFields {
		if v, ok := g[k]; ok {
			out[k] = v
		}
	}
	node := objOf(out["node"])
	if disk := objOf(node["disk"]); disk != nil && FixedSizeDisk(provider, disk["type"]) {
		trimmed := Obj{}
		for k, v := range disk {
			if k != "storage" && k != "iops" {
				trimmed[k] = v
			}
		}
		n := copyObj(node)
		n["disk"] = trimmed
		out["node"] = n
	}
	return out
}

// BuildUpdateBody is the full PUT body: observed overlaid with desired. Service groups are
// matched by their service set; observed groups the desired state omits are kept when it has no
// serviceGroups key and dropped when it does; new desired groups are appended.
func BuildUpdateBody(observed, desired Obj, rename bool) Obj {
	desiredGroups := map[string]Obj{}
	var desiredOrder []string
	for _, g := range objsOf(desired["serviceGroups"]) {
		k := plan.GroupKey(g)
		if _, seen := desiredGroups[k]; !seen {
			desiredOrder = append(desiredOrder, k)
		}
		desiredGroups[k] = g
	}
	provider := objOf(observed["cloudProvider"])["type"]
	if !truthy(observed["cloudProvider"]) {
		provider = objOf(desired["cloudProvider"])["type"]
	}
	groups := []any{}
	seen := map[string]bool{}
	_, hasDesiredGroups := desired["serviceGroups"]
	hasDesiredGroups = hasDesiredGroups && desired["serviceGroups"] != nil
	for _, og := range objsOf(observed["serviceGroups"]) {
		k := plan.GroupKey(og)
		seen[k] = true
		if dg, ok := desiredGroups[k]; ok {
			merged, _ := plan.DeepMerge(APIGroup(og, nil), APIGroup(dg, nil)).(Obj)
			groups = append(groups, APIGroup(merged, provider))
		} else if !hasDesiredGroups {
			groups = append(groups, APIGroup(og, provider))
		}
	}
	for _, k := range desiredOrder {
		if !seen[k] {
			groups = append(groups, APIGroup(desiredGroups[k], provider))
		}
	}
	name := observed["name"]
	if rename || name == nil {
		name = desired["name"]
	}
	description := orEmpty(observed["description"])
	if has(desired, "description") {
		description = desired["description"]
	}
	return Obj{
		"name":          name,
		"description":   description,
		"support":       plan.DeepMerge(orObj(observed["support"]), orObj(desired["support"])),
		"serviceGroups": groups,
	}
}

func orObj(v any) any {
	if o := objOf(v); o != nil {
		return o
	}
	return Obj{}
}

// ServiceGroupChanges compares the declared service groups with the observed ones. It returns the
// changes, the reasons a change is blocked, and the labels ("scale out", "scale up", "scale in",
// "scale down") describing it.
func ServiceGroupChanges(desired, observed Obj) (changes []plan.Change, blocked []string, labels map[string]bool) {
	labels = map[string]bool{}
	if !has(desired, "serviceGroups") {
		return nil, nil, labels
	}
	dGroups, oGroups := map[string]Obj{}, map[string]Obj{}
	var dOrder, oOrder []string
	for _, g := range objsOf(desired["serviceGroups"]) {
		k := plan.GroupKey(g)
		if _, ok := dGroups[k]; !ok {
			dOrder = append(dOrder, k)
		}
		dGroups[k] = g
	}
	for _, g := range objsOf(observed["serviceGroups"]) {
		k := plan.GroupKey(g)
		if _, ok := oGroups[k]; !ok {
			oOrder = append(oOrder, k)
		}
		oGroups[k] = g
	}
	for _, k := range dOrder {
		dg := dGroups[k]
		og, ok := oGroups[k]
		if !ok {
			changes = append(changes, plan.Change{Path: fmt.Sprintf("serviceGroups[%s]", k), Before: nil, After: groupSize(dg)})
			labels["scale out"] = true
			continue
		}
		for _, c := range plan.FieldChanges(APIGroup(dg, nil), og, fmt.Sprintf("serviceGroups[%s]", k), map[string]bool{"services": true}) {
			changes = append(changes, c)
			before, bok := plan.AsInt(c.Before)
			after, aok := plan.AsInt(c.After)
			shrinks := bok && aok && after < before
			switch {
			case strings.HasSuffix(c.Path, "numOfNodes"):
				if shrinks {
					labels["scale in"] = true
				} else {
					labels["scale out"] = true
				}
			case strings.HasSuffix(c.Path, "disk.storage") && shrinks:
				blocked = append(blocked, fmt.Sprintf("%s: %d → %d GB  disk size cannot be reduced", c.Path, before, after))
			case strings.HasSuffix(c.Path, "compute.ram") && shrinks:
				labels["scale down"] = true
			default:
				labels["scale up"] = true
			}
		}
	}
	for _, k := range oOrder {
		if _, ok := dGroups[k]; !ok {
			changes = append(changes, plan.Change{Path: fmt.Sprintf("serviceGroups[%s]", k), Before: groupSize(oGroups[k]), After: nil})
			labels["scale in"] = true
		}
	}
	return changes, blocked, labels
}

// clusterRec reconciles clusters, including free-tier ones (Data["free"]).
type clusterRec struct {
	plan.Base
	seen map[string][]plan.Observed
}

func newCluster(children ...plan.Reconciler) *clusterRec {
	r := &clusterRec{
		Base: plan.NewBase(kindCluster, plan.Flags{ListedWhenAsked: true}, children...),
		seen: map[string][]plan.Observed{},
	}
	r.Bind(r)
	return r
}

func (r *clusterRec) DesiredItems(parent any) []any {
	var items []any
	for _, c := range objsOf(objOf(parent)["_clusters"]) {
		items = append(items, c)
	}
	return items
}

func (r *clusterRec) Key(desired any, _ *plan.Action) string {
	return fmt.Sprint(objOf(desired)["name"])
}

func (r *clusterRec) AfterChildrenNote() string { return "(after cluster is healthy)" }

func (r *clusterRec) UnmanagedLabel(o plan.Observed, _ *plan.Action) string {
	name := strOf(o.Body, "name")
	if name == "" {
		name = o.ID
	}
	return "cluster " + name
}

func (r *clusterRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	if parent == nil || parent.ID() == "" {
		return nil, errors.New("cluster: the project has no id yet")
	}
	api, err := newRawAPI(rc)
	if err != nil {
		return nil, err
	}
	list, err := api.list(ctx, api.clusters(parent.ID()), "data")
	if err != nil {
		return nil, err
	}
	found := make([]plan.Observed, 0, len(list))
	listed := map[string]bool{}
	for _, c := range list {
		found = append(found, plan.Observed{ID: strOf(c, "id"), Body: c})
		listed[strOf(c, "id")] = true
	}
	// Free-tier clusters are recorded with their id; read any that the list does not include.
	for _, d := range objsOf(objOf(parent.Desired)["_clusters"]) {
		id := strOf(d, "id")
		if d["tier"] != "free" || id == "" || listed[id] {
			continue
		}
		body, _, err := api.get(ctx, api.freeCluster(parent.ID(), id))
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		found = append(found, plan.Observed{ID: id, Body: body})
	}
	r.seen[parent.ID()] = found
	return found, nil
}

// renameHint is the warning for a new cluster name next to exactly one undeclared cluster, which
// is probably a rename.
func (r *clusterRec) renameHint(desired Obj, parent *plan.Action) string {
	if parent == nil || parent.ID() == "" || strOf(desired, "id") != "" {
		return ""
	}
	declared := map[string]bool{}
	for _, c := range objsOf(objOf(parent.Desired)["_clusters"]) {
		declared[strOf(c, "name")] = true
	}
	var others []plan.Observed
	for _, o := range r.seen[parent.ID()] {
		if !declared[strOf(o.Body, "name")] {
			others = append(others, o)
		}
	}
	if len(others) != 1 {
		return ""
	}
	return fmt.Sprintf("(warning: no cluster named %s; did you mean to rename %s? pin id: %s to rename)",
		pyStr(desired["name"]), pyStr(others[0].Body["name"]), others[0].ID)
}

func (r *clusterRec) Match(desired any, observed []plan.Observed) ([]plan.Observed, error) {
	d := objOf(desired)
	if pinned := strOf(d, "id"); pinned != "" {
		for _, o := range observed {
			if o.ID == pinned {
				return []plan.Observed{o}, nil
			}
		}
		return nil, &plan.PlanError{Msg: fmt.Sprintf("cluster %s not found", pinned)}
	}
	return plan.MatchByName(d, observed), nil
}

// clusterChanges splits the differences between the file and a cluster body into immutable
// changes, updatable changes (including service groups), blocked reasons and scale labels.
func clusterChanges(desired, body Obj) (immutable, updatable []plan.Change, blocked []string, labels map[string]bool) {
	for _, f := range Immutable {
		if !has(desired, f) {
			continue
		}
		if f == "couchbaseServer" {
			dv := strOf(objOf(desired[f]), "version")
			ov := strOf(objOf(body[f]), "version")
			if !versionMatches(dv, ov) {
				var before, after any
				if ov != "" {
					before = ov
				}
				if dv != "" {
					after = dv
				}
				immutable = append(immutable, plan.Change{Path: "couchbaseServer.version", Before: before, After: after})
			}
			continue
		}
		if f == "zones" && !has(body, "zones") {
			continue
		}
		immutable = append(immutable, plan.FieldChanges(Obj{f: desired[f]}, body, "", nil)...)
	}
	skip := map[string]bool{"serviceGroups": true}
	for _, f := range Immutable {
		skip[f] = true
	}
	for k := range NotAPI {
		skip[k] = true
	}
	if strOf(desired, "id") == "" {
		skip["name"] = true
	}
	updatable = plan.FieldChanges(desired, body, "", skip)
	sgChanges, sgBlocked, labels := ServiceGroupChanges(desired, body)
	return immutable, append(updatable, sgChanges...), sgBlocked, labels
}

func (r *clusterRec) Diff(ctx context.Context, rc *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	free := desired["tier"] == "free"
	if observed == nil {
		a := newAction(r, kindCluster, key, plan.Create, desired, nil, parent)
		a.Summary = CreateSummary(desired)
		a.Data = map[string]any{"state": nil, "free": free}
		a.Remarks = append(a.Remarks, CreateGroupLines(desired)...)
		if hint := r.renameHint(desired, parent); hint != "" {
			a.Notes = append(a.Notes, hint)
		}
		return a, nil
	}
	body := observed.Body
	state := "unknown"
	if s, ok := body["currentState"]; ok && s != nil {
		state = fmt.Sprint(s)
	}
	a := newAction(r, kindCluster, key, plan.Noop, desired, observed, parent)
	a.Data = map[string]any{"state": state, "free": free}
	if free != IsFree(body) {
		a.Type, a.Topic = plan.Blocked, "config"
		if IsFree(body) {
			a.Reason = fmt.Sprintf("%s is a free-tier cluster in Capella; capella.yaml should say tier: free (run 'cbctl capella sync')", key)
		} else {
			a.Reason = fmt.Sprintf("%s is a provisioned cluster in Capella; it cannot become a free-tier cluster", key)
		}
		return a, nil
	}
	immutable, changes, sgBlocked, labels := clusterChanges(desired, body)
	switch {
	case state == "deploymentFailed":
		a.Type, a.Topic = plan.Blocked, "safety"
		a.Reason = fmt.Sprintf("in deploymentFailed; delete it (cbctl capella cluster delete %s) and re-run apply", key)
		return a, nil
	case Failed[state] || state == "degraded" || state == "offline":
		a.Type, a.Topic = plan.Blocked, "safety"
		a.Reason = fmt.Sprintf("cluster is %s; check the Capella UI activity log before changing it", state)
		return a, nil
	case len(immutable) > 0:
		a.Type, a.Changes, a.Topic = plan.Blocked, immutable, "idempotency"
		paths := make([]string, len(immutable))
		for i, c := range immutable {
			paths[i] = c.Path
		}
		a.Reason = "immutable: " + strings.Join(paths, ", ") + " (requires a new cluster)"
		return a, nil
	}

	checks := ""
	if len(sgBlocked) == 0 && (labels["scale in"] || labels["scale down"]) && !OffStates[state] && parent != nil {
		old := objsOf(body["serviceGroups"])
		var declared []Obj
		if has(desired, "serviceGroups") {
			declared = objsOf(desired["serviceGroups"])
			if declared == nil {
				declared = []Obj{}
			}
		}
		updated := AfterChange(old, declared)
		if ReducesCapacity(old, updated) {
			var err error
			if sgBlocked, checks, err = Check(ctx, rc, parent.ID(), observed.ID, old, updated); err != nil {
				return nil, err
			}
		}
	}
	if len(sgBlocked) > 0 {
		a.Type, a.Changes, a.Topic = plan.Blocked, changes, "scaling"
		a.Reason = strings.Join(sgBlocked, "; ")
		return a, nil
	}
	label := strings.Join(sortedKeys(labels), " / ")
	if OffStates[state] && rc.PowerOn {
		a.Type = plan.Update
		a.Changes = append([]plan.Change{{Path: "power", Before: "off", After: "on"}}, changes...)
		a.Label = strings.Join(append([]string{"power on"}, sortedKeys(labels)...), " / ")
		a.Data["power_on"] = true
		a.Data["more"] = len(changes) > 0
		return a, nil
	}
	if len(changes) > 0 && OffStates[state] {
		a.Type, a.Changes, a.Topic = plan.Blocked, changes, "hibernation"
		what := "changing it"
		if len(labels) > 0 {
			what = "scaling"
		}
		a.Reason = fmt.Sprintf("turned off; power it on before %s (cbctl capella cluster power %s --on, or apply --power-on)", what, key)
		return a, nil
	}
	if len(changes) > 0 {
		a.Type, a.Changes = plan.Update, changes
		a.Label = label
		for _, c := range changes {
			if c.Path == "name" {
				a.Notes = append(a.Notes, plan.RenamedNote)
				break
			}
		}
		touchesGroups := false
		for _, c := range changes {
			if strings.HasPrefix(c.Path, "serviceGroups") {
				touchesGroups = true
				break
			}
		}
		if !touchesGroups && !free {
			a.Data["settings_only"] = true
			a.Remarks = append(a.Remarks, SettingsOnlyNote)
		}
		if checks != "" {
			a.Remarks = append(a.Remarks, checks)
		}
	}
	return a, nil
}

// DeleteAction is the destroy step for a cluster, applied after everything below it. It waits
// until the cluster is gone.
func (r *clusterRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	obs := o
	a := newAction(r, kindCluster, strOf(o.Body, "name"), plan.Delete, Obj{"name": o.Body["name"]}, &obs, parent)
	a.Data = map[string]any{"state": o.Body["currentState"], "free": IsFree(o.Body)}
	return a, nil
}

func (r *clusterRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	d := objOf(a.Desired)
	pid := projectID(a)
	if err := needIDs("cluster "+a.Key, pid); err != nil {
		return err
	}
	free := truthy(a.Data["free"])
	api, err := newRawAPI(rc)
	if err != nil {
		return err
	}

	if a.Type == plan.Delete {
		if a.Observed == nil || a.Observed.ID == "" {
			return fmt.Errorf("cluster %s has no id to delete", a.Key)
		}
		if free {
			err = rc.Client.DeleteFreeCluster(ctx, pid, a.Observed.ID)
		} else {
			err = rc.Client.DeleteCluster(ctx, pid, a.Observed.ID, !truthy(a.Data["discard_snapshots"]))
		}
		if err != nil {
			return err
		}
		a.Set("sent", true)
		return nil
	}

	if a.Type == plan.Create && free {
		body := Obj{}
		for _, k := range []string{"name", "description", "cloudProvider"} {
			if v, ok := d[k]; ok {
				body[k] = v
			}
		}
		id, err := rc.Client.CreateFreeCluster(ctx, pid, body)
		if err != nil {
			return err
		}
		a.CreatedID = id
		a.Set("state", "deploying")
		a.Set("sent", true)
		return nil
	}

	if a.Type == plan.Update && truthy(a.Data["power_on"]) {
		if a.Observed == nil || a.Observed.ID == "" {
			return fmt.Errorf("cluster %s has no id to power on", a.Key)
		}
		if a.Data["state"] == "turningOff" {
			if err := r.wait(ctx, rc, a, engine.OpPowerOff); err != nil {
				return err
			}
		}
		if free {
			err = rc.Client.TurnFreeClusterOn(ctx, pid, a.Observed.ID)
		} else {
			err = rc.Client.TurnClusterOn(ctx, pid, a.Observed.ID, false)
		}
		if err != nil {
			return err
		}
		a.Set("state", "turningOn")
		if err := r.wait(ctx, rc, a, engine.OpPowerOn); err != nil {
			return err
		}
		if !truthy(a.Data["more"]) {
			a.Set("sent", false)
			return nil
		}
	}

	if a.Type == plan.Update && free {
		if a.Observed == nil || a.Observed.ID == "" {
			return fmt.Errorf("cluster %s has no id to update", a.Key)
		}
		id := a.Observed.ID
		if a.Data["state"] != "healthy" {
			if err := r.wait(ctx, rc, a, engine.OpReady); err != nil {
				return err
			}
		}
		current, etag, err := api.get(ctx, api.freeCluster(pid, id))
		if err != nil {
			return err
		}
		name := current["name"]
		if strOf(d, "id") != "" || name == nil {
			name = d["name"]
		}
		description := orEmpty(current["description"])
		if has(d, "description") {
			description = d["description"]
		}
		if err := rc.Client.UpdateFreeCluster(ctx, pid, id, Obj{"name": name, "description": description}, etag); err != nil {
			return err
		}
		a.Set("sent", false) // name and description only: nothing to wait for
		return nil
	}

	if a.Type == plan.Create {
		body := Obj{}
		for _, k := range CreateFields {
			if v, ok := d[k]; ok {
				body[k] = v
			}
		}
		provider := objOf(d["cloudProvider"])["type"]
		groups := []any{}
		for _, g := range objsOf(d["serviceGroups"]) {
			groups = append(groups, APIGroup(g, provider))
		}
		body["serviceGroups"] = groups
		id, err := api.postID(ctx, api.clusters(pid), body)
		if err != nil {
			return err
		}
		a.CreatedID = id
		a.Set("state", "deploying")
		a.Set("sent", true)
		return nil
	}

	if a.Type == plan.Update {
		if a.Observed == nil || a.Observed.ID == "" {
			return fmt.Errorf("cluster %s has no id to update", a.Key)
		}
		cid := a.Observed.ID
		// Capella only accepts deployments when Draft/Healthy. Always wait (unless
		// --no-wait) so a prior scale still marked scaling cannot 422 this PUT.
		if !rc.NoWait {
			if err := r.wait(ctx, rc, a, engine.OpReady); err != nil {
				return err
			}
		} else if st, _ := a.Data["state"].(string); st != "" && st != "healthy" && engine.Transitional[st] {
			return fmt.Errorf("cluster %s is %s; wait until it is healthy, or omit --no-wait", a.Key, st)
		}
		current, etag, err := api.get(ctx, api.cluster(pid, cid))
		if err != nil {
			return err
		}
		_, changes, _, _ := clusterChanges(d, current)
		if len(changes) == 0 {
			a.Set("sent", false)
			return nil
		}
		body := BuildUpdateBody(current, d, strOf(d, "id") != "")
		if err := api.put(ctx, api.cluster(pid, cid), body, etag); err != nil {
			return err
		}
		a.Set("state", "scaling")
		a.Set("sent", true)
	}
	return nil
}

// wait polls the cluster until op completes, printing the state as it changes.
func (r *clusterRec) wait(ctx context.Context, rc *plan.RunContext, a *plan.Action, op engine.Op) error {
	pid, cid := projectID(a), a.ID()
	if pid == "" || cid == "" {
		return fmt.Errorf("cluster %s has no id to wait for", a.Key)
	}
	settingsOnly := truthy(a.Data["settings_only"])
	hooks := engine.WaitHooks{
		OnState: func(state string, elapsed time.Duration) {
			shown := state
			if settingsOnly && engine.Transitional[state] {
				shown = fmt.Sprintf("applying the change (Capella: %s)", state)
			}
			line := fmt.Sprintf("  cluster %s: %s (%s)", a.Key, shown, plan.FormatDuration(elapsed))
			if rc.ProgressInline != nil && engine.Transitional[state] {
				rc.Inline(line+" ", true) // dots follow on the same line
			} else {
				rc.Say(line)
			}
		},
	}
	if rc.ProgressInline != nil {
		hooks.OnTick = func() { rc.Inline(".", false) }
	}
	getter := rc.Client.AsClusterGetter()
	if truthy(a.Data["free"]) {
		api, err := newRawAPI(rc)
		if err != nil {
			return err
		}
		getter = freeGetter{a: api}
	}
	state, err := engine.WaitForCluster(ctx, getter, pid, cid, op, rc.Timeout, rc.Clock, a.Key, hooks)
	if err != nil {
		return err
	}
	a.Set("state", state)
	return nil
}

// Wait waits for the operation the cluster write started, unless nothing was sent.
func (r *clusterRec) Wait(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	if !truthy(a.Data["sent"]) {
		return nil
	}
	op := engine.OpUpdate
	switch a.Type {
	case plan.Create:
		op = engine.OpCreate
	case plan.Delete:
		op = engine.OpDelete
	}
	return r.wait(ctx, rc, a, op)
}

// ChildBlockReason blocks every change below a turned-off cluster except its schedule, unless
// the run powers the cluster on.
func (r *clusterRec) ChildBlockReason(ctx context.Context, rc *plan.RunContext, a *plan.Action, childKind string) (string, error) {
	state, _ := a.Data["state"].(string)
	if !OffStates[state] || rc.PowerOn || childKind == "schedule" {
		return "", nil
	}
	reason := fmt.Sprintf("cluster is turned off; run 'cbctl capella cluster power %s --on' or pass --power-on", a.Key)
	if _, ok := a.Data["next_on"]; !ok {
		a.Data["next_on"] = r.nextOn(ctx, rc, a)
	}
	if next, _ := a.Data["next_on"].(string); next != "" {
		reason += fmt.Sprintf(" (the schedule turns it on at %s)", next)
	}
	return reason, nil
}

// nextOn is when the cluster's schedule next turns it on; informational, so errors give "".
func (r *clusterRec) nextOn(ctx context.Context, rc *plan.RunContext, a *plan.Action) string {
	if rc.Client == nil || projectID(a) == "" || a.ID() == "" {
		return ""
	}
	body, _, err := rc.Client.GetSchedule(ctx, projectID(a), a.ID())
	if err != nil {
		return ""
	}
	normObj(body)
	if body["activationStatus"] == "paused" {
		return ""
	}
	return NextOnText(body, rc.Clock.Now())
}

// EnsureReady waits for a transitional cluster to become healthy before its children are applied.
// A schedule can be changed while the cluster is off.
func (r *clusterRec) EnsureReady(ctx context.Context, rc *plan.RunContext, a *plan.Action, child *plan.Action) (bool, error) {
	state, _ := a.Data["state"].(string)
	if state == "healthy" {
		return true, nil
	}
	if state == "turnedOff" && child != nil && child.Kind == "schedule" {
		return true, nil
	}
	if engine.Transitional[state] || a.Data["state"] == nil {
		if rc.NoWait || a.ID() == "" {
			return false, nil
		}
		if err := r.wait(ctx, rc, a, engine.OpReady); err != nil {
			return false, err
		}
		return a.Data["state"] == "healthy", nil
	}
	return false, nil
}
