package plan

import (
	"context"
	"fmt"
)

// stub is a configurable Reconciler for planner and executor tests.
type stub struct {
	Base
	field    string                // key of the parent desired mapping that lists this kind's items
	observed map[string][]Observed // by parent key ("" for no parent)
	diff     func(s *stub, desired any, observed *Observed, parent *Action) *Action
	apply    func(s *stub, a *Action) error
	refresh  func(s *stub, a *Action) (*Action, error)
	wait     func(a *Action) error
	ready    func(a *Action, child *Action) (bool, error)
	block    func(a *Action, childKind string) string
	blockErr error
	qualify  bool // key is "<parent key>/<name>", like buckets
	manages  func(o Observed) bool
	observes int
	applied  []string
}

func newStub(kind, field string, flags Flags, children ...Reconciler) *stub {
	s := &stub{Base: NewBase(kind, flags, children...), field: field, observed: map[string][]Observed{}}
	s.Bind(s)
	return s
}

func (s *stub) DesiredItems(parent any) []any {
	m, ok := parent.(Obj)
	if !ok {
		return nil
	}
	list, _ := AsList(m[s.field])
	return list
}

func (s *stub) Key(desired any, parent *Action) string {
	if m, ok := desired.(Obj); ok {
		name, _ := m["name"].(string)
		if s.qualify && parent != nil {
			return parent.Key + "/" + name
		}
		return name
	}
	return s.Name
}

func (s *stub) Observe(_ context.Context, _ *RunContext, parent *Action) ([]Observed, error) {
	s.observes++
	key := ""
	if parent != nil {
		key = parent.Key
	}
	return s.observed[key], nil
}

func (s *stub) Manages(o Observed) bool {
	if s.manages != nil {
		return s.manages(o)
	}
	return true
}

func (s *stub) Diff(_ context.Context, _ *RunContext, desired any, observed *Observed, parent *Action) (*Action, error) {
	if s.diff != nil {
		return s.diff(s, desired, observed, parent), nil
	}
	t := Create
	if observed != nil {
		t = Noop
	}
	a := NewAction(s, s.Name, s.Key(desired, parent), t)
	a.Desired, a.Observed, a.Parent = desired, observed, parent
	return a, nil
}

func (s *stub) DeleteAction(_ context.Context, _ *RunContext, o Observed, parent *Action) (*Action, error) {
	name, _ := o.Body["name"].(string)
	a := NewAction(s, s.Name, name, Delete)
	a.Observed, a.Parent = &o, parent
	return a, nil
}

func (s *stub) Apply(_ context.Context, _ *RunContext, a *Action) error {
	s.applied = append(s.applied, a.Key)
	if s.apply != nil {
		return s.apply(s, a)
	}
	if a.Type == Create {
		a.CreatedID = "id-" + a.Key
	}
	return nil
}

func (s *stub) Wait(_ context.Context, _ *RunContext, a *Action) error {
	if s.wait != nil {
		return s.wait(a)
	}
	return nil
}

func (s *stub) Refresh(ctx context.Context, rc *RunContext, a *Action) (*Action, error) {
	if s.refresh != nil {
		return s.refresh(s, a)
	}
	return s.Base.Refresh(ctx, rc, a)
}

func (s *stub) EnsureReady(_ context.Context, _ *RunContext, a *Action, child *Action) (bool, error) {
	if s.ready != nil {
		return s.ready(a, child)
	}
	return true, nil
}

func (s *stub) ChildBlockReason(_ context.Context, _ *RunContext, a *Action, childKind string) (string, error) {
	if s.blockErr != nil {
		return "", s.blockErr
	}
	if s.block != nil {
		return s.block(a, childKind), nil
	}
	return "", nil
}

func obs(id, name string) Observed { return Observed{ID: id, Body: Obj{"name": name}} }

func named(names ...string) []any {
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = Obj{"name": n}
	}
	return out
}

func summarize(p *Plan) []string {
	var out []string
	for _, a := range p.Actions {
		h := ""
		if a.Hidden {
			h = "(hidden)"
		}
		out = append(out, fmt.Sprintf("%s %s/%s%s", a.Type, a.Kind, a.Key, h))
	}
	return out
}
