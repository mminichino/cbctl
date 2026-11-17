// Package plan is the declarative engine core: it diffs the desired state from capella.yaml
// against what Capella reports (BuildPlan), applies the result in dependency order (Execute)
// and renders both for people and machines. Concrete per-kind reconcilers live in
// internal/resources and implement Reconciler; this package never imports them.
package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/redact"
)

// Obj is a JSON-compatible mapping. It is the same type as capellafile.Obj (an alias of
// map[string]any), so values flow between the packages without conversion.
type Obj = map[string]any

// ActionType is what the plan intends to do with one resource.
type ActionType string

// Action types.
const (
	Noop     ActionType = "noop"
	Create   ActionType = "create"
	Update   ActionType = "update"
	Replace  ActionType = "replace"
	Delete   ActionType = "delete"
	Blocked  ActionType = "blocked"
	Deferred ActionType = "deferred"
)

// Symbol is the one-character marker printed in front of each action in plan text.
var Symbol = map[ActionType]string{
	Create:   "+",
	Update:   "~",
	Replace:  "±",
	Delete:   "-",
	Noop:     "=",
	Blocked:  "!",
	Deferred: "…",
}

// IsWrite reports whether the action type changes Capella.
func (t ActionType) IsWrite() bool {
	switch t {
	case Create, Update, Replace, Delete:
		return true
	}
	return false
}

// Change is one field difference inside an Update or Replace action.
type Change struct {
	Path   string
	Before any
	After  any
}

// ToJSON returns the stable JSON form of the change.
func (c Change) ToJSON() map[string]any {
	return map[string]any{"path": c.Path, "before": c.Before, "after": c.After}
}

// Observed is one resource as Capella reports it. An empty ID means it has none.
type Observed struct {
	ID   string
	Body Obj
	ETag string
}

// RenamedNote is attached to a pinned project or cluster whose name differs in Capella.
const RenamedNote = "(renamed in Capella? 'cbctl capella sync' keeps the new name; apply renames it back)"

// Action is one planned step for one resource.
type Action struct {
	Kind       string
	Key        string
	Type       ActionType
	Reconciler Reconciler
	// Desired is the file's mapping for the resource (an Obj; a bool for deletion-protection).
	Desired  any
	Observed *Observed
	Parent   *Action
	Changes  []Change

	Reason  string
	Topic   string
	Summary string
	Label   string
	Notes   []string
	// Remarks are own lines printed under the changes ("note: …", "checks: …").
	Remarks []string
	// Hidden actions are context only: a parent that is looked up but not changed.
	Hidden bool

	// CreatedID, Status, Duration and Err are filled during apply. Status is one of
	// pending, done, failed, skipped, deferred, blocked, unchanged, interrupted.
	CreatedID string
	Status    string
	Duration  time.Duration
	Err       string
	// Data is scratch space for the reconciler (state, free, power_on, sent, body, …).
	Data map[string]any
}

// NewAction returns a pending action of the given kind, key and type owned by rec.
func NewAction(rec Reconciler, kind, key string, t ActionType) *Action {
	return &Action{Kind: kind, Key: key, Type: t, Reconciler: rec, Status: "pending", Data: map[string]any{}}
}

// Set stores a scratch value on the action, allocating Data on first use.
func (a *Action) Set(key string, value any) {
	if a.Data == nil {
		a.Data = map[string]any{}
	}
	a.Data[key] = value
}

// ID is the created id if the action created the resource, else the observed id.
func (a *Action) ID() string {
	if a.CreatedID != "" {
		return a.CreatedID
	}
	if a.Observed != nil {
		return a.Observed.ID
	}
	return ""
}

// Exists is true when the resource exists in Capella now or was created during this run.
func (a *Action) Exists() bool {
	return a.ID() != "" && a.Type != Delete
}

// Ancestor returns the nearest ancestor action of the given kind, or nil.
func (a *Action) Ancestor(kind string) *Action {
	for p := a.Parent; p != nil; p = p.Parent {
		if p.Kind == kind {
			return p
		}
	}
	return nil
}

// ToJSON returns the stable JSON form of the action.
func (a *Action) ToJSON() map[string]any {
	changes := make([]any, 0, len(a.Changes))
	for _, c := range a.Changes {
		changes = append(changes, c.ToJSON())
	}
	out := map[string]any{
		"kind":    a.Kind,
		"key":     a.Key,
		"action":  string(a.Type),
		"changes": changes,
	}
	if a.Reason != "" {
		out["reason"] = a.Reason
	}
	if a.Summary != "" {
		out["summary"] = a.Summary
	}
	if len(a.Notes) > 0 || len(a.Remarks) > 0 {
		notes := make([]any, 0, len(a.Notes)+len(a.Remarks))
		for _, n := range a.Notes {
			notes = append(notes, n)
		}
		for _, n := range a.Remarks {
			notes = append(notes, n)
		}
		out["notes"] = notes
	}
	return out
}

