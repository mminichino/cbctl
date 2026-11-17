package dedupe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)

func run(id int64, sha, event, status, conclusion string, at time.Time) RunRef {
	return RunRef{ID: id, Attempt: 1, HeadSHA: sha, Event: event, Status: status, Conclusion: conclusion, CreatedAt: at}
}

func probe(r RunRef) RunRef { r.Probe = true; return r }

func TestDecide(t *testing.T) {
	self := run(10, "abc", "schedule", "in_progress", "", t0)
	dispatch := run(10, "abc", "workflow_dispatch", "in_progress", "", t0)
	tests := []struct {
		name   string
		self   RunRef
		others []RunRef
		skip   bool
	}{
		{"alone", self, nil, false},
		{"same SHA in progress", self, []RunRef{run(9, "abc", "workflow_dispatch", "in_progress", "", t0.Add(-time.Hour))}, true},
		{"same SHA queued", self, []RunRef{run(9, "abc", "schedule", "queued", "", t0.Add(-time.Minute))}, true},
		{"newer active run does not skip the older", self, []RunRef{run(11, "abc", "schedule", "in_progress", "", t0.Add(time.Minute))}, false},
		{"other SHA in progress", self, []RunRef{run(9, "def", "schedule", "in_progress", "", t0.Add(-time.Hour))}, false},
		{"dispatch after a success within the window", dispatch, []RunRef{run(9, "abc", "schedule", "completed", "success", t0.Add(-5*time.Hour))}, true},
		{"dispatch after a success outside the window", dispatch, []RunRef{run(9, "abc", "schedule", "completed", "success", t0.Add(-21*time.Hour))}, false},
		{"dispatch after a failure", dispatch, []RunRef{run(9, "abc", "schedule", "completed", "failure", t0.Add(-time.Hour))}, false},
		{"schedule after yesterday's success", self, []RunRef{run(9, "abc", "schedule", "completed", "success", t0.Add(-24*time.Hour))}, false},
		{"schedule after a dispatch success today", self, []RunRef{run(9, "abc", "workflow_dispatch", "completed", "success", t0.Add(-time.Hour))}, false},
		{"schedule fired twice today", self, []RunRef{run(9, "abc", "schedule", "completed", "success", t0.Add(-time.Minute))}, true},
		{"re-run attempt of itself", RunRef{ID: 10, Attempt: 2, HeadSHA: "abc", Event: "schedule", Status: "in_progress", CreatedAt: t0},
			[]RunRef{run(10, "abc", "schedule", "completed", "failure", t0)}, false},
		{"dispatch after a probe success", dispatch, []RunRef{probe(run(9, "abc", "workflow_dispatch", "completed", "success", t0.Add(-time.Hour)))}, false},
		{"probe after a suite success", probe(dispatch), []RunRef{run(9, "abc", "schedule", "completed", "success", t0.Add(-time.Hour))}, false},
		{"probe while the suite runs", probe(dispatch), []RunRef{run(9, "abc", "schedule", "in_progress", "", t0.Add(-time.Hour))}, true},
		{"suite while a probe runs", self, []RunRef{probe(run(9, "abc", "workflow_dispatch", "in_progress", "", t0.Add(-time.Hour)))}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			others := append([]RunRef{tt.self}, tt.others...)
			if skip, reason := Decide(tt.self, others, Window); skip != tt.skip {
				t.Fatalf("skip = %v (%s), want %v", skip, reason, tt.skip)
			}
		})
	}
}

type fakeLister struct {
	runs []RunRef
	err  error
}

func (f fakeLister) ListRuns(context.Context, string, string) ([]RunRef, error) { return f.runs, f.err }

func TestCheck(t *testing.T) {
	ctx := context.Background()
	t.Run("lister error fails open", func(t *testing.T) {
		skip, _, err := Check(ctx, fakeLister{err: errors.New("rate limited")}, "o/r", "nightly.yml", 1, Window)
		if skip || err == nil {
			t.Fatalf("skip=%v err=%v; want run with an error", skip, err)
		}
	})
	t.Run("self missing fails open", func(t *testing.T) {
		skip, _, err := Check(ctx, fakeLister{runs: []RunRef{run(2, "a", "schedule", "completed", "success", t0)}}, "o/r", "nightly.yml", 1, Window)
		if skip || !errors.Is(err, ErrNoSelf) {
			t.Fatalf("skip=%v err=%v", skip, err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		runs := []RunRef{run(1, "a", "workflow_dispatch", "in_progress", "", t0), run(0, "a", "schedule", "in_progress", "", t0.Add(-time.Hour))}
		skip, reason, err := Check(ctx, fakeLister{runs: runs}, "o/r", "nightly.yml", 1, Window)
		if !skip || err != nil || !strings.Contains(reason, "already in_progress") {
			t.Fatalf("skip=%v reason=%q err=%v", skip, reason, err)
		}
	})
}

func TestParseRuns(t *testing.T) {
	raw := `{"total_count":1,"workflow_runs":[{"id":42,"run_attempt":2,"head_sha":"abc","event":"schedule","status":"completed","conclusion":"success","created_at":"2026-11-16T07:00:03Z","extra":1}]}`
	runs, err := ParseRuns([]byte(raw))
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	if r := runs[0]; r.ID != 42 || r.Attempt != 2 || r.Conclusion != "success" || !r.CreatedAt.Equal(t0.Add(3*time.Second)) {
		t.Errorf("run = %+v", r)
	}
	if _, err := ParseRuns([]byte("not json")); err == nil {
		t.Error("want a decode error")
	}
	for title, want := range map[string]bool{"probe: schedule": true, "nightly": false, "": false, "probes": false} {
		raw := `{"workflow_runs":[{"id":1,"display_title":"` + title + `"}]}`
		if runs, err := ParseRuns([]byte(raw)); err != nil || runs[0].Probe != want {
			t.Errorf("title %q: Probe = %v (err %v), want %v", title, runs[0].Probe, err, want)
		}
	}
}
