package plan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/redact"
)

// ConcurrentChange reports a resource that changed between planning and applying, twice in a row.
type ConcurrentChange struct{ Msg string }

// Error implements error.
func (e *ConcurrentChange) Error() string { return e.Msg }

// interruptedMessage is the error text of an action whose wait was cancelled.
const interruptedMessage = "stopped waiting; the operation continues in Capella"

// ApplyResult is what Execute did. The first failure stops the run, so at most one action is Failed.
type ApplyResult struct {
	Actions     []*Action
	Failed      *Action
	Err         error
	Interrupted bool
	// Extra is merged into the JSON form (for example connectionStrings).
	Extra map[string]any
}

// OK is true when nothing failed and the run was not interrupted.
func (r *ApplyResult) OK() bool { return r.Failed == nil && !r.Interrupted }

// ToJSON returns {"status", "actions": [...]}: visible or write actions only, plus Extra.
func (r *ApplyResult) ToJSON() map[string]any {
	status := "ok"
	switch {
	case r.Interrupted:
		status = "interrupted"
	case r.Failed != nil:
		status = "failed"
	}
	actions := make([]any, 0, len(r.Actions))
	for _, a := range r.Actions {
		if a.Hidden && !a.Type.IsWrite() {
			continue
		}
		item := map[string]any{
			"kind":            a.Kind,
			"key":             a.Key,
			"action":          string(a.Type),
			"status":          a.Status,
			"id":              nil,
			"durationSeconds": math.Round(a.Duration.Seconds()*1000) / 1000,
		}
		if id := a.ID(); id != "" {
			item["id"] = id
		}
		if a.Err != "" {
			item["error"] = redact.String(a.Err)
		}
		actions = append(actions, item)
	}
	out := map[string]any{"status": status, "actions": actions}
	for k, v := range r.Extra {
		out[k] = v
	}
	return out
}

// blockedBy returns the status of the nearest ancestor that failed, was skipped or deferred.
func blockedBy(a *Action) string {
	for p := a.Parent; p != nil; p = p.Parent {
		switch p.Status {
		case "failed", "skipped", "deferred":
			return p.Status
		}
	}
	return ""
}

func isCancel(err error) bool { return errors.Is(err, context.Canceled) }

func adopt(ctx context.Context, rc *RunContext, a *Action) (*Action, error) {
	fresh, err := a.Reconciler.Refresh(ctx, rc, a)
	if err != nil {
		return nil, err
	}
	if fresh == nil {
		return nil, errors.New("refresh returned no action")
	}
	return fresh, nil
}

// applyOne applies one action and recovers from the races a write can lose:
// 409 on create adopts the existing resource, a lost response re-observes before deciding, and 412
// re-plans that resource and retries once.
func applyOne(ctx context.Context, rc *RunContext, a *Action) error {
	rec := a.Reconciler
	err := rec.Apply(ctx, rc, a)
	if err == nil {
		return nil
	}
	var ambiguous *capellaapi.AmbiguousWriteError
	switch {
	case errors.Is(err, capellaapi.ErrConflict):
		if a.Type != Create {
			return err
		}
		fresh, ferr := adopt(ctx, rc, a)
		if ferr != nil {
			return ferr
		}
		if fresh.Observed == nil {
			return err
		}
		a.Observed = fresh.Observed
		rc.Say(fmt.Sprintf("  %s %s already exists; using it", a.Kind, a.Key))
		if fresh.Type == Update {
			a.Type = Update
			a.Changes = fresh.Changes
			return rec.Apply(ctx, rc, a)
		}
		return nil
	case errors.As(err, &ambiguous):
		fresh, ferr := adopt(ctx, rc, a)
		if ferr != nil {
			return ferr
		}
		if fresh.Observed == nil {
			return err
		}
		a.Observed = fresh.Observed
		rc.Say(fmt.Sprintf("  %s %s was created despite the lost response; using it", a.Kind, a.Key))
		return nil
	case errors.Is(err, capellaapi.ErrPreconditionFailed):
		fresh, ferr := adopt(ctx, rc, a)
		if ferr != nil {
			return ferr
		}
		if fresh.Type == Noop {
			a.Observed = fresh.Observed
			return nil
		}
		a.Observed, a.Changes = fresh.Observed, fresh.Changes
		if err := rec.Apply(ctx, rc, a); err != nil {
			if errors.Is(err, capellaapi.ErrPreconditionFailed) {
				return &ConcurrentChange{Msg: fmt.Sprintf("%s %s changed concurrently; re-run plan", a.Kind, a.Key)}
			}
			return err
		}
		return nil
	}
	return err
}

