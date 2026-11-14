package stories

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseStory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "CAP-066-bucket-flush.md")
	body := `# CAP-066 — Flush a bucket

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Data access & tools | P1 | 2 | CAP-023, CAP-024 | Not started |

## User story
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ParseStory(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "CAP-066" || s.Points != 2 || s.Priority != "P1" || s.Status != "Not started" {
		t.Fatalf("unexpected story: %+v", s)
	}
}

func TestConfig1_5(t *testing.T) {
	cfg, err := Config("1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Phases) != 7 {
		t.Fatalf("want 7 phases, got %d", len(cfg.Phases))
	}
}
