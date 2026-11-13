package plan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type tree struct {
	root                                  *stub
	project, cluster, bucket, sched, cred *stub
}

// newTree builds root → project → cluster → (schedule, bucket, credential).
func newTree() *tree {
	t := &tree{}
	t.cred = newStub("credential", "databaseCredentials", Flags{Prunable: true, ReportsUnmanaged: true})
	t.bucket = newStub("bucket", "buckets", Flags{Prunable: true})
	t.bucket.qualify = true
	t.sched = newStub("schedule", "hibernation", Flags{})
	t.sched.Flag = Flags{Prunable: true, ReportsUnmanaged: true}
	t.cluster = newStub("cluster", "clusters", Flags{ListedWhenAsked: true}, t.sched, t.bucket, t.cred)
	t.project = newStub("project", "projects", Flags{}, t.cluster)
	t.root = newStub(RootKind, "", Flags{}, t.project)
	return t
}

func cfg(projects ...any) Obj { return Obj{"projects": projects} }

func project(name string, clusters ...any) Obj { return Obj{"name": name, "clusters": clusters} }

func cluster(name string, buckets ...string) Obj {
	return Obj{"name": name, "buckets": named(buckets...)}
}

func build(t *testing.T, tr *tree, c Obj, opts Options) *Plan {
	t.Helper()
	rc := &RunContext{Config: c}
	p, err := BuildPlan(context.Background(), rc, tr.root, opts)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return p
}

