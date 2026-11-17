// Package covergate checks Go statement coverage against per-tier minimums (P-TST-18).
//
// It reads one or more coverage profiles written with -coverpkg=./internal/..., merges them
// (a block counts as covered when any profile ran it), totals statements per package and
// compares each package with the minimum of the tier that claims it.
package covergate

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// TiersFile is the checked-in tier table, relative to the repository root.
const TiersFile = "internal/ci/covergate/tiers.json"

//go:embed tiers.json
var tiersJSON []byte

// Tier is a named group of packages that share a minimum statement coverage percent.
type Tier struct {
	Name string   `json:"name"`
	Min  float64  `json:"min"`
	Pkgs []string `json:"packages"`
}

// Config is the tier table plus the module path its package patterns are relative to.
type Config struct {
	Root  string `json:"root"`
	Tiers []Tier `json:"tiers"`
}

// Counts is the statement total and the number of those statements a profile ran.
type Counts struct {
	Statements int
	Covered    int
}

// Percent is the covered share of statements; a package with no statements is 100%.
func (c Counts) Percent() float64 {
	if c.Statements == 0 {
		return 100
	}
	return 100 * float64(c.Covered) / float64(c.Statements)
}

// Status is the outcome for one package row.
type Status string

// Row statuses.
const (
	StatusOK      Status = "OK"
	StatusBelow   Status = "BELOW"
	StatusMissing Status = "MISSING"
)

// Row is one package's result within a tier.
type Row struct {
	Tier    string
	Min     float64
	Package string // pattern from the tier table
	Counts
	Status Status
}

// Report is the outcome of a gate run.
type Report struct {
	Rows     []Row
	Untiered []string // profiled packages that no tier claims
}

// Failed reports whether any row is below its minimum or missing.
func (r Report) Failed() bool {
	for _, row := range r.Rows {
		if row.Status != StatusOK {
			return true
		}
	}
	return false
}

// DefaultConfig returns the embedded tier table.
func DefaultConfig() (Config, error) {
	return ParseConfig(tiersJSON)
}

// LoadConfig reads a tier table from path.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading tiers: %w", err)
	}
	return ParseConfig(b)
}

// ParseConfig decodes and validates a tier table.
func ParseConfig(b []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parsing tiers: %w", err)
	}
	if c.Root == "" {
		return Config{}, errors.New("tiers: root is required")
	}
	if len(c.Tiers) == 0 {
		return Config{}, errors.New("tiers: no tiers defined")
	}
	seenTier := map[string]bool{}
	seenPkg := map[string]string{}
	for _, t := range c.Tiers {
		if t.Name == "" {
			return Config{}, errors.New("tiers: a tier has no name")
		}
		if seenTier[t.Name] {
			return Config{}, fmt.Errorf("tiers: duplicate tier %q", t.Name)
		}
		seenTier[t.Name] = true
		if t.Min <= 0 || t.Min > 100 {
			return Config{}, fmt.Errorf("tiers: %s: min %.1f must be in (0, 100]", t.Name, t.Min)
		}
		if len(t.Pkgs) == 0 {
			return Config{}, fmt.Errorf("tiers: %s: no packages", t.Name)
		}
		for _, p := range t.Pkgs {
			if p == "" {
				return Config{}, fmt.Errorf("tiers: %s: empty package pattern", t.Name)
			}
			if other, ok := seenPkg[p]; ok {
				return Config{}, fmt.Errorf("tiers: package %q is in both %s and %s", p, other, t.Name)
			}
			seenPkg[p] = t.Name
		}
	}
	return c, nil
}

// Profile is merged coverage: package import path → block key → (statements, ran).
type Profile map[string]map[string]block

type block struct {
	stmts int
	ran   bool
}

