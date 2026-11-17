// Package stories parses CAP story headers and renders release indexes / CAP-ID-MAP.
package stories

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mminichino/cbctl/internal/docgen/data"
)

// Statuses are the status values accepted in story headers and stories.yaml.
var Statuses = []string{"Not started", "In progress", "Implemented", "Descoped"}

var priorities = []string{"P0", "P1", "P2"}

var titleRE = regexp.MustCompile(`^#\s+(CAP-\d{3})\s+—\s+(.+)$`)

// Story is one CAP backlog item.
type Story struct {
	ID       string
	Title    string
	File     string
	Epic     string
	Priority string
	Points   int
	Depends  string
	Status   string
	Phase    string // stories.yaml only
	Ref      string // stories.yaml only
	Note     string // stories.yaml only
	Release  string
}

// ReleaseConfig is an alias of data.ReleaseConfig for callers.
type ReleaseConfig = data.ReleaseConfig

// Phase is an alias of data.Phase.
type Phase = data.Phase

// ParseStory reads a CAP-*.md story header.
func ParseStory(path string) (Story, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Story{}, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 {
		return Story{}, fmt.Errorf("%s: empty file", filepath.Base(path))
	}
	m := titleRE.FindStringSubmatch(strings.TrimRight(lines[0], "\r"))
	if m == nil {
		return Story{}, fmt.Errorf("%s: first line must be '# CAP-NNN — Title'", filepath.Base(path))
	}
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "| Epic | Priority | Points |") {
			continue
		}
		if i+2 >= len(lines) {
			break
		}
		cells := splitRow(lines[i+2])
		if len(cells) < 5 {
			return Story{}, fmt.Errorf("%s: header table row incomplete", filepath.Base(path))
		}
		epic, priority, pointsS, depends, status := cells[0], cells[1], cells[2], cells[3], cells[4]
		if !contains(Statuses, status) {
			return Story{}, fmt.Errorf("%s: status %q not in %v", filepath.Base(path), status, Statuses)
		}
		if !contains(priorities, priority) {
			return Story{}, fmt.Errorf("%s: priority %q not in %v", filepath.Base(path), priority, priorities)
		}
		pts, err := strconv.Atoi(pointsS)
		if err != nil {
			return Story{}, fmt.Errorf("%s: points %q: %w", filepath.Base(path), pointsS, err)
		}
		return Story{
			ID: m[1], Title: m[2], File: filepath.Base(path), Epic: epic,
			Priority: priority, Points: pts, Depends: depends, Status: status,
		}, nil
	}
	return Story{}, fmt.Errorf("%s: header table not found", filepath.Base(path))
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

type yamlFile struct {
	Release      string `yaml:"release"`
	Architecture string `yaml:"architecture"`
	Stories      []struct {
		ID       string `yaml:"id"`
		Title    string `yaml:"title"`
		Epic     string `yaml:"epic"`
		Priority string `yaml:"priority"`
		Points   int    `yaml:"points"`
		Depends  string `yaml:"depends"`
		Status   string `yaml:"status"`
		Phase    string `yaml:"phase"`
		Ref      string `yaml:"ref"`
		Note     string `yaml:"note"`
	} `yaml:"stories"`
}

// LoadRelease loads stories from CAP-*.md files, or stories.yaml when none exist.
func LoadRelease(dir string) ([]Story, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "CAP-*.md"))
	if err != nil {
		return nil, err
	}
	release := strings.TrimPrefix(filepath.Base(dir), "release-")
	if len(matches) > 0 {
		var out []Story
		for _, p := range matches {
			s, err := ParseStory(p)
			if err != nil {
				return nil, err
			}
			s.Release = release
			out = append(out, s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, nil
	}
	yf := filepath.Join(dir, "stories.yaml")
	raw, err := os.ReadFile(yf)
	if err != nil {
		return nil, fmt.Errorf("%s: no CAP-*.md and stories.yaml: %w", dir, err)
	}
	var doc yamlFile
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("stories.yaml: %w", err)
	}
	out := make([]Story, 0, len(doc.Stories))
	for _, row := range doc.Stories {
		if !contains(Statuses, row.Status) {
			return nil, fmt.Errorf("stories.yaml: %s status %q invalid", row.ID, row.Status)
		}
		out = append(out, Story{
			ID: row.ID, Title: row.Title, File: "", Epic: row.Epic, Priority: row.Priority,
			Points: row.Points, Depends: row.Depends, Status: row.Status,
			Phase: row.Phase, Ref: row.Ref, Note: row.Note, Release: release,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LoadAll loads every known story from backlog release dirs (1.0.0 + 1.5.0).
func LoadAll(backlogRoot string) (map[string]Story, error) {
	out := map[string]Story{}
	for _, rel := range []string{"1.0.0", "1.5.0"} {
		stories, err := LoadRelease(filepath.Join(backlogRoot, "release-"+rel))
		if err != nil {
			return nil, err
		}
		for _, s := range stories {
			if _, ok := out[s.ID]; ok {
				return nil, fmt.Errorf("duplicate story id %s", s.ID)
			}
			out[s.ID] = s
		}
	}
	return out, nil
}

// ValidateRelease checks contiguous IDs and phase membership.
func ValidateRelease(release string, stories []Story, cfg ReleaseConfig) error {
	if len(stories) == 0 {
		return fmt.Errorf("release %s: no stories", release)
	}
	ids := make([]int, len(stories))
	for i, s := range stories {
		n, err := strconv.Atoi(strings.TrimPrefix(s.ID, "CAP-"))
		if err != nil {
			return fmt.Errorf("bad id %s", s.ID)
		}
		ids[i] = n
	}
	for i := range ids {
		if ids[i] != ids[0]+i {
			return fmt.Errorf("story numbering has gaps or duplicates: %v", ids)
		}
	}
	phased := map[string]bool{}
	for _, p := range cfg.Phases {
		for _, id := range p.IDs {
			if phased[id] {
				return fmt.Errorf("story %s appears in more than one phase", id)
			}
			phased[id] = true
		}
	}
	got := map[string]bool{}
	for _, s := range stories {
		got[s.ID] = true
	}
	if len(got) != len(phased) {
		var miss []string
		for id := range got {
			if !phased[id] {
				miss = append(miss, id)
			}
		}
		for id := range phased {
			if !got[id] {
				miss = append(miss, id)
			}
		}
		sort.Strings(miss)
		return fmt.Errorf("every story must be in exactly one phase; mismatch: %v", miss)
	}
	for id := range got {
		if !phased[id] {
			return fmt.Errorf("story %s not in any phase", id)
		}
	}
	return nil
}

// Config returns the ReleaseConfig for a release version from embedded phases.yaml.
func Config(release string) (ReleaseConfig, error) {
	b, err := data.Load()
	if err != nil {
		return ReleaseConfig{}, err
	}
	cfg, ok := b.Releases[release]
	if !ok {
		return ReleaseConfig{}, fmt.Errorf("no phase configuration for release %s", release)
	}
	return cfg, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func priorityMeaning(release, p string) string {
	minor := release
	if parts := strings.Split(release, "."); len(parts) >= 2 {
		minor = parts[0] + "." + parts[1]
	}
	switch p {
	case "P0":
		return "Must have — release blocker"
	case "P1":
		return fmt.Sprintf("Should have — ships in %s unless explicitly descoped", minor)
	case "P2":
		return "Nice to have"
	default:
		return p
	}
}
