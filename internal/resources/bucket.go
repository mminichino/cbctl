package resources

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/plan"
)

const kindBucket = "bucket"

// Bucket field groups. Capella's update endpoint takes every UpdateRequired field and the
// optional ones it already has; the type, storage backend, vbuckets, conflict resolution and
// eviction policy never change.
var (
	BucketCreateFields    = []string{"name", "type", "storageBackend", "vbuckets", "memoryAllocationInMb", "bucketConflictResolution", "durabilityLevel", "replicas", "flushEnabled", "timeToLiveInSeconds", "evictionPolicy", "priority"}
	BucketImmutable       = []string{"type", "storageBackend", "vbuckets", "bucketConflictResolution", "evictionPolicy"}
	BucketUpdateRequired  = []string{"memoryAllocationInMb", "durabilityLevel", "replicas", "timeToLiveInSeconds"}
	BucketUpdateOptional  = []string{"flushEnabled", "enableCrossClusterVersioning", "priority"}
	BucketUpdatable       = append(append([]string(nil), BucketUpdateRequired...), BucketUpdateOptional...)
	BucketFreeUpdatable   = []string{"memoryAllocationInMb", "enableCrossClusterVersioning"}
	ReplicaNote           = "note: the new replica count takes full effect after a rebalance"
	OneNodeNote           = "note: the cluster has one data node; the replica is placed when data nodes are added"
	freeBucketDefaultSize = int64(100)
)

func isFreeCluster(cluster *plan.Action) bool {
	return cluster != nil && truthy(cluster.Data["free"])
}

// ObservedView is the bucket as compared: flushEnabled is read from the deprecated flush only
// when it is absent, and stats are dropped.
func ObservedView(body Obj) Obj {
	view := copyObj(body)
	flush, ok := plan.CanonFlush(body)
	delete(view, "flush")
	delete(view, "stats")
	if ok {
		view["flushEnabled"] = flush
	}
	return view
}

// BucketCreateSummary is `couchbase · magma · 1024 MB · 1 replica · durability majority`; on a
// free-tier cluster `free tier · 100 MB`.
func BucketCreateSummary(d Obj, free bool) string {
	if free {
		mem := d["memoryAllocationInMb"]
		if mem == nil {
			mem = freeBucketDefaultSize
		}
		return fmt.Sprintf("free tier · %s MB", pyStr(mem))
	}
	typ := "couchbase"
	if v, ok := d["type"]; ok {
		typ = pyStr(v)
	}
	parts := []string{typ}
	if truthy(d["storageBackend"]) {
		parts = append(parts, pyStr(d["storageBackend"]))
	}
	if d["memoryAllocationInMb"] != nil {
		parts = append(parts, pyStr(d["memoryAllocationInMb"])+" MB")
	}
	if d["replicas"] != nil {
		parts = append(parts, plural(intOf(d["replicas"]), "replica"))
	}
	if truthy(d["durabilityLevel"]) {
		parts = append(parts, "durability "+pyStr(d["durabilityLevel"]))
	}
	return strings.Join(parts, " · ")
}

// BuildBucketUpdateBody is the full PUT body: observed overlaid with desired. The required
// fields are always present.
func BuildBucketUpdateBody(observed, desired Obj, free bool) Obj {
	if free {
		body := Obj{}
		for _, f := range BucketFreeUpdatable {
			if observed[f] != nil {
				body[f] = observed[f]
			}
			if v, ok := desired[f]; ok {
				body[f] = v
			}
		}
		return body
	}
	view := ObservedView(observed)
	body := Obj{}
	for _, f := range BucketUpdateRequired {
		body[f] = view[f]
	}
	for _, f := range BucketUpdateOptional {
		if view[f] != nil {
			body[f] = view[f]
		}
	}
	for _, f := range BucketUpdatable {
		if v, ok := desired[f]; ok {
			body[f] = v
		}
	}
	return body
}

func pick(src Obj, fields []string) Obj {
	out := Obj{}
	for _, f := range fields {
		if v, ok := src[f]; ok {
			out[f] = v
		}
	}
	return out
}

