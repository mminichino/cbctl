package links

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"## Hello World":                       "hello-world",
		"### CAP-066 — Flush":                  "cap-066--flush",
		"# Snapshot":                           "snapshot",
		"## Delivery phases":                   "delivery-phases",
		"# Part 1 — Day-2 tasks":               "part-1--day-2-tasks",
		"# Part 3 — 20-minute demo (Azure)":    "part-3--20-minute-demo-azure",
		"### Using the commands in PowerShell": "using-the-commands-in-powershell",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckFileOK(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	if err := os.WriteFile(b, []byte("# Target\n\nHi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("# A\n\nSee [b](b.md#target).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if issues := CheckFile(a); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestCheckFileBroken(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	if err := os.WriteFile(a, []byte("# A\n\nSee [missing](nope.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := CheckFile(a)
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %v", issues)
	}
}
