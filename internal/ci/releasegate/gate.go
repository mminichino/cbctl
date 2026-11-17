// Package releasegate summarizes recent scheduled nightly CI runs as a health signal for a
// branch or a release commit (CAP-032 AC5). It is informational: release.yml runs it with
// --report-only and nothing waits for it.
//
// A night counts only when a scheduled run of the nightly workflow completed and its
// integration job actually ran: a run whose integration job was skipped (the dedupe job
// found a duplicate on the same commit) finishes "success" without testing anything, so it
// neither counts nor fills that night. Within one UTC day the newest counted attempt wins.
package releasegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// IntegrationJob is the nightly job whose execution makes a run count.
const IntegrationJob = "integration"

// maxJobLookups bounds the per-run job requests FetchRuns makes (newest runs first).
const maxJobLookups = 10

// Run is one GitHub Actions workflow run (the fields Evaluate needs).
type Run struct {
	ID           int64
	Status       string
	Conclusion   string
	Event        string
	HeadSHA      string
	HTMLURL      string
	CreatedAt    time.Time
	RunStartedAt time.Time
	// IntegrationRan is true when the integration job reached a terminal conclusion
	// (success, failure, cancelled or timed_out) rather than being skipped.
	IntegrationRan bool
	// Deduped is true when the integration job was skipped because dedupe matched another run.
	Deduped bool
}

// Job is one job of a workflow run.
type Job struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// APIFunc performs a GitHub REST GET for path (relative to api.github.com) and returns the body.
type APIFunc func(ctx context.Context, path string) ([]byte, error)

// Evaluate reports whether the last nights counted scheduled runs of the nightly workflow
// all succeeded on consecutive UTC dates, with the newest on commit. runs may be in any order.
func Evaluate(runs []Run, commit string, nights int) (ok bool, lines []string) {
	if nights < 0 {
		nights = 0
	}
	var skipped int
	byDay := map[time.Time]Run{}
	for _, r := range runs {
		if r.Status != "completed" || r.Event != "schedule" {
			continue
		}
		if r.Deduped || !r.IntegrationRan {
			skipped++
			continue
		}
		day := dayOf(r)
		if prev, seen := byDay[day]; !seen || startedAt(r).After(startedAt(prev)) {
			byDay[day] = r
		}
	}
	days := make([]Run, 0, len(byDay))
	for _, r := range byDay {
		days = append(days, r)
	}
	sortNewestFirst(days)

	last := days
	if len(last) > nights {
		last = last[:nights]
	}
	for _, r := range last {
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", dayOf(r).Format("2006-01-02"), r.Conclusion, short(r.HeadSHA), r.HTMLURL))
	}
	if skipped > 0 {
		lines = append(lines, fmt.Sprintf("ignored %d scheduled run(s) whose integration job did not run (deduped or skipped)", skipped))
	}
	if len(last) < nights {
		return false, append(lines, fmt.Sprintf("only %d night(s) with a completed nightly integration run; %d green nights are needed", len(last), nights))
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
	if nights == 0 {
		return true, append(lines, "no green nights required")
	}
	if newest := last[0].HeadSHA; newest != commit {
		return false, append(lines, fmt.Sprintf(
			"the newest green nightly tested %s, not the release commit %s; the next nightly will test it (informational, not a gate)",
			short(newest), short(commit)))
	}
	return true, append(lines, fmt.Sprintf("%d consecutive green nights, the newest on %s", nights, short(commit)))
}

// FetchRuns loads the most recent scheduled runs of workflow on branch, and the integration
// job outcome of each, via `gh api`. ctx cancels the subprocesses.
func FetchRuns(ctx context.Context, repo, workflow, branch string) ([]Run, error) {
	return FetchRunsWith(ctx, GHAPI, repo, workflow, branch)
}

// FetchRunsWith is FetchRuns with an injectable API client.
func FetchRunsWith(ctx context.Context, api APIFunc, repo, workflow, branch string) ([]Run, error) {
	path := fmt.Sprintf("repos/%s/actions/workflows/%s/runs?branch=%s&event=schedule&per_page=30",
		repo, workflow, branch)
	raw, err := api(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("release-gate: listing runs: %w", err)
	}
	runs, err := ParseRunsJSON(raw)
	if err != nil {
		return nil, err
	}
	sortNewestFirst(runs)
	looked := 0
	for i := range runs {
		r := &runs[i]
		if r.Status != "completed" || r.Event != "schedule" {
			continue
		}
		if looked == maxJobLookups {
			break
		}
		looked++
		if r.ID == 0 {
			return nil, fmt.Errorf("release-gate: run %s has no id", r.HTMLURL)
		}
		jobsRaw, err := api(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, r.ID))
		if err != nil {
			return nil, fmt.Errorf("release-gate: listing jobs of run %d: %w", r.ID, err)
		}
		jobs, err := ParseJobsJSON(jobsRaw)
		if err != nil {
			return nil, fmt.Errorf("release-gate: run %d: %w", r.ID, err)
		}
		r.IntegrationRan, r.Deduped = classify(jobs)
	}
	return runs, nil
}

