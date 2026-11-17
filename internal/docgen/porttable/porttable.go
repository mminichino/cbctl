// Package porttable checks that every capellactl test has a cbctl counterpart: a Go test that
// covers it, or a reason it does not apply.
//
// Inputs:
//   - a source list (one "path::test<TAB>CAPxxx_ACn,…" line per Python test), generated from
//     the capellactl checkout and checked in;
//   - a port map (TSV rows "pyid<TAB>status<TAB>GoTest,…<TAB>reason"), written by hand;
//   - the Go test functions found under a source root.
//
// A Python test with no row is covered when every story tag it carries names at least one Go
// test (by the CAPxxx_ACn segment in its name); otherwise it is missing.
package porttable

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Status is how a Python test is accounted for.
type Status string

// The statuses a row may have.
const (
	Covered Status = "covered"
	Partial Status = "partial"
	Missing Status = "missing"
	NA      Status = "n/a"
)

func parseStatus(s string) (Status, error) {
	switch st := Status(strings.ToLower(strings.TrimSpace(s))); st {
	case Covered, Partial, Missing, NA:
		return st, nil
	}
	return "", fmt.Errorf("unknown status %q (want covered, partial, missing or n/a)", s)
}

// SourceTest is one Python test from the source list.
type SourceTest struct {
	ID      string   // tests/scenario/test_sync.py::test_sync_ac9_refreshes_with_diff
	Stories []string // CAP038_AC9, … (CAPxxx alone when the marker names no AC)
}

// Tier is the capellactl test directory under tests/ (scenario, unit, contract, …).
func (s SourceTest) Tier() string {
	parts := strings.Split(s.ID, "/")
	if len(parts) >= 3 && parts[0] == "tests" {
		return parts[1]
	}
	return "other"
}

// File is the part of the id before "::".
func (s SourceTest) File() string {
	f, _, _ := strings.Cut(s.ID, "::")
	return f
}

// TestRow is one hand-written port map row. PyID may end in "*" to match every test with that
// prefix (a whole file is "path::*").
type TestRow struct {
	PyID    string
	Status  Status
	GoTests []string
	Reason  string
	Line    int
}

// ParseSourceList reads the generated source list.
func ParseSourceList(r io.Reader) ([]SourceTest, error) {
	var out []SourceTest
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, tags, _ := strings.Cut(line, "\t")
		id = strings.TrimSpace(id)
		if !strings.Contains(id, "::") {
			return nil, fmt.Errorf("line %d: %q is not a path::test id", n, id)
		}
		if seen[id] {
			return nil, fmt.Errorf("line %d: duplicate id %s", n, id)
		}
		seen[id] = true
		st := SourceTest{ID: id}
		for _, t := range strings.Split(tags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				st.Stories = append(st.Stories, t)
			}
		}
		out = append(out, st)
	}
	return out, sc.Err()
}

// ParseMap reads the port map.
func ParseMap(r io.Reader) ([]TestRow, error) {
	var out []TestRow
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		cells := strings.Split(raw, "\t")
		for len(cells) < 4 {
			cells = append(cells, "")
		}
		st, err := parseStatus(cells[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		row := TestRow{PyID: strings.TrimSpace(cells[0]), Status: st, Reason: strings.TrimSpace(strings.Join(cells[3:], "\t")), Line: n}
		for _, g := range strings.Split(cells[2], ",") {
			if g = strings.TrimSpace(g); g != "" {
				row.GoTests = append(row.GoTests, g)
			}
		}
		if row.PyID == "" {
			return nil, fmt.Errorf("line %d: empty test id", n)
		}
		out = append(out, row)
	}
	return out, sc.Err()
}

var (
	goTestRE = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	capRE    = regexp.MustCompile(`CAP\d{3}(?:_AC\d+)*`)
)

// GoTests is the set of Go test functions under a root and the story tags in their names.
type GoTests struct {
	Names map[string]bool
	Tags  map[string][]string // CAPxxx_ACn and CAPxxx → test names
}

// CollectGoTests scans *_test.go files under root (skipping vendor and dot directories).
func CollectGoTests(root string) (GoTests, error) {
	g := GoTests{Names: map[string]bool{}, Tags: map[string][]string{}}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		for _, m := range goTestRE.FindAllStringSubmatch(string(raw), -1) {
			g.add(m[1])
		}
		return nil
	})
	return g, err
}

func (g GoTests) add(name string) {
	g.Names[name] = true
	for _, c := range capRE.FindAllString(name, -1) {
		parts := strings.Split(c, "_")
		g.Tags[parts[0]] = appendUnique(g.Tags[parts[0]], name)
		for _, ac := range parts[1:] {
			k := parts[0] + "_" + ac
			g.Tags[k] = appendUnique(g.Tags[k], name)
		}
	}
}

