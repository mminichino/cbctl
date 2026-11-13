package plan

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Options tune BuildPlan.
type Options struct {
	// Targets are "kind/name" selectors (repeatable). Cluster targets also accept "project/cluster".
	Targets []string
	// NoDescendants plans targeted resources without their children (for example `project update`).
	NoDescendants bool
	// Prune also plans DELETE for prunable kinds present in Capella but absent from the file.
	Prune bool
	// AllUnmanaged (drift) lists every undeclared resource of a prunable kind and undeclared clusters.
	AllUnmanaged bool
}

// KindOrder ranks kinds within one cluster: every resource of a kind comes before the next kind
// (all buckets, then all scopes, then all collections), so apply reads top-down and destroy, the
// reverse, bottom-up. Dependencies still hold: each kind ranks after its parent's.
var KindOrder = []string{
	"project", "cluster", "deletion-protection", "schedule", "backup-schedule", "allowed-cidr",
	"bucket", "bucket-backup-schedule", "scope", "collection", "credential",
}

type target struct{ kind, path string }

func normalizeTargets(targets []string) ([]target, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	out := make([]target, 0, len(targets))
	for _, t := range targets {
		t = strings.Trim(strings.TrimSpace(t), "/")
		kind, path, ok := strings.Cut(t, "/")
		if !ok {
			return nil, &TargetError{Msg: fmt.Sprintf("invalid --target '%s': use KIND/NAME, for example cluster/nova-dev-01", t)}
		}
		out = append(out, target{kind, path})
	}
	return out, nil
}

// targetMatch is "self" if targeted, "ancestor" if a target is below it, "" otherwise. alt is the
// project-qualified key of a cluster ("PROJECT/NAME"), accepted when a name repeats.
func targetMatch(kind, key string, targets []target, alt string, hasAlt bool) string {
	if len(targets) == 0 {
		return "self"
	}
	rel := ""
	for _, t := range targets {
		if t.kind == kind && (t.path == key || (hasAlt && t.path == alt)) {
			return "self"
		}
		if t.kind == kind && hasAlt && strings.Count(t.path, "/") == strings.Count(alt, "/") && t.path != alt {
			continue // qualified target for the same name in another project
		}
		below := strings.HasPrefix(t.path, key+"/") || (hasAlt && strings.HasPrefix(t.path, alt+"/"))
		// schedule/NAME: one per cluster, so it shares the cluster's key.
		sameKeyChild := t.kind != kind && (t.path == key || (hasAlt && t.path == alt))
		if kind == "project" || below || sameKeyChild {
			rel = "ancestor"
		}
	}
	return rel
}

type builder struct {
	rc          *RunContext
	opts        Options
	targets     []target
	descendants bool
	actions     []*Action
	unmanaged   []string
}

// BuildPlan diffs the desired state (rc.Config, walked through root's reconcilers) against what
// Capella reports. A root of Kind RootKind is not planned itself; its children are walked with the
// configuration as their parent desired value.
func BuildPlan(ctx context.Context, rc *RunContext, root Reconciler, opts Options) (*Plan, error) {
	tgts, err := normalizeTargets(opts.Targets)
	if err != nil {
		return nil, err
	}
	b := &builder{rc: rc, opts: opts, targets: tgts, descendants: !opts.NoDescendants}
	if root.Kind() == RootKind {
		for _, child := range root.Children() {
			if err := b.walk(ctx, child, rc.Config, nil, ""); err != nil {
				return nil, err
			}
		}
	} else if err := b.walk(ctx, root, rc.Config, nil, ""); err != nil {
		return nil, err
	}
	return &Plan{Actions: byKind(b.actions), Unmanaged: b.unmanaged}, nil
}

func inScope(parentTarget string) bool { return parentTarget == "self" || parentTarget == "descendant" }

