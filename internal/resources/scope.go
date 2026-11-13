package resources

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/plan"
)

const (
	kindScope      = "scope"
	kindCollection = "collection"

	// DefaultScope is the scope (and collection) every bucket has.
	DefaultScope = "_default"
	// SystemDeleteMessage explains why system scopes and collections are never deleted.
	SystemDeleteMessage = "system scopes and collections cannot be deleted"
)

// SystemScopes are never created, changed, deleted or listed as unmanaged.
var SystemScopes = []string{DefaultScope, "_system"}

// MaxTTLMinVersion is the first Couchbase Server version that can change a collection's maxTTL.
var MaxTTLMinVersion = []int{7, 6, 0}

// IsSystemScope reports whether name is _default or _system.
func IsSystemScope(name any) bool {
	s, _ := name.(string)
	return contains(SystemScopes, s)
}

// IsSystemCollection is true for every collection of _system and for _default._default.
func IsSystemCollection(scope, name any) bool {
	return scope == "_system" || (scope == DefaultScope && name == DefaultScope)
}

var digits = regexp.MustCompile(`\d+`)

// ParseVersion reads "7.6.2" as [7 6 2] and "7.2.4-build" as [7 2 4]; nil when unknown.
func ParseVersion(text string) []int {
	text, _, _ = strings.Cut(text, "-")
	var out []int
	for _, m := range digits.FindAllString(text, 3) {
		n, _ := strconv.Atoi(m)
		out = append(out, n)
	}
	return out
}

// ServerVersion is the Couchbase Server version of a cluster as observed; nil for a new cluster.
func ServerVersion(cluster *plan.Action) []int {
	if cluster == nil || cluster.Observed == nil {
		return nil
	}
	return ParseVersion(strOf(objOf(cluster.Observed.Body["couchbaseServer"]), "version"))
}

