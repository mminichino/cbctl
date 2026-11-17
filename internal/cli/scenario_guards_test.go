package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// The fake reports half of each data node's RAM as bucket quota (8192 MB per 16 GB node).
// Multi-AZ groups need at least 3 nodes, so scale-in cases start from 4 nodes (32768 MB) and
// go to 3 (24576 MB).

const statsPath = `/clusters/[^/]+/stats$`

func scaledDoc(nodes string, services string) string {
	d := strings.Replace(scenarioDoc, "numOfNodes: 3", "numOfNodes: "+nodes, 1)
	return strings.Replace(d, "services: [data, index, query]", "services: ["+services+"]", 1)
}

// fourNodeScenario applies scenarioDoc with 4 data nodes.
func fourNodeScenario(t *testing.T) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scaledDoc("4", "data, index, query"))
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply 4 nodes")
	c := s.Fake.Client()
	cls, err := c.ListClusters(t.Context(), projectOf(t, s))
	if err != nil || len(cls) != 1 {
		t.Fatalf("clusters = %v, %v", cls, err)
	}
	s.Fake.ResetCalls()
	return s, cls[0].ID
}

// TestScaleIn_CAP016_Guards walks the scale-in guards of a declarative apply.
func TestScaleIn_CAP016_Guards(t *testing.T) {
	t.Run("bucket memory exceeds the smaller cluster", func(t *testing.T) {
		s, cid := fourNodeScenario(t)
		s.Fake.SeedBucket(cid, "big", capellafake.WithField("memoryAllocationInMb", 30000))
		writeCapellaYAML(t, s, scaledDoc("3", "data, index, query"))
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "scale-in blocked: buckets allocate", "after scale-in (4 → 3 nodes)")
		clitest.AssertNoWrites(t, s.Fake)
	})
	t.Run("memory fits", func(t *testing.T) {
		s, cid := fourNodeScenario(t)
		s.Fake.SeedBucket(cid, "small", capellafake.WithField("memoryAllocationInMb", 1000))
		writeCapellaYAML(t, s, scaledDoc("3", "data, index, query"))
		r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply scale-in")
		wantContains(t, r.Stdout, "memory ok")
		clitest.AssertWrites(t, s.Fake, "PUT /v4/organizations/*/projects/*/clusters/*")
	})
	t.Run("stats unavailable blocks", func(t *testing.T) {
		s, _ := fourNodeScenario(t)
		s.Fake.FailNext("GET", statsPath, 403, 100)
		writeCapellaYAML(t, s, scaledDoc("3", "data, index, query"))
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "cannot verify capacity (stats unavailable); pass --skip-capacity-check")
		clitest.AssertNoWrites(t, s.Fake)
	})
	t.Run("--skip-capacity-check proceeds", func(t *testing.T) {
		s, _ := fourNodeScenario(t)
		s.Fake.FailNext("GET", statsPath, 403, 100)
		writeCapellaYAML(t, s, scaledDoc("3", "data, index, query"))
		r := mustScenario(t, s.Run("capella", "apply", "--yes", "--skip-capacity-check"), "apply --skip-capacity-check")
		wantContains(t, r.Stdout, "memory not checked (--skip-capacity-check)")
		clitest.AssertWrites(t, s.Fake, "PUT /v4/organizations/*/projects/*/clusters/*")
	})
	t.Run("group removal takes the last nodes of a service", func(t *testing.T) {
		s, _ := appliedScenario(t)
		// Changing the services changes the group's key: the old group is removed.
		writeCapellaYAML(t, s, scaledDoc("3", "data"))
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "removes the last index, query nodes; pass --allow-service-removal --allow-service-group-removal")
		clitest.AssertNoWrites(t, s.Fake)

		// Each flag lifts only its own guard.
		r = s.Run("capella", "apply", "--yes", "--allow-service-removal")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "pass --allow-service-group-removal")
		clitest.AssertNoWrites(t, s.Fake)

		mustScenario(t, s.Run("capella", "apply", "--yes", "--allow-service-removal", "--allow-service-group-removal"), "apply with both flags")
		clitest.AssertWrites(t, s.Fake, "PUT /v4/organizations/*/projects/*/clusters/*")
	})
	t.Run("rerun reads no stats", func(t *testing.T) {
		s, _ := appliedScenario(t)
		mustScenario(t, s.Run("capella", "apply", "--yes"), "apply rerun")
		if n := s.Fake.Count("GET", statsPath); n != 0 {
			t.Errorf("no-change apply read stats %d time(s)", n)
		}
	})
}

// TestBucket_CAP023_AC6_Preflight: a bucket that does not fit, or asks for more replicas than
// the data nodes allow, is blocked before anything is written.
func TestBucket_CAP023_AC6_Preflight(t *testing.T) {
	bucketDoc := func(mb, replicas string) string {
		return scenarioDoc + "    buckets:\n      - name: orders\n        memoryAllocationInMb: " + mb + "\n        replicas: " + replicas + "\n"
	}
	t.Run("memory", func(t *testing.T) {
		s, _ := appliedScenario(t)
		writeCapellaYAML(t, s, bucketDoc("30000", "1"))
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "needs 30000 MB but the cluster has 24576 MB free")
		clitest.AssertNoWrites(t, s.Fake)
	})
	t.Run("replicas", func(t *testing.T) {
		s, _ := appliedScenario(t)
		writeCapellaYAML(t, s, bucketDoc("1024", "3"))
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "replicas: 3 but the cluster supports at most 2")
		clitest.AssertNoWrites(t, s.Fake)
	})
	t.Run("two buckets share the free memory", func(t *testing.T) {
		s, _ := appliedScenario(t)
		doc := bucketDoc("20000", "1") + "      - name: second\n        memoryAllocationInMb: 5000\n"
		writeCapellaYAML(t, s, doc)
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "needs 5000 MB but the cluster has 4576 MB free")
	})
	t.Run("fits", func(t *testing.T) {
		s, _ := appliedScenario(t)
		writeCapellaYAML(t, s, bucketDoc("1024", "2"))
		mustScenario(t, s.Run("capella", "apply", "--yes"), "apply bucket")
	})
}

// TestClusterScale_CAP016_ImperativeCapacity: the imperative scale-in has the same guard.
func TestClusterScale_CAP016_ImperativeCapacity(t *testing.T) {
	s, cid := fourNodeScenario(t)
	s.Fake.SeedBucket(cid, "big", capellafake.WithField("memoryAllocationInMb", 30000))
	r := s.Run("capella", "--yes", "cluster", "scale", "envclu", "--nodes", "3", "--direct")
	wantExit(t, r, cli.ExitBlocked)
	clitest.AssertNoWrites(t, s.Fake)

	s.Fake.FailNext("GET", statsPath, 403, 100)
	r = s.Run("capella", "--yes", "cluster", "scale", "envclu", "--nodes", "3", "--direct")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "--skip-capacity-check")
	clitest.AssertNoWrites(t, s.Fake)
}
