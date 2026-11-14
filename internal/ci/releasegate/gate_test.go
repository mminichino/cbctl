package releasegate_test

import (
	"encoding/json"
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
		Status:       "completed",
		Conclusion:   "success",
		Event:        "schedule",
		HeadSHA:      sha,
		HTMLURL:      "https://github.com/o/r/actions/runs/" + strconv.Itoa(day),
		CreatedAt:    time.Date(2026, 10, day, 7, 0, 0, 0, time.UTC),
		RunStartedAt: time.Date(2026, 10, day, 7, 3, 0, 0, time.UTC),
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

func TestEvaluateBlocks(t *testing.T) {
	cases := []struct {
		name   string
		runs   []releasegate.Run
		reason string
	}{
		{"too few", []releasegate.Run{run(19), run(20)}, "only 2 completed nightly run(s)"},
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

func TestParseRunsJSONFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "three-green.json"))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := releasegate.ParseRunsJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	ok, lines := releasegate.Evaluate(runs, sha, 3)
	if !ok {
		t.Fatalf("fixture should pass: %v", lines)
	}
}

func TestParseRunsJSONTooFew(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "too-few.json"))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := releasegate.ParseRunsJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	ok, lines := releasegate.Evaluate(runs, sha, 3)
	if ok || !strings.Contains(lines[len(lines)-1], "only 2") {
		t.Fatalf("ok=%v lines=%v", ok, lines)
	}
}

func TestParseRunsJSONRoundTrip(t *testing.T) {
	payload := map[string]any{
		"workflow_runs": []map[string]any{
			{
				"status": "completed", "conclusion": "success", "event": "schedule",
				"head_sha": sha, "html_url": "https://example/1",
				"created_at": "2026-10-20T07:00:00Z", "run_started_at": "2026-10-20T07:03:00Z",
			},
		},
	}
	raw, _ := json.Marshal(payload)
	runs, err := releasegate.ParseRunsJSON(raw)
	if err != nil || len(runs) != 1 || runs[0].HeadSHA != sha {
		t.Fatalf("got %#v err=%v", runs, err)
	}
}