// Plan is the ordered list of actions plus the undeclared resources found in Capella.
type Plan struct {
	Actions []*Action
	// Unmanaged holds one display label per resource in Capella that the file does not declare,
	// for kinds that report them. Only `apply --prune` deletes them.
	Unmanaged []string
}

// Visible returns the actions that are not context-only.
func (p *Plan) Visible() []*Action {
	out := make([]*Action, 0, len(p.Actions))
	for _, a := range p.Actions {
		if !a.Hidden {
			out = append(out, a)
		}
	}
	return out
}

// Count returns the number of visible actions of type t.
func (p *Plan) Count(t ActionType) int {
	n := 0
	for _, a := range p.Visible() {
		if a.Type == t {
			n++
		}
	}
	return n
}

// HasChanges reports whether any visible action writes to Capella.
func (p *Plan) HasChanges() bool {
	for _, a := range p.Visible() {
		if a.Type.IsWrite() {
			return true
		}
	}
	return false
}

// Blocked returns the visible blocked actions.
func (p *Plan) Blocked() []*Action {
	var out []*Action
	for _, a := range p.Visible() {
		if a.Type == Blocked {
			out = append(out, a)
		}
	}
	return out
}

// SummaryLine is "Plan: A to add, C to change, D to delete, B blocked." where A counts creates and
// replaces; with deferred actions the final "." becomes ", N deferred.".
func (p *Plan) SummaryLine() string {
	line := fmt.Sprintf("Plan: %d to add, %d to change, %d to delete, %d blocked.",
		p.Count(Create)+p.Count(Replace), p.Count(Update), p.Count(Delete), p.Count(Blocked))
	if n := p.Count(Deferred); n > 0 {
		line = strings.TrimSuffix(line, ".") + fmt.Sprintf(", %d deferred.", n)
	}
	return line
}

// ToJSON returns {"actions": [visible…], "unmanaged"?: […]}.
func (p *Plan) ToJSON() map[string]any {
	actions := make([]any, 0, len(p.Actions))
	for _, a := range p.Visible() {
		actions = append(actions, a.ToJSON())
	}
	out := map[string]any{"actions": actions}
	if len(p.Unmanaged) > 0 {
		un := make([]any, len(p.Unmanaged))
		for i, u := range p.Unmanaged {
			un[i] = u
		}
		out["unmanaged"] = un
	}
	return out
}

// PlanError means planning cannot continue, for example when a pinned id no longer exists. Exit 1.
type PlanError struct{ Msg string }

// Error implements error.
func (e *PlanError) Error() string { return redact.String(e.Msg) }

// TargetError reports a malformed --target value. The CLI maps it to exit code 3.
type TargetError struct{ Msg string }

// Error implements error.
func (e *TargetError) Error() string { return e.Msg }

// Flags are the per-kind planning switches.
type Flags struct {
	// Grouped: one summarised action for all desired items.
	Grouped bool
	// Prunable: `apply --prune` deletes observed items the file no longer declares.
	Prunable bool
	// ReportsUnmanaged: `plan` lists observed items the file does not declare.
	ReportsUnmanaged bool
	// ListedWhenAsked: `drift` and `apply --prune` list undeclared items (never pruned).
	ListedWhenAsked bool
}

// RootKind is the Kind of the synthetic root reconciler. BuildPlan does not plan a root; it walks
// the root's children with the configuration as their parent desired value.
const RootKind = "root"

// Reconciler is implemented once per resource kind (internal/resources). Embed Base for the
// optional methods.
type Reconciler interface {
	Kind() string
	Flags() Flags
	Children() []Reconciler
	DesiredItems(parentDesired any) []any
	Key(desired any, parent *Action) string
	Observe(ctx context.Context, rc *RunContext, parent *Action) ([]Observed, error)
	// Match returns the observed resources the desired item refers to. The default matches by "name".
	Match(desired any, observed []Observed) ([]Observed, error)
	// Manages is false for system objects, which are never pruned or listed as unmanaged.
	Manages(o Observed) bool
	UnmanagedLabel(o Observed, parent *Action) string
	Diff(ctx context.Context, rc *RunContext, desired any, observed *Observed, parent *Action) (*Action, error)
	DeleteAction(ctx context.Context, rc *RunContext, o Observed, parent *Action) (*Action, error)
	// ReadyForChildren reports whether children can be observed now. Default: the resource exists.
	ReadyForChildren(a *Action) bool
	AfterChildrenNote() string
	Apply(ctx context.Context, rc *RunContext, a *Action) error
	Wait(ctx context.Context, rc *RunContext, a *Action) error
	// Refresh re-observes one resource and re-diffs it (after 409, 412 or an ambiguous write).
	Refresh(ctx context.Context, rc *RunContext, a *Action) (*Action, error)
	// EnsureReady is called before child is applied. It may wait; false defers the child.
	EnsureReady(ctx context.Context, rc *RunContext, a *Action, child *Action) (bool, error)
	// ChildBlockReason says why a change to a childKind resource below a cannot be made now.
	ChildBlockReason(ctx context.Context, rc *RunContext, a *Action, childKind string) (string, error)
}

