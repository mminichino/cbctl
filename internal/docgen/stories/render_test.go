package stories

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleStories() []Story {
	return []Story{
		{ID: "CAP-101", Title: "Alpha", File: "CAP-101-alpha.md", Epic: "E1 Core", Priority: "P0", Points: 3, Depends: "—", Status: "Implemented"},
		{ID: "CAP-102", Title: "Beta", File: "CAP-102-beta.md", Epic: "E1 Core", Priority: "P1", Points: 5, Depends: "CAP-101", Status: "In progress"},
		{ID: "CAP-103", Title: "Gamma", Epic: "E2 Extra", Priority: "P2", Points: 2, Depends: "CAP-102", Status: "Not started"},
		{ID: "CAP-104", Title: "Delta", File: "CAP-104-delta.md", Epic: "E2 Extra", Priority: "P1", Points: 8, Depends: "—", Status: "Descoped"},
	}
}

func sampleConfig() ReleaseConfig {
	return ReleaseConfig{
		Scope: "Scope paragraph.",
		Phases: []Phase{
			{Name: "Phase 1", Outcome: "First outcome", IDs: []string{"CAP-101"}},
			{Name: "Phase 2", Outcome: "Second outcome", IDs: []string{"CAP-102", "CAP-104"}},
			{Name: "Phase 3", Outcome: "Third outcome", IDs: []string{"CAP-103"}},
		},
		Checklist: []string{"Ship it", "Tag it"},
	}
}

func TestRenderIndex(t *testing.T) {
	tests := []struct {
		name    string
		release string
		cfg     ReleaseConfig
		want    []string
		notWant []string
	}{
		{
			name:    "story files",
			release: "1.5.0",
			cfg:     sampleConfig(),
			want: []string{
				"# Release 1.5.0 — Story Index",
				"Scope paragraph.",
				"[00-architecture.md](00-architecture.md).",
				"| **Total** | **3** | **10** |",
				"| ✅ Implemented | 1 | 3 |",
				"| 🚧 In progress | 1 | 5 |",
				"| ⬜ Not started | 1 | 2 |",
				"| Not yet implemented (in progress + not started) | 2 | 7 |",
				"| **Completion** | 33% | 30% |",
				"| 🚫 Descoped (not counted above) | 1 | 8 |",
				"| P0 | Must have — release blocker | 1 | 3 | 1 / 3 |",
				"| P1 | Should have — ships in 1.5 unless explicitly descoped | 1 | 5 | 0 / 0 |",
				"| P2 | Nice to have | 1 | 2 | 0 / 0 |",
				"| E1 Core | 2 | 8 | 1 / 3 |",
				"| E2 Extra | 1 | 2 | 0 / 0 |",
				"### P0 — Must have — release blocker (1 stories, 3 points)",
				"| 1 | ~~[CAP-101](CAP-101-alpha.md)~~ | ~~Alpha~~ | ~~E1 Core~~ | ~~3~~ | ✅ Implemented |",
				"| 1 | [CAP-102](CAP-102-beta.md) | Beta | E1 Core | 5 | 🚧 In progress |",
				"| 1 | [CAP-103](../CAP-ID-MAP.md#cap-103) | Gamma | E2 Extra | 2 | ⬜ Not started |",
				"## Delivery phases",
				"| Phase 1 | ~~CAP-101~~ | 3 | 1/1 (3 pts) | ✅ Done | First outcome |",
				"| Phase 2 | CAP-102 | 5 | 0/1 (0 pts) | 🚧 Current | Second outcome |",
				"| Phase 3 | CAP-103 | 2 | 0/1 (0 pts) | ⬜ Planned | Third outcome |",
				"### Phase 1 — ✅ Done (1/1 stories, 3/3 points)",
				"### Phase 2 — 🚧 Current (0/1 stories, 0/5 points)",
				"### Phase 3 — ⬜ Planned (0/1 stories, 0/2 points)",
				"_Second outcome._",
				"| 2 | ~~[CAP-104](CAP-104-delta.md)~~ | ~~Delta~~ | ~~P1~~ | ~~E2 Extra~~ | ~~8~~ | ~~—~~ | 🚫 Descoped |",
				"| 1 | [CAP-101](CAP-101-alpha.md) | Alpha | P0 | E1 Core | 3 | — | ✅ Implemented |",
				"| 1 | [CAP-103](../CAP-ID-MAP.md#cap-103) | Gamma | P2 | E2 Extra | 2 | CAP-102 | ⬜ Not started |",
				"## Descoped",
				"| [CAP-104](CAP-104-delta.md) | Delta | E2 Extra | 8 |",
				"## Release checklist",
				"- [ ] Ship it",
				"- [ ] Tag it",
			},
		},
		{
			name:    "release 1.0.0 links to map, custom architecture, no descoped",
			release: "1.0.0",
			cfg: ReleaseConfig{
				Architecture: "../arch.md",
				Phases:       []Phase{{Name: "Only", Outcome: "o", IDs: []string{"CAP-101"}}},
			},
			want: []string{
				"# Release 1.0.0 — Story Index",
				"[00-architecture.md](../arch.md).",
				"[CAP-101](../CAP-ID-MAP.md#cap-101)",
				"| Only | ~~CAP-101~~ | 3 | 1/1 (3 pts) | ✅ Done | o |",
			},
			notWant: []string{"## Descoped", "Descoped (not counted above)", "CAP-101-alpha.md", "- [ ]"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			every := sampleStories()
			if tc.release == "1.0.0" {
				every = every[:1]
			}
			got := RenderIndex(tc.release, every, tc.cfg)
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in:\n%s", s, got)
				}
			}
			for _, s := range tc.notWant {
				if strings.Contains(got, s) {
					t.Errorf("unexpected %q in output", s)
				}
			}
		})
	}
}