// BucketChanges returns the immutable and the updatable changes for the fields present in the file.
func BucketChanges(desired, observed Obj, free bool) (immutable, updatable []plan.Change) {
	view := ObservedView(observed)
	if _, ok := view["enableCrossClusterVersioning"]; !ok {
		view["enableCrossClusterVersioning"] = false
	}
	if free { // nothing else can be set on a free-tier bucket
		return nil, plan.FieldChanges(pick(desired, BucketFreeUpdatable), view, "", nil)
	}
	return plan.FieldChanges(pick(desired, BucketImmutable), view, "", nil),
		plan.FieldChanges(pick(desired, BucketUpdatable), view, "", nil)
}

// DependentCredentials lists the database credentials whose access names the bucket.
func DependentCredentials(ctx context.Context, c *capellaapi.Client, projectID, clusterID, bucket string) ([]string, error) {
	creds, err := listCredentials(ctx, c, projectID, clusterID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, u := range creds {
		if accessNamesBucket(u["access"], bucket) {
			names = append(names, pyStr(u["name"]))
		}
	}
	sort.Strings(names)
	return names, nil
}

func accessNamesBucket(access any, bucket string) bool {
	for _, a := range objsOf(access) {
		for _, b := range objsOf(objOf(a["resources"])["buckets"]) {
			if b["name"] == bucket {
				return true
			}
		}
	}
	return false
}

func listCredentials(ctx context.Context, c *capellaapi.Client, projectID, clusterID string) ([]Obj, error) {
	org, err := c.OrgPath()
	if err != nil {
		return nil, err
	}
	api := &rawAPI{c: c, org: org}
	return api.list(ctx, api.users(projectID, clusterID), "data")
}

// BucketDeleteDetails are the lines shown before a bucket delete is confirmed: its size, then the
// credentials that lose access.
func BucketDeleteDetails(ctx context.Context, c *capellaapi.Client, projectID, clusterID string, body Obj) ([]string, error) {
	stats := objOf(body["stats"])
	line := fmt.Sprintf("%s · %s items", pyStr(body["name"]), commas(intOf(stats["itemCount"])))
	if stats["memoryUsedInMib"] != nil {
		line += fmt.Sprintf(" · %s MB used", pyStr(stats["memoryUsedInMib"]))
	}
	lines := []string{line}
	creds, err := DependentCredentials(ctx, c, projectID, clusterID, pyPlain(body["name"]))
	if err != nil {
		return nil, err
	}
	if len(creds) > 0 {
		lines = append(lines, "These credentials lose access to this bucket:")
		for _, n := range creds {
			lines = append(lines, "  "+n)
		}
	}
	return lines, nil
}

// clusterStats is a cluster's memory and replica capacity as the bucket preflight needs it.
type clusterStats struct {
	free, maxReplicas       int64
	hasFree, hasMaxReplicas bool
}

// bucketRec reconciles buckets, on provisioned and free-tier clusters.
type bucketRec struct {
	plan.Base
	// Per cluster id: its stats, and the memory still free after the creates and increases
	// planned so far.
	stats map[string]*clusterStats
	free  map[string]int64
}

func newBucket(children ...plan.Reconciler) *bucketRec {
	r := &bucketRec{
		Base:  plan.NewBase(kindBucket, plan.Flags{Prunable: true}, children...),
		stats: map[string]*clusterStats{},
		free:  map[string]int64{},
	}
	r.Bind(r)
	return r
}

func (r *bucketRec) DesiredItems(parent any) []any {
	var items []any
	for _, b := range objsOf(objOf(parent)["buckets"]) {
		items = append(items, b)
	}
	return items
}

func (r *bucketRec) Key(desired any, parent *plan.Action) string {
	name := pyPlain(objOf(desired)["name"])
	if parent != nil {
		return parent.Key + "/" + name
	}
	return name
}

func (r *bucketRec) AfterChildrenNote() string { return "(after bucket is created)" }

func (r *bucketRec) remember(clusterID string, s *clusterStats) {
	r.stats[clusterID] = s
	if s != nil && s.hasFree {
		r.free[clusterID] = s.free
	}
}

func statsFromObj(o Obj) *clusterStats {
	if o == nil {
		return nil
	}
	s := &clusterStats{}
	if v, ok := o["freeMemoryInMb"]; ok && v != nil {
		s.free, s.hasFree = intOf(v), true
	}
	if v, ok := o["maxReplicas"]; ok && v != nil {
		s.maxReplicas, s.hasMaxReplicas = intOf(v), true
	}
	return s
}

func (r *bucketRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	pid, cid := clusterIDs(parent)
	if err := needIDs("bucket", pid, cid); err != nil {
		return nil, err
	}
	delete(r.stats, cid)
	delete(r.free, cid)
	api, err := newRawAPI(rc)
	if err != nil {
		return nil, err
	}
	var buckets []Obj
	if isFreeCluster(parent) {
		var stats Obj
		if buckets, stats, err = api.freeBucketList(ctx, pid, cid); err != nil {
			return nil, err
		}
		r.remember(cid, statsFromObj(stats))
	} else if buckets, err = api.list(ctx, api.buckets(pid, cid), "data"); err != nil {
		return nil, err
	}
	out := make([]plan.Observed, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, plan.Observed{ID: strOf(b, "id"), Body: b})
	}
	return out, nil
}

