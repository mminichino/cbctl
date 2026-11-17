package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// appliedScenario applies scenarioDoc and returns the scenario with the cluster's ID.
func appliedScenario(t *testing.T) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioDoc)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes")
	ctx := context.Background()
	c := s.Fake.Client()
	ps, err := c.ListProjects(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("projects = %v, %v", ps, err)
	}
	cls, err := c.ListClusters(ctx, ps[0].ID)
	if err != nil || len(cls) != 1 {
		t.Fatalf("clusters = %v, %v", cls, err)
	}
	s.Fake.ResetCalls()
	return s, cls[0].ID
}

// TestApply_CAP006_AC2_DeclineNoWrites: answering no at the terminal changes nothing.
func TestApply_CAP006_AC2_DeclineNoWrites(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)
	before := s.ReadFile("capella.yaml")
	s.TTY(true)
	s.Answers("n")

	r := mustScenario(t, s.Run("capella", "apply"), "apply (declined)")
	wantContains(t, r.Stderr, "Apply cancelled.")
	clitest.AssertNoWrites(t, s.Fake)
	if after := s.ReadFile("capella.yaml"); after != before {
		t.Errorf("capella.yaml changed on decline:\n%s", after)
	}
}

// TestCI_CAP042_AC5_TerminalStillConfirms: with a terminal, apply asks and a yes applies.
func TestCI_CAP042_AC5_TerminalStillConfirms(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioDoc)
	s.TTY(true)
	s.Answers("y")
	r := mustScenario(t, s.Run("capella", "apply"), "apply (confirmed)")
	if strings.Contains(r.Stdout+r.Stderr, "no terminal") {
		t.Errorf("asked for --yes with a terminal attached:\n%s", r.Stderr)
	}
	if len(s.Fake.Writes()) == 0 {
		t.Fatal("confirmed apply made no writes")
	}
}

// TestPrune_CAP031_AC4_TypedNameConfirmation: deleting a bucket with --prune needs its name typed;
// a wrong name cancels with no writes, the right name and a yes delete only that bucket.
func TestPrune_CAP031_AC4_TypedNameConfirmation(t *testing.T) {
	s, cid := appliedScenario(t)
	s.Fake.SeedBucket(cid, "scratch", capellafake.WithField("stats", map[string]any{"itemCount": 42}))
	s.TTY(true)

	s.Answers("wrong")
	r := mustScenario(t, s.Run("capella", "apply", "--prune"), "apply --prune (wrong name)")
	wantContains(t, r.Stderr, "Destructive changes:", "bucket envclu/scratch",
		"deletes bucket envclu/scratch with every scope, collection and document in it",
		"items stored: 42", "Apply cancelled; nothing was changed.")
	clitest.AssertNoWrites(t, s.Fake)

	s.Answers("scratch", "y")
	mustScenario(t, s.Run("capella", "apply", "--prune"), "apply --prune (confirmed)")
	clitest.AssertWrites(t, s.Fake, "DELETE /v4/organizations/*/projects/*/clusters/*/buckets/*")

	// Without a terminal and without --yes, prune is refused before any question.
	s.TTY(false)
	s.Fake.SeedBucket(cid, "scratch2")
	s.Fake.ResetCalls()
	r = s.Run("capella", "apply", "--prune")
	wantExit(t, r, cli.ExitBlocked)
	clitest.AssertNoWrites(t, s.Fake)
}

// TestPrune_CAP031_AC4_ProtectionBlocks: deletion protection blocks a prune even with --yes.
func TestPrune_CAP031_AC4_ProtectionBlocks(t *testing.T) {
	s, cid := appliedScenario(t)
	s.Fake.SeedBucket(cid, "scratch")
	if err := s.Fake.Client().SetDeletionProtection(context.Background(), projectOf(t, s), cid, true); err != nil {
		t.Fatal(err)
	}
	s.Fake.ResetCalls()
	r := s.Run("capella", "apply", "--prune", "--yes")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout+r.Stderr, "deletion protection is on for envclu")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestApply_RotatePasswords_ListsRotating: --rotate-passwords names each credential before asking.
func TestApply_RotatePasswords_ListsRotating(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	t.Setenv("APP_PW", "Rotate-Me-Pass-01!")
	s.Env["APP_PW"] = "Rotate-Me-Pass-01!"
	doc := scenarioBucketDoc + `    databaseCredentials:
      - name: app
        password: ${APP_PW}
        access:
          - privileges: [read]
            resources:
              buckets:
                - name: orders
`
	writeCapellaYAML(t, s, doc)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes")
	s.Fake.ResetCalls()

	s.TTY(true)
	s.Answers("n")
	r := mustScenario(t, s.Run("capella", "apply", "--rotate-passwords"), "apply --rotate-passwords (declined)")
	wantContains(t, r.Stderr, "Passwords to rotate:", "app", "Apply cancelled.")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestDestroy_CAP030_TypedProjectNames: destroy needs the project name typed; anything else cancels.
func TestDestroy_CAP030_TypedProjectNames(t *testing.T) {
	s, _ := appliedScenario(t)
	s.TTY(true)

	s.Answers("not-it")
	r := mustScenario(t, s.Run("capella", "destroy"), "destroy (wrong name)")
	wantContains(t, r.Stdout, "Destroy cancelled; nothing was deleted.")
	wantContains(t, r.Stderr, "This deletes everything listed above", "Snapshot backups are kept.")
	clitest.AssertNoWrites(t, s.Fake)

	s.Answers(" envproj ")
	r = mustScenario(t, s.Run("capella", "destroy"), "destroy (confirmed)")
	wantContains(t, r.Stderr, "Destroy complete")
	if len(s.Fake.Writes()) == 0 {
		t.Fatal("confirmed destroy made no writes")
	}
}

func projectOf(t *testing.T, s *clitest.Scenario) string {
	t.Helper()
	ps, err := s.Fake.Client().ListProjects(context.Background())
	if err != nil || len(ps) == 0 {
		t.Fatalf("projects = %v, %v", ps, err)
	}
	return ps[0].ID
}