// ParseProfile reads one Go coverage profile and merges it into p.
func (p Profile) ParseProfile(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if line == 1 {
			if !strings.HasPrefix(text, "mode:") {
				return fmt.Errorf("profile line 1: missing mode header")
			}
			continue
		}
		// file.go:L.C,L.C stmts count
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return fmt.Errorf("profile line %d: want 3 fields, got %d", line, len(fields))
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			return fmt.Errorf("profile line %d: no file:range", line)
		}
		file := fields[0][:colon]
		slash := strings.LastIndex(file, "/")
		if slash < 0 {
			return fmt.Errorf("profile line %d: file %q has no package path", line, file)
		}
		pkg := file[:slash]
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return fmt.Errorf("profile line %d: statements: %w", line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("profile line %d: count: %w", line, err)
		}
		blocks := p[pkg]
		if blocks == nil {
			blocks = map[string]block{}
			p[pkg] = blocks
		}
		b, seen := blocks[fields[0]]
		if seen && b.stmts != stmts {
			// Profiles from different source revisions cannot be merged meaningfully.
			return fmt.Errorf("profile line %d: block %s has %d statements here but %d in an earlier profile (stale profile?)",
				line, fields[0], stmts, b.stmts)
		}
		b.stmts = stmts
		b.ran = b.ran || count > 0
		blocks[fields[0]] = b
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("reading profile: %w", err)
	}
	if line == 0 {
		return errors.New("profile is empty")
	}
	return nil
}

// LoadProfiles reads and merges the profiles at paths.
func LoadProfiles(paths []string) (Profile, error) {
	p := Profile{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening profile: %w", err)
		}
		err = p.ParseProfile(f)
		cerr := f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if cerr != nil {
			return nil, fmt.Errorf("closing %s: %w", path, cerr)
		}
	}
	return p, nil
}

// Totals returns statement counts per package.
func (p Profile) Totals() map[string]Counts {
	out := make(map[string]Counts, len(p))
	for pkg, blocks := range p {
		var c Counts
		for _, b := range blocks {
			c.Statements += b.stmts
			if b.ran {
				c.Covered += b.stmts
			}
		}
		out[pkg] = c
	}
	return out
}

// match reports whether pkg (a full import path) is selected by pattern relative to root.
// "plan" selects root/plan only; "ci/..." selects root/ci and every package below it.
func match(root, pattern, pkg string) bool {
	if rest, ok := strings.CutSuffix(pattern, "/..."); ok {
		base := root + "/" + rest
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pkg == root+"/"+pattern
}

// Evaluate checks every tier package against the merged profile. A pattern that selects
// no profiled package is MISSING, so a renamed or deleted package cannot silently drop out.
// A "/..." pattern is gated on the combined statements of everything it selects.
func Evaluate(c Config, totals map[string]Counts) Report {
	var rep Report
	claimed := map[string]bool{}
	for _, t := range c.Tiers {
		for _, pattern := range t.Pkgs {
			row := Row{Tier: t.Name, Min: t.Min, Package: pattern}
			found := false
			for pkg, cnt := range totals {
				if !match(c.Root, pattern, pkg) {
					continue
				}
				found = true
				claimed[pkg] = true
				row.Statements += cnt.Statements
				row.Covered += cnt.Covered
			}
			switch {
			case !found:
				row.Status = StatusMissing
			case row.Percent() < t.Min:
				row.Status = StatusBelow
			default:
				row.Status = StatusOK
			}
			rep.Rows = append(rep.Rows, row)
		}
	}
	for pkg, cnt := range totals {
		if !claimed[pkg] && cnt.Statements > 0 {
			rep.Untiered = append(rep.Untiered, strings.TrimPrefix(pkg, c.Root+"/"))
		}
	}
	sort.Strings(rep.Untiered)
	return rep
}

// Write prints the report as a per-tier table.
func Write(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintf(w, "%-14s %-26s %8s %6s %s\n", "TIER", "PACKAGE", "COVER", "MIN", "STATUS"); err != nil {
		return err
	}
	for _, r := range rep.Rows {
		pct := "n/a"
		if r.Status != StatusMissing {
			pct = fmt.Sprintf("%.1f%%", r.Percent())
		}
		if _, err := fmt.Fprintf(w, "%-14s %-26s %8s %5.0f%% %s\n", r.Tier, r.Package, pct, r.Min, r.Status); err != nil {
			return err
		}
	}
	if len(rep.Untiered) > 0 {
		if _, err := fmt.Fprintf(w, "untiered (not gated): %s\n", strings.Join(rep.Untiered, ", ")); err != nil {
			return err
		}
	}
	return nil
}