// clusterStats reads the cluster's stats once per cluster and plan (a free-tier cluster's come
// with the bucket list). It returns nil when the cluster does not exist yet or has no stats.
func (r *bucketRec) clusterStats(ctx context.Context, rc *plan.RunContext, cluster *plan.Action) (*clusterStats, error) {
	if cluster == nil || cluster.Observed == nil || cluster.ID() == "" {
		return nil, nil
	}
	id := cluster.ID()
	if _, ok := r.stats[id]; !ok {
		raw, err := rc.Client.GetClusterStats(ctx, projectID(cluster), id)
		var s *clusterStats
		switch {
		case err == nil:
			s = &clusterStats{free: int64(raw.FreeMemoryInMb), maxReplicas: int64(raw.MaxReplicas), hasFree: true, hasMaxReplicas: true}
		default:
			var apiErr *capellaapi.APIError
			if !errors.As(err, &apiErr) {
				return nil, err
			}
		}
		r.remember(id, s)
	}
	return r.stats[id], nil
}

// preflight checks the cluster's free memory and data nodes. It returns the blocking reason, if
// any, and notes to attach to the action.
func (r *bucketRec) preflight(ctx context.Context, rc *plan.RunContext, cluster *plan.Action, needMB int64, replicas any, checkReplicas bool) (string, []string, error) {
	stats, err := r.clusterStats(ctx, rc, cluster)
	if err != nil || stats == nil || cluster == nil || cluster.ID() == "" {
		return "", nil, err
	}
	var notes []string
	if want, ok := plan.AsInt(replicas); checkReplicas && ok && stats.hasMaxReplicas && want > stats.maxReplicas {
		// The API's minimum is 1 replica, so a cluster with one data node (maxReplicas 0) still accepts 1.
		allowed := max(stats.maxReplicas, 1)
		if want > allowed {
			return fmt.Sprintf("replicas: %d but the cluster supports at most %d; scale out with 'cbctl capella cluster scale' or reduce replicas",
				want, allowed), nil, nil
		}
		notes = append(notes, OneNodeNote)
	}
	if free, ok := r.free[cluster.ID()]; ok && needMB > 0 {
		if needMB > free {
			return fmt.Sprintf("needs %d MB but the cluster has %d MB free; scale out with 'cbctl capella cluster scale' or reduce memoryAllocationInMb",
				needMB, free), nil, nil
		}
		r.free[cluster.ID()] = free - needMB
	}
	return "", notes, nil
}

func (r *bucketRec) Diff(ctx context.Context, rc *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	free := isFreeCluster(parent)
	if observed == nil {
		a := newAction(r, kindBucket, key, plan.Create, desired, nil, parent)
		a.Summary = BucketCreateSummary(desired, free)
		need := intOf(desired["memoryAllocationInMb"])
		if need == 0 && free {
			need = freeBucketDefaultSize
		}
		blocked, notes, err := r.preflight(ctx, rc, parent, need, desired["replicas"], !free)
		if err != nil {
			return nil, err
		}
		if blocked != "" {
			a.Type, a.Reason, a.Topic = plan.Blocked, blocked, "scaling"
		}
		a.Notes = append(a.Notes, notes...)
		return a, nil
	}
	a := newAction(r, kindBucket, key, plan.Noop, desired, observed, parent)
	immutable, changes := BucketChanges(desired, observed.Body, free)
	if len(immutable) > 0 {
		a.Type, a.Topic = plan.Blocked, "idempotency"
		parts := make([]string, len(immutable))
		for i, c := range immutable {
			parts[i] = fmt.Sprintf("%s: %s → %s", c.Path, pyStr(c.Before), pyStr(c.After))
		}
		a.Reason = strings.Join(parts, "; ") + "  BLOCKED (immutable; create a new bucket and migrate data)"
		return a, nil
	}
	if len(changes) == 0 {
		return a, nil
	}
	a.Type, a.Changes = plan.Update, changes
	byPath := map[string]plan.Change{}
	for _, c := range changes {
		byPath[c.Path] = c
	}
	var increase int64
	if mem, ok := byPath["memoryAllocationInMb"]; ok {
		before, bok := plan.AsInt(mem.Before)
		after, aok := plan.AsInt(mem.After)
		if bok && aok {
			increase = after - before
		}
	}
	_, replicasChanged := byPath["replicas"]
	blocked, notes, err := r.preflight(ctx, rc, parent, max(increase, 0), desired["replicas"], replicasChanged)
	if err != nil {
		return nil, err
	}
	if blocked != "" {
		a.Type, a.Reason, a.Topic = plan.Blocked, blocked, "scaling"
		return a, nil
	}
	if replicasChanged {
		a.Notes = append(a.Notes, ReplicaNote)
	}
	a.Notes = append(a.Notes, notes...)
	return a, nil
}