func TestRenderIndexEmpty(t *testing.T) {
	got := RenderIndex("2.0.0", nil, ReleaseConfig{})
	for _, s := range []string{"| **Total** | **0** | **0** |", "| **Completion** | 0% | 0% |"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(got, "### P0") {
		t.Error("empty priority groups should be skipped")
	}
}

func TestRenderCAPIDMap(t *testing.T) {
	tests := []struct {
		name string
		s    Story
		want string
	}{
		{"phase and ref and note", Story{ID: "CAP-001", Title: "T", Epic: "E", Priority: "P0", Points: 1, Status: "Implemented", Phase: "P1", Ref: "PR 5", Note: "n"},
			`| <a id="cap-001"></a>[CAP-001](#cap-001) | T | E | P0 | 1 | Implemented | P1 · PR 5 — n |`},
		{"phase only", Story{ID: "CAP-002", Phase: "P2"}, "| 0 |  | P2 |"},
		{"ref only", Story{ID: "CAP-003", Ref: "r"}, "|  | r |"},
		{"note only", Story{ID: "CAP-004", Note: "only note"}, "|  | only note |"},
		{"nothing", Story{ID: "CAP-005"}, `[CAP-005](#cap-005) |  |  |  | 0 |  |  |`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderCAPIDMap([]Story{tc.s})
			if !strings.HasPrefix(got, "# CAP ID map — release 1.0.0") {
				t.Errorf("missing header: %s", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("missing %q in:\n%s", tc.want, got)
			}
		})
	}
}

func TestPriorityMeaning(t *testing.T) {
	tests := []struct{ release, p, want string }{
		{"1.5.0", "P0", "Must have — release blocker"},
		{"1.5.0", "P1", "Should have — ships in 1.5 unless explicitly descoped"},
		{"2", "P1", "Should have — ships in 2 unless explicitly descoped"},
		{"1.5.0", "P2", "Nice to have"},
		{"1.5.0", "P9", "P9"},
	}
	for _, tc := range tests {
		t.Run(tc.release+"/"+tc.p, func(t *testing.T) {
			if got := priorityMeaning(tc.release, tc.p); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestValidateRelease(t *testing.T) {
	st := func(ids ...string) []Story {
		out := make([]Story, len(ids))
		for i, id := range ids {
			out[i] = Story{ID: id}
		}
		return out
	}
	ph := func(groups ...[]string) ReleaseConfig {
		var c ReleaseConfig
		for _, g := range groups {
			c.Phases = append(c.Phases, Phase{IDs: g})
		}
		return c
	}
	tests := []struct {
		name    string
		stories []Story
		cfg     ReleaseConfig
		wantErr string
	}{
		{"ok", st("CAP-001", "CAP-002"), ph([]string{"CAP-001"}, []string{"CAP-002"}), ""},
		{"empty", nil, ReleaseConfig{}, "release 9.9.9: no stories"},
		{"bad id", st("CAP-x"), ReleaseConfig{}, "bad id CAP-x"},
		{"gap", st("CAP-001", "CAP-003"), ReleaseConfig{}, "gaps or duplicates: [1 3]"},
		{"duplicate", st("CAP-001", "CAP-001"), ReleaseConfig{}, "gaps or duplicates"},
		{"two phases", st("CAP-001"), ph([]string{"CAP-001"}, []string{"CAP-001"}), "story CAP-001 appears in more than one phase"},
		{"missing from phase", st("CAP-001", "CAP-002"), ph([]string{"CAP-001"}), "mismatch: [CAP-002]"},
		{"extra in phase", st("CAP-001"), ph([]string{"CAP-001", "CAP-009"}), "mismatch: [CAP-009]"},
		{"swapped", st("CAP-001", "CAP-002"), ph([]string{"CAP-001", "CAP-009"}), "not in any phase"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRelease("9.9.9", tc.stories, tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseStoryErrors(t *testing.T) {
	header := "# CAP-010 — Title\n\n| Epic | Priority | Points | Depends on | Status |\n|---|---|---|---|---|\n"
	tests := []struct {
		name, body, wantErr string
	}{
		{"bad title", "# nope\n", "first line must be"},
		{"no table", "# CAP-010 — Title\n\nbody\n", "header table not found"},
		{"table truncated", "# CAP-010 — Title\n| Epic | Priority | Points |\n", "header table not found"},
		{"incomplete row", header + "| E | P0 | 1 |\n", "header table row incomplete"},
		{"bad status", header + "| E | P0 | 1 | — | Done |\n", `status "Done" not in`},
		{"bad priority", header + "| E | P5 | 1 | — | Implemented |\n", `priority "P5" not in`},
		{"bad points", header + "| E | P0 | x | — | Implemented |\n", `points "x"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "CAP-010.md")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ParseStory(p)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want %q", err, tc.wantErr)
			}
		})
	}
	if _, err := ParseStory(filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func storyMD(id, status string) string {
	return "# " + id + " — T " + id + "\n\n| Epic | Priority | Points | Depends on | Status |\n|---|---|---|---|---|\n| E | P0 | 2 | — | " + status + " |\n"
}

func TestLoadReleaseAndAll(t *testing.T) {
	root := t.TempDir()
	r10 := filepath.Join(root, "release-1.0.0")
	r15 := filepath.Join(root, "release-1.5.0")
	writeFile(t, filepath.Join(r10, "stories.yaml"), `release: 1.0.0
stories:
  - {id: CAP-002, title: B, epic: E, priority: P1, points: 1, status: Implemented, phase: P1, ref: r, note: n}
  - {id: CAP-001, title: A, epic: E, priority: P0, points: 2, status: Not started}
`)
	writeFile(t, filepath.Join(r15, "CAP-011-b.md"), storyMD("CAP-011", "In progress"))
	writeFile(t, filepath.Join(r15, "CAP-010-a.md"), storyMD("CAP-010", "Implemented"))

	got, err := LoadRelease(r10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "CAP-001" || got[1].Ref != "r" || got[1].Release != "1.0.0" {
		t.Fatalf("yaml release: %+v", got)
	}
	got, err = LoadRelease(r15)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "CAP-010" || got[0].File != "CAP-010-a.md" || got[1].Release != "1.5.0" {
		t.Fatalf("md release: %+v", got)
	}
	all, err := LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("LoadAll: %d stories", len(all))
	}

	t.Run("duplicate across releases", func(t *testing.T) {
		dup := filepath.Join(r15, "CAP-001-dup.md")
		writeFile(t, dup, storyMD("CAP-001", "Implemented"))
		t.Cleanup(func() {
			if err := os.Remove(dup); err != nil {
				t.Errorf("removing %s: %v", dup, err)
			}
		})
		if _, err := LoadAll(root); err == nil || !strings.Contains(err.Error(), "duplicate story id CAP-001") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLoadReleaseErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{"nothing", nil, "no CAP-*.md and stories.yaml"},
		{"bad yaml", map[string]string{"stories.yaml": "stories: [::"}, "stories.yaml:"},
		{"bad status", map[string]string{"stories.yaml": "stories:\n  - {id: CAP-001, status: Bogus}\n"}, `CAP-001 status "Bogus" invalid`},
		{"bad md", map[string]string{"CAP-001.md": "nope\n"}, "first line must be"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for n, b := range tc.files {
				writeFile(t, filepath.Join(dir, n), b)
			}
			_, err := LoadRelease(dir)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want %q", err, tc.wantErr)
			}
		})
	}
	if _, err := LoadAll(t.TempDir()); err == nil {
		t.Fatal("LoadAll on empty root should fail")
	}
}

func TestConfigUnknown(t *testing.T) {
	if _, err := Config("0.0.1"); err == nil || !strings.Contains(err.Error(), "no phase configuration for release 0.0.1") {
		t.Fatalf("got %v", err)
	}
}
