package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/porttable"
)

// portCheck is the --port-table part of 'cbdev docs ac-coverage'.
type portCheck struct {
	mapFile    string
	sourceList string
	minCovered float64
	footer     string // document whose generated footer is checked (or written)
	write      bool
	gaps       bool
}

// run checks the port map and returns the exit code (0 ok, 1 problems).
func (p portCheck) run(root string, stdout, stderr io.Writer) int {
	abs := func(f string) string {
		if f == "" || filepath.IsAbs(f) {
			return f
		}
		return filepath.Join(root, f)
	}
	srcFile, err := os.Open(abs(p.sourceList))
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
		return 1
	}
	defer func() { _ = srcFile.Close() }() // read-only
	src, err := porttable.ParseSourceList(srcFile)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %s: %v\n", p.sourceList, err)
		return 1
	}
	mapFile, err := os.Open(abs(p.mapFile))
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
		return 1
	}
	defer func() { _ = mapFile.Close() }() // read-only
	rows, err := porttable.ParseMap(mapFile)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %s: %v\n", p.mapFile, err)
		return 1
	}
	g, err := porttable.CollectGoTests(root)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
		return 1
	}
	rep := porttable.Check(src, rows, g, p.minCovered)
	summary := porttable.Summary(rep)
	footer := summary + "\n" + porttable.FileTable(rep)
	code := 0
	if p.footer != "" {
		path := abs(p.footer)
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
			return 1
		}
		want := porttable.ReplaceFooter(string(raw), footer)
		switch {
		case p.write && want != string(raw):
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "updated the counts in %s\n", p.footer)
		case !p.write && want != string(raw):
			fmt.Fprintf(stderr, "FAIL: the counts in %s are out of date; run 'make port-footer'\n", p.footer)
			code = 1
		}
	}
	if p.gaps {
		for _, gap := range porttable.Gaps(rep) {
			fmt.Fprintln(stdout, gap)
		}
	}
	fmt.Fprint(stdout, summary)
	if !rep.OK() {
		fmt.Fprintf(stderr, "FAIL: %d port table problem(s):\n", len(rep.Problems))
		for _, pr := range rep.Problems {
			fmt.Fprintln(stderr, "  "+pr)
		}
		return 1
	}
	fmt.Fprintf(stdout, "Port table OK: %d of %d capellactl tests covered or n/a (%.1f%%, floor %.1f%%)\n",
		rep.All.Done(), rep.All.Total(), rep.All.Percent(), p.minCovered)
	return code
}

func parsePercent(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || v < 0 || v > 100 {
		return 0, fmt.Errorf("--min-covered %q: want a number from 0 to 100", s)
	}
	return v, nil
}

// cmdDocsPortList regenerates the capellactl test list from a checkout.
func cmdDocsPortList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("port-list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "../capellactl", "capellactl checkout")
	out := fs.String("out", "internal/cli/testdata/capellactl-tests.txt", "file to write ('-' for stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs port-list: %v\n", err)
		return 1
	}
	src := *source
	if !filepath.IsAbs(src) {
		src = filepath.Join(root, src)
	}
	tests, err := porttable.ScanPython(src)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs port-list: %v\n", err)
		return 1
	}
	if len(tests) == 0 {
		fmt.Fprintf(stderr, "cbdev docs port-list: no tests found under %s/tests\n", src)
		return 1
	}
	head := gitHead(src)
	text := porttable.FormatSourceList(fmt.Sprintf(
		"Every module-level test in capellactl @ %s (%d tests).\nRegenerate: go run ./cmd/cbdev docs port-list --source PATH\nFormat: path::test<TAB>story tags", head, len(tests)), tests)
	if *out == "-" {
		fmt.Fprint(stdout, text)
		return 0
	}
	dst := *out
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(root, dst)
	}
	if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
		fmt.Fprintf(stderr, "cbdev docs port-list: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %d tests to %s\n", len(tests), *out)
	return 0
}

// gitHead returns the short HEAD of the checkout at dir, or "unknown".
func gitHead(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return "unknown"
	}
	ref := strings.TrimSpace(string(raw))
	if r, ok := strings.CutPrefix(ref, "ref: "); ok {
		b, err := os.ReadFile(filepath.Join(dir, ".git", filepath.FromSlash(r)))
		if err != nil {
			return headFromPacked(dir, r)
		}
		ref = strings.TrimSpace(string(b))
	}
	if len(ref) >= 7 {
		return ref[:7]
	}
	return "unknown"
}

func headFromPacked(dir, ref string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ".git", "packed-refs"))
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if sha, name, ok := strings.Cut(line, " "); ok && name == ref && len(sha) >= 7 {
			return sha[:7]
		}
	}
	return "unknown"
}
