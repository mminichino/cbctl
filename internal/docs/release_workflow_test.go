package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReleaseWorkflowNightlyIsInformational pins that the nightly is a health signal, not a
// release gate: the release runs the report with --report-only, goreleaser does not need it,
// permissions are least privilege, and goreleaser cannot run unbounded.
func TestReleaseWorkflowNightlyIsInformational(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	wf := loadYAML(t, path)
	if p, _ := wf["permissions"].(map[string]any); asString(p["contents"]) != "read" || len(p) != 1 {
		t.Errorf("workflow permissions = %#v, want only contents: read", wf["permissions"])
	}
	jobs, _ := wf["jobs"].(map[string]any)
	release, _ := jobs["goreleaser"].(map[string]any)
	if release == nil {
		t.Fatal("release.yml has no goreleaser job")
	}
	if p, _ := release["permissions"].(map[string]any); asString(p["contents"]) != "write" {
		t.Errorf("goreleaser permissions = %#v, want contents: write", release["permissions"])
	}
	if mins, ok := release["timeout-minutes"].(int); !ok || mins <= 0 || mins > 30 {
		t.Errorf("goreleaser timeout-minutes = %#v, want 1..30", release["timeout-minutes"])
	}
	if needs := release["needs"]; needs != nil {
		t.Errorf("goreleaser needs = %#v; the nightly must not gate a release", needs)
	}
	health, _ := jobs["nightly-health"].(map[string]any)
	if health == nil {
		t.Fatal("release.yml has no nightly-health job")
	}
	if p, _ := health["permissions"].(map[string]any); asString(p["contents"]) != "read" || asString(p["actions"]) != "read" {
		t.Errorf("nightly-health permissions = %#v, want contents: read, actions: read", health["permissions"])
	}
	if health["continue-on-error"] != true {
		t.Errorf("nightly-health continue-on-error = %#v, want true", health["continue-on-error"])
	}
	if !strings.Contains(stepRuns(health), "--report-only") {
		t.Error("nightly-health must pass --report-only: the nightly never blocks")
	}
	for name := range jobs {
		job, _ := jobs[name].(map[string]any)
		if needsContains(job["needs"], "nightly-health") {
			t.Errorf("job %s needs nightly-health; the nightly must not gate anything", name)
		}
	}
}

// TestNoWorkflowWaitsForTheNightly: no other workflow depends on nightly.yml (workflow_run,
// a required gate step), so the nightly can never block a merge, push or tag.
func TestNoWorkflowWaitsForTheNightly(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "nightly.yml" {
			continue
		}
		raw := readFile(t, filepath.Join(dir, e.Name()))
		if strings.Contains(raw, "workflow_run") {
			t.Errorf("%s triggers on workflow_run; the nightly must stay informational", e.Name())
		}
		if strings.Contains(raw, "cbdev ci release-gate") && !strings.Contains(raw, "--report-only") {
			t.Errorf("%s runs the nightly report without --report-only", e.Name())
		}
	}
}

var usesRE = regexp.MustCompile(`(?m)^\s*(?:-\s*)?uses:\s*([^\s#]+)`)
var pinnedRE = regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)

// TestWorkflowActionsArePinned: every third-party action is pinned to a full commit SHA.
func TestWorkflowActionsArePinned(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		raw := readFile(t, filepath.Join(dir, e.Name()))
		for _, m := range usesRE.FindAllStringSubmatch(raw, -1) {
			ref := strings.Trim(m[1], `"'`)
			if strings.HasPrefix(ref, "./") {
				continue // local action
			}
			seen++
			if !pinnedRE.MatchString(ref) {
				t.Errorf("%s: %s is not pinned to a 40-hex commit SHA", e.Name(), ref)
			}
		}
	}
	if seen == 0 {
		t.Fatal("found no uses: lines; the pattern is broken")
	}
}

// TestCIRunsTieredCoverGate pins P-TST-18 in ci.yml: the cover job builds both profiles and
// runs the tiered gate over them.
func TestCIRunsTieredCoverGate(t *testing.T) {
	wf := loadYAML(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	jobs, _ := wf["jobs"].(map[string]any)
	cover, _ := jobs["cover"].(map[string]any)
	if cover == nil {
		t.Fatal("ci.yml has no cover job")
	}
	var runs []string
	steps, _ := cover["steps"].([]any)
	for _, s := range steps {
		m, _ := s.(map[string]any)
		runs = append(runs, asString(m["run"]))
	}
	all := strings.Join(runs, "\n")
	for _, want := range []string{"make cover-profile", "make cover-server", "cbdev ci cover-gate --tiers default", "--profile .cover/unit.out", "--profile .cover/server.out"} {
		if !strings.Contains(all, want) {
			t.Errorf("cover job does not run %q:\n%s", want, all)
		}
	}
}
