package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDocCoverage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		sources  []string
		mappings []DocMapping
		want     []string // substrings, one per expected problem
	}{
		{"all mapped", []string{"a.md", "rel/x.md", "old.md"},
			[]DocMapping{{Source: "a.md", Target: "docs/a.md"}, {Source: "rel/", Target: "docs"}, {Source: "old.md", Dropped: "never shipped"}}, nil},
		{"unmapped source", []string{"a.md", "b.md"}, []DocMapping{{Source: "a.md", Target: "docs/a.md"}},
			[]string{"b.md: no mapping"}},
		{"missing target", []string{"a.md"}, []DocMapping{{Source: "a.md", Target: "docs/nope.md"}},
			[]string{"target docs/nope.md does not exist"}},
		{"empty dropped", []string{"a.md"}, []DocMapping{{Source: "a.md", Dropped: "  "}},
			[]string{"needs a target or a dropped reason"}},
		{"both", []string{"a.md"}, []DocMapping{{Source: "a.md", Target: "docs/a.md", Dropped: "x"}},
			[]string{"not both"}},
		{"twice", []string{"rel/x.md"}, []DocMapping{{Source: "rel/", Target: "docs"}, {Source: "rel/x.md", Target: "docs/a.md"}},
			[]string{"mapped more than once"}},
		{"stale mapping", []string{"a.md"}, []DocMapping{{Source: "a.md", Target: "docs/a.md"}, {Source: "gone.md", Dropped: "x"}},
			[]string{"gone.md: maps no capellactl document"}},
		{"no source", nil, []DocMapping{{Target: "docs/a.md"}}, []string{"has no source"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckDocCoverage(tc.sources, tc.mappings, root)
			if len(got) != len(tc.want) {
				t.Fatalf("problems = %q, want %d matching %q", got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("problem %d = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

// TestRepoDocMap is the gate: the checked-in doc-map.yaml must cover every capellactl document.
func TestRepoDocMap(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sources, mappings, err := LoadDocMap(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) < 100 {
		t.Fatalf("doc-map.yaml lists %d sources; capellactl had 103 documents", len(sources))
	}
	for _, p := range CheckDocCoverage(sources, mappings, root) {
		t.Error(p)
	}
	// With a capellactl checkout next to this repo, the list must also be current.
	src := filepath.Join(root, "..", "capellactl", "docs")
	if _, err := os.Stat(src); err == nil {
		live, err := ListSourceDocs(src)
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]bool{}
		for _, s := range sources {
			listed[s] = true
		}
		for _, d := range live {
			if !listed[d] {
				t.Errorf("capellactl document %s is not in doc-map.yaml", d)
			}
		}
	}
}
