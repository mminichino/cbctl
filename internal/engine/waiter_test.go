package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// scriptedGetter returns states in order, repeating the last one; a state of
// "404" yields ErrNotFound and "boom" a generic error.
type scriptedGetter struct {
	states []string
	calls  int
	// polledAt records the clock reading of each poll.
	clk      Clock
	polledAt []time.Duration
}

func (g *scriptedGetter) GetCluster(_ context.Context, _, _ string) (*ClusterWithState, error) {
	if g.clk != nil {
		g.polledAt = append(g.polledAt, g.clk.Monotonic())
	}
	s := g.states[min(g.calls, len(g.states)-1)]
	g.calls++
	switch s {
	case "404":
		return nil, fmt.Errorf("get cluster: %w", ErrNotFound)
	case "boom":
		return nil, errors.New("boom")
	}
	return &ClusterWithState{Name: "prod", CurrentState: s}, nil
}

func TestPollIntervals(t *testing.T) {
	want := []time.Duration{
		10 * time.Second, 15 * time.Second, 22500 * time.Millisecond, 33750 * time.Millisecond,
		50625 * time.Millisecond, 60 * time.Second, 60 * time.Second, 60 * time.Second,
	}
	if got := PollIntervals(8); !reflect.DeepEqual(got, want) {
		t.Fatalf("PollIntervals(8) = %v, want %v", got, want)
	}
	if got := PollIntervals(0); len(got) != 0 {
		t.Fatalf("PollIntervals(0) = %v", got)
	}
}

func TestWaitForClusterSleepsFollowPollIntervals(t *testing.T) {
	clk := NewFakeClock(time.Time{})
	g := &scriptedGetter{states: []string{"deploying", "deploying", "deploying", "deploying", "deploying",
		"deploying", "deploying", "healthy"}, clk: clk}
	got, err := WaitForCluster(context.Background(), g, "p", "c", OpCreate, 2*time.Hour, clk, "", WaitHooks{})
	if err != nil || got != "healthy" {
		t.Fatalf("got %q, %v", got, err)
	}
	if g.calls != 8 {
		t.Fatalf("polls = %d, want 8", g.calls)
	}
	if g.polledAt[0] != 0 {
		t.Fatalf("first poll at %v, want immediate", g.polledAt[0])
	}
	if got, want := clk.Sleeps(), PollIntervals(7); !reflect.DeepEqual(got, want) {
		t.Fatalf("sleeps = %v, want %v", got, want)
	}
}

func TestWaitForClusterOps(t *testing.T) {
	tests := []struct {
		name      string
		op        Op
		states    []string
		want      string
		wantFail  string
		wantErrIs error
	}{
		{name: "create healthy", op: OpCreate, states: []string{"draft", "deploying", "healthy"}, want: "healthy"},
		{name: "create failed", op: OpCreate, states: []string{"deploying", "deploymentFailed"}, wantFail: "deploymentFailed"},
		{name: "update healthy", op: OpUpdate, states: []string{"scaling", "healthy"}, want: "healthy"},
		// Capella may still say healthy on the first poll after accepting a scale PUT.
		{name: "update ignores stale healthy then settles", op: OpUpdate, states: []string{"healthy", "scaling", "healthy"}, want: "healthy"},
		// Missed the transitional window entirely: accept healthy once firstPoll has elapsed.
		{name: "update accepts healthy after firstPoll", op: OpUpdate, states: []string{"healthy"}, want: "healthy"},
		{name: "update failed", op: OpUpdate, states: []string{"scaling", "scaleFailed"}, wantFail: "scaleFailed"},
		{name: "power off", op: OpPowerOff, states: []string{"turningOff", "turnedOff"}, want: "turnedOff"},
		{name: "power off failed", op: OpPowerOff, states: []string{"turningOffFailed"}, wantFail: "turningOffFailed"},
		{name: "power on", op: OpPowerOn, states: []string{"turningOn", "healthy"}, want: "healthy"},
		{name: "ready failed", op: OpReady, states: []string{"peeringFailed"}, wantFail: "peeringFailed"},
		{name: "delete 404", op: OpDelete, states: []string{"destroying", "404"}, want: "deleted"},
		{name: "delete failed", op: OpDelete, states: []string{"destroying", "destroyFailed"}, wantFail: "destroyFailed"},
		{name: "404 on create is an error", op: OpCreate, states: []string{"404"}, wantErrIs: ErrNotFound},
		{name: "read error", op: OpCreate, states: []string{"deploying", "boom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := NewFakeClock(time.Time{})
			g := &scriptedGetter{states: tt.states}
			got, err := WaitForCluster(context.Background(), g, "p", "c", tt.op, time.Hour, clk, "", WaitHooks{})
			switch {
			case tt.wantFail != "":
				var wf *WaitFailed
				if !errors.As(err, &wf) || wf.State != tt.wantFail || wf.Name != "prod" {
					t.Fatalf("err = %v, want WaitFailed(%s)", err, tt.wantFail)
				}
				want := fmt.Sprintf("cluster prod entered %s; check the Capella UI activity log", tt.wantFail)
				if err.Error() != want {
					t.Fatalf("message = %q, want %q", err.Error(), want)
				}
			case tt.wantErrIs != nil:
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("err = %v, want %v", err, tt.wantErrIs)
				}
			case tt.want == "":
				if err == nil {
					t.Fatal("want error")
				}
			default:
				if err != nil || got != tt.want {
					t.Fatalf("got %q, %v; want %q", got, err, tt.want)
				}
			}
		})
	}
}