func appendUnique(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

// Resolved is how one Python test was accounted for.
type Resolved struct {
	Test    SourceTest
	Status  Status
	ByTag   bool // covered only through story tags, with no row
	GoTests []string
	Reason  string
}

// Counts tallies statuses.
type Counts struct {
	Covered, ByTag, Partial, Missing, NA int
}

// Total is the number of tests counted.
func (c Counts) Total() int { return c.Covered + c.Partial + c.Missing + c.NA }

// Done is covered plus n/a.
func (c Counts) Done() int { return c.Covered + c.NA }

// Percent is Done as a percentage of Total.
func (c Counts) Percent() float64 {
	if c.Total() == 0 {
		return 100
	}
	return 100 * float64(c.Done()) / float64(c.Total())
}

func (c *Counts) add(r Resolved) {
	switch r.Status {
	case Covered:
		c.Covered++
		if r.ByTag {
			c.ByTag++
		}
	case Partial:
		c.Partial++
	case Missing:
		c.Missing++
	case NA:
		c.NA++
	}
}

// Report is the result of Check.
type Report struct {
	Tests    []Resolved
	Tiers    map[string]Counts
	All      Counts
	Problems []string
}

// OK reports whether the check passed.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Check resolves every source test against rows and Go tests. minCovered is the lowest
// acceptable percentage of covered + n/a tests; below it is a problem.
func Check(src []SourceTest, rows []TestRow, g GoTests, minCovered float64) Report {
	rep := Report{Tiers: map[string]Counts{}}
	used := make([]bool, len(rows))
	for _, t := range src {
		res := Resolved{Test: t, Status: Missing}
		if i := bestRow(rows, t.ID); i >= 0 {
			used[i] = true
			row := rows[i]
			res.Status, res.GoTests, res.Reason = row.Status, row.GoTests, row.Reason
		} else if hits := tagHits(t.Stories, g); hits != nil {
			res.Status, res.ByTag, res.GoTests = Covered, true, hits
		}
		rep.Tests = append(rep.Tests, res)
		c := rep.Tiers[t.Tier()]
		c.add(res)
		rep.Tiers[t.Tier()] = c
		rep.All.add(res)
	}
	for i, row := range rows {
		where := fmt.Sprintf("port map line %d (%s)", row.Line, row.PyID)
		if !used[i] {
			if matchesAny(src, row.PyID) {
				rep.Problems = append(rep.Problems, where+": shadowed by a more specific row")
			} else {
				rep.Problems = append(rep.Problems, where+": matches no capellactl test")
			}
		}
		switch row.Status {
		case Covered:
			if len(row.GoTests) == 0 {
				rep.Problems = append(rep.Problems, where+": covered but names no Go test")
			}
		case NA:
			if row.Reason == "" {
				rep.Problems = append(rep.Problems, where+": n/a needs a reason")
			}
		}
		for _, gt := range row.GoTests {
			fn, _, _ := strings.Cut(gt, "/")
			if !g.Names[fn] {
				rep.Problems = append(rep.Problems, fmt.Sprintf("%s: Go test %s does not exist", where, fn))
			}
		}
	}
	if p := rep.All.Percent(); p+1e-9 < minCovered {
		rep.Problems = append(rep.Problems, fmt.Sprintf("%.1f%% of capellactl tests are covered or n/a (%d of %d); the floor is %.1f%%",
			p, rep.All.Done(), rep.All.Total(), minCovered))
	}
	return rep
}

// bestRow returns the index of the most specific row matching id: an exact row wins over a
// prefix row, and a longer prefix over a shorter one.
func bestRow(rows []TestRow, id string) int {
	best, bestLen := -1, -1
	for i, r := range rows {
		n := matchLen(r.PyID, id)
		if n > bestLen {
			best, bestLen = i, n
		}
	}
	return best
}

// matchLen is how specific pattern is for id (-1 when it does not match). Exact matches rank
// above every prefix.
func matchLen(pattern, id string) int {
	if pattern == id {
		return 1 << 30
	}
	if p, ok := strings.CutSuffix(pattern, "*"); ok && strings.HasPrefix(id, p) {
		return len(p)
	}
	return -1
}

func matchesAny(src []SourceTest, pattern string) bool {
	for _, t := range src {
		if matchLen(pattern, t.ID) >= 0 {
			return true
		}
	}
	return false
}

// tagHits returns the Go tests for every story tag, or nil when there are no tags or any tag
// has no Go test.
func tagHits(stories []string, g GoTests) []string {
	if len(stories) == 0 {
		return nil
	}
	var hits []string
	for _, s := range stories {
		h := g.Tags[s]
		if len(h) == 0 {
			return nil
		}
		for _, x := range h {
			hits = appendUnique(hits, x)
		}
	}
	sort.Strings(hits)
	return hits
}

var (
	storyRE  = regexp.MustCompile(`^@pytest\.mark\.story\(\s*"(CAP-\d{3})"((?:\s*,\s*"AC\d+")*)\s*\)`)
	acArgRE  = regexp.MustCompile(`"(AC\d+)"`)
	pyTestRE = regexp.MustCompile(`^(?:async\s+)?def (test_\w+)\(`)
)

// ScanPython lists the top-level test functions under root/tests with their story markers, in
// the source-list format, sorted by id. Only module-level functions count, as pytest collects
// them; story markers are the decorators directly above the function.
func ScanPython(root string) ([]SourceTest, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "tests"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "test_") && strings.HasSuffix(d.Name(), ".py") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []SourceTest
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		rel, err := filepath.Rel(root, f)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		var pending []string
		for _, line := range strings.Split(string(raw), "\n") {
			if m := storyRE.FindStringSubmatch(line); m != nil {
				cp := strings.ReplaceAll(m[1], "-", "")
				acs := acArgRE.FindAllStringSubmatch(m[2], -1)
				if len(acs) == 0 {
					pending = append(pending, cp)
				}
				for _, a := range acs {
					pending = append(pending, cp+"_"+a[1])
				}
				continue
			}
			if m := pyTestRE.FindStringSubmatch(line); m != nil {
				out = append(out, SourceTest{ID: rel + "::" + m[1], Stories: pending})
				pending = nil
				continue
			}
			// Another top-level statement ends the decorator run; continuation lines of a
			// multi-line decorator are indented or close a bracket.
			if line != "" && !strings.HasPrefix(line, "@") && !strings.ContainsAny(line[:1], " \t)]}#") {
				pending = nil
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// FormatSourceList renders tests in the format ParseSourceList reads.
func FormatSourceList(header string, tests []SourceTest) string {
	var b strings.Builder
	for _, h := range strings.Split(strings.TrimRight(header, "\n"), "\n") {
		b.WriteString("# " + h + "\n")
	}
	for _, t := range tests {
		b.WriteString(t.ID)
		if len(t.Stories) > 0 {
			b.WriteString("\t" + strings.Join(t.Stories, ","))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Summary renders the counts as a Markdown table, one row per tier and a total.
func Summary(rep Report) string {
	var b strings.Builder
	b.WriteString("| Tier | Tests | Covered (by AC tag) | Partial | Missing | N/A | Done |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	tiers := make([]string, 0, len(rep.Tiers))
	for t := range rep.Tiers {
		tiers = append(tiers, t)
	}
	sort.Strings(tiers)
	row := func(name string, c Counts) {
		fmt.Fprintf(&b, "| %s | %d | %d (%d) | %d | %d | %d | %.0f%% |\n",
			name, c.Total(), c.Covered, c.ByTag, c.Partial, c.Missing, c.NA, c.Percent())
	}
	for _, t := range tiers {
		row(t, rep.Tiers[t])
	}
	row("**all**", rep.All)
	return b.String()
}

// FileTable renders per-file counts (C/P/M/N/A) for every source file, in id order.
func FileTable(rep Report) string {
	type agg struct {
		c Counts
	}
	var order []string
	files := map[string]*agg{}
	for _, r := range rep.Tests {
		f := r.Test.File()
		a := files[f]
		if a == nil {
			a = &agg{}
			files[f] = a
			order = append(order, f)
		}
		a.c.add(r)
	}
	var b strings.Builder
	b.WriteString("| capellactl file | Tests | C | P | M | N/A | Status |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, f := range order {
		c := files[f].c
		status := "partial"
		switch {
		case c.NA == c.Total():
			status = "n/a"
		case c.Done() == c.Total():
			status = "**ported**"
		case c.Covered == 0 && c.Partial == 0:
			status = "missing"
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %s |\n", strings.TrimPrefix(f, "tests/"), c.Total(), c.Covered, c.Partial, c.Missing, c.NA, status)
	}
	return b.String()
}

// Footer markers delimit the generated summary in the port table document.
const (
	FooterBegin = "<!-- porttable:begin (generated by cbdev docs ac-coverage --write-footer; do not edit) -->"
	FooterEnd   = "<!-- porttable:end -->"
)

// ReplaceFooter puts summary between the footer markers in doc, appending them when absent.
func ReplaceFooter(doc, summary string) string {
	block := FooterBegin + "\n\n" + summary + "\n" + FooterEnd
	i := strings.Index(doc, FooterBegin)
	j := strings.Index(doc, FooterEnd)
	if i < 0 || j < i {
		return strings.TrimRight(doc, "\n") + "\n\n" + block + "\n"
	}
	return doc[:i] + block + doc[j+len(FooterEnd):]
}

// Gaps lists the tests that are partial or missing, grouped by file, for the report.
func Gaps(rep Report) []string {
	var out []string
	for _, r := range rep.Tests {
		if r.Status == Partial || r.Status == Missing {
			out = append(out, fmt.Sprintf("%s\t%s", r.Status, r.Test.ID))
		}
	}
	return out
}
