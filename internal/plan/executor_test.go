package plan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
)

var (
	errConflict = &capellaapi.APIError{Status: 409, Method: "POST", Path: "/x"}
	errPrecond  = &capellaapi.APIError{Status: 412, Method: "PUT", Path: "/x"}
)

type execEnv struct {
	rc    *RunContext
	clock *engine.FakeClock
	lines []string
}

func newExecEnv() *execEnv {
	e := &execEnv{clock: engine.NewFakeClock(time.Time{})}
	e.rc = &RunContext{Clock: e.clock, Progress: func(s string) { e.lines = append(e.lines, s) }}
	return e
}

// act builds an action of the given type owned by s.
func act(s *stub, kind, key string, t ActionType, parent *Action) *Action {
	a := NewAction(s, kind, key, t)
	a.Parent = parent
	a.Desired = Obj{"name": key}
	return a
}

func TestExecute_HappyPathStatusesAndProgress(t *testing.T) {
	e := newExecEnv()
	s := newStub("thing", "", Flags{})
	s.apply = func(s *stub, a *Action) error {
		e.clock.Advance(3 * time.Second)
		if a.Type == Create {
			a.CreatedID = "new-" + a.Key
		}
		return nil
	}
	parent := act(s, "project", "p", Create, nil)
	child := act(s, "cluster", "c", Update, parent)
	noop := act(s, "bucket", "n", Noop, child)
	hidden := act(s, "bucket", "h", Update, child)
	hidden.Hidden = true
	blocked := act(s, "bucket", "x", Blocked, child)
	deferred := act(s, "bucket", "d", Deferred, child)
	del := act(s, "bucket", "gone", Delete, child)
	repl := act(s, "allowed-cidr", "1.2.3.4/32", Replace, child)
	p := &Plan{Actions: []*Action{parent, child, noop, hidden, blocked, deferred, del, repl}}

	r := Execute(context.Background(), e.rc, p)
	if !r.OK() || r.Failed != nil || r.Err != nil || r.Interrupted {
		t.Fatalf("result: %+v", r)
	}
	statuses := map[string]string{}
	for _, a := range p.Actions {
		statuses[a.Kind+"/"+a.Key] = a.Status
	}
	want := map[string]string{
		"project/p": "done", "cluster/c": "done", "bucket/n": "unchanged", "bucket/h": "unchanged",
		"bucket/x": "blocked", "bucket/d": "deferred", "bucket/gone": "done", "allowed-cidr/1.2.3.4/32": "done",
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("got %v", statuses)
	}
	if parent.ID() != "new-p" || parent.Duration != 3*time.Second {
		t.Fatalf("parent: id=%q dur=%v", parent.ID(), parent.Duration)
	}
	wantLines := []string{
		"→ creating project p …", "  done project p in 3s",
		"→ updating cluster c …", "  done cluster c in 3s",
		"→ deleting bucket gone …", "  done bucket gone in 3s",
		"→ replacing allowed-cidr 1.2.3.4/32 …", "  done allowed-cidr 1.2.3.4/32 in 3s",
	}
	if !reflect.DeepEqual(e.lines, wantLines) {
		t.Fatalf("lines: %q", e.lines)
	}
}

func TestExecute_StopsAtFirstFailure(t *testing.T) {
	e := newExecEnv()
	s := newStub("thing", "", Flags{})
	s.apply = func(s *stub, a *Action) error {
		if a.Key == "bad" {
			return errors.New("boom")
		}
		return nil
	}
	ok := act(s, "bucket", "ok", Create, nil)
	bad := act(s, "bucket", "bad", Create, nil)
	later := act(s, "bucket", "later", Update, nil)
	laterNoop := act(s, "bucket", "later-noop", Noop, nil)
	laterHidden := act(s, "bucket", "later-hidden", Update, nil)
	laterHidden.Hidden = true
	p := &Plan{Actions: []*Action{ok, bad, later, laterNoop, laterHidden}}
	r := Execute(context.Background(), e.rc, p)
	if r.OK() || r.Failed != bad || r.Err == nil || r.Err.Error() != "boom" || r.Interrupted {
		t.Fatalf("result: %+v", r)
	}
	if ok.Status != "done" || bad.Status != "failed" || bad.Err != "boom" || later.Status != "skipped" {
		t.Fatalf("statuses: %s %s %s", ok.Status, bad.Status, later.Status)
	}
	if laterNoop.Status != "pending" || laterHidden.Status != "pending" {
		t.Fatalf("non-writes stay pending: %s %s", laterNoop.Status, laterHidden.Status)
	}
	if !reflect.DeepEqual(s.applied, []string{"ok", "bad"}) {
		t.Fatalf("applied %v", s.applied)
	}
	j := r.ToJSON()
	if j["status"] != "failed" {
		t.Fatal(j)
	}
}

