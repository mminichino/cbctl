package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
)

// Ports of capellactl's tests/scenario/test_sync_all.py (CAP-054).

// seedNonProd mirrors capellactl's _non_prod: project non-prod with np-01..np-03 (Azure), each
// with a bucket orders; the first off clusters are turned off.
func seedNonProd(s *clitest.Scenario, off int) (pid string, ids []string) {
	pid = s.Fake.SeedProject("non-prod")
	for i, name := range []string{"np-01", "np-02", "np-03"} {
		cid := s.Fake.SeedCluster(pid, name, "healthy", capellafake.WithField("cloudProvider",
			capellafake.Obj{"type": "azure", "region": "eastus", "cidr": fmt.Sprintf("10.30.%d.0/24", i)}))
		s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 512))
		if i < off {
			s.Fake.SetClusterState(cid, "turnedOff")
		}
		ids = append(ids, cid)
	}
	return pid, ids
}

// oneProjectFile mirrors capellactl's one_project_file fixture.
func oneProjectFile(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	seedHealthy(s)
	syncOK(t, s, "--project", "nova-dev")
	s.Fake.ResetCalls()
	return s
}

// TestSync_CAP054_AC3_ProjectLimitsRun: --project reads only that project and says so.
func TestSync_CAP054_AC3_ProjectLimitsRun(t *testing.T) {
	s := oneProjectFile(t)
	nonProd, _ := seedNonProd(s, 0)
	s.Fake.ResetCalls()
	r := syncOK(t, s, "--project", "nova-dev")
	wantContains(t, r.Stdout, "already in sync with Capella for nova-dev (other projects not checked)")
	if n := s.Fake.Count("GET", `/projects/`+nonProd+`/clusters`); n != 0 {
		t.Errorf("%d reads of non-prod's clusters, want 0", n)
	}
	wantNames(t, "projects", docProjects(t, s), "nova-dev")
	wantContains(t, syncOK(t, s, "--project", "non-prod").Stdout, "added project non-prod (3 clusters)")
}

// TestSync_CAP054_AC3_SkipProjectRememberedFull: --skip-project records skipProjects, a later sync
// reports it as skipped, and --project brings it back and drops skipProjects.
func TestSync_CAP054_AC3_SkipProjectRememberedFull(t *testing.T) {
	s := oneProjectFile(t)
	seedNonProd(s, 0)
	syncOK(t, s)
	r := syncOK(t, s, "--skip-project", "non-prod")
	wantContains(t, r.Stdout+r.Stderr, "project non-prod removed from capella.yaml and skipped from now on")
	doc := fileDoc(t, s)
	if sk, _ := doc["skipProjects"].([]any); len(sk) != 1 || sk[0] != "non-prod" {
		t.Errorf("skipProjects = %v, want [non-prod]", doc["skipProjects"])
	}
	wantNames(t, "projects", docProjects(t, s), "nova-dev")
	mustScenario(t, s.Run("capella", "validate"), "validate")
	r = syncOK(t, s)
	wantContains(t, r.Stdout, "skipped: non-prod")
	wantNotContains(t, r.Stdout, "added project")
	wantContains(t, syncOK(t, s, "--project", "non-prod").Stdout, "added project non-prod")
	if _, ok := fileDoc(t, s)["skipProjects"]; ok {
		t.Errorf("skipProjects kept after --project brought non-prod back:\n%s", s.ReadFile("capella.yaml"))
	}
	wantNames(t, "projects", docProjects(t, s), "nova-dev", "non-prod")
}

// TestSync_CAP054_AC4_TurnedOffNotWaitedFor: new turned-off clusters are recorded without waiting
// and without reading their buckets or credentials.
func TestSync_CAP054_AC4_TurnedOffNotWaitedFor(t *testing.T) {
	s := oneProjectFile(t)
	_, ids := seedNonProd(s, 2)
	s.Fake.ResetCalls()
	start := s.Clock.Monotonic()
	r := syncOK(t, s)
	if s.Clock.Monotonic() != start || len(s.Clock.Sleeps()) != 0 {
		t.Errorf("sync waited: %v", s.Clock.Sleeps())
	}
	for _, cid := range ids[:2] {
		if n := s.Fake.Count("GET", `/clusters/`+cid+`/(buckets|users)$`); n != 0 {
			t.Errorf("%d bucket/credential reads of turned-off cluster %s", n, cid)
		}
	}
	wantContains(t, r.Stdout, "2 clusters are turned off: np-01, np-02",
		"not read yet: buckets and credentials of np-01, np-02 — run 'cbctl capella sync' again when they are on")
	np1 := docCluster(t, s, "non-prod", "np-01")
	if np1["serviceGroups"] == nil {
		t.Error("np-01 has no serviceGroups")
	}
	if _, ok := np1["buckets"]; ok {
		t.Error("np-01 has buckets although it is off")
	}
	wantNames(t, "np-03 buckets", asMaps(docCluster(t, s, "non-prod", "np-03")["buckets"]), "orders")
}

// TestSync_CAP054_AC4_PickedUpWhenOn: a cluster turned on is read on the next sync; one recorded
// with buckets and then turned off keeps them.
func TestSync_CAP054_AC4_PickedUpWhenOn(t *testing.T) {
	s := oneProjectFile(t)
	_, ids := seedNonProd(s, 2)
	syncOK(t, s)
	s.Fake.SetClusterState(ids[0], "healthy")
	r := syncOK(t, s)
	wantContains(t, r.Stdout, "1 cluster is turned off: np-02")
	wantNames(t, "np-01 buckets", asMaps(docCluster(t, s, "non-prod", "np-01")["buckets"]), "orders")
	if _, ok := docCluster(t, s, "non-prod", "np-02")["buckets"]; ok {
		t.Error("np-02 has buckets although it is off")
	}
	s.Fake.SetClusterState(ids[2], "turnedOff")
	r = syncOK(t, s)
	wantContains(t, r.Stdout, "kept from capella.yaml: buckets and credentials of np-03")
	wantNames(t, "np-03 buckets", asMaps(docCluster(t, s, "non-prod", "np-03")["buckets"]), "orders")
}

// TestSync_CAP054_AC6_OnlyGetsAndNoRewrite: sync only reads, and an in-sync run leaves the file
// byte for byte and writes no new backup.
func TestSync_CAP054_AC6_OnlyGetsAndNoRewrite(t *testing.T) {
	s := oneProjectFile(t)
	seedNonProd(s, 1)
	syncOK(t, s)
	bak := filepath.Join(s.Dir, "capella.yaml.bak")
	if err := os.Remove(bak); err != nil {
		t.Fatalf("no backup after the refresh: %v", err)
	}
	text := s.ReadFile("capella.yaml")
	wantContains(t, syncOK(t, s).Stdout, "already in sync")
	if s.ReadFile("capella.yaml") != text {
		t.Error("an in-sync run rewrote capella.yaml")
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Errorf("an in-sync run wrote a backup (stat err %v)", err)
	}
}