func TestWaitForClusterUnknownOp(t *testing.T) {
	_, err := WaitForCluster(context.Background(), &scriptedGetter{states: []string{"healthy"}}, "p", "c", Op("nope"),
		time.Hour, NewFakeClock(time.Time{}), "", WaitHooks{})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestWaitForClusterTimeoutNeverSleepsPastDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{5 * time.Second, 25 * time.Second, 90 * time.Second, 5 * time.Minute} {
		t.Run(timeout.String(), func(t *testing.T) {
			clk := NewFakeClock(time.Time{})
			g := &scriptedGetter{states: []string{"deploying"}, clk: clk}
			got, err := WaitForCluster(context.Background(), g, "p", "c", OpCreate, timeout, clk, "my-cluster", WaitHooks{})
			var wt *WaitTimeout
			if !errors.As(err, &wt) {
				t.Fatalf("err = %v, want WaitTimeout", err)
			}
			if got != "deploying" || wt.State != "deploying" || wt.Name != "my-cluster" || wt.Timeout != timeout {
				t.Fatalf("got %q, %+v", got, wt)
			}
			if now := clk.Monotonic(); now != timeout {
				t.Fatalf("clock at %v, want exactly the deadline %v", now, timeout)
			}
			var total time.Duration
			for _, s := range clk.Sleeps() {
				total += s
			}
			if total != timeout {
				t.Fatalf("slept %v in total, want %v", total, timeout)
			}
			// One last poll happens at the deadline itself.
			if last := g.polledAt[len(g.polledAt)-1]; last != timeout {
				t.Fatalf("last poll at %v, want %v", last, timeout)
			}
		})
	}
}

func TestWaitTimeoutMessage(t *testing.T) {
	e := &WaitTimeout{Name: "prod", State: "deploying", Timeout: 4 * time.Minute}
	want := "cluster prod still deploying after 4m0s; the operation continues in Capella; re-run to resume waiting"
	if e.Error() != want {
		t.Fatalf("got %q", e.Error())
	}
}

