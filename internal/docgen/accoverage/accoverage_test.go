package accoverage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckImplementedMissingFails(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	tests := filepath.Join(dir, "tests")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tests, 0o755); err != nil {
		t.Fatal(err)
	}
	story := `# CAP-099 — Example

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 | P0 | 1 | — | Implemented |

### AC1 Do the thing
### AC2 Also this
`
	if err := os.WriteFile(filepath.Join(docs, "CAP-099-example.md"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Docs: []string{docs}, Tests: tests})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("expected failure")
	}
	if len(res.Failing) != 2 {
		t.Fatalf("want 2 failing, got %v", res.Failing)
	}
}

func TestCheckImplementedWithTestsOK(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	tests := filepath.Join(dir, "tests")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tests, 0o755); err != nil {
		t.Fatal(err)
	}
	story := `# CAP-099 — Example

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 | P0 | 1 | — | Implemented |

### AC1 Do the thing
`
	if err := os.WriteFile(filepath.Join(docs, "CAP-099-example.md"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	testSrc := `package tests

func TestExample_CAP099_AC1_DoTheThing(t *testing.T) {}
`
	if err := os.WriteFile(filepath.Join(tests, "example_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Docs: []string{docs}, Tests: tests})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %s", res.Message)
	}
}

func TestNotStartedIgnored(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	story := `# CAP-099 — Example

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 | P0 | 1 | — | Not started |

### AC1 Do the thing
`
	if err := os.WriteFile(filepath.Join(docs, "CAP-099-example.md"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(Options{Docs: []string{docs}, Tests: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Total != 0 {
		t.Fatalf("not started should be ignored: %+v", res)
	}
}