func TestBuildPlan_CreateAndNoop(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p-1", "nova")}
	tr.cluster.observed["nova"] = []Observed{obs("c-1", "dev")}
	tr.bucket.observed["dev"] = []Observed{obs("b-1", "orders")}
	p := build(t, tr, cfg(project("nova", cluster("dev", "orders", "events"))), Options{})
	want := []string{"noop project/nova", "noop cluster/dev", "noop bucket/dev/orders", "create bucket/dev/events"}
	if got := summarize(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if p.HasChanges() != true {
		t.Fatal("create is a change")
	}
}

func TestBuildPlan_ParentNotYetExisting(t *testing.T) {
	tr := newTree()
	p := build(t, tr, cfg(project("nova", cluster("dev", "orders"))), Options{})
	want := []string{"create project/nova", "create cluster/dev", "create bucket/dev/orders"}
	if got := summarize(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if n := p.Actions[0].Notes; len(n) != 0 {
		t.Fatalf("project has no parent note: %v", n)
	}
	if n := p.Actions[1].Notes; !reflect.DeepEqual(n, []string{"(after project is created)"}) {
		t.Fatalf("cluster note: %v", n)
	}
	if tr.cluster.observes != 0 || tr.bucket.observes != 0 {
		t.Fatalf("children of a missing parent must not be observed: %d %d", tr.cluster.observes, tr.bucket.observes)
	}
}

func TestBuildPlan_KindOrderAndProjectGrouping(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha"), obs("p2", "beta")}
	tr.cluster.observed["alpha"] = []Observed{obs("c1", "a1")}
	tr.cluster.observed["beta"] = []Observed{obs("c2", "b1")}
	tr.bucket.observed["a1"] = []Observed{obs("x", "b")}
	tr.bucket.observed["b1"] = []Observed{obs("y", "b")}
	c := cfg(
		project("alpha", Obj{"name": "a1", "buckets": named("b"), "databaseCredentials": named("u"), "hibernation": named("h")}),
		project("beta", Obj{"name": "b1", "buckets": named("b"), "databaseCredentials": named("u")}),
	)
	p := build(t, tr, c, Options{})
	want := []string{
		"noop project/alpha", "noop cluster/a1", "create schedule/h", "noop bucket/a1/b", "create credential/u",
		"noop project/beta", "noop cluster/b1", "noop bucket/b1/b", "create credential/u",
	}
	if got := summarize(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestBuildPlan_KindOrderWithinCluster(t *testing.T) {
	// Walk order is schedule, bucket, credential; ranks reorder a mixed list to the KindOrder.
	mk := func(kind, key string, parent *Action) *Action {
		a := NewAction(nil, kind, key, Noop)
		a.Parent = parent
		return a
	}
	pr := mk("project", "p", nil)
	cl := mk("cluster", "c", pr)
	actions := []*Action{pr, cl,
		mk("credential", "u", cl), mk("collection", "c1", cl), mk("scope", "s", cl), mk("bucket", "b", cl),
		mk("allowed-cidr", "1.2.3.4/32", cl), mk("backup-schedule", "c", cl), mk("schedule", "c", cl), mk("deletion-protection", "c", cl),
		mk("zzz", "z", cl),
	}
	var got []string
	for _, a := range byKind(actions) {
		got = append(got, a.Kind)
	}
	want := []string{"project", "cluster", "deletion-protection", "schedule", "backup-schedule", "allowed-cidr", "bucket", "scope", "collection", "credential", "zzz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestBuildPlan_Targets(t *testing.T) {
	setup := func() *tree {
		tr := newTree()
		tr.project.observed[""] = []Observed{obs("p1", "alpha"), obs("p2", "beta")}
		tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev")}
		tr.cluster.observed["beta"] = []Observed{obs("c2", "dev")}
		tr.bucket.observed["dev"] = []Observed{obs("x", "b")}
		return tr
	}
	c := cfg(project("alpha", cluster("dev", "b")), project("beta", cluster("dev", "b")))

	tests := []struct {
		name   string
		target string
		want   []string
	}{
		{"cluster by name hits both", "cluster/dev", []string{
			"noop project/alpha(hidden)", "noop cluster/dev", "noop bucket/dev/b",
			"noop project/beta(hidden)", "noop cluster/dev", "noop bucket/dev/b"}},
		{"project qualified", "cluster/alpha/dev", []string{
			"noop project/alpha(hidden)", "noop cluster/dev", "noop bucket/dev/b", "noop project/beta(hidden)"}},
		{"other project qualified", "cluster/beta/dev", []string{
			"noop project/alpha(hidden)", "noop project/beta(hidden)", "noop cluster/dev", "noop bucket/dev/b"}},
		{"project", "project/beta", []string{
			"noop project/alpha(hidden)", "noop project/beta", "noop cluster/dev", "noop bucket/dev/b"}},
		{"bucket qualified by cluster", "bucket/dev/b", []string{
			"noop project/alpha(hidden)", "noop cluster/dev(hidden)", "noop bucket/dev/b",
			"noop project/beta(hidden)", "noop cluster/dev(hidden)", "noop bucket/dev/b"}},
		{"slashes trimmed", " /cluster/alpha/dev/ ", []string{
			"noop project/alpha(hidden)", "noop cluster/dev", "noop bucket/dev/b", "noop project/beta(hidden)"}},
		{"unknown", "cluster/nope", []string{"noop project/alpha(hidden)", "noop project/beta(hidden)"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := build(t, setup(), c, Options{Targets: []string{tc.target}})
			if got := summarize(p); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestBuildPlan_SameKeyChildTarget(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev")}
	// schedule/dev: a one-per-cluster kind sharing the cluster's key.
	c := cfg(project("alpha", Obj{"name": "dev", "hibernation": named("dev")}))
	p := build(t, tr, c, Options{Targets: []string{"schedule/dev"}})
	want := []string{"noop project/alpha(hidden)", "noop cluster/dev(hidden)", "create schedule/dev"}
	if got := summarize(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestBuildPlan_HiddenAncestorStaysVisibleWhenCreated(t *testing.T) {
	tr := newTree()
	p := build(t, tr, cfg(project("alpha", cluster("dev", "b"))), Options{Targets: []string{"bucket/dev/b"}})
	want := []string{"create project/alpha", "create cluster/dev", "create bucket/dev/b"}
	if got := summarize(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestBuildPlan_NoDescendants(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	p := build(t, tr, cfg(project("alpha", cluster("dev", "b"))), Options{Targets: []string{"project/alpha"}, NoDescendants: true})
	if got := summarize(p); !reflect.DeepEqual(got, []string{"noop project/alpha"}) {
		t.Fatalf("got %v", got)
	}
	// Without targets, NoDescendants has nothing to prune and everything is planned.
	p = build(t, newTree(), cfg(project("alpha", cluster("dev"))), Options{NoDescendants: true})
	if len(p.Actions) != 2 {
		t.Fatalf("got %v", summarize(p))
	}
}

func TestBuildPlan_InvalidTarget(t *testing.T) {
	_, err := BuildPlan(context.Background(), &RunContext{}, newTree().root, Options{Targets: []string{"nova-dev-01"}})
	var te *TargetError
	if !errors.As(err, &te) {
		t.Fatalf("want TargetError, got %v", err)
	}
	if te.Msg != "invalid --target 'nova-dev-01': use KIND/NAME, for example cluster/nova-dev-01" {
		t.Fatal(te.Msg)
	}
}

func TestBuildPlan_AmbiguousMatchBlocked(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha"), obs("p2", "alpha")}
	tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev")}
	p := build(t, tr, cfg(project("alpha", cluster("dev"))), Options{})
	if len(p.Actions) != 1 {
		t.Fatalf("children of a blocked, unobserved action are skipped: %v", summarize(p))
	}
	a := p.Actions[0]
	if a.Type != Blocked || a.Topic != "idempotency" || a.Observed != nil {
		t.Fatalf("got %+v", a)
	}
	want := "2 projects share this name (ids: p1, p2), so cbctl cannot tell which one capella.yaml means; " +
		"'cbctl capella sync --force' rewrites the file from Capella with each one's id (the current file is kept as .bak)"
	if a.Reason != want {
		t.Fatalf("reason:\n%s", a.Reason)
	}
	if len(p.Blocked()) != 1 {
		t.Fatal("Blocked()")
	}
}

func TestBuildPlan_BlockedWithObservedStillWalksChildren(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	tr.project.diff = func(s *stub, d any, o *Observed, parent *Action) *Action {
		a := NewAction(s, "project", "alpha", Blocked)
		a.Observed, a.Reason = o, "nope"
		return a
	}
	p := build(t, tr, cfg(project("alpha", cluster("dev"))), Options{})
	if got := summarize(p); !reflect.DeepEqual(got, []string{"blocked project/alpha", "create cluster/dev"}) {
		t.Fatalf("got %v", got)
	}
}

func TestBuildPlan_BlockByAncestor(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev")}
	tr.cluster.block = func(a *Action, kind string) string {
		if kind == "schedule" {
			return ""
		}
		return "cluster is turned off"
	}
	c := cfg(project("alpha", Obj{"name": "dev", "buckets": named("b"), "hibernation": named("h")}))
	p := build(t, tr, c, Options{})
	byKey := map[string]*Action{}
	for _, a := range p.Actions {
		byKey[a.Kind] = a
	}
	if a := byKey["bucket"]; a.Type != Blocked || a.Reason != "cluster is turned off" || a.Topic != "hibernation" {
		t.Fatalf("bucket: %+v", a)
	}
	if a := byKey["schedule"]; a.Type != Create {
		t.Fatalf("schedule: %+v", a)
	}
	if a := byKey["cluster"]; a.Type != Noop {
		t.Fatalf("noop is never re-typed: %+v", a)
	}
}

func TestBuildPlan_ChildBlockReasonError(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	tr.project.blockErr = errors.New("block boom")
	_, err := BuildPlan(context.Background(), &RunContext{Config: cfg(project("alpha", cluster("dev")))}, tr.root, Options{})
	if err == nil || !strings.Contains(err.Error(), "block boom") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildPlan_PruneAndUnmanaged(t *testing.T) {
	setup := func() *tree {
		tr := newTree()
		tr.project.observed[""] = []Observed{obs("p1", "alpha"), obs("p9", "other")}
		tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev"), obs("c2", "extra")}
		tr.bucket.observed["dev"] = []Observed{obs("b1", "keep"), obs("b2", "stray")}
		tr.cred.observed["dev"] = []Observed{obs("u1", "olduser")}
		return tr
	}
	c := cfg(project("alpha", cluster("dev", "keep")))

	t.Run("plain: only reports kinds that report", func(t *testing.T) {
		p := build(t, setup(), c, Options{})
		if got := summarize(p); !reflect.DeepEqual(got, []string{"noop project/alpha", "noop cluster/dev", "noop bucket/dev/keep"}) {
			t.Fatalf("got %v", got)
		}
		if !reflect.DeepEqual(p.Unmanaged, []string{"credential dev/olduser"}) {
			t.Fatalf("unmanaged %v", p.Unmanaged)
		}
	})
	t.Run("prune deletes prunable kinds only", func(t *testing.T) {
		p := build(t, setup(), c, Options{Prune: true})
		want := []string{"noop project/alpha", "noop cluster/dev", "noop bucket/dev/keep", "delete bucket/stray", "delete credential/olduser"}
		if got := summarize(p); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v", got)
		}
		// Clusters are listed (never pruned) because prune asks.
		if !reflect.DeepEqual(p.Unmanaged, []string{"cluster alpha/extra  (not pruned: clusters are never pruned)"}) {
			t.Fatalf("unmanaged %v", p.Unmanaged)
		}
	})
	t.Run("drift lists everything undeclared", func(t *testing.T) {
		p := build(t, setup(), c, Options{AllUnmanaged: true})
		want := []string{
			"bucket dev/stray",
			"credential dev/olduser",
			"cluster alpha/extra  (not pruned: clusters are never pruned)",
		}
		if !reflect.DeepEqual(p.Unmanaged, want) {
			t.Fatalf("unmanaged %v", p.Unmanaged)
		}
	})
	t.Run("targets suppress undeclared", func(t *testing.T) {
		p := build(t, setup(), c, Options{Prune: true, Targets: []string{"cluster/dev"}})
		for _, a := range p.Actions {
			if a.Type == Delete && a.Kind == "bucket" {
				continue // inside the targeted cluster
			}
		}
		if len(p.Unmanaged) != 0 {
			t.Fatalf("unmanaged %v", p.Unmanaged)
		}
	})
	t.Run("unmanaged skips system objects", func(t *testing.T) {
		tr := setup()
		tr.cred.manages = func(o Observed) bool { return o.Body["name"] != "olduser" }
		p := build(t, tr, c, Options{})
		if len(p.Unmanaged) != 0 {
			t.Fatalf("unmanaged %v", p.Unmanaged)
		}
	})
}

func TestBuildPlan_PruneDeleteBlockedByAncestor(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev")}
	tr.bucket.observed["dev"] = []Observed{obs("b1", "stray")}
	tr.cluster.block = func(*Action, string) string { return "off" }
	p := build(t, tr, cfg(project("alpha", cluster("dev"))), Options{Prune: true})
	last := p.Actions[len(p.Actions)-1]
	if last.Kind != "bucket" || last.Type != Blocked || last.Reason != "off" {
		t.Fatalf("got %+v", last)
	}
}

func TestBuildPlan_PrunePlansUndeclaredWithoutDesired(t *testing.T) {
	// A kind with no desired items is still walked when it can prune or report.
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	tr.cluster.observed["alpha"] = []Observed{obs("c1", "dev")}
	tr.bucket.observed["dev"] = []Observed{obs("b1", "stray")}
	p := build(t, tr, cfg(project("alpha", Obj{"name": "dev"})), Options{Prune: true})
	if got := summarize(p); !reflect.DeepEqual(got, []string{"noop project/alpha", "noop cluster/dev", "delete bucket/stray"}) {
		t.Fatalf("got %v", got)
	}
}

func TestBuildPlan_Grouped(t *testing.T) {
	tr := newTree()
	g := newStub("grouped", "extras", Flags{Grouped: true})
	g.diff = func(s *stub, d any, o *Observed, parent *Action) *Action {
		list, _ := d.([]any)
		a := NewAction(s, "grouped", "all", Deferred)
		a.Summary = "n=" + string(rune('0'+len(list)))
		return a
	}
	tr.project.Kids = []Reconciler{g}
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	c := cfg(Obj{"name": "alpha", "extras": named("a", "b")})

	p := build(t, tr, c, Options{})
	if got := summarize(p); !reflect.DeepEqual(got, []string{"noop project/alpha", "deferred grouped/all"}) {
		t.Fatalf("got %v", got)
	}
	// Targeting the project includes its descendants; targeting something else does not.
	p = build(t, tr, c, Options{Targets: []string{"project/alpha"}})
	if len(p.Actions) != 2 {
		t.Fatalf("got %v", summarize(p))
	}
	p = build(t, tr, c, Options{Targets: []string{"grouped/zzz"}})
	if got := summarize(p); !reflect.DeepEqual(got, []string{"noop project/alpha(hidden)"}) {
		t.Fatalf("got %v", got)
	}
	p = build(t, tr, c, Options{Targets: []string{"grouped/all"}})
	if len(p.Actions) != 2 {
		t.Fatalf("got %v", summarize(p))
	}
}

func TestBuildPlan_NonRootTopLevel(t *testing.T) {
	tr := newTree()
	tr.project.observed[""] = []Observed{obs("p1", "alpha")}
	rc := &RunContext{Config: cfg(project("alpha"))}
	p, err := BuildPlan(context.Background(), rc, tr.project, Options{})
	if err != nil || len(p.Actions) != 1 {
		t.Fatalf("got %v, %v", p, err)
	}
}

func TestBuildPlan_ReconcilerErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name  string
		setup func(tr *tree)
	}{
		{"observe", func(tr *tree) {
			tr.project.Kids = []Reconciler{&failing{stub: newStub("cluster", "clusters", Flags{}), observe: boom}}
		}},
		{"match", func(tr *tree) {
			tr.project.Kids = []Reconciler{&failing{stub: newStub("cluster", "clusters", Flags{}), match: boom}}
		}},
		{"diff", func(tr *tree) {
			tr.project.Kids = []Reconciler{&failing{stub: newStub("cluster", "clusters", Flags{}), diff: boom}}
		}},
		{"delete", func(tr *tree) {
			tr.project.Kids = []Reconciler{&failing{stub: newStub("cluster", "clusters", Flags{Prunable: true}), del: boom}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree()
			tr.project.observed[""] = []Observed{obs("p1", "alpha")}
			tc.setup(tr)
			rc := &RunContext{Config: cfg(project("alpha", cluster("dev")))}
			_, err := BuildPlan(context.Background(), rc, tr.root, Options{Prune: true})
			if !errors.Is(err, boom) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

type failing struct {
	*stub
	observe, match, diff, del error
}

func (f *failing) Children() []Reconciler { return nil }
func (f *failing) Observe(ctx context.Context, rc *RunContext, p *Action) ([]Observed, error) {
	if f.observe != nil {
		return nil, f.observe
	}
	return []Observed{obs("x", "orphan")}, nil
}
func (f *failing) Match(d any, o []Observed) ([]Observed, error) {
	if f.match != nil {
		return nil, f.match
	}
	return f.stub.Match(d, o)
}
func (f *failing) Diff(ctx context.Context, rc *RunContext, d any, o *Observed, p *Action) (*Action, error) {
	if f.diff != nil {
		return nil, f.diff
	}
	return f.stub.Diff(ctx, rc, d, o, p)
}
func (f *failing) DeleteAction(ctx context.Context, rc *RunContext, o Observed, p *Action) (*Action, error) {
	if f.del != nil {
		return nil, f.del
	}
	return f.stub.DeleteAction(ctx, rc, o, p)
}

func TestMatchByName_And_Base(t *testing.T) {
	observed := []Observed{obs("1", "a"), obs("2", "b"), obs("3", "a")}
	if got := MatchByName(Obj{"name": "a"}, observed); len(got) != 2 {
		t.Fatal(got)
	}
	if MatchByName("x", observed) != nil || MatchByName(Obj{}, observed) != nil {
		t.Fatal("non-matching desired")
	}
	b := NewBase("widget", Flags{Prunable: true})
	if b.Kind() != "widget" || !b.Flags().Prunable || len(b.Children()) != 0 {
		t.Fatal("Base accessors")
	}
	if b.AfterChildrenNote() != "(after widget is created)" {
		t.Fatal(b.AfterChildrenNote())
	}
	if b.ReadyForChildren(&Action{}) || !b.ReadyForChildren(&Action{Observed: &Observed{}}) {
		t.Fatal("ReadyForChildren")
	}
	if !b.Manages(Observed{}) {
		t.Fatal("Manages")
	}
	if got := b.UnmanagedLabel(obs("1", "n"), nil); got != "widget n" {
		t.Fatal(got)
	}
	if got := b.UnmanagedLabel(Observed{ID: "7"}, &Action{Key: "p"}); got != "widget p/7" {
		t.Fatal(got)
	}
	ctx := context.Background()
	if _, err := b.Diff(ctx, nil, nil, nil, nil); err == nil {
		t.Fatal("Diff stub must error")
	}
	if _, err := b.DeleteAction(ctx, nil, Observed{}, nil); err == nil {
		t.Fatal("DeleteAction stub must error")
	}
	if err := b.Apply(ctx, nil, nil); err == nil {
		t.Fatal("Apply stub must error")
	}
	if err := b.Wait(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.EnsureReady(ctx, nil, nil, nil); !ok || err != nil {
		t.Fatal("EnsureReady")
	}
	if r, err := b.ChildBlockReason(ctx, nil, nil, "x"); r != "" || err != nil {
		t.Fatal("ChildBlockReason")
	}
	if items := b.DesiredItems(nil); items != nil || b.Key(nil, nil) != "" {
		t.Fatal("DesiredItems/Key")
	}
	if o, err := b.Observe(ctx, nil, nil); o != nil || err != nil {
		t.Fatal("Observe")
	}
	if _, err := b.Refresh(ctx, nil, &Action{}); err == nil {
		t.Fatal("unbound Refresh must error")
	}
}

func TestRefresh_ErrorsAndNotFound(t *testing.T) {
	s := newStub("thing", "things", Flags{})
	s.observed[""] = []Observed{obs("1", "a")}
	a := NewAction(s, "thing", "b", Create)
	a.Desired = Obj{"name": "b"}
	fresh, err := s.Refresh(context.Background(), &RunContext{}, a)
	if err != nil || fresh.Type != Create || fresh.Observed != nil {
		t.Fatalf("not found re-diffs as create: %+v %v", fresh, err)
	}
	a.Desired = Obj{"name": "a"}
	fresh, _ = s.Refresh(context.Background(), &RunContext{}, a)
	if fresh.Type != Noop || fresh.Observed == nil || fresh.Observed.ID != "1" {
		t.Fatalf("found re-diffs as noop: %+v", fresh)
	}
	boom := errors.New("boom")
	if _, err := Refresh(context.Background(), &RunContext{}, &failing{stub: s, observe: boom}, a); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := Refresh(context.Background(), &RunContext{}, &failing{stub: s, match: boom}, a); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestMarkMatchedWithoutIDs(t *testing.T) {
	observed := []Observed{{Body: Obj{"name": "a"}}, {Body: Obj{"name": "b"}}}
	matched := make([]bool, 2)
	markMatched(observed, []Observed{{Body: Obj{"name": "b"}}}, matched)
	if matched[0] || !matched[1] {
		t.Fatal(matched)
	}
}
