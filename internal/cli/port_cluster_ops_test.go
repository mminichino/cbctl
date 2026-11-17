package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// TestClusterDelete_CAP014_AC3_ListsBucketsInConfirmation: the confirmation counts and names the
// buckets that go with the cluster; one DELETE, no separate bucket deletes.
func TestClusterDelete_CAP014_AC3_ListsBucketsInConfirmation(t *testing.T) {
	s, cid := nodeScenario(t, scenarioDoc)
	s.Fake.SeedBucket(cid, "orders")
	s.Fake.SeedBucket(cid, "events")
	s.TTY(true)
	s.Answers("envclu")
	r := mustScenario(t, s.Run("capella", "cluster", "delete", "envclu"), "delete")
	wantContains(t, r.Stdout, "· 2 buckets", "buckets deleted with the cluster: events, orders")
	clitest.AssertWrites(t, s.Fake, "DELETE /v4/organizations/*/projects/*/clusters/"+cid)
}

// TestClusterDelete_CAP014_AC6_MentionsSchedule: the summary says the on/off (hibernation)
// schedule is deleted with the cluster, and the DELETE keeps snapshot backups.
func TestClusterDelete_CAP014_AC6_MentionsSchedule(t *testing.T) {
	s, cid := nodeScenario(t, scenarioDoc)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "schedule", "set", "envclu", "--timezone", "US/Pacific",
		"--weekdays", "08:00-18:00", "--direct"), "schedule set")
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "delete", "envclu"), "delete")
	// cbctl calls the hibernation schedule the on/off schedule (its Capella name).
	wantContains(t, r.Stdout, "on/off schedule (US/Pacific) is deleted with the cluster")
	ws := s.Fake.Writes()
	if len(ws) != 1 || ws[0].Method != "DELETE" || !strings.HasSuffix(ws[0].Path, "/clusters/"+cid) ||
		ws[0].Query.Get("retainSnapshotBackups")+ws[0].Query.Get("retainsnapshotbackups") != "true" {
		t.Errorf("writes = %v, want one cluster DELETE keeping snapshot backups", ws)
	}
}

// cloneScenario is a synced capella.yaml (as capellactl's fixture imports it) of cluster envclu
// in us-east-1 with zones, a bucket and an on/off schedule. The request log is empty.
func cloneScenario(t *testing.T) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	pid := s.Fake.SeedProject("envproj")
	cid := s.Fake.SeedCluster(pid, "envclu", "healthy", capellafake.WithField("zones", []any{"use1-az1", "use1-az2", "use1-az4"}))
	s.Fake.SeedBucket(cid, "orders")
	mustScenario(t, s.Run("capella", "--yes", "cluster", "schedule", "set", "envclu", "--timezone", "US/Pacific",
		"--weekdays", "08:00-18:00", "--direct"), "schedule set")
	mustScenario(t, s.Run("capella", "sync", "--project", "envproj"), "sync")
	s.Fake.ResetCalls()
	return s, pid
}

// clonePost is the body of the only cluster POST.
func clonePost(t *testing.T, s *clitest.Scenario) map[string]any {
	t.Helper()
	posts := s.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters")
	if len(posts) != 1 {
		t.Fatalf("cluster POSTs = %v", posts)
	}
	b, _ := posts[0].Body.(map[string]any)
	return b
}

var cloneArgs = []string{"capella", "--yes", "cluster", "clone", "envclu", "envclu-w", "--region", "us-west-2"}

// TestClusterClone_CAP049_CIDRFreeInOrganization is the nightly failure of 2026-11-17: the managed
// clone picked its network from capella.yaml alone, and Capella refused it as "not unique within
// this organization" because a cluster of another project had it.
func TestClusterClone_CAP049_CIDRFreeInOrganization(t *testing.T) {
	first, _ := cloneScenario(t)
	mustScenario(t, first.Run(cloneArgs...), "clone")
	picked := clonePost(t, first)["cloudProvider"].(map[string]any)["cidr"]

	s, _ := cloneScenario(t)
	other := s.Fake.SeedProject("elsewhere")
	s.Fake.SeedCluster(other, "taken", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": picked}))
	mustScenario(t, s.Run(cloneArgs...), "clone")
	if got := clonePost(t, s)["cloudProvider"].(map[string]any)["cidr"]; got == picked || got == nil {
		t.Fatalf("clone CIDR = %v, want a range other than %v (used by project elsewhere)", got, picked)
	}
}

// TestClusterClone_CAP049_AC3_RegionBoundValuesReset: zones and the schedule stay behind; the
// clone gets its own CIDR and no schedule is written.
func TestClusterClone_CAP049_AC3_RegionBoundValuesReset(t *testing.T) {
	s, _ := cloneScenario(t)
	file := filepath.Join(s.Dir, "capella.yaml")
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "hibernation:") {
		t.Fatalf("synced file has no schedule:\n%s", b)
	}
	r := mustScenario(t, s.Run(cloneArgs...), "clone")
	wantContains(t, r.Stdout, "not copied: documents, on/off schedule")
	body := clonePost(t, s)
	cp, _ := body["cloudProvider"].(map[string]any)
	if _, ok := body["zones"]; ok || cp["cidr"] == "10.0.0.0/20" || cp["cidr"] == nil || cp["region"] != "us-west-2" {
		t.Errorf("clone POST = %v, want no zones and a new CIDR in us-west-2", body)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	clone := string(b)[strings.Index(string(b), "name: envclu-w"):]
	if strings.Contains(clone, "hibernation") || strings.Contains(clone, "zones") {
		t.Errorf("clone entry copies region-bound values:\n%s", clone)
	}
	for _, w := range s.Fake.Writes() {
		if strings.Contains(w.Path, "onOffSchedule") {
			t.Errorf("clone wrote a schedule: %v", w)
		}
	}
}

// TestClusterClone_CAP049_AC8_Idempotent: after a clone, plan and apply see no change, sync is in
// step, and cloning again is refused (exit 4) with no writes.
func TestClusterClone_CAP049_AC8_Idempotent(t *testing.T) {
	s, _ := cloneScenario(t)
	mustScenario(t, s.Run(cloneArgs...), "clone")
	mustScenario(t, s.Run("capella", "plan", "--detailed-exitcode"), "plan")
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply")
	clitest.AssertNoWrites(t, s.Fake)
	mustScenario(t, s.Run("capella", "sync", "--dry-run"), "sync --dry-run")
	r := s.Run(cloneArgs...)
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "cluster envclu-w already exists")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestClusterClone_CAP049_ServerVersionLatest: --server-version latest sends no couchbaseServer.
func TestClusterClone_CAP049_ServerVersionLatest(t *testing.T) {
	s, _ := cloneScenario(t)
	mustScenario(t, s.Run(append(cloneArgs, "--server-version", "latest")...), "clone")
	if v, ok := clonePost(t, s)["couchbaseServer"]; ok {
		t.Errorf("clone POST has couchbaseServer %v", v)
	}
	// Without the flag the source's release line is kept: Capella reports 7.6.2 but refuses a
	// patch version on create (422 "The Couchbase Server version '8.0.3' is not supported").
	s2, _ := cloneScenario(t)
	mustScenario(t, s2.Run(cloneArgs...), "clone")
	if v := clonePost(t, s2)["couchbaseServer"]; !jsonEqual(v, map[string]any{"version": "7.6"}) {
		t.Errorf("clone POST couchbaseServer = %v, want the source's release line 7.6", v)
	}
}
