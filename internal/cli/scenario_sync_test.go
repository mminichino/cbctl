package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/config/capellafile"
)

// TestSync_CAP038_AC2_RoundTrip ports sync --stdout / dry-run / write / re-sync onto Scenario.
func TestSync_CAP038_AC2_RoundTrip(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("proj")
	cid := s.Fake.SeedCluster(pid, "clu", "healthy")
	s.Fake.SeedBucket(cid, "orders")

	r := mustScenario(t, s.Run("capella", "sync", "--stdout"), "sync --stdout")
	wantContains(t, r.Stdout, "apiVersion: cbctl/v1", "name: proj", "name: clu", "name: orders")
	if _, err := os.Stat(filepath.Join(s.Dir, "capella.yaml")); err == nil {
		t.Fatal("--stdout wrote a file")
	}
	if _, err := capellafile.LoadText(r.Stdout, "stdout", s.Dir, func(string) string { return "" }); err != nil {
		t.Fatalf("sync output does not validate: %v\n%s", err, r.Stdout)
	}

	s.Fake.ResetCalls()
	r = s.Run("capella", "sync", "--dry-run")
	if r.Code != int(cli.ExitChanges) {
		t.Fatalf("dry-run exit = %d, want %d\n%s\n%s", r.Code, cli.ExitChanges, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "does not exist yet")
	clitest.AssertNoWrites(t, s.Fake)

	r = mustScenario(t, s.Run("capella", "sync"), "sync")
	wantContains(t, r.Stdout, "Imported 1 project, 1 cluster", "capella.yaml")
	clitest.AssertNoWrites(t, s.Fake)

	wantContains(t, mustScenario(t, s.Run("capella", "validate"), "validate").Stdout, "capella.yaml is valid")
	r = mustScenario(t, s.Run("capella", "plan", "--detailed-exitcode"), "plan")
	wantContains(t, r.Stdout, "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked.")
	wantContains(t, mustScenario(t, s.Run("capella", "drift", "--detailed-exitcode"), "drift").Stdout, "No drift")

	r = mustScenario(t, s.Run("capella", "sync"), "sync again")
	wantContains(t, r.Stdout, "already in sync with Capella")
	mustScenario(t, s.Run("capella", "sync", "--dry-run"), "sync --dry-run in sync")

	s.Fake.SeedBucket(cid, "second")
	r = s.Run("capella", "sync", "--dry-run")
	if r.Code != int(cli.ExitChanges) {
		t.Fatalf("out-of-sync dry-run exit = %d, want %d\n%s\n%s", r.Code, cli.ExitChanges, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stdout, "+++ capella.yaml (from Capella)", "second")
	r = mustScenario(t, s.Run("capella", "sync"), "sync refresh")
	wantContains(t, r.Stdout, "capella.yaml updated from Capella")
	if _, err := os.Stat(filepath.Join(s.Dir, "capella.yaml.bak")); err != nil {
		t.Errorf("no .bak after refresh: %v", err)
	}
	wantContains(t, mustScenario(t, s.Run("capella", "plan"), "plan after refresh").Stdout, "Plan: 0 to add")
}

// TestSync_CAP036_AC3_OrgMismatch ports org mismatch refusal onto Scenario.
func TestSync_CAP036_AC3_OrgMismatch(t *testing.T) {
	s := newScenario(t)
	doc := "organizationId: some-other-org\n" + scenarioDoc
	writeCapellaYAML(t, s, doc)

	r := s.Run("capella", "sync")
	if r.Code != int(cli.ExitConfigInvalid) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitConfigInvalid, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "is for organization some-other-org")
	wantContains(t, r.Stderr+r.Stdout, "--force", "--file")
}
