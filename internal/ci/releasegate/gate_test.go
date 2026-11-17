package releasegate_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/ci/releasegate"
)

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func run(day int, opts ...func(*releasegate.Run)) releasegate.Run {
	r := releasegate.Run{
		ID:             int64(day),
		Status:         "completed",
		Conclusion:     "success",
		Event:          "schedule",
		HeadSHA:        sha,
		HTMLURL:        "https://github.com/o/r/actions/runs/" + strconv.Itoa(day),
		CreatedAt:      time.Date(2026, 10, day, 7, 0, 0, 0, time.UTC),
		RunStartedAt:   time.Date(2026, 10, day, 7, 3, 0, 0, time.UTC),
		IntegrationRan: true,
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func withSHA(s string) func(*releasegate.Run) {
	return func(r *releasegate.Run) { r.HeadSHA = s }
}
func withConclusion(c string) func(*releasegate.Run) {
	return func(r *releasegate.Run) { r.Conclusion = c }
}
func withEvent(e string) func(*releasegate.Run) {
	return func(r *releasegate.Run) { r.Event = e }
}
func withStatus(s string) func(*releasegate.Run) {
	return func(r *releasegate.Run) { r.Status = s }
}
func deduped(r *releasegate.Run) { r.IntegrationRan, r.Deduped = false, true }
func notRun(r *releasegate.Run)  { r.IntegrationRan = false }
func atHour(h int) func(*releasegate.Run) {
	return func(r *releasegate.Run) {
		y, m, d := r.RunStartedAt.Date()
		r.RunStartedAt = time.Date(y, m, d, h, 0, 0, 0, time.UTC)
		r.CreatedAt = r.RunStartedAt
	}
}

func TestEvaluateThreeGreenNightsPass(t *testing.T) {
	ok, lines := releasegate.Evaluate([]releasegate.Run{
		run(18, withSHA(strings.Repeat("b", 40))),
		run(19, withSHA(strings.Repeat("c", 40))),
		run(20),
	}, sha, 3)
	if !ok {
		t.Fatalf("expected pass, lines=%v", lines)
	}
}

func TestEvaluatePasses(t *testing.T) {
	cases := []struct {
		name string
		runs []releasegate.Run
	}{
		// A re-run on the same day: the newest attempt wins, so an earlier red attempt is replaced.
		{"same-day re-run wins", []releasegate.Run{
			run(18), run(19),
			run(20, withConclusion("failure"), atHour(7)),
			run(20, atHour(9)),
		}},
		// Two green runs on one date are one day; they do not fill a missing night.
		{"deduped run ignored when others are green", []releasegate.Run{
			run(18), run(19), run(20), run(20, deduped, atHour(12)),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, lines := releasegate.Evaluate(tc.runs, sha, 3)
			if !ok {
				t.Fatalf("expected pass, lines=%v", lines)
			}
		})
	}
}

func TestEvaluateBlocks(t *testing.T) {
	cases := []struct {
		name   string
		runs   []releasegate.Run
		reason string
	}{
		{"too few", []releasegate.Run{run(19), run(20)}, "only 2 night(s)"},
		{"failure", []releasegate.Run{run(18), run(19, withConclusion("failure")), run(20)}, "did not succeed"},
		{"gap", []releasegate.Run{run(17), run(19), run(20)}, "not on consecutive nights"},
		{"wrong sha", []releasegate.Run{run(18), run(19), run(20, withSHA(strings.Repeat("d", 40)))}, "not the release commit"},
		{"dispatch", []releasegate.Run{run(18), run(19), run(20, withEvent("workflow_dispatch"))}, "only 2"},
		{"in progress", []releasegate.Run{run(18), run(19), run(20, withStatus("in_progress"))}, "only 2"},
		{"new failure", []releasegate.Run{
			run(16, withConclusion("failure")),
			run(18), run(19),
			run(20, withConclusion("failure")),
		}, "did not succeed"},
		{"two runs one date count once", []releasegate.Run{run(19), run(20, atHour(7)), run(20, atHour(9))}, "only 2 night(s)"},
		{"deduped night breaks the streak", []releasegate.Run{run(17), run(18), run(19, deduped), run(20)}, "not on consecutive nights"},
		{"skipped integration does not count", []releasegate.Run{run(18), run(19), run(20, notRun)}, "only 2"},
		{"same-day re-run red wins", []releasegate.Run{
			run(18), run(19),
			run(20, atHour(7)),
			run(20, withConclusion("failure"), atHour(9)),
		}, "did not succeed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, lines := releasegate.Evaluate(tc.runs, sha, 3)
			if ok {
				t.Fatalf("expected fail, lines=%v", lines)
			}
			if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], tc.reason) {
				t.Fatalf("lines=%v, want reason %q", lines, tc.reason)
			}
		})
	}
}