func (b *builder) walk(ctx context.Context, rec Reconciler, parentDesired any, parent *Action, parentTarget string) error {
	desiredList := rec.DesiredItems(parentDesired)
	fl := rec.Flags()
	lists := fl.ReportsUnmanaged || (b.opts.AllUnmanaged && fl.Prunable) ||
		(fl.ListedWhenAsked && (b.opts.AllUnmanaged || b.opts.Prune))
	looksForUndeclared := !fl.Grouped && ((b.opts.Prune && fl.Prunable) || lists)
	if len(desiredList) == 0 && !looksForUndeclared {
		return nil
	}
	kind := rec.Kind()

	if fl.Grouped {
		action, err := rec.Diff(ctx, b.rc, desiredList, nil, parent)
		if err != nil {
			return err
		}
		if inScope(parentTarget) || targetMatch(kind, action.Key, b.targets, "", false) == "self" {
			b.actions = append(b.actions, action)
		}
		return nil
	}

	parentExists := parent == nil || parent.Reconciler.ReadyForChildren(parent)
	var observed []Observed
	if parentExists {
		var err error
		if observed, err = rec.Observe(ctx, b.rc, parent); err != nil {
			return err
		}
	}
	matched := make([]bool, len(observed))
	for _, d := range desiredList {
		key := rec.Key(d, parent)
		var alt string
		hasAlt := kind == "cluster" && parent != nil
		if hasAlt {
			alt = parent.Key + "/" + key
		}
		rel := "descendant"
		if !inScope(parentTarget) {
			rel = targetMatch(kind, key, b.targets, alt, hasAlt)
		}
		if rel == "" {
			continue
		}
		matches, err := rec.Match(d, observed)
		if err != nil {
			return err
		}
		markMatched(observed, matches, matched)

		var action *Action
		if len(matches) > 1 {
			ids := make([]string, len(matches))
			for i, m := range matches {
				ids[i] = m.ID
			}
			action = NewAction(rec, kind, key, Blocked)
			action.Desired = d
			action.Parent = parent
			action.Reason = fmt.Sprintf("%d %ss share this name (ids: %s), so cbctl cannot tell which one capella.yaml means; "+
				"'cbctl capella sync --force' rewrites the file from Capella with each one's id (the current file is kept as .bak)",
				len(matches), kind, strings.Join(ids, ", "))
			action.Topic = "idempotency"
		} else {
			var obs *Observed
			if len(matches) == 1 {
				m := matches[0]
				obs = &m
			}
			if action, err = rec.Diff(ctx, b.rc, d, obs, parent); err != nil {
				return err
			}
		}
		if parent != nil && !parentExists && action.Type == Create {
			action.Notes = append(action.Notes, parent.Reconciler.AfterChildrenNote())
		}
		if err := b.blockByAncestor(ctx, action); err != nil {
			return err
		}
		if rel == "ancestor" && action.Type != Create {
			action.Hidden = true
		}
		b.actions = append(b.actions, action)
		if rel == "self" && len(b.targets) > 0 && !b.descendants {
			continue
		}
		if action.Type == Blocked && action.Observed == nil {
			continue // nothing exists below a resource that is blocked before it is created
		}
		for _, child := range rec.Children() {
			if err := b.walk(ctx, child, d, action, rel); err != nil {
				return err
			}
		}
	}

	if len(b.targets) == 0 || inScope(parentTarget) {
		var undeclared []Observed
		for i, o := range observed {
			if !matched[i] && rec.Manages(o) {
				undeclared = append(undeclared, o)
			}
		}
		switch {
		case b.opts.Prune && fl.Prunable:
			for _, o := range undeclared {
				deletion, err := rec.DeleteAction(ctx, b.rc, o, parent)
				if err != nil {
					return err
				}
				if err := b.blockByAncestor(ctx, deletion); err != nil {
					return err
				}
				b.actions = append(b.actions, deletion)
			}
		case lists:
			note := ""
			if !fl.Prunable {
				note = fmt.Sprintf("  (not pruned: %ss are never pruned)", kind)
			}
			for _, o := range undeclared {
				b.unmanaged = append(b.unmanaged, rec.UnmanagedLabel(o, parent)+note)
			}
		}
	}
	return nil
}

// markMatched flags the observed entries that the matches copy. Entries are identified by id, or by
// body equality when they have none.
func markMatched(observed, matches []Observed, matched []bool) {
	for _, m := range matches {
		for i, o := range observed {
			if matched[i] {
				continue
			}
			if (m.ID != "" && o.ID == m.ID) || (m.ID == "" && o.ID == "" && reflect.DeepEqual(o.Body, m.Body)) {
				matched[i] = true
				break
			}
		}
	}
}

// blockByAncestor turns a write below a resource that cannot take it now (for example a
// turned-off cluster) into BLOCKED.
func (b *builder) blockByAncestor(ctx context.Context, a *Action) error {
	if !a.Type.IsWrite() {
		return nil
	}
	for anc := a.Parent; anc != nil; anc = anc.Parent {
		reason, err := anc.Reconciler.ChildBlockReason(ctx, b.rc, anc, a.Kind)
		if err != nil {
			return err
		}
		if reason != "" {
			a.Type, a.Reason, a.Topic = Blocked, reason, "hibernation"
			return nil
		}
	}
	return nil
}

func kindRank(kind string) int {
	for i, k := range KindOrder {
		if k == kind {
			return i
		}
	}
	return len(KindOrder)
}

// byKind orders the actions by (first appearance of project, first appearance of cluster, kind
// rank, original index).
func byKind(actions []*Action) []*Action {
	owner := func(a *Action, kind string) *Action {
		if a.Kind == kind {
			return a
		}
		return a.Ancestor(kind)
	}
	first := map[*Action]int{}
	for i, a := range actions {
		for _, kind := range []string{"project", "cluster"} {
			if o := owner(a, kind); o != nil {
				if _, seen := first[o]; !seen {
					first[o] = i
				}
			}
		}
	}
	type keyed struct {
		k [4]int
		a *Action
	}
	items := make([]keyed, len(actions))
	for i, a := range actions {
		p, c := owner(a, "project"), owner(a, "cluster")
		k0, k1 := i, -1
		if p != nil {
			k0 = first[p]
		}
		if c != nil {
			k1 = first[c]
		}
		items[i] = keyed{[4]int{k0, k1, kindRank(a.Kind), i}, a}
	}
	sort.SliceStable(items, func(x, y int) bool {
		for n := range items[x].k {
			if items[x].k[n] != items[y].k[n] {
				return items[x].k[n] < items[y].k[n]
			}
		}
		return false
	})
	out := make([]*Action, len(items))
	for i, it := range items {
		out[i] = it.a
	}
	return out
}
