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

	"github.com/mminichino/cbctl/internal/ci/covergate"
)

var coverRE = regexp.MustCompile(`coverage: ([0-9.]+)% of statements`)

// profileList collects repeated --profile flags.
type profileList []string

func (p *profileList) String() string { return strings.Join(*p, ",") }

func (p *profileList) Set(v string) error {
	if v == "" {
		return fmt.Errorf("empty profile path")
	}
	*p = append(*p, v)
	return nil
}

// cmdCICoverGate has two modes:
//
//	cover-gate --tiers [FILE] --profile P [--profile P2 ...]   tiered gate over merged profiles
//	cover-gate --min N PKG...                                  one floor, go test -cover per PKG
func cmdCICoverGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci cover-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	min := fs.Float64("min", 0, "minimum coverage percent required (per-package mode)")
	tiers := fs.String("tiers", "", "tier table JSON (\"default\" = "+covergate.TiersFile+")")
	var profiles profileList
	fs.Var(&profiles, "profile", "coverage profile written with -coverpkg=./internal/... (repeatable; tiered mode)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tiers != "" {
		if *min != 0 || fs.NArg() > 0 {
			fmt.Fprintln(stderr, "cbdev ci cover-gate: --tiers takes --profile only, not --min or PKG")
			return 2
		}
		return coverTiers(*tiers, profiles, stdout, stderr)
	}
	if len(profiles) > 0 {
		fmt.Fprintln(stderr, "cbdev ci cover-gate: --profile needs --tiers")
		return 2
	}
	pkgs := fs.Args()
	if *min <= 0 {
		fmt.Fprintln(stderr, "cbdev ci cover-gate: --min N is required (N > 0), or use --tiers")
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

func coverTiers(tiersPath string, profiles []string, stdout, stderr io.Writer) int {
	if len(profiles) == 0 {
		fmt.Fprintln(stderr, "cbdev ci cover-gate: --tiers needs at least one --profile")
		return 2
	}
	var (
		cfg covergate.Config
		err error
	)
	if tiersPath == "default" {
		cfg, err = covergate.DefaultConfig()
	} else {
		cfg, err = covergate.LoadConfig(tiersPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cbdev ci cover-gate: %v\n", err)
		return 2
	}
	prof, err := covergate.LoadProfiles(profiles)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev ci cover-gate: %v\n", err)
		return 2
	}
	rep := covergate.Evaluate(cfg, prof.Totals())
	if err := covergate.Write(stdout, rep); err != nil {
		fmt.Fprintf(stderr, "cbdev ci cover-gate: writing report: %v\n", err)
		return 1
	}
	if rep.Failed() {
		fmt.Fprintln(stderr, "cover-gate: one or more tier packages are below their minimum or missing")
		return 1
	}
	return 0
}

func coverPackage(pkg string) (float64, error) {
	dir, err := os.MkdirTemp("", "cbdev-cover-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }() // temp dir cleanup; failure only leaks a temp file
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
