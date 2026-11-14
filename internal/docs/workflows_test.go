package docs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestWorkflowsLint(t *testing.T) {
	root := repoRoot(t)
	wfDir := filepath.Join(root, ".github", "workflows")

	release := loadYAML(t, filepath.Join(wfDir, "release.yml"))
	jobs, _ := release["jobs"].(map[string]any)
	goreleaser, _ := jobs["goreleaser"].(map[string]any)
	needs := goreleaser["needs"]
	if !needsContains(needs, "gate") {
		t.Fatalf("release.yml goreleaser job must need gate, got %#v", needs)
	}

	nightly := loadYAML(t, filepath.Join(wfDir, "nightly.yml"))
	on, _ := nightly["on"].(map[string]any)
	if on == nil {
		on, _ = nightly["true"].(map[string]any) // yaml may parse on as true
	}
	// GitHub workflow "on" key can be parsed as boolean true by yaml.v3
	var schedule any
	if on != nil {
		schedule = on["schedule"]
	} else {
		// re-read raw for "on:"
		raw := readFile(t, filepath.Join(wfDir, "nightly.yml"))
		if !strings.Contains(raw, `cron: "0 7 * * *"`) || !strings.Contains(raw, `cron: "0 9 * * 0"`) {
			t.Fatalf("nightly.yml missing expected cron schedules")
		}
	}
	if schedule != nil {
		list, _ := schedule.([]any)
		if len(list) < 2 {
			t.Fatalf("nightly.yml expected 2 cron schedules, got %#v", schedule)
		}
	}
	conc, _ := nightly["concurrency"].(map[string]any)
	if conc["cancel-in-progress"] != false {
		t.Fatalf("nightly.yml concurrency.cancel-in-progress want false, got %#v", conc["cancel-in-progress"])
	}

	ciRaw := readFile(t, filepath.Join(wfDir, "ci.yml"))
	if !strings.Contains(ciRaw, "make test-integration-fake") {
		t.Fatal("ci.yml must run make test-integration-fake")
	}
	if !strings.Contains(ciRaw, "make docs-check") {
		t.Fatal("ci.yml must run make docs-check")
	}

	for _, name := range []string{"ci.yml", "release.yml"} {
		raw := readFile(t, filepath.Join(wfDir, name))
		if strings.Contains(raw, "CAPELLA_SANDBOX_") {
			t.Fatalf("%s must not reference CAPELLA_SANDBOX_* secrets", name)
		}
	}
	nightlyRaw := readFile(t, filepath.Join(wfDir, "nightly.yml"))
	if !strings.Contains(nightlyRaw, "CAPELLA_SANDBOX_") {
		t.Fatal("nightly.yml should reference CAPELLA_SANDBOX_* secrets")
	}
}

func TestNOTICEMentionsCapellactl(t *testing.T) {
	raw := readFile(t, filepath.Join(repoRoot(t), "NOTICE"))
	if !strings.Contains(raw, "capellactl") {
		t.Fatal("NOTICE must mention capellactl")
	}
	if !strings.Contains(raw, "Apache-2.0") && !strings.Contains(raw, "Apache 2.0") && !strings.Contains(raw, "Apache License") {
		t.Fatal("NOTICE must mention Apache license")
	}
}

func TestLinksBacklog(t *testing.T) {
	// Imported via shell-free package boundary: call links from here would create a cycle with docs tests.
	// Keep a smoke assertion that generated indexes exist after make docs.
	root := repoRoot(t)
	for _, p := range []string{
		"docs/backlog/CAP-ID-MAP.md",
		"docs/backlog/release-1.5.0/README.md",
		"docs/backlog/release-1.0.0/API-COVERAGE.md",
		"docs/capella/reference.md",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Fatalf("missing generated file %s: %v", p, err)
		}
	}
}

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	raw := readFile(t, path)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func needsContains(needs any, want string) bool {
	switch v := needs.(type) {
	case string:
		return v == want
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}