var verbs = map[ActionType]string{Create: "creating", Update: "updating", Replace: "replacing", Delete: "deleting"}

// Execute applies the plan in order. The first failure stops the run and later writes are reported
// as skipped; re-running resumes because finished actions re-plan as no-ops. A cancelled ctx
// (Ctrl-C) marks the current action interrupted and stops.
func Execute(ctx context.Context, rc *RunContext, p *Plan) *ApplyResult {
	result := &ApplyResult{Actions: p.Actions}
	stop := false
	for _, a := range p.Actions {
		if stop {
			if a.Type.IsWrite() && !a.Hidden {
				a.Status = "skipped"
			}
			continue
		}
		why := blockedBy(a)
		if why == "failed" || why == "skipped" {
			a.Status = "skipped"
			continue
		}
		if a.Type == Deferred {
			a.Status = "deferred"
			continue
		}
		if a.Type == Noop || (a.Hidden && a.Type != Create) {
			a.Status = "unchanged"
			continue
		}
		if a.Type == Blocked {
			a.Status = "blocked"
			continue
		}
		if why == "deferred" {
			a.Status = "deferred"
			kind := "parent"
			if a.Parent != nil {
				kind = a.Parent.Kind
			}
			a.Notes = append(a.Notes, fmt.Sprintf("(waiting for %s)", kind))
			continue
		}
		started := rc.Clock.Monotonic()
		if parent := a.Parent; parent != nil {
			ready, err := parent.Reconciler.EnsureReady(ctx, rc, parent, a)
			if err != nil {
				fail(rc, result, a, err, started)
				stop = true
				continue
			}
			if !ready {
				state := "not ready"
				if s, ok := parent.Data["state"]; ok && s != nil && s != "" {
					state = fmt.Sprint(s)
				}
				a.Status = "deferred"
				a.Notes = append(a.Notes, fmt.Sprintf("(deferred: %s %s is %s)", parent.Kind, parent.Key, state))
				continue
			}
		}

		g := rc.glyphs()
		rc.Say(fmt.Sprintf("%s %s %s %s %s", g.Arrow, verbs[a.Type], a.Kind, a.Key, g.Ellipsis))
		started = rc.Clock.Monotonic()
		err := applyOne(ctx, rc, a)
		if err == nil && !rc.NoWait {
			err = a.Reconciler.Wait(ctx, rc, a)
		}
		switch {
		case err == nil:
			a.Status = "done"
			a.Duration = rc.Clock.Monotonic() - started
			rc.Say(fmt.Sprintf("  done %s %s in %s", a.Kind, a.Key, FormatDuration(a.Duration)))
		case isCancel(err):
			a.Status = "interrupted"
			a.Err = interruptedMessage
			a.Duration = rc.Clock.Monotonic() - started
			result.Interrupted = true
			result.Failed = a
			result.Err = err
			stop = true
		default:
			fail(rc, result, a, err, started)
			stop = true
		}
	}
	return result
}

func fail(rc *RunContext, r *ApplyResult, a *Action, err error, started time.Duration) {
	a.Status = "failed"
	a.Err = err.Error()
	a.Duration = rc.Clock.Monotonic() - started
	r.Failed = a
	r.Err = err
}
