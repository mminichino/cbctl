// Package dedupe decides whether a nightly sandbox run duplicates another run of the same
// workflow, so the paid sandbox is not exercised twice for the same commit. It fails open: when
// the run list cannot be read, the run goes ahead.
package dedupe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Window is how recent a successful run on the same commit must be for a manual dispatch to be
// skipped as a duplicate.
const Window = 20 * time.Hour

// RunRef is one workflow run, with the fields Decide needs.
type RunRef struct {
	ID         int64
	Attempt    int
	HeadSHA    string
	Event      string // schedule | workflow_dispatch | ...
	Status     string // queued | in_progress | completed | ...
	Conclusion string // success | failure | cancelled | ...
	CreatedAt  time.Time
	// Probe is a dispatch that ran a live API probe instead of the suite (its title starts with
	// ProbeTitlePrefix). Its success proves nothing about the commit.
	Probe bool
}

// ProbeTitlePrefix starts the run name nightly.yml gives a probe dispatch.
const ProbeTitlePrefix = "probe: "

// Lister lists the runs of one workflow, newest first or in any order.
type Lister interface {
	ListRuns(ctx context.Context, repo, workflow string) ([]RunRef, error)
}

// ErrNoSelf is returned when the current run is not in the listing.
var ErrNoSelf = errors.New("the current run is not in the workflow's run list")

// Decide reports whether self duplicates one of others, and why. Runs are duplicates only on
// the same commit:
//   - another run of the commit is queued or in progress (whatever the event);
//   - self is a manual dispatch and the commit already succeeded within window;
//   - self is scheduled and another scheduled run of the commit already succeeded the same
//     UTC day (GitHub occasionally fires a cron twice).
//
// A re-run attempt of self (same ID) is never a duplicate, and a scheduled run is never skipped
// for a success on an earlier day: the nightly health report counts one scheduled run per night. A probe
// is skipped only while another run is active (one sandbox user at a time), and a probe's
// success never makes another run a duplicate.
func Decide(self RunRef, others []RunRef, window time.Duration) (skip bool, reason string) {
	for _, o := range others {
		if o.ID == self.ID || o.HeadSHA != self.HeadSHA {
			continue
		}
		active := o.Status == "queued" || o.Status == "in_progress" || o.Status == "waiting" || o.Status == "pending"
		// Only an older active run counts, so two runs started together don't both skip.
		if active && (o.CreatedAt.Before(self.CreatedAt) || o.CreatedAt.Equal(self.CreatedAt) && o.ID < self.ID) {
			return true, fmt.Sprintf("run %d on %s is already %s", o.ID, short(o.HeadSHA), o.Status)
		}
		if self.Probe || o.Probe || o.Status != "completed" || o.Conclusion != "success" || !o.CreatedAt.Before(self.CreatedAt) {
			continue
		}
		switch self.Event {
		case "workflow_dispatch":
			if self.CreatedAt.Sub(o.CreatedAt) <= window {
				return true, fmt.Sprintf("run %d already passed on %s at %s (within %s)", o.ID, short(o.HeadSHA), o.CreatedAt.UTC().Format(time.RFC3339), window)
			}
		case "schedule":
			if o.Event == "schedule" && sameDay(o.CreatedAt, self.CreatedAt) {
				return true, fmt.Sprintf("scheduled run %d already passed on %s today", o.ID, short(o.HeadSHA))
			}
		}
	}
	return false, "no duplicate run"
}

// Check lists the workflow's runs and decides for selfID. It fails open: any error returns
// skip=false with the error, which the caller reports as a warning.
func Check(ctx context.Context, l Lister, repo, workflow string, selfID int64, window time.Duration) (skip bool, reason string, err error) {
	runs, err := l.ListRuns(ctx, repo, workflow)
	if err != nil {
		return false, "run list unavailable; running", err
	}
	for _, r := range runs {
		if r.ID == selfID {
			skip, reason = Decide(r, runs, window)
			return skip, reason, nil
		}
	}
	return false, "current run not listed; running", fmt.Errorf("%w (run %d)", ErrNoSelf, selfID)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// GH lists runs with the gh CLI (GH_TOKEN in Actions).
type GH struct{}

// ListRuns calls GET repos/{repo}/actions/workflows/{workflow}/runs.
func (GH) ListRuns(ctx context.Context, repo, workflow string) ([]RunRef, error) {
	path := fmt.Sprintf("repos/%s/actions/workflows/%s/runs?per_page=50", repo, workflow)
	out, err := exec.CommandContext(ctx, "gh", "api", path).Output()
	if err != nil {
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("gh api %s: %s", path, msg)
	}
	return ParseRuns(out)
}

// ParseRuns decodes a {"workflow_runs":[...]} payload.
func ParseRuns(raw []byte) ([]RunRef, error) {
	var payload struct {
		WorkflowRuns []struct {
			ID         int64     `json:"id"`
			Attempt    int       `json:"run_attempt"`
			HeadSHA    string    `json:"head_sha"`
			Event      string    `json:"event"`
			Status     string    `json:"status"`
			Conclusion string    `json:"conclusion"`
			CreatedAt  time.Time `json:"created_at"`
			Title      string    `json:"display_title"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decoding workflow runs: %w", err)
	}
	out := make([]RunRef, 0, len(payload.WorkflowRuns))
	for _, r := range payload.WorkflowRuns {
		out = append(out, RunRef{ID: r.ID, Attempt: r.Attempt, HeadSHA: r.HeadSHA, Event: r.Event, Status: r.Status,
			Conclusion: r.Conclusion, CreatedAt: r.CreatedAt, Probe: strings.HasPrefix(r.Title, ProbeTitlePrefix)})
	}
	return out, nil
}
