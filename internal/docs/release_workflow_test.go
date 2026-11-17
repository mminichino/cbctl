package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReleaseWorkflowBlocks pins Phase 7: the release gate blocks, permissions are least
// privilege, and goreleaser cannot run unbounded.
func TestReleaseWorkflowBlocks(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	if raw := readFile(t, path); strings.Contains(raw, "--report-only") {
		t.Error("release.yml must not pass --report-only: the gate blocks")
	}
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
	gate, _ := jobs["gate"].(map[string]any)
	if p, _ := gate["permissions"].(map[string]any); asString(p["contents"]) != "read" || asString(p["actions"]) != "read" {
		t.Errorf("gate permissions = %#v, want contents: read, actions: read", gate["permissions"])
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
