package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// TestValidate_CAP002_AC6_OfflineValidate ports the offline validate summary against Scenario.
func TestValidate_CAP002_AC6_OfflineValidate(t *testing.T) {
	s := newScenario(t)
	// Offline: clear API credentials after Scenario presets them.
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID"} {
		s.Env[k] = ""
		t.Setenv(k, "")
	}
	writeCapellaYAML(t, s, scenarioDoc)

	r := mustScenario(t, s.Run("capella", "validate"), "validate")
	wantContains(t, r.Stdout, "capella.yaml is valid: 1 project, 1 cluster")
	clitest.AssertNoWrites(t, s.Fake)

	js := mustScenario(t, s.Run("capella", "-o", "json", "validate"), "validate -o json").JSON(t).(map[string]any)
	if js["valid"] != true {
		t.Errorf("valid = %v", js["valid"])
	}
}

// TestApply_CAP006_AC2_RequiresYes ports non-interactive apply without approval (exit blocked).
func TestApply_CAP006_AC2_RequiresYes(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)

	r := s.Run("capella", "apply")
	if r.Code != int(cli.ExitBlocked) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitBlocked, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "no terminal")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestApply_CAP006_AC1_ZeroWritesRerun applies once, then asserts a second apply is a no-op with zero writes.
func TestApply_CAP006_AC1_ZeroWritesRerun(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)

	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes")
	wantContains(t, r.Stdout, "KIND", "envclu", "done")
	wantContains(t, r.Stderr, "Apply complete")
	if n := strings.Count(s.ReadFile("capella.yaml"), "id: "); n < 2 {
		t.Errorf("expected pinned project+cluster ids, got %d id lines:\n%s", n, s.ReadFile("capella.yaml"))
	}
	if len(s.Fake.Writes()) == 0 {
		t.Fatal("first apply produced no writes")
	}

	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes (rerun)")
	wantContains(t, r.Stdout, "No changes. Capella matches capella.yaml.")
	clitest.AssertNoWrites(t, s.Fake)

	js := mustScenario(t, s.Run("capella", "-o", "json", "apply"), "apply -o json").JSON(t).(map[string]any)
	if result, _ := js["result"].(map[string]any); result["status"] != "ok" {
		t.Errorf("json result = %v", js["result"])
	}
	clitest.AssertNoWrites(t, s.Fake)
}
