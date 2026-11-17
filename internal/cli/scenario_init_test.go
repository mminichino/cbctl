package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/config/capellafile"
)

// TestInit_CAP035_AC7_NonInteractive ports the non-interactive init happy path onto Scenario.
func TestInit_CAP035_AC7_NonInteractive(t *testing.T) {
	s := newScenario(t)
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID"} {
		s.Env[k] = ""
		t.Setenv(k, "")
	}

	r := s.Run("capella", "init", "--non-interactive")
	if r.Code != int(cli.ExitConfigInvalid) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitConfigInvalid, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "--org-id", "--project", "--cluster")

	args := []string{"capella", "init", "--non-interactive",
		"--org-id", "org-1", "--project", "nova", "--cluster", "nova-01",
		"--cidr", "203.0.113.10/32", "--bucket", "orders",
		"--scope", "inv:items,stock", "--credential", "app",
		"--hibernation", "office-hours"}

	r = mustScenario(t, s.Run(append(append([]string{}, args...), "--dry-run")...), "init --dry-run")
	wantContains(t, r.Stdout, "organizationId: org-1", "name: nova-01", "${CBCTL_PWD_NOVA_01_APP}", "weekdays: 08:00-18:00")
	if strings.Contains(r.Stdout, "Placeholder") {
		t.Error("validation placeholder leaked into dry-run output")
	}
	if entries, _ := filepath.Glob(filepath.Join(s.Dir, "capella.y*ml")); len(entries) != 0 {
		t.Fatalf("--dry-run wrote %v", entries)
	}

	r = mustScenario(t, s.Run(args...), "init")
	wantContains(t, r.Stdout, "Wrote ", "capella.yaml (validated)", "export CBCTL_PWD_NOVA_01_APP")
	text := s.ReadFile("capella.yaml")
	if _, err := capellafile.LoadText(text, "capella.yaml", ".", func(k string) string {
		if k == "CBCTL_PWD_NOVA_01_APP" {
			return "Aa1!aaaaaaaa"
		}
		return ""
	}); err != nil {
		t.Fatalf("written file does not validate: %v", err)
	}
	if strings.Contains(text, "Aa1!") || strings.Contains(text, "Placeholder") {
		t.Errorf("a password value was written:\n%s", text)
	}
	clitest.AssertNoSecret(t, r, "Aa1!aaaaaaaa")

	r = s.Run(args...)
	if r.Code != int(cli.ExitError) {
		t.Fatalf("second init exit = %d, want %d\n%s\n%s", r.Code, cli.ExitError, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "already exists")
}
