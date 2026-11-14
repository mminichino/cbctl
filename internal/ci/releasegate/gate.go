// Package releasegate decides whether a release tag is allowed based on recent
// nightly CI runs (CAP-032 AC5).
package releasegate

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Run is one GitHub Actions workflow run (the fields Evaluate needs).
type Run struct {
	Status       string
	Conclusion   string
	Event        string
	HeadSHA      string
	HTMLURL      string
	CreatedAt    time.Time
	RunStartedAt time.Time
}

// Evaluate reports whether the last nights completed scheduled runs of the
// nightly workflow all succeeded on consecutive UTC dates, with the newest on
// commit. runs may be in any order; only completed schedule events count.
// Default nights behaviour is for the caller to pass 3.
func Evaluate(runs []Run, commit string, nights int) (ok bool, lines []string) {
	var done []Run
	for _, r := range runs {
		if r.Status == "completed" && r.Event == "schedule" {
			done = append(done, r)
		}
	}
	sortNewestFirst(done)
	if nights < 0 {
		nights = 0
	}
	last := done
	if len(last) > nights {
		last = last[:nights]
	}
	for _, r := range last {
		day := dayOf(r)
		sha := r.HeadSHA
		if len(sha) > 12 {
			sha = sha[:12]
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", day.Format("2006-01-02"), r.Conclusion, sha, r.HTMLURL))
	}
	if len(last) < nights {
		return false, append(lines, fmt.Sprintf("only %d completed nightly run(s); %d green nights are needed", len(last), nights))
	}
	var red int
	for _, r := range last {
		if r.Conclusion != "success" {
			red++
		}
	}
	if red > 0 {
		return false, append(lines, fmt.Sprintf("%d of the last %d nightly runs did not succeed", red, nights))
	}
	for i := 0; i < len(last)-1; i++ {
		if dayOf(last[i]).Sub(dayOf(last[i+1])) != 24*time.Hour {
			return false, append(lines, fmt.Sprintf("the last %d green runs are not on consecutive nights", nights))
		}
	}
	newest := last[0].HeadSHA
	if newest != commit {
		short := func(s string) string {
			if len(s) > 12 {
				return s[:12]
			}
			return s
		}
		return false, append(lines, fmt.Sprintf(
			"the newest green nightly tested %s, not the release commit %s; tag the commit the nightly tested, or wait for the next nightly",
			short(newest), short(commit)))
	}
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	return true, append(lines, fmt.Sprintf("%d consecutive green nights, the newest on %s", nights, short))
}

// FetchRuns loads the most recent scheduled runs of workflow on branch via
// `gh api`. ctx cancels the subprocess.
func FetchRuns(ctx context.Context, repo, workflow, branch string) ([]Run, error) {
	path := fmt.Sprintf("repos/%s/actions/workflows/%s/runs?branch=%s&event=schedule&per_page=30",
		repo, workflow, branch)
	cmd := exec.CommandContext(ctx, "gh", "api", path)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("release-gate: gh api failed: %s", msg)
	}
	return ParseRunsJSON(out)
}

// ParseRunsJSON decodes a GitHub Actions workflow-runs list payload
// ({"workflow_runs":[…]}).
func ParseRunsJSON(raw []byte) ([]Run, error) {
	var payload struct {
		WorkflowRuns []ghRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("release-gate: decode runs JSON: %w", err)
	}
	out := make([]Run, 0, len(payload.WorkflowRuns))
	for _, g := range payload.WorkflowRuns {
		out = append(out, g.toRun())
	}
	return out, nil
}

type ghRun struct {
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Event        string `json:"event"`
	HeadSHA      string `json:"head_sha"`
	HTMLURL      string `json:"html_url"`
	CreatedAt    string `json:"created_at"`
	RunStartedAt string `json:"run_started_at"`
}

func (g ghRun) toRun() Run {
	return Run{
		Status:       g.Status,
		Conclusion:   g.Conclusion,
		Event:        g.Event,
		HeadSHA:      g.HeadSHA,
		HTMLURL:      g.HTMLURL,
		CreatedAt:    parseTime(g.CreatedAt),
		RunStartedAt: parseTime(g.RunStartedAt),
	}
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func dayOf(r Run) time.Time {
	t := r.RunStartedAt
	if t.IsZero() {
		t = r.CreatedAt
	}
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func sortNewestFirst(runs []Run) {
	for i := 0; i < len(runs); i++ {
		for j := i + 1; j < len(runs); j++ {
			if startedAt(runs[j]).After(startedAt(runs[i])) {
				runs[i], runs[j] = runs[j], runs[i]
			}
		}
	}
}

func startedAt(r Run) time.Time {
	if !r.RunStartedAt.IsZero() {
		return r.RunStartedAt
	}
	return r.CreatedAt
}