func TestExecute_ParentStatusPropagation(t *testing.T) {
	e := newExecEnv()
	s := newStub("thing", "", Flags{})
	s.ready = func(a, child *Action) (bool, error) {
		if a.Key == "slow" {
			a.Set("state", "deploying")
			return false, nil
		}
		if a.Key == "stateless" {
			return false, nil
		}
		return true, nil
	}
	slow := act(s, "cluster", "slow", Noop, nil)
	underSlow := act(s, "bucket", "b1", Create, slow)
	underDeferred := act(s, "scope", "s1", Create, underSlow)
	stateless := act(s, "cluster", "stateless", Noop, nil)
	underStateless := act(s, "bucket", "b2", Create, stateless)
	p := &Plan{Actions: []*Action{slow, underSlow, underDeferred, stateless, underStateless}}
	r := Execute(context.Background(), e.rc, p)
	if !r.OK() {
		t.Fatalf("deferral is not a failure: %+v", r)
	}
	if underSlow.Status != "deferred" || !reflect.DeepEqual(underSlow.Notes, []string{"(deferred: cluster slow is deploying)"}) {
		t.Fatalf("underSlow: %s %v", underSlow.Status, underSlow.Notes)
	}
	if underDeferred.Status != "deferred" || !reflect.DeepEqual(underDeferred.Notes, []string{"(waiting for bucket)"}) {
		t.Fatalf("underDeferred: %s %v", underDeferred.Status, underDeferred.Notes)
	}
	if !reflect.DeepEqual(underStateless.Notes, []string{"(deferred: cluster stateless is not ready)"}) {
		t.Fatalf("notes: %v", underStateless.Notes)
	}
	if len(s.applied) != 0 {
		t.Fatalf("nothing applied: %v", s.applied)
	}
}

func TestExecute_SkipsBelowFailedParent(t *testing.T) {
	e := newExecEnv()
	s := newStub("thing", "", Flags{})
	parent := act(s, "cluster", "c", Create, nil)
	parent.Status = "failed" // as if set by an earlier run step
	child := act(s, "bucket", "b", Create, parent)
	p := &Plan{Actions: []*Action{child}}
	Execute(context.Background(), e.rc, p)
	if child.Status != "skipped" {
		t.Fatal(child.Status)
	}
}

func TestExecute_EnsureReadyError(t *testing.T) {
	e := newExecEnv()
	s := newStub("thing", "", Flags{})
	s.ready = func(a, child *Action) (bool, error) { return false, errors.New("wait failed") }
	parent := act(s, "cluster", "c", Noop, nil)
	child := act(s, "bucket", "b", Create, parent)
	after := act(s, "bucket", "after", Create, nil)
	r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{parent, child, after}})
	if r.Failed != child || child.Status != "failed" || child.Err != "wait failed" || after.Status != "skipped" {
		t.Fatalf("got %s %q %s", child.Status, child.Err, after.Status)
	}
}

func TestExecute_NoWait(t *testing.T) {
	for _, noWait := range []bool{false, true} {
		e := newExecEnv()
		e.rc.NoWait = noWait
		waited := false
		s := newStub("thing", "", Flags{})
		s.wait = func(*Action) error { waited = true; return nil }
		Execute(context.Background(), e.rc, &Plan{Actions: []*Action{act(s, "bucket", "b", Create, nil)}})
		if waited == noWait {
			t.Fatalf("noWait=%v waited=%v", noWait, waited)
		}
	}
}

func TestExecute_WaitErrorFails(t *testing.T) {
	e := newExecEnv()
	s := newStub("thing", "", Flags{})
	s.wait = func(*Action) error { return &engine.WaitTimeout{} }
	a := act(s, "cluster", "c", Create, nil)
	r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
	var wt *engine.WaitTimeout
	if r.OK() || !errors.As(r.Err, &wt) || a.Status != "failed" {
		t.Fatalf("got %+v", r)
	}
}