func TestEvaluateReportsIgnoredRuns(t *testing.T) {
	_, lines := releasegate.Evaluate([]releasegate.Run{run(18), run(19), run(20), run(20, deduped, atHour(9))}, sha, 3)
	if !strings.Contains(strings.Join(lines, "\n"), "ignored 1 scheduled run(s)") {
		t.Fatalf("lines=%v, want the ignored count", lines)
	}
}

func fixture(t *testing.T, name string) []releasegate.Run {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := releasegate.ParseRunsJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func TestFixtures(t *testing.T) {
	cases := []struct {
		file   string
		ok     bool
		reason string
	}{
		{"three-green.json", true, "3 consecutive green nights"},
		{"too-few.json", false, "only 2"},
		{"not-green.json", false, "did not succeed"},
		{"deduped.json", false, "only 2"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			ok, lines := releasegate.Evaluate(fixture(t, tc.file), sha, 3)
			if ok != tc.ok || !strings.Contains(lines[len(lines)-1], tc.reason) {
				t.Fatalf("ok=%v lines=%v, want ok=%v reason %q", ok, lines, tc.ok, tc.reason)
			}
		})
	}
}

func TestParseRunsJSON(t *testing.T) {
	payload := map[string]any{
		"workflow_runs": []map[string]any{
			{
				"id": 42, "status": "completed", "conclusion": "success", "event": "schedule",
				"head_sha": sha, "html_url": "https://example/1",
				"created_at": "2026-10-20T07:00:00Z", "run_started_at": "2026-10-20T07:03:00.5Z",
				"jobs": []map[string]any{{"name": "dedupe", "conclusion": "success"}, {"name": "integration", "conclusion": "failure"}},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := releasegate.ParseRunsJSON(raw)
	if err != nil || len(runs) != 1 {
		t.Fatalf("got %#v err=%v", runs, err)
	}
	r := runs[0]
	if r.ID != 42 || r.HeadSHA != sha || !r.IntegrationRan || r.Deduped || r.RunStartedAt.IsZero() {
		t.Fatalf("run = %#v", r)
	}

	for name, text := range map[string]string{"not json": "{", "no workflow_runs": `{"total_count":0}`} {
		t.Run(name, func(t *testing.T) {
			if _, err := releasegate.ParseRunsJSON([]byte(text)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestParseJobsJSON(t *testing.T) {
	jobs, err := releasegate.ParseJobsJSON([]byte(`{"jobs":[{"name":"integration","status":"completed","conclusion":"skipped"}]}`))
	if err != nil || len(jobs) != 1 || jobs[0].Conclusion != "skipped" {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	for _, text := range []string{"{", `{}`} {
		if _, err := releasegate.ParseJobsJSON([]byte(text)); err == nil {
			t.Errorf("ParseJobsJSON(%s) succeeded", text)
		}
	}
}

// fakeAPI serves the runs list and per-run jobs from maps keyed by path.
func fakeAPI(t *testing.T, bodies map[string]string, errs map[string]error) (releasegate.APIFunc, *[]string) {
	var calls []string
	return func(_ context.Context, path string) ([]byte, error) {
		calls = append(calls, path)
		if err, ok := errs[path]; ok {
			return nil, err
		}
		body, ok := bodies[path]
		if !ok {
			t.Errorf("unexpected API call %s", path)
			return nil, errors.New("404")
		}
		return []byte(body), nil
	}, &calls
}

const runsPath = "repos/o/r/actions/workflows/nightly.yml/runs?branch=main&event=schedule&per_page=30"

func runsBody(ids ...int) string {
	var parts []string
	for _, id := range ids {
		parts = append(parts, `{"id":`+strconv.Itoa(id)+`,"status":"completed","conclusion":"success","event":"schedule","head_sha":"`+sha+`","run_started_at":"2026-10-`+strconv.Itoa(id)+`T07:03:00Z"}`)
	}
	parts = append(parts, `{"id":99,"status":"in_progress","event":"schedule","run_started_at":"2026-10-21T07:03:00Z"}`)
	return `{"workflow_runs":[` + strings.Join(parts, ",") + `]}`
}

func jobsPath(id int) string {
	return "repos/o/r/actions/runs/" + strconv.Itoa(id) + "/jobs?per_page=100"
}

func TestFetchRunsWith(t *testing.T) {
	api, calls := fakeAPI(t, map[string]string{
		runsPath:     runsBody(18, 19, 20),
		jobsPath(18): `{"jobs":[{"name":"integration","conclusion":"success"}]}`,
		jobsPath(19): `{"jobs":[{"name":"dedupe","conclusion":"success"},{"name":"integration","conclusion":"skipped"}]}`,
		jobsPath(20): `{"jobs":[{"name":"integration","conclusion":"timed_out"}]}`,
	}, nil)
	runs, err := releasegate.FetchRunsWith(context.Background(), api, "o/r", "nightly.yml", "main")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]releasegate.Run{}
	for _, r := range runs {
		got[r.ID] = r
	}
	if !got[18].IntegrationRan || got[18].Deduped {
		t.Errorf("run 18 = %+v, want ran", got[18])
	}
	if got[19].IntegrationRan || !got[19].Deduped {
		t.Errorf("run 19 = %+v, want deduped", got[19])
	}
	if !got[20].IntegrationRan {
		t.Errorf("run 20 = %+v, want ran (timed_out is terminal)", got[20])
	}
	for _, c := range *calls {
		if strings.Contains(c, "/runs/99/") {
			t.Errorf("fetched jobs of an in-progress run: %s", c)
		}
	}
}

func TestFetchRunsWith_Errors(t *testing.T) {
	boom := errors.New("API rate limit exceeded")
	cases := []struct {
		name   string
		bodies map[string]string
		errs   map[string]error
		want   string
	}{
		{"list fails", nil, map[string]error{runsPath: boom}, "rate limit"},
		{"list not json", map[string]string{runsPath: "<html>"}, nil, "decode runs JSON"},
		{"jobs fail", map[string]string{runsPath: runsBody(20)}, map[string]error{jobsPath(20): boom}, "jobs of run 20"},
		{"jobs not json", map[string]string{runsPath: runsBody(20), jobsPath(20): "nope"}, nil, "run 20"},
		{"run without id", map[string]string{runsPath: `{"workflow_runs":[{"status":"completed","event":"schedule","html_url":"u"}]}`}, nil, "no id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := fakeAPI(t, tc.bodies, tc.errs)
			_, err := releasegate.FetchRunsWith(context.Background(), api, "o/r", "nightly.yml", "main")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// Only the newest completed scheduled runs get a job lookup; older pages are not needed.
func TestFetchRunsWith_BoundsJobLookups(t *testing.T) {
	ids := []int{}
	bodies := map[string]string{}
	for d := 1; d <= 15; d++ {
		ids = append(ids, d)
		bodies[jobsPath(d)] = `{"jobs":[{"name":"integration","conclusion":"success"}]}`
	}
	bodies[runsPath] = runsBody(ids...)
	var jobsCalls int
	api := func(_ context.Context, path string) ([]byte, error) {
		if strings.Contains(path, "/jobs") {
			jobsCalls++
		}
		return []byte(bodies[path]), nil
	}
	if _, err := releasegate.FetchRunsWith(context.Background(), api, "o/r", "nightly.yml", "main"); err != nil {
		t.Fatal(err)
	}
	if jobsCalls != 10 {
		t.Fatalf("job lookups = %d, want 10", jobsCalls)
	}
}

func TestRepoFromRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:mminichino/cbctl.git\n":   "mminichino/cbctl",
		"https://github.com/owner/name.git":       "owner/name",
		"https://github.com/owner/name/":          "owner/name",
		"ssh://git@github.com/owner/name.git":     "owner/name",
		"https://gitlab.com/owner/name.git":       "",
		"https://github.com/owner":                "",
		"https://github.com/owner/name/extra.git": "",
		"": "",
	}
	for in, want := range cases {
		if got := releasegate.RepoFromRemote(in); got != want {
			t.Errorf("RepoFromRemote(%q) = %q, want %q", in, got, want)
		}
	}
}
