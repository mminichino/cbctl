package accoverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestACCoverage_CAP001_AC5_InProgressGapsListedNotFailed: an In progress story's untested ACs
// are listed but do not fail the check; --strict fails on them.
func TestACCoverage_CAP001_AC5_InProgressGapsListedNotFailed(t *testing.T) {
	dir := t.TempDir()
	docs, tests := filepath.Join(dir, "docs"), filepath.Join(dir, "tests")
	for _, d := range []string{docs, tests} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	story := "# CAP-999 — Example\n\n| Epic | Priority | Points | Depends on | Status |\n|---|---|---|---|---|\n" +
		"| E1 | P0 | 1 | — | In progress |\n\n### AC1 One\n### AC2 Two\n### AC3 Three\n"
	if err := os.WriteFile(filepath.Join(docs, "CAP-999-example.md"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package tests\n\nfunc TestExample_CAP999_AC1_One(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(tests, "example_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Check(Options{Docs: []string{docs}, Tests: tests})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.Failing) != 0 {
		t.Fatalf("in-progress gaps must not fail: %+v", res)
	}
	if !strings.Contains(res.Message, "In progress: 2 acceptance criteria not tested yet") || !strings.Contains(res.Message, "CAP-999 AC3") {
		t.Fatalf("message does not list the gaps:\n%s", res.Message)
	}

	strict, err := Check(Options{Docs: []string{docs}, Tests: tests, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if strict.OK || len(strict.Failing) != 2 {
		t.Fatalf("--strict must fail on the 2 gaps: %+v", strict)
	}
}
