package cli_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// TestPlan_CAP005_AC2_ReadOnly asserts plan issues only GET traffic (AssertNoWrites).
func TestPlan_CAP005_AC2_ReadOnly(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)

	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "Project envproj", "+ project envproj", "+ cluster envclu", "Plan: ")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestPlan_CAP005_AC8_DetailedExitCode asserts --detailed-exitcode returns ExitChanges when adds are pending.
func TestPlan_CAP005_AC8_DetailedExitCode(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)

	r := s.Run("capella", "plan", "--detailed-exitcode")
	if r.Code != int(cli.ExitChanges) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitChanges, r.Stdout, r.Stderr)
	}
	clitest.AssertNoWrites(t, s.Fake)

	js := mustScenario(t, s.Run("capella", "-o", "json", "plan"), "plan -o json").JSON(t).(map[string]any)
	plan, _ := js["plan"].(map[string]any)
	actions, _ := plan["actions"].([]any)
	if len(actions) == 0 {
		t.Fatalf("plan json has no actions: %v", js)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestPlan_CAP005_CleanAfterApply asserts plan --detailed-exitcode is 0 after a successful apply.
func TestPlan_CAP005_CleanAfterApply(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes")

	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "plan", "--detailed-exitcode"), "plan --detailed-exitcode")
	wantContains(t, r.Stdout, "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked.")
	clitest.AssertNoWrites(t, s.Fake)
}