func TestExecute_Interrupted(t *testing.T) {
	for _, where := range []string{"apply", "wait"} {
		t.Run(where, func(t *testing.T) {
			e := newExecEnv()
			s := newStub("thing", "", Flags{})
			if where == "apply" {
				s.apply = func(*stub, *Action) error { return context.Canceled }
			} else {
				s.wait = func(*Action) error { return context.Canceled }
			}
			a := act(s, "cluster", "c", Create, nil)
			next := act(s, "bucket", "b", Create, nil)
			r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a, next}})
			if !r.Interrupted || r.OK() || r.Failed != a || a.Status != "interrupted" ||
				a.Err != "stopped waiting; the operation continues in Capella" || next.Status != "skipped" {
				t.Fatalf("got %+v / %s %q %s", r, a.Status, a.Err, next.Status)
			}
			if r.ToJSON()["status"] != "interrupted" {
				t.Fatal("json status")
			}
		})
	}
}

func TestExecute_ConflictOnCreateAdopts(t *testing.T) {
	tests := []struct {
		name         string
		refreshType  ActionType
		wantApplies  int
		wantChanges  int
		wantObserved bool
	}{
		{"adopt as is", Noop, 1, 0, true},
		{"adopt then update", Update, 2, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecEnv()
			s := newStub("thing", "", Flags{})
			s.apply = func(s *stub, a *Action) error {
				if len(s.applied) == 1 {
					return errConflict
				}
				return nil
			}
			s.refresh = func(s *stub, a *Action) (*Action, error) {
				f := NewAction(s, a.Kind, a.Key, tc.refreshType)
				f.Observed = &Observed{ID: "existing"}
				f.Changes = []Change{{Path: "x"}}[:tc.wantChanges]
				return f, nil
			}
			a := act(s, "bucket", "b", Create, nil)
			r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
			if !r.OK() || a.Status != "done" || len(s.applied) != tc.wantApplies || a.ID() != "existing" || len(a.Changes) != tc.wantChanges {
				t.Fatalf("got %+v status=%s applies=%d id=%q", r, a.Status, len(s.applied), a.ID())
			}
			if !contains(e.lines, "  bucket b already exists; using it") {
				t.Fatalf("lines %q", e.lines)
			}
			if tc.refreshType == Update && a.Type != Update {
				t.Fatal("type should become update")
			}
		})
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestExecute_ConflictFailures(t *testing.T) {
	t.Run("not a create", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errConflict }
		a := act(s, "bucket", "b", Update, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		if r.OK() || !errors.Is(r.Err, capellaapi.ErrConflict) {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("nothing to adopt", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errConflict }
		s.refresh = func(s *stub, a *Action) (*Action, error) { return NewAction(s, a.Kind, a.Key, Create), nil }
		a := act(s, "bucket", "b", Create, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		if r.OK() || !errors.Is(r.Err, capellaapi.ErrConflict) || a.Status != "failed" {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("refresh error", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errConflict }
		s.refresh = func(*stub, *Action) (*Action, error) { return nil, errors.New("refresh boom") }
		a := act(s, "bucket", "b", Create, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		if r.OK() || a.Err != "refresh boom" {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("refresh returns nil", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errConflict }
		s.refresh = func(*stub, *Action) (*Action, error) { return nil, nil }
		a := act(s, "bucket", "b", Create, nil)
		if r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}}); r.OK() {
			t.Fatal("must fail")
		}
	})
}

func TestExecute_AmbiguousWrite(t *testing.T) {
	ambiguous := &capellaapi.AmbiguousWriteError{Method: "POST", Path: "/b", Cause: errors.New("EOF")}
	t.Run("adopts", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return ambiguous }
		s.refresh = func(s *stub, a *Action) (*Action, error) {
			f := NewAction(s, a.Kind, a.Key, Noop)
			f.Observed = &Observed{ID: "made"}
			return f, nil
		}
		a := act(s, "bucket", "b", Create, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		if !r.OK() || a.ID() != "made" || len(s.applied) != 1 {
			t.Fatalf("got %+v applied=%v", r, s.applied)
		}
		if !contains(e.lines, "  bucket b was created despite the lost response; using it") {
			t.Fatalf("lines %q", e.lines)
		}
	})
	t.Run("never created: fails without a second create", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return ambiguous }
		s.refresh = func(s *stub, a *Action) (*Action, error) { return NewAction(s, a.Kind, a.Key, Create), nil }
		a := act(s, "bucket", "b", Create, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		var amb *capellaapi.AmbiguousWriteError
		if r.OK() || !errors.As(r.Err, &amb) || len(s.applied) != 1 {
			t.Fatalf("got %+v applied=%v", r, s.applied)
		}
	})
	t.Run("refresh error", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return ambiguous }
		s.refresh = func(*stub, *Action) (*Action, error) { return nil, errors.New("nope") }
		a := act(s, "bucket", "b", Create, nil)
		if r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}}); r.OK() || a.Err != "nope" {
			t.Fatalf("got %q", a.Err)
		}
	})
}

