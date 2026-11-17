package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGate_CAP032_AC5_CLIAndReportOnly: without --report-only the health report exits 1 on too
// few green nights (local diagnosis), and --report-only prints the verdict but exits 0.
func TestGate_CAP032_AC5_CLIAndReportOnly(t *testing.T) {
	sha := strings.Repeat("a", 40)
	run := func(day int) map[string]any {
		return map[string]any{
			"run_started_at": fmt.Sprintf("2026-10-%02dT07:03:00Z", day), "created_at": fmt.Sprintf("2026-10-%02dT07:00:00Z", day),
			"head_sha": sha, "conclusion": "success", "event": "schedule", "status": "completed",
			"html_url": fmt.Sprintf("https://github.com/o/r/actions/runs/%d", day),
		}
	}
	raw, err := json.Marshal(map[string]any{"workflow_runs": []any{run(19), run(20)}})
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "runs.json")
	if err := os.WriteFile(f, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := cmdCIReleaseGate([]string{"--commit", sha, "--runs-json", f}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FAILED") || strings.Contains(out.String(), "report only") {
		t.Errorf("blocking run output:\n%s", out.String())
	}
	out.Reset()
	if code := cmdCIReleaseGate([]string{"--commit", sha, "--runs-json", f, "--report-only"}, &out, &errb); code != 0 {
		t.Fatalf("--report-only exit = %d, want 0\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "report only") {
		t.Errorf("--report-only output lacks 'report only':\n%s", out.String())
	}
	out.Reset()
	if code := cmdCIReleaseGate([]string{"--runs-json", f}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--commit is required") {
		t.Errorf("missing --commit: exit %d, stderr %q", code, errb.String())
	}
}