// GHAPI runs `gh api PATH`.
func GHAPI(ctx context.Context, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", path)
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(strings.TrimSpace(string(ee.Stderr))) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("gh api %s: %s", path, msg)
	}
	return out, nil
}

// classify reads the integration job outcome: ran when it reached a terminal conclusion,
// deduped when it was skipped. A run without an integration job did not test anything.
func classify(jobs []Job) (ran, deduped bool) {
	for _, j := range jobs {
		if j.Name != IntegrationJob {
			continue
		}
		switch j.Conclusion {
		case "success", "failure", "cancelled", "timed_out":
			return true, false
		case "skipped":
			return false, true
		}
		return false, false
	}
	return false, false
}

// ParseRunsJSON decodes a GitHub Actions workflow-runs list payload ({"workflow_runs":[…]}).
// A run may carry a "jobs" array (fixture extension; the live API lists jobs separately),
// which sets IntegrationRan and Deduped.
func ParseRunsJSON(raw []byte) ([]Run, error) {
	var payload struct {
		WorkflowRuns *[]ghRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("release-gate: decode runs JSON: %w", err)
	}
	if payload.WorkflowRuns == nil {
		return nil, errors.New("release-gate: runs JSON has no workflow_runs")
	}
	out := make([]Run, 0, len(*payload.WorkflowRuns))
	for _, g := range *payload.WorkflowRuns {
		out = append(out, g.toRun())
	}
	return out, nil
}

// ParseJobsJSON decodes a GitHub Actions run-jobs payload ({"jobs":[…]}).
func ParseJobsJSON(raw []byte) ([]Job, error) {
	var payload struct {
		Jobs *[]Job `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode jobs JSON: %w", err)
	}
	if payload.Jobs == nil {
		return nil, errors.New("jobs JSON has no jobs")
	}
	return *payload.Jobs, nil
}

type ghRun struct {
	ID           int64  `json:"id"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Event        string `json:"event"`
	HeadSHA      string `json:"head_sha"`
	HTMLURL      string `json:"html_url"`
	CreatedAt    string `json:"created_at"`
	RunStartedAt string `json:"run_started_at"`
	Jobs         []Job  `json:"jobs"`
}

func (g ghRun) toRun() Run {
	r := Run{
		ID:           g.ID,
		Status:       g.Status,
		Conclusion:   g.Conclusion,
		Event:        g.Event,
		HeadSHA:      g.HeadSHA,
		HTMLURL:      g.HTMLURL,
		CreatedAt:    parseTime(g.CreatedAt),
		RunStartedAt: parseTime(g.RunStartedAt),
	}
	if g.Jobs != nil {
		r.IntegrationRan, r.Deduped = classify(g.Jobs)
	}
	return r
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func dayOf(r Run) time.Time {
	y, m, d := startedAt(r).UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func sortNewestFirst(runs []Run) {
	sort.SliceStable(runs, func(i, j int) bool { return startedAt(runs[i]).After(startedAt(runs[j])) })
}

func startedAt(r Run) time.Time {
	if !r.RunStartedAt.IsZero() {
		return r.RunStartedAt
	}
	return r.CreatedAt
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// RepoFromRemote returns OWNER/NAME for a GitHub remote URL (https, ssh or scp form), or "".
func RepoFromRemote(url string) string {
	u := strings.TrimSpace(url)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:"} {
		if rest, ok := strings.CutPrefix(u, prefix); ok {
			parts := strings.Split(rest, "/")
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				return parts[0] + "/" + parts[1]
			}
			return ""
		}
	}
	return ""
}