func versionLess(a, b []int) bool {
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func versionText(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// keyspaceIDs returns the ids of a bucket action's project, cluster and bucket.
func keyspaceIDs(bucket *plan.Action) (pid, cid, bid string) { return bucketIDs(bucket) }

func nameOf(a *plan.Action) string { return pyPlain(objOf(a.Desired)["name"]) }

func protected(cluster *plan.Action) bool {
	return cluster != nil && cluster.Observed != nil && DeletionProtected(cluster.Observed.Body)
}

// DependentScopeCredentials lists the database credentials whose access names the scope, or the
// collection when one is given.
func DependentScopeCredentials(ctx context.Context, c *capellaapi.Client, projectID, clusterID, bucket, scope, collection string) ([]string, error) {
	creds, err := listCredentials(ctx, c, projectID, clusterID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, u := range creds {
		found := false
		for _, a := range objsOf(u["access"]) {
			for _, b := range objsOf(objOf(a["resources"])["buckets"]) {
				if b["name"] != bucket {
					continue
				}
				for _, s := range objsOf(b["scopes"]) {
					if s["name"] == scope && (collection == "" || contains(stringsOf(s["collections"]), collection)) {
						found = true
					}
				}
			}
		}
		if found {
			names = append(names, pyStr(u["name"]))
		}
	}
	sort.Strings(names)
	return names, nil
}

// KeyspaceDeleteDetails are the lines shown before a scope delete (collection == "") or a
// collection delete is confirmed: what is lost, then the credentials that lose access. Document
// counts are best effort; a missing scope read never blocks.
func KeyspaceDeleteDetails(ctx context.Context, c *capellaapi.Client, projectID, clusterID, bucket, bucketID, scope, collection string) ([]string, error) {
	var lines []string
	what := "collection"
	if collection == "" {
		what = "scope"
		var colls []string
		if sc, _, err := c.GetScope(ctx, projectID, clusterID, bucketID, scope); err == nil && sc != nil {
			for _, col := range sc.Collections {
				colls = append(colls, col.Name)
			}
		}
		sort.Strings(colls)
		line := fmt.Sprintf("%s · %d collection%s", scope, len(colls), map[bool]string{true: "", false: "s"}[len(colls) == 1])
		if len(colls) > 0 {
			line += ": " + strings.Join(colls, ", ")
		}
		lines = append(lines, line)
		if len(colls) > 0 {
			lines = append(lines, "These collections and all their documents are deleted.")
		}
	} else {
		lines = append(lines, fmt.Sprintf("%s.%s and all its documents are deleted.", scope, collection))
	}
	creds, err := DependentScopeCredentials(ctx, c, projectID, clusterID, bucket, scope, collection)
	if err != nil {
		return nil, err
	}
	if len(creds) > 0 {
		lines = append(lines, fmt.Sprintf("These credentials lose access to this %s:", what))
		for _, n := range creds {
			lines = append(lines, "  "+n)
		}
	}
	return lines, nil
}

// scopeRec reconciles scopes. _default and _system are system objects; _default may be declared so
// that the collections in it are managed.
type scopeRec struct{ plan.Base }

func newScope(children ...plan.Reconciler) *scopeRec {
	r := &scopeRec{Base: plan.NewBase(kindScope, plan.Flags{Prunable: true, ReportsUnmanaged: true}, children...)}
	r.Bind(r)
	return r
}

func (r *scopeRec) DesiredItems(parent any) []any {
	var items []any
	for _, s := range objsOf(objOf(parent)["scopes"]) {
		items = append(items, s)
	}
	return items
}

func (r *scopeRec) Key(desired any, parent *plan.Action) string {
	name := pyPlain(objOf(desired)["name"])
	if parent != nil {
		return parent.Key + "/" + name
	}
	return name
}

func (r *scopeRec) AfterChildrenNote() string { return "(after scope is created)" }

func (r *scopeRec) Manages(o plan.Observed) bool { return !IsSystemScope(o.Body["name"]) }

func (r *scopeRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	pid, cid, bid := keyspaceIDs(parent)
	if err := needIDs("scope", pid, cid, bid); err != nil {
		return nil, err
	}
	api, err := newRawAPI(rc)
	if err != nil {
		return nil, err
	}
	scopes, err := api.list(ctx, api.scopes(pid, cid, bid), "scopes")
	if err != nil {
		return nil, err
	}
	out := make([]plan.Observed, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, plan.Observed{ID: strOf(s, "name"), Body: s})
	}
	return out, nil
}

func (r *scopeRec) Diff(_ context.Context, _ *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if observed != nil || desired["name"] == DefaultScope { // _default exists with every bucket
		return newAction(r, kindScope, key, plan.Noop, desired, observed, parent), nil
	}
	return newAction(r, kindScope, key, plan.Create, desired, nil, parent), nil
}

// DeleteAction is `apply --prune` for a scope the file no longer declares. A protected cluster
// blocks it.
func (r *scopeRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	return deleteKeyspace(r, kindScope, o, parent, func(p *plan.Action) *plan.Action {
		if p == nil {
			return nil
		}
		return p.Parent
	}), nil
}

func deleteKeyspace(rec plan.Reconciler, kind string, o plan.Observed, parent *plan.Action, clusterOf func(*plan.Action) *plan.Action) *plan.Action {
	name := pyPlain(o.Body["name"])
	key := o.ID
	if parent != nil {
		key = parent.Key + "/" + name
	}
	obs := o
	a := newAction(rec, kind, key, plan.Delete, Obj{"name": o.Body["name"]}, &obs, parent)
	if cluster := clusterOf(parent); protected(cluster) {
		a.Type, a.Topic, a.Reason = plan.Blocked, "safety", ProtectionMessage(cluster.Key)
	}
	return a
}

func (r *scopeRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid, bid := keyspaceIDs(a.Parent)
	if err := needIDs("scope "+a.Key, pid, cid, bid); err != nil {
		return err
	}
	switch a.Type {
	case plan.Create:
		if err := rc.Client.CreateScope(ctx, pid, cid, bid, nameOf(a)); err != nil {
			return err
		}
		a.CreatedID = nameOf(a)
	case plan.Delete:
		return ignoreNotFound(rc.Client.DeleteScope(ctx, pid, cid, bid, nameOf(a))) // already gone: the goal is reached
	}
	return nil
}

// collectionRec reconciles collections. maxTTL is compared only when the file declares it.
type collectionRec struct{ plan.Base }

func newCollection() *collectionRec {
	r := &collectionRec{Base: plan.NewBase(kindCollection, plan.Flags{Prunable: true, ReportsUnmanaged: true})}
	r.Bind(r)
	return r
}

func (r *collectionRec) DesiredItems(parent any) []any {
	var items []any
	for _, c := range objsOf(objOf(parent)["collections"]) {
		items = append(items, c)
	}
	return items
}

func (r *collectionRec) Key(desired any, parent *plan.Action) string {
	name := pyPlain(objOf(desired)["name"])
	if parent != nil {
		return parent.Key + "/" + name
	}
	return name
}

// where returns the ids of the scope's project, cluster and bucket, and the scope's name.
func collectionWhere(scope *plan.Action) (pid, cid, bid, name string) {
	if scope == nil {
		return "", "", "", ""
	}
	pid, cid, bid = keyspaceIDs(scope.Parent)
	return pid, cid, bid, nameOf(scope)
}

func (r *collectionRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	pid, cid, bid, scope := collectionWhere(parent)
	if err := needIDs("collection", pid, cid, bid); err != nil {
		return nil, err
	}
	api, err := newRawAPI(rc)
	if err != nil {
		return nil, err
	}
	colls, err := api.list(ctx, api.collections(pid, cid, bid, scope), "data")
	if err != nil {
		return nil, err
	}
	var out []plan.Observed
	for _, c := range colls {
		if IsSystemCollection(scope, c["name"]) {
			continue
		}
		out = append(out, plan.Observed{ID: strOf(c, "name"), Body: c})
	}
	return out, nil
}

func (r *collectionRec) Diff(_ context.Context, _ *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if observed == nil {
		a := newAction(r, kindCollection, key, plan.Create, desired, nil, parent)
		if has(desired, "maxTTL") {
			a.Summary = "maxTTL " + pyStr(desired["maxTTL"])
		}
		return a, nil
	}
	a := newAction(r, kindCollection, key, plan.Noop, desired, observed, parent)
	var declared Obj
	if has(desired, "maxTTL") {
		declared = Obj{"maxTTL": desired["maxTTL"]}
	}
	changes := plan.FieldChanges(declared, observed.Body, "", nil)
	if len(changes) == 0 {
		return a, nil
	}
	a.Changes = changes
	var cluster *plan.Action
	if parent != nil {
		cluster = parent.Ancestor(kindCluster)
	}
	if version := ServerVersion(cluster); version != nil && versionLess(version, MaxTTLMinVersion) {
		a.Type, a.Topic = plan.Blocked, "idempotency"
		name := "the cluster"
		if cluster != nil {
			name = cluster.Key
		}
		a.Reason = fmt.Sprintf("maxTTL can only be changed on Couchbase Server %s or later (%s runs %s)",
			versionText(MaxTTLMinVersion), name, versionText(version))
		return a, nil
	}
	a.Type = plan.Update
	return a, nil
}

// DeleteAction is `apply --prune` for a collection the file no longer declares. A protected
// cluster blocks it.
func (r *collectionRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	return deleteKeyspace(r, kindCollection, o, parent, func(p *plan.Action) *plan.Action {
		if p == nil {
			return nil
		}
		return p.Ancestor(kindCluster)
	}), nil
}

func (r *collectionRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid, bid, scope := collectionWhere(a.Parent)
	if err := needIDs("collection "+a.Key, pid, cid, bid); err != nil {
		return err
	}
	d := objOf(a.Desired)
	name := nameOf(a)
	switch a.Type {
	case plan.Create:
		var ttl *int
		if has(d, "maxTTL") {
			n := int(intOf(d["maxTTL"]))
			ttl = &n
		}
		if err := rc.Client.CreateCollection(ctx, pid, cid, bid, scope, name, ttl); err != nil {
			return err
		}
		a.CreatedID = name
	case plan.Update:
		current, etag, err := rc.Client.GetCollection(ctx, pid, cid, bid, scope, name)
		if err != nil {
			return err
		}
		if want := intOf(d["maxTTL"]); int64(current.MaxTTL) != want { // 412 on a stale ETag: the executor re-plans
			return rc.Client.UpdateCollection(ctx, pid, cid, bid, scope, name, int(want), etag)
		}
	case plan.Delete:
		return ignoreNotFound(rc.Client.DeleteCollection(ctx, pid, cid, bid, scope, name))
	}
	return nil
}
