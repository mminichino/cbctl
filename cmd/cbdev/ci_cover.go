package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var coverRE = regexp.MustCompile(`coverage: ([0-9.]+)% of statements`)

func cmdCICoverGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci cover-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	min := fs.Float64("min", 0, "minimum coverage percent required")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pkgs := fs.Args()
	if *min <= 0 {
		fmt.Fprintln(stderr, "cbdev ci cover-gate: --min N is required (N > 0)")
		return 2
	}
	if len(pkgs) == 0 {
		fmt.Fprintln(stderr, "cbdev ci cover-gate: missing PKG")
		return 2
	}

	type row struct {
		pkg string
		pct float64
		ok  bool
		err string
	}
	var rows []row
	failed := false
	for _, pkg := range pkgs {
		pct, err := coverPackage(pkg)
		r := row{pkg: pkg, pct: pct}
		if err != nil {
			r.err = err.Error()
			failed = true
		} else if pct < *min {
			r.ok = false
			failed = true
		} else {
			r.ok = true
		}
		rows = append(rows, r)
	}

	fmt.Fprintf(stdout, "%-48s %8s %s\n", "PACKAGE", "COVER", "STATUS")
	for _, r := range rows {
		status := "OK"
		if r.err != "" {
			status = "ERROR: " + r.err
		} else if !r.ok {
			status = fmt.Sprintf("BELOW %.0f%%", *min)
		}
		pct := "n/a"
		if r.err == "" {
			pct = fmt.Sprintf("%.1f%%", r.pct)
		}
		fmt.Fprintf(stdout, "%-48s %8s %s\n", r.pkg, pct, status)
	}
	if failed {
		fmt.Fprintf(stderr, "cover-gate: one or more packages below %.0f%%\n", *min)
		return 1
	}
	return 0
}

func coverPackage(pkg string) (float64, error) {
	dir, err := os.MkdirTemp("", "cbdev-cover-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	profile := filepath.Join(dir, "cover.out")
	cmd := exec.Command("go", "test", "-cover", "-coverprofile="+profile, pkg)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%w\n%s", err, strings.TrimSpace(out.String()))
	}
	m := coverRE.FindStringSubmatch(out.String())
	if m == nil {
		// Packages with no statements still pass go test; treat as 100%.
		if strings.Contains(out.String(), "[no test files]") || strings.Contains(out.String(), "coverage: [no statements]") {
			return 100, nil
		}
		return 0, fmt.Errorf("no coverage line in output:\n%s", out.String())
	}
	return strconv.ParseFloat(m[1], 64)
}