// Base supplies default implementations of Reconciler's optional methods. Embed it by value in a
// reconciler, build it with NewBase and call Bind(self) once so that Refresh can reach the
// embedding type's Observe, Match and Diff.
type Base struct {
	Name string
	Flag Flags
	Kids []Reconciler
	self Reconciler
}

// NewBase returns a Base for the given kind.
func NewBase(kind string, flags Flags, children ...Reconciler) Base {
	return Base{Name: kind, Flag: flags, Kids: children}
}

// Bind records the embedding reconciler.
func (b *Base) Bind(self Reconciler) { b.self = self }

// Kind returns the resource kind.
func (b *Base) Kind() string { return b.Name }

// Flags returns the planning switches.
func (b *Base) Flags() Flags { return b.Flag }

// Children returns the child reconcilers.
func (b *Base) Children() []Reconciler { return b.Kids }

// DesiredItems has no items by default.
func (b *Base) DesiredItems(any) []any { return nil }

// Key is empty by default; every concrete reconciler overrides it.
func (b *Base) Key(any, *Action) string { return "" }

// Observe observes nothing by default.
func (b *Base) Observe(context.Context, *RunContext, *Action) ([]Observed, error) { return nil, nil }

// Match matches observed resources whose "name" equals the desired "name".
func (b *Base) Match(desired any, observed []Observed) ([]Observed, error) {
	return MatchByName(desired, observed), nil
}

// Manages is true by default.
func (b *Base) Manages(Observed) bool { return true }

// UnmanagedLabel is "<kind> <parent key>/<name>" or "<kind> <name>".
func (b *Base) UnmanagedLabel(o Observed, parent *Action) string {
	name, _ := o.Body["name"].(string)
	if name == "" {
		name = o.ID
	}
	if parent != nil {
		return fmt.Sprintf("%s %s/%s", b.Name, parent.Key, name)
	}
	return fmt.Sprintf("%s %s", b.Name, name)
}

// Diff must be overridden.
func (b *Base) Diff(context.Context, *RunContext, any, *Observed, *Action) (*Action, error) {
	return nil, fmt.Errorf("%s cannot be planned by this build", b.Name)
}

// DeleteAction must be overridden by prunable kinds.
func (b *Base) DeleteAction(context.Context, *RunContext, Observed, *Action) (*Action, error) {
	return nil, fmt.Errorf("%s cannot be pruned by this build", b.Name)
}

// ReadyForChildren is true once the resource exists in Capella.
func (b *Base) ReadyForChildren(a *Action) bool { return a.Observed != nil }

// AfterChildrenNote is the note on a child planned beneath a parent that does not exist yet.
func (b *Base) AfterChildrenNote() string { return fmt.Sprintf("(after %s is created)", b.Name) }

// Apply must be overridden by kinds that write.
func (b *Base) Apply(context.Context, *RunContext, *Action) error {
	return fmt.Errorf("%s cannot be applied by this build", b.Name)
}

// Wait does not wait by default.
func (b *Base) Wait(context.Context, *RunContext, *Action) error { return nil }

// Refresh observes, matches and diffs through the bound reconciler.
func (b *Base) Refresh(ctx context.Context, rc *RunContext, a *Action) (*Action, error) {
	if b.self == nil {
		return nil, fmt.Errorf("%s cannot refresh: Base.Bind was not called", b.Name)
	}
	return Refresh(ctx, rc, b.self, a)
}

// EnsureReady is always ready by default.
func (b *Base) EnsureReady(context.Context, *RunContext, *Action, *Action) (bool, error) {
	return true, nil
}

// ChildBlockReason never blocks by default.
func (b *Base) ChildBlockReason(context.Context, *RunContext, *Action, string) (string, error) {
	return "", nil
}

// MatchByName returns the observed resources whose "name" equals the desired mapping's "name".
func MatchByName(desired any, observed []Observed) []Observed {
	d, ok := desired.(Obj)
	if !ok {
		return nil
	}
	name, ok := d["name"]
	if !ok {
		return nil
	}
	var out []Observed
	for _, o := range observed {
		if v, ok := o.Body["name"]; ok && v == name {
			out = append(out, o)
		}
	}
	return out
}

// Refresh re-observes the action's resource through rec and re-diffs it.
func Refresh(ctx context.Context, rc *RunContext, rec Reconciler, a *Action) (*Action, error) {
	observed, err := rec.Observe(ctx, rc, a.Parent)
	if err != nil {
		return nil, err
	}
	found, err := rec.Match(a.Desired, observed)
	if err != nil {
		return nil, err
	}
	var obs *Observed
	if len(found) > 0 {
		obs = &found[0]
	}
	return rec.Diff(ctx, rc, a.Desired, obs, a.Parent)
}
