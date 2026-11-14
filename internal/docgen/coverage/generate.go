// Package coverage generates Management API coverage reports for releases 1.0.0, 1.5.0 and 2.0.0.
package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/data"
	"github.com/mminichino/cbctl/internal/docgen/stories"
)

const (
	Done  = "✅ Done"
	Part  = "🟡 Partial"
	Todo  = "⬜ Not built"
	None  = "➖ Not needed"
	Later = "⏭️ Not in 1.0"

	Rel10   = "1.0.0"
	Rel15   = "1.5.0"
	NoStory = "none"
	refBase = "https://docs.couchbase.com/cloud/management-api-reference/index.html#tag/"
)

var order = []string{Done, Part, Todo, None, Later}

// Snapshot is the offline Management API operations list.
type Snapshot struct {
	Source     string   `json:"source"`
	APIVersion string   `json:"apiVersion"`
	Retrieved  string   `json:"retrieved"`
	Operations []SpecOp `json:"operations"`
}

// SpecOp is one operation from the snapshot.
type SpecOp struct {
	Tag     string `json:"tag"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

// Known is the story map used for status derivation.
type Known = map[string]stories.Story

// LoadSnapshot reads management-api-v4.0-operations.json.
func LoadSnapshot(path string) (Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

type collectedOp struct {
	Tag        string
	Method     string
	Path       string
	Summary    string
	Names      []string
	Stories    []string
	Fixed      string
	Commands   string
	Note       string
	Left       string
	Requires   []string
	PartialUse bool
	Status     string
}

func isAppServices(tag, path string) bool {
	return strings.Contains(tag, "App Service") || tag == "App Endpoints" || strings.Contains(path, "/appservices")
}

func isAI(tag string) bool {
	return strings.HasPrefix(tag, "AI ") || strings.HasPrefix(tag, "MCP ") || strings.Contains(tag, "Model")
}

func ver(release string) []int {
	parts := strings.Split(release, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

func verLE(a, b string) bool {
	va, vb := ver(a), ver(b)
	for i := 0; i < len(va) || i < len(vb); i++ {
		x, y := 0, 0
		if i < len(va) {
			x = va[i]
		}
		if i < len(vb) {
			y = vb[i]
		}
		if x != y {
			return x < y
		}
	}
	return true // equal
}

func inRelease(sts []string, known Known, release string) []string {
	var out []string
	for _, s := range sts {
		if s == NoStory {
			out = append(out, s)
			continue
		}
		k, ok := known[s]
		if !ok {
			continue
		}
		if verLE(k.Release, release) {
			out = append(out, s)
		}
	}
	return out
}

func releaseOf(sts []string, known Known) string {
	var rels []string
	for _, s := range sts {
		if s == NoStory {
			continue
		}
		if k, ok := known[s]; ok {
			rels = append(rels, k.Release)
		}
	}
	if len(rels) == 0 {
		return ""
	}
	best := rels[0]
	for _, r := range rels[1:] {
		if !verLE(r, best) {
			best = r
		}
	}
	return best
}

func storyLink(s string, known Known, here string) string {
	k := known[s]
	if k.Release == here && k.File != "" {
		return k.File
	}
	if k.File == "" {
		return "../CAP-ID-MAP.md#" + strings.ToLower(s)
	}
	return fmt.Sprintf("../release-%s/%s", k.Release, k.File)
}

func statusOf(sts []string, known Known, release string) string {
	sts = inRelease(sts, known, release)
	if len(sts) == 0 {
		return Later
	}
	allDone, anyDone := true, false
	for _, s := range sts {
		done := s != NoStory && (known[s].Status == "Implemented" || known[s].Status == "Descoped")
		if done {
			anyDone = true
		} else {
			allDone = false
		}
	}
	if allDone {
		return Done
	}
	if anyDone {
		return Part
	}
	return Todo
}

func openStories(sts []string, known Known, release string) []string {
	var out []string
	for _, s := range inRelease(sts, known, release) {
		if s == NoStory || (known[s].Status != "Implemented" && known[s].Status != "Descoped") {
			out = append(out, s)
		}
	}
	return out
}

func short(path string) string {
	org := "/v4/organizations/{organizationId}"
	r := strings.NewReplacer(
		org, "{org}",
		"/projects/{projectId}", "/projects/{p}",
		"/clusters/{clusterId}", "/clusters/{c}",
		"/freeTier/{clusterId}", "/freeTier/{c}",
		"/buckets/{bucketId}", "/buckets/{b}",
		"/appservices/{appServiceId}", "/appservices/{a}",
		"/appEndpoints/{appEndpointName}", "/appEndpoints/{e}",
		"/appEndpoints/{appEndpointKeyspace}", "/appEndpoints/{keyspace}",
	)
	return r.Replace(path)
}

func pct(a, b int) string {
	if b == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", int(float64(100*a)/float64(b)+0.5))
}

func anchor(tag string) string {
	s := strings.ReplaceAll(tag, " & ", " and ")
	s = strings.ReplaceAll(s, "&", "and")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "/", "-")
	re := regexp.MustCompile(`\s+`)
	return refBase + re.ReplaceAllString(s, "-")
}

func collectOps(snap Snapshot, known Known, bundle data.Bundle) ([]collectedOp, map[string]int, error) {
	for name, c := range bundle.Commands {
		for _, s := range c.Stories {
			if s != NoStory {
				if _, ok := known[s]; !ok {
					return nil, nil, fmt.Errorf("command dictionary names unknown story %s (%s)", s, name)
				}
			}
		}
	}
	for _, w := range bundle.Workflow {
		for _, s := range w.Stories {
			if _, ok := known[s]; !ok {
				return nil, nil, fmt.Errorf("workflow command names unknown story %s", s)
			}
		}
	}
	excl := map[string]int{}
	var ops []collectedOp
	seen := map[string]bool{}
	for _, o := range snap.Operations {
		tag, m, p := o.Tag, o.Method, o.Path
		if isAppServices(tag, p) {
			excl["as"]++
			continue
		}
		if isAI(tag) {
			excl["ai"]++
			continue
		}
		key := m + " " + p
		spec, ok := bundle.Ops[key]
		if !ok {
			return nil, nil, fmt.Errorf("unmapped operation %s %s (%s): add it to ops.json", m, p, tag)
		}
		seen[key] = true
		var names []string
		if !strings.HasPrefix(spec.Commands, "internal") && spec.Commands != "—" {
			names = strings.Split(spec.Commands, " / ")
		}
		for _, n := range names {
			if _, ok := bundle.Commands[n]; !ok {
				return nil, nil, fmt.Errorf("%s %s: command %q is not in the command dictionary", m, p, n)
			}
		}
		for _, s := range append(append([]string{}, spec.Stories...), spec.Requires...) {
			if s != NoStory {
				if _, ok := known[s]; !ok {
					return nil, nil, fmt.Errorf("%s %s: unknown story %s", m, p, s)
				}
			}
		}
		base := append([]string{}, spec.Stories...)
		if len(base) == 0 {
			uniq := map[string]bool{}
			for _, n := range names {
				for _, s := range bundle.Commands[n].Stories {
					if !uniq[s] {
						uniq[s] = true
						base = append(base, s)
					}
				}
			}
		}
		fixed := ""
		if spec.Status != nil {
			fixed = *spec.Status
		}
		ops = append(ops, collectedOp{
			Tag: tag, Method: m, Path: p, Summary: o.Summary, Names: names,
			Stories: append(base, spec.Requires...), Fixed: fixed,
			Commands: spec.Commands, Note: spec.Note, Left: spec.Left,
			Requires: spec.Requires, PartialUse: spec.PartialUse,
		})
	}
	for key := range bundle.Ops {
		if !seen[key] {
			return nil, nil, fmt.Errorf("OPS maps operations that are not in the spec snapshot: %s", key)
		}
	}
	tags := map[string]bool{}
	for _, o := range ops {
		tags[o.Tag] = true
	}
	areaTags := map[string]bool{}
	for _, a := range bundle.Areas {
		areaTags[a.Tag] = true
	}
	if len(tags) != len(areaTags) {
		var diff []string
		for t := range tags {
			if !areaTags[t] {
				diff = append(diff, t)
			}
		}
		for t := range areaTags {
			if !tags[t] {
				diff = append(diff, t)
			}
		}
		sort.Strings(diff)
		return nil, nil, fmt.Errorf("spec tags changed: %v", diff)
	}
	return ops, excl, nil
}

func opStatus(o collectedOp, known Known, release string) string {
	if o.Fixed != "" {
		return o.Fixed
	}
	st := statusOf(o.Stories, known, release)
	if st == Todo && o.PartialUse {
		return Part
	}
	return st
}

func breakdown(snap Snapshot, known Known, bundle data.Bundle) ([]string, []collectedOp, map[string]int, error) {
	ops, excl, err := collectOps(snap, known, bundle)
	if err != nil {
		return nil, nil, nil, err
	}
	rows := map[string][2]int{} // group -> [ops, done]
	for _, o := range ops {
		var group string
		if o.Fixed == None {
			group = "none"
		} else {
			group = releaseOf(o.Stories, known)
			if group == "" && o.Fixed != "" {
				group = Rel10
			}
		}
		r := rows[group]
		r[0]++
		st := opStatus(o, known, group)
		if group == "" {
			st = opStatus(o, known, Rel15)
		}
		if st == Done {
			r[1]++
		}
		rows[group] = r
	}
	r10 := "../release-1.0.0/API-COVERAGE.md"
	r15 := "../release-1.5.0/API-COVERAGE.md"
	r20 := "../release-2.0.0/API-COVERAGE-APP-SERVICES.md"
	type row struct {
		g, where string
		n        int
		d        *int
	}
	d10, d15 := rows[Rel10][1], rows[Rel15][1]
	table := []row{
		{"Operational database, release 1.0.0", "[1.0.0 report](" + r10 + ")", rows[Rel10][0], &d10},
		{"Operational database, not needed (another operation gives the same data)", "[1.0.0 report](" + r10 + ")", rows["none"][0], nil},
		{"Operational database, release 1.5.0", "[1.5.0 report](" + r15 + ")", rows[Rel15][0], &d15},
	}
	if rows[""][0] > 0 {
		d := rows[""][1]
		table = append(table, row{"Operational database, no release yet", "—", rows[""][0], &d})
	}
	asDone := 0
	table = append(table,
		row{"App Services / App Endpoints, release 2.0.0", "[2.0.0 report](" + r20 + ")", excl["as"], &asDone},
		row{"AI (AI Data Plane, AI Workflows, Models, AI Agent Memory, MCP servers and tools)", "Not planned", excl["ai"], nil},
	)
	total := 0
	doneSum := 0
	for _, t := range table {
		total += t.n
		if t.d != nil {
			doneSum += *t.d
		}
	}
	if total != len(snap.Operations) {
		return nil, nil, nil, fmt.Errorf("operation breakdown adds up to %d, the spec has %d", total, len(snap.Operations))
	}
	out := []string{
		"**Where the spec's operations go**\n",
		"| Group | Operations | ✅ Done | Tracked in |",
		"|---|---|---|---|",
	}
	for _, t := range table {
		ds := "—"
		if t.d != nil {
			ds = strconv.Itoa(*t.d)
		}
		out = append(out, fmt.Sprintf("| %s | %d | %s | %s |", t.g, t.n, ds, t.where))
	}
	out = append(out, fmt.Sprintf("| **Total in the spec** | **%d** | **%d** | |", total, doneSum), "")
	return out, ops, excl, nil
}

// Generate writes the three API-COVERAGE reports under backlogRoot.
func Generate(backlogRoot string, check bool) error {
	bundle, err := data.Load()
	if err != nil {
		return err
	}
	snap, err := LoadSnapshot(filepath.Join(backlogRoot, "api", "management-api-v4.0-operations.json"))
	if err != nil {
		return err
	}
	known, err := stories.LoadAll(backlogRoot)
	if err != nil {
		return err
	}
	table, ops, excl, err := breakdown(snap, known, bundle)
	if err != nil {
		return err
	}
	// Copy ops for each report (status is mutated per release).
	ops10 := append([]collectedOp(nil), ops...)
	ops15 := append([]collectedOp(nil), ops...)
	for i := range ops15 {
		ops15[i].Status = opStatus(ops15[i], known, Rel15)
		if ops15[i].Status == Later {
			return fmt.Errorf("operations planned for no release (give their commands a story): %s %s",
				ops15[i].Method, ops15[i].Path)
		}
	}
	db := Build1_0(snap, known, bundle, table, ops10, excl)
	r15 := Build1_5(snap, known, bundle, table, ops15, excl)
	as := BuildAppServices(snap, bundle, table)
	if strings.HasPrefix(as, "# ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(strings.TrimSpace(as), "# ERROR: "))
	}
	reports := map[string]string{
		filepath.Join(backlogRoot, "release-1.0.0", "API-COVERAGE.md"):              db,
		filepath.Join(backlogRoot, "release-1.5.0", "API-COVERAGE.md"):              r15,
		filepath.Join(backlogRoot, "release-2.0.0", "API-COVERAGE-APP-SERVICES.md"): as,
	}
	var stale []string
	for path, text := range reports {
		if check {
			cur, err := os.ReadFile(path)
			if err != nil || string(cur) != text {
				stale = append(stale, path)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
	}
	if check && len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("%s out of date: run 'cbdev docs coverage' (or 'make docs') and commit the result",
			strings.Join(stale, ", "))
	}
	return nil
}