func TestWaitForClusterHooks(t *testing.T) {
	clk := NewFakeClock(time.Time{})
	g := &scriptedGetter{states: []string{"deploying", "deploying", "scaling", "healthy"}}
	type change struct {
		state   string
		elapsed time.Duration
	}
	var changes []change
	h := WaitHooks{OnState: func(s string, e time.Duration) { changes = append(changes, change{s, e}) }}
	if _, err := WaitForCluster(context.Background(), g, "p", "c", OpCreate, time.Hour, clk, "", h); err != nil {
		t.Fatal(err)
	}
	want := []change{{"deploying", 0}, {"scaling", 25 * time.Second}, {"healthy", 47500 * time.Millisecond}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}

func TestWaitForClusterDeleteReportsDeleted(t *testing.T) {
	clk := NewFakeClock(time.Time{})
	var states []string
	h := WaitHooks{OnState: func(s string, _ time.Duration) { states = append(states, s) }}
	got, err := WaitForCluster(context.Background(), &scriptedGetter{states: []string{"destroying", "404"}}, "p", "c",
		OpDelete, time.Hour, clk, "", h)
	if err != nil || got != "deleted" {
		t.Fatalf("got %q, %v", got, err)
	}
	if want := []string{"destroying", "deleted"}; !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
}

func TestOnTickFiresEvery30Seconds(t *testing.T) {
	const timeout = 5 * time.Minute
	t.Run("cluster", func(t *testing.T) {
		clk := NewFakeClock(time.Time{})
		var ticks []time.Duration
		h := WaitHooks{OnTick: func() { ticks = append(ticks, clk.Monotonic()) }}
		g := &scriptedGetter{states: []string{"deploying"}}
		_, err := WaitForCluster(context.Background(), g, "p", "c", OpCreate, timeout, clk, "", h)
		var wt *WaitTimeout
		if !errors.As(err, &wt) {
			t.Fatalf("err = %v", err)
		}
		assertTicks(t, ticks, timeout)
	})
	t.Run("status", func(t *testing.T) {
		clk := NewFakeClock(time.Time{})
		var ticks []time.Duration
		h := WaitHooks{OnTick: func() { ticks = append(ticks, clk.Monotonic()) }}
		read := func(context.Context) (string, error) { return "running", nil }
		_, err := WaitForStatus(context.Background(), read, map[string]bool{"done": true}, nil, timeout, clk, "backup", h)
		var st *StatusTimeout
		if !errors.As(err, &st) {
			t.Fatalf("err = %v", err)
		}
		assertTicks(t, ticks, timeout)
	})
}

func assertTicks(t *testing.T, ticks []time.Duration, total time.Duration) {
	t.Helper()
	var want []time.Duration
	for d := 30 * time.Second; d <= total; d += 30 * time.Second {
		want = append(want, d)
	}
	if !reflect.DeepEqual(ticks, want) {
		t.Fatalf("ticks at %v, want %v", ticks, want)
	}
}

func TestWaitForClusterContextCancelDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clk := NewFakeClock(time.Time{})
	_, err := WaitForCluster(ctx, &scriptedGetter{states: []string{"deploying"}}, "p", "c", OpCreate, time.Hour, clk, "", WaitHooks{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(clk.Sleeps()) != 0 {
		t.Fatalf("sleeps = %v", clk.Sleeps())
	}
}

func TestWaitForStatus(t *testing.T) {
	done := map[string]bool{"complete": true, "ready": true}
	failed := map[string]bool{"failed": true}
	tests := []struct {
		name       string
		reads      []string
		want       string
		wantFailed bool
		wantTO     bool
		wantStates []string
	}{
		{name: "empty is queued then done", reads: []string{"", "", "Complete"}, want: "Complete",
			wantStates: []string{"queued", "Complete"}},
		{name: "case-insensitive done", reads: []string{"READY"}, want: "READY", wantStates: []string{"READY"}},
		{name: "case-insensitive failed", reads: []string{"inProgress", "Failed"}, want: "Failed", wantFailed: true,
			wantStates: []string{"inProgress", "Failed"}},
		{name: "timeout", reads: []string{"pending"}, wantTO: true, wantStates: []string{"pending"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := NewFakeClock(time.Time{})
			i := 0
			read := func(context.Context) (string, error) {
				s := tt.reads[min(i, len(tt.reads)-1)]
				i++
				return s, nil
			}
			var states []string
			h := WaitHooks{OnState: func(s string, _ time.Duration) { states = append(states, s) }}
			got, err := WaitForStatus(context.Background(), read, done, failed, time.Minute, clk, "backup b1", h)
			switch {
			case tt.wantFailed:
				var sf *StatusFailed
				if !errors.As(err, &sf) || sf.State != tt.want || sf.Label != "backup b1" {
					t.Fatalf("err = %v", err)
				}
				if want := "backup b1 ended Failed; check the Capella UI activity log"; err.Error() != want {
					t.Fatalf("message = %q", err.Error())
				}
			case tt.wantTO:
				var st *StatusTimeout
				if !errors.As(err, &st) || st.Timeout != time.Minute {
					t.Fatalf("err = %v", err)
				}
				if want := "backup b1 still pending after 1m0s; it continues in Capella"; err.Error() != want {
					t.Fatalf("message = %q", err.Error())
				}
				if clk.Monotonic() != time.Minute {
					t.Fatalf("clock at %v, want deadline", clk.Monotonic())
				}
			default:
				if err != nil || got != tt.want {
					t.Fatalf("got %q, %v", got, err)
				}
			}
			if !reflect.DeepEqual(states, tt.wantStates) {
				t.Fatalf("states = %v, want %v", states, tt.wantStates)
			}
		})
	}
}

func TestWaitForStatusReadError(t *testing.T) {
	sentinel := errors.New("read failed")
	read := func(context.Context) (string, error) { return "", sentinel }
	_, err := WaitForStatus(context.Background(), read, nil, nil, time.Minute, NewFakeClock(time.Time{}), "x", WaitHooks{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusSets(t *testing.T) {
	// Spot-check that the tables mirror capellactl's waiter.py.
	for op, want := range map[Op]string{OpCreate: "healthy", OpUpdate: "healthy", OpReady: "healthy",
		OpPowerOff: "turnedOff", OpPowerOn: "healthy"} {
		if !Success[op][want] || len(Success[op]) != 1 {
			t.Errorf("Success[%s] = %v", op, Success[op])
		}
	}
	if len(Success[OpDelete]) != 0 {
		t.Errorf("Success[delete] = %v", Success[OpDelete])
	}
	if len(Failure[OpReady]) != 8 || len(Failure[OpUpdate]) != 3 || len(Transitional) != 9 {
		t.Errorf("table sizes: ready=%d update=%d transitional=%d", len(Failure[OpReady]), len(Failure[OpUpdate]), len(Transitional))
	}
}

func TestFakeClock(t *testing.T) {
	clk := NewFakeClock(time.Time{})
	start := clk.Now()
	clk.Advance(3 * time.Second)
	if err := clk.Sleep(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if clk.Monotonic() != 5*time.Second || clk.Now().Sub(start) != 5*time.Second {
		t.Fatalf("monotonic = %v", clk.Monotonic())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := clk.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := clk.Sleeps(); !reflect.DeepEqual(got, []time.Duration{2 * time.Second}) {
		t.Fatalf("sleeps = %v", got)
	}
	if clk.Monotonic() != 5*time.Second {
		t.Fatal("cancelled sleep must not advance the clock")
	}
}

func TestRealClockSleepCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if err := (RealClock{}).Sleep(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(begin) > 5*time.Second {
		t.Fatal("Sleep did not return promptly")
	}
}
