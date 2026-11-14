package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

// guidePlaceholders substitutes NAME-style tokens in ```bash run blocks.
var guidePlaceholders = map[string]string{
	"PROJECT": "envproj",
	"CLUSTER": "envclu",
	"APP_PW":  canaryPassword,
	"CBCTL":   "cbctl",
}

var bashRunFence = regexp.MustCompile("(?ms)^```bash run\\s*\\n(.*?)\\n```")

// TestGuides_CAP033_AC5_GettingStartedRun extracts ```bash run blocks from
// docs/capella/getting-started.md and runs each cbctl invocation against Scenario.
func TestGuides_CAP033_AC5_GettingStartedRun(t *testing.T) {
	root := findRepoRoot(t)
	docPath := filepath.Join(root, "docs", "capella", "getting-started.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := bashRunFence.FindAllStringSubmatch(string(raw), -1)
	if len(blocks) == 0 {
		t.Fatalf("%s: no ```bash run blocks found", docPath)
	}

	s := newScenario(t)
	s.Env["APP_PW"] = canaryPassword
	t.Setenv("APP_PW", canaryPassword)
	// Guide assumes a working directory with capella.yaml written by an earlier step;
	// seed the minimal doc used by the run blocks when they reference apply/plan.
	writeCapellaYAML(t, s, scenarioDoc)

	for i, block := range blocks {
		for _, line := range splitGuideCommands(block[1]) {
			args, ok := parseCbctlArgs(line)
			if !ok {
				continue
			}
			r := s.Run(args...)
			if r.Code != 0 {
				t.Fatalf("block %d %v: exit %d\nstdout: %s\nstderr: %s", i+1, args, r.Code, r.Stdout, r.Stderr)
			}
			clitest.AssertNoSecret(t, r, canaryPassword, s.Fake.APIKey)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}

func splitGuideCommands(block string) []string {
	var lines []string
	var buf strings.Builder
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "$ ")
		if strings.HasSuffix(line, "\\") {
			buf.WriteString(strings.TrimSuffix(line, "\\"))
			buf.WriteByte(' ')
			continue
		}
		buf.WriteString(line)
		lines = append(lines, substituteGuidePlaceholders(buf.String()))
		buf.Reset()
	}
	if buf.Len() > 0 {
		lines = append(lines, substituteGuidePlaceholders(buf.String()))
	}
	return lines
}

func substituteGuidePlaceholders(s string) string {
	for k, v := range guidePlaceholders {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

func parseCbctlArgs(line string) ([]string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false
	}
	// Only exercise cbctl lines; skip mkdir/export/etc.
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, false
	}
	switch fields[0] {
	case "cbctl":
		return fields[1:], true
	default:
		return nil, false
	}
}
