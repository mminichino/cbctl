package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsPortTable(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "# list\ntests/scenario/test_a.py::test_one\tCAP999_AC99\ntests/unit/test_b.py::test_two\n")
	good := writeFile(t, dir, "map.tsv", "tests/unit/test_b.py::*\tn/a\t\tPython-only\n")
	bad := writeFile(t, dir, "bad.tsv", "tests/unit/test_b.py::test_two\tcovered\tTestDoesNotExistAnywhere\t\n")
	doc := writeFile(t, dir, "port.md", "# Port\n")

	code, out, errOut := runCBDev(t, "docs", "ac-coverage", "--port-table", good, "--source-list", src, "--min-covered", "50")
	if code != 0 || !strings.Contains(out, "Port table OK: 1 of 2") {
		t.Fatalf("good map: code %d\n%s\n%s", code, out, errOut)
	}
	if code, _, errOut := runCBDev(t, "docs", "ac-coverage", "--port-table", good, "--source-list", src, "--min-covered", "100"); code != 1 || !strings.Contains(errOut, "the floor is 100.0%") {
		t.Errorf("floor: code %d\n%s", code, errOut)
	}
	if code, _, errOut := runCBDev(t, "docs", "ac-coverage", "--port-table", bad, "--source-list", src); code != 1 || !strings.Contains(errOut, "TestDoesNotExistAnywhere does not exist") {
		t.Errorf("bad map: code %d\n%s", code, errOut)
	}

	// Footer: stale until written, then current.
	args := []string{"docs", "ac-coverage", "--port-table", good, "--source-list", src, "--footer", doc}
	if code, _, errOut := runCBDev(t, args...); code != 1 || !strings.Contains(errOut, "out of date") {
		t.Errorf("stale footer: code %d\n%s", code, errOut)
	}
	if code, out, errOut := runCBDev(t, append(args, "--write-footer")...); code != 0 || !strings.Contains(out, "updated the counts") {
		t.Fatalf("write footer: code %d\n%s\n%s", code, out, errOut)
	}
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `unit/test_b.py` | 1 | 0 | 0 | 0 | 1 | n/a |") {
		t.Errorf("footer:\n%s", raw)
	}
	if code, _, errOut := runCBDev(t, args...); code != 0 {
		t.Errorf("current footer: code %d\n%s", code, errOut)
	}
	if code, out, _ := runCBDev(t, append(args, "--gaps")...); code != 0 || !strings.Contains(out, "missing\ttests/scenario/test_a.py::test_one") {
		t.Errorf("gaps: %d\n%s", code, out)
	}

	usage := [][]string{
		{"--port-table", good},
		{"--footer", doc},
		{"--port-table", good, "--source-list", src, "--write-footer"},
		{"--port-table", good, "--source-list", src, "--min-covered", "120"},
	}
	for _, u := range usage {
		if code, _, _ := runCBDev(t, append([]string{"docs", "ac-coverage"}, u...)...); code != 2 {
			t.Errorf("%v: code %d, want 2", u, code)
		}
	}
	if code, _, _ := runCBDev(t, "docs", "ac-coverage", "--port-table", filepath.Join(dir, "nope"), "--source-list", src); code != 1 {
		t.Errorf("missing map: code %d", code)
	}
}

func TestDocsPortList(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tests", "unit"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tests", "unit"), "test_x.py",
		"import pytest\n\n@pytest.mark.story(\"CAP-001\", \"AC1\", \"AC2\")\n@pytest.mark.parametrize(\"a\", [\n    1,\n])\ndef test_x(a):\n    def test_inner():\n        pass\n\n\n@pytest.mark.story(\"CAP-002\")\nasync def test_y():\n    pass\n\nX = 1\ndef test_z():\n    pass\n")
	code, out, errOut := runCBDev(t, "docs", "port-list", "--source", dir, "--out", "-")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	for _, want := range []string{
		"tests/unit/test_x.py::test_x\tCAP001_AC1,CAP001_AC2\n",
		"tests/unit/test_x.py::test_y\tCAP002\n",
		"tests/unit/test_x.py::test_z\n",
		"capellactl @ unknown (3 tests)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "test_inner") {
		t.Error("nested function listed")
	}
	if code, _, _ := runCBDev(t, "docs", "port-list", "--source", t.TempDir(), "--out", "-"); code != 1 {
		t.Errorf("empty checkout: code %d", code)
	}
	outFile := filepath.Join(dir, "list.txt")
	if code, _, errOut := runCBDev(t, "docs", "port-list", "--source", dir, "--out", outFile); code != 0 {
		t.Fatalf("write: %d %s", code, errOut)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Error(err)
	}
}

// TestCheckedInPortTable keeps the checked-in map, list and counts consistent (the floor is
// enforced by 'make port-table').
func TestCheckedInPortTable(t *testing.T) {
	code, out, errOut := runCBDev(t, "docs", "ac-coverage",
		"--port-table", "internal/cli/testdata/port-map.tsv",
		"--source-list", "internal/cli/testdata/capellactl-tests.txt",
		"--footer", "internal/cli/testdata/scenario-port.md")
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
}
