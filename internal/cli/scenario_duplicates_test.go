package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// Capella allows two projects, or two clusters in a project, with the same name. cbctl must
// never guess between them.

const dupClusterDoc = `apiVersion: cbctl/v1
project:
  name: nova-dev
clusters:
  - name: nova-dev-01
    cloudProvider:
      type: aws
      region: us-east-1
      cidr: 10.0.0.0/24
    availability:
      type: multi
    support:
      plan: developer pro
      timezone: PT
    serviceGroups:
      - node:
          compute: {cpu: 4, ram: 16}
          disk: {type: gp3, storage: 50, iops: 3000}
        numOfNodes: 3
        services: [data, index, query]
`

// TestPlan_CAP005_AC5_AmbiguousClusterBlocked: two clusters share the file's name.
func TestPlan_CAP005_AC5_AmbiguousClusterBlocked(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	a := s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	b := s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	writeCapellaYAML(t, s, dupClusterDoc)
	r := s.Run("capella", "plan", "--detailed-exitcode")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "! cluster nova-dev-01", "2 clusters share this name", a, b)

	s.Fake.ResetCalls()
	r = s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitBlocked)
	clitest.AssertNoWrites(t, s.Fake)
}

// TestPlan_CAP005_AC5_AmbiguousProjectBlocked: two projects share the file's name.
func TestPlan_CAP005_AC5_AmbiguousProjectBlocked(t *testing.T) {
	s := newScenario(t)
	p1 := s.Fake.SeedProject("nova-dev")
	p2 := s.Fake.SeedProject("nova-dev")
	writeCapellaYAML(t, s, dupClusterDoc)
	r := s.Run("capella", "plan")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "2 projects share this name", p1, p2)
}

// TestSync_CAP038_AC6_AmbiguousClustersPinnedByID: sync imports both same-named clusters and
// tells them apart by id, and the plan of that file is clean.
func TestSync_CAP038_AC6_AmbiguousClustersPinnedByID(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	a := s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	b := s.Fake.SeedCluster(pid, "nova-dev-01", "healthy",
		capellafake.WithField("cloudProvider", capellafake.Obj{"type": "aws", "region": "us-east-1", "cidr": "10.1.0.0/20"}))
	r := mustScenario(t, s.Run("capella", "sync"), "sync")
	wantContains(t, r.Stdout+r.Stderr, "2 clusters are named nova-dev-01")
	text := s.ReadFile("capella.yaml")
	if !strings.Contains(text, a) || !strings.Contains(text, b) {
		t.Fatalf("both clusters are not pinned by id:\n%s", text)
	}
	mustScenario(t, s.Run("capella", "plan", "--detailed-exitcode"), "plan of the synced file")

	mustScenario(t, s.Run("capella", "sync", "--cluster-id", b, "--force"), "sync --cluster-id")
	text = s.ReadFile("capella.yaml")
	if strings.Contains(text, a) || !strings.Contains(text, b) {
		t.Errorf("--cluster-id %s kept the other cluster:\n%s", b, text)
	}
}

// TestSync_CAP054_AC3_AmbiguousProjectNeedsID: --project naming two projects is refused with
// their ids; --project-id picks one.
func TestSync_CAP054_AC3_AmbiguousProjectNeedsID(t *testing.T) {
	s := newScenario(t)
	p1 := s.Fake.SeedProject("dup")
	p2 := s.Fake.SeedProject("dup")
	r := s.Run("capella", "sync", "--project", "dup")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "2 projects are named dup", p1, p2, "--project-id")
	mustScenario(t, s.Run("capella", "sync", "--project-id", p2), "sync --project-id")
	if text := s.ReadFile("capella.yaml"); !strings.Contains(text, p2) || strings.Contains(text, p1) {
		t.Errorf("--project-id did not pick %s:\n%s", p2, text)
	}
}

// TestProject_CAP009_AC2_CreateRefusesExistingName: Capella would accept a second project
// with the name, so cbctl checks first and sends nothing.
func TestProject_CAP009_AC2_CreateRefusesExistingName(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("alpha")
	s.Fake.ResetCalls()
	r := s.Run("capella", "project", "create", "alpha", "--direct")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "project alpha already exists")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestClusterDelete_CAP036_AC6_AmbiguousBlocked: deleting by a shared name is refused.
func TestClusterDelete_CAP036_AC6_AmbiguousBlocked(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	s.Fake.ResetCalls()
	r := s.Run("capella", "cluster", "delete", "nova-dev-01", "--direct", "--yes")
	if r.Code == 0 {
		t.Fatalf("delete of an ambiguous name succeeded\n%s", r.Stdout)
	}
	wantContains(t, r.Stderr, "clusters are named nova-dev-01")
	clitest.AssertNoWrites(t, s.Fake)
}