func TestExecute_PreconditionFailed(t *testing.T) {
	t.Run("already matches", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errPrecond }
		s.refresh = func(s *stub, a *Action) (*Action, error) {
			f := NewAction(s, a.Kind, a.Key, Noop)
			f.Observed = &Observed{ID: "same"}
			return f, nil
		}
		a := act(s, "bucket", "b", Update, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		if !r.OK() || len(s.applied) != 1 || a.ID() != "same" {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("retry once succeeds", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(s *stub, a *Action) error {
			if len(s.applied) == 1 {
				return errPrecond
			}
			return nil
		}
		s.refresh = func(s *stub, a *Action) (*Action, error) {
			f := NewAction(s, a.Kind, a.Key, Update)
			f.Observed = &Observed{ID: "i", ETag: "new"}
			f.Changes = []Change{{Path: "v", Before: 1, After: 2}}
			return f, nil
		}
		a := act(s, "bucket", "b", Update, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		if !r.OK() || len(s.applied) != 2 || a.Observed.ETag != "new" || len(a.Changes) != 1 {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("second 412 is a concurrent change", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errPrecond }
		s.refresh = func(s *stub, a *Action) (*Action, error) { return NewAction(s, a.Kind, a.Key, Update), nil }
		a := act(s, "bucket", "b", Update, nil)
		r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}})
		var cc *ConcurrentChange
		if r.OK() || !errors.As(r.Err, &cc) || a.Err != "bucket b changed concurrently; re-run plan" || len(s.applied) != 2 {
			t.Fatalf("got %+v err=%q", r, a.Err)
		}
	})
	t.Run("other error on retry", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(s *stub, a *Action) error {
			if len(s.applied) == 1 {
				return errPrecond
			}
			return errors.New("other")
		}
		s.refresh = func(s *stub, a *Action) (*Action, error) { return NewAction(s, a.Kind, a.Key, Update), nil }
		a := act(s, "bucket", "b", Update, nil)
		if r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}}); r.OK() || a.Err != "other" {
			t.Fatalf("err=%q", a.Err)
		}
	})
	t.Run("refresh error", func(t *testing.T) {
		e := newExecEnv()
		s := newStub("thing", "", Flags{})
		s.apply = func(*stub, *Action) error { return errPrecond }
		s.refresh = func(*stub, *Action) (*Action, error) { return nil, errors.New("gone") }
		a := act(s, "bucket", "b", Update, nil)
		if r := Execute(context.Background(), e.rc, &Plan{Actions: []*Action{a}}); r.OK() || a.Err != "gone" {
			t.Fatalf("err=%q", a.Err)
		}
	})
}

func TestApplyResult_ToJSON(t *testing.T) {
	s := newStub("thing", "", Flags{})
	done := act(s, "bucket", "b", Create, nil)
	done.Status, done.CreatedID, done.Duration = "done", "id-1", 1234*time.Millisecond+400*time.Microsecond
	failed := act(s, "bucket", "f", Update, nil)
	failed.Status, failed.Err = "failed", "boom"
	hiddenWrite := act(s, "project", "p", Update, nil)
	hiddenWrite.Hidden, hiddenWrite.Status = true, "unchanged"
	hiddenNoop := act(s, "cluster", "c", Noop, nil)
	hiddenNoop.Hidden = true
	r := &ApplyResult{Actions: []*Action{done, failed, hiddenWrite, hiddenNoop}, Failed: failed, Extra: map[string]any{"connectionStrings": []any{"couchbases://x"}}}
	got := r.ToJSON()
	if got["status"] != "failed" {
		t.Fatal(got["status"])
	}
	actions := got["actions"].([]any)
	if len(actions) != 3 {
		t.Fatalf("hidden noop must be dropped: %v", actions)
	}
	first := actions[0].(map[string]any)
	if first["id"] != "id-1" || first["durationSeconds"] != 1.234 || first["status"] != "done" {
		t.Fatalf("first: %v", first)
	}
	if _, has := first["error"]; has {
		t.Fatal("no error key on success")
	}
	second := actions[1].(map[string]any)
	if second["id"] != nil || second["error"] != "boom" {
		t.Fatalf("second: %v", second)
	}
	if got["connectionStrings"] == nil {
		t.Fatal("extra merged")
	}
	if (&ApplyResult{}).ToJSON()["status"] != "ok" {
		t.Fatal("ok status")
	}
	if !strings.Contains(first["action"].(string), "create") {
		t.Fatal(first)
	}
}
