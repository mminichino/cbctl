package resources

import (
	"context"
	"fmt"

	"github.com/mminichino/cbctl/internal/plan"
)

const kindDeletionProtection = "deletion-protection"

// protectionRec reconciles `deletionProtection: true|false` on a cluster; omitting it leaves the
// value unchanged. The current value is the cluster's own field, so planning needs no extra GET.
// It runs after the cluster is healthy and before the buckets (the cluster's first child).
type protectionRec struct{ plan.Base }

func newDeletionProtection() *protectionRec {
	r := &protectionRec{Base: plan.NewBase(kindDeletionProtection, plan.Flags{})}
	r.Bind(r)
	return r
}

func (r *protectionRec) DesiredItems(parent any) []any {
	d := objOf(parent)
	if v, ok := d["deletionProtection"]; ok {
		return []any{truthy(v)}
	}
	return nil
}

func (r *protectionRec) Key(_ any, parent *plan.Action) string {
	if parent == nil {
		return ""
	}
	return parent.Key
}

func (r *protectionRec) Observe(_ context.Context, _ *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	if parent == nil || parent.Observed == nil {
		return nil, nil
	}
	return []plan.Observed{{ID: parent.ID(), Body: Obj{"deletionProtection": truthy(parent.Observed.Body["deletionProtection"])}}}, nil
}

func (r *protectionRec) Match(_ any, observed []plan.Observed) ([]plan.Observed, error) {
	if len(observed) > 1 {
		return observed[:1], nil
	}
	return observed, nil
}

func (r *protectionRec) Diff(_ context.Context, _ *plan.RunContext, desired any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	want := truthy(desired)
	key := r.Key(desired, parent)
	if observed == nil { // new cluster: Capella creates it unprotected
		if !want {
			a := newAction(r, kindDeletionProtection, key, plan.Noop, want, nil, parent)
			a.Hidden = true
			return a, nil
		}
		a := newAction(r, kindDeletionProtection, key, plan.Create, want, nil, parent)
		a.Summary = "deletionProtection: true"
		return a, nil
	}
	current := truthy(observed.Body["deletionProtection"])
	if current == want {
		return newAction(r, kindDeletionProtection, key, plan.Noop, want, observed, parent), nil
	}
	a := newAction(r, kindDeletionProtection, key, plan.Update, want, observed, parent)
	a.Changes = []plan.Change{{Path: "deletionProtection", Before: current, After: want}}
	return a, nil
}

func (r *protectionRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	cluster := a.Parent
	pid := projectID(a)
	if cluster == nil || cluster.ID() == "" || pid == "" {
		return fmt.Errorf("deletion protection for %s: the cluster has no id yet", a.Key)
	}
	want := truthy(a.Desired)
	if err := rc.Client.SetDeletionProtection(ctx, pid, cluster.ID(), want); err != nil {
		return err
	}
	if cluster.Observed != nil { // later delete guards in the same run see the new value
		cluster.Observed.Body["deletionProtection"] = want
	}
	return nil
}