// DeleteAction is `apply --prune` for a bucket the file no longer declares. A protected cluster
// blocks it.
func (r *bucketRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	name := pyPlain(o.Body["name"])
	key := name
	if parent != nil {
		key = parent.Key + "/" + name
	}
	obs := o
	a := newAction(r, kindBucket, key, plan.Delete, Obj{"name": o.Body["name"]}, &obs, parent)
	if parent != nil && parent.Observed != nil && DeletionProtected(parent.Observed.Body) {
		a.Type, a.Topic, a.Reason = plan.Blocked, "safety", ProtectionMessage(parent.Key)
	}
	return a, nil
}

func (r *bucketRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid := clusterIDs(a.Parent)
	if err := needIDs("bucket "+a.Key, pid, cid); err != nil {
		return err
	}
	api, err := newRawAPI(rc)
	if err != nil {
		return err
	}
	d := objOf(a.Desired)
	free := isFreeCluster(a.Parent)
	if a.Type == plan.Create {
		return r.applyCreate(ctx, rc, api, a, pid, cid, free)
	}
	if a.Observed == nil || a.Observed.ID == "" {
		return fmt.Errorf("bucket %s has no id", a.Key)
	}
	bid := a.Observed.ID
	switch a.Type {
	case plan.Update:
		path := api.bucket(pid, cid, bid)
		if free {
			path = api.freeBucket(pid, cid, bid)
		}
		current, _, err := api.get(ctx, path)
		if err != nil {
			return err
		}
		if _, updatable := BucketChanges(d, current, free); len(updatable) > 0 {
			return r.update(ctx, rc, pid, cid, bid, BuildBucketUpdateBody(current, d, free), free)
		}
	case plan.Delete:
		if free {
			return ignoreNotFound(rc.Client.DeleteFreeBucket(ctx, pid, cid, bid))
		}
		return ignoreNotFound(rc.Client.DeleteBucket(ctx, pid, cid, bid))
	}
	return nil
}

func (r *bucketRec) update(ctx context.Context, rc *plan.RunContext, pid, cid, bid string, body Obj, free bool) error {
	if free {
		return rc.Client.UpdateFreeBucket(ctx, pid, cid, bid, body)
	}
	return rc.Client.UpdateBucket(ctx, pid, cid, bid, body, "")
}

func (r *bucketRec) applyCreate(ctx context.Context, rc *plan.RunContext, api *rawAPI, a *plan.Action, pid, cid string, free bool) error {
	d := objOf(a.Desired)
	var id string
	var err error
	if free {
		body := pick(d, []string{"name", "memoryAllocationInMb"})
		id, err = rc.Client.CreateFreeBucket(ctx, pid, cid, body)
	} else {
		// flushEnabled, never the deprecated flush.
		id, err = api.postID(ctx, api.buckets(pid, cid), pick(d, BucketCreateFields))
	}
	if err != nil {
		return err
	}
	a.CreatedID = id
	if truthy(d["enableCrossClusterVersioning"]) { // only accepted by the update endpoint
		path := api.bucket(pid, cid, id)
		if free {
			path = api.freeBucket(pid, cid, id)
		}
		current, _, err := api.get(ctx, path)
		if err != nil {
			return err
		}
		return r.update(ctx, rc, pid, cid, id, BuildBucketUpdateBody(current, d, free), free)
	}
	return nil
}
