package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// In cbctl, `capella sync` replaces capellactl's `import` (cbctl's top-level `import` is the JSON
// Lines importer); these tests port capellactl's tests/scenario/test_import.py onto sync.

// TestSync_CAP036_AC1_AllKindsExported: the first sync writes every kind and counts them.
func TestSync_CAP036_AC1_AllKindsExported(t *testing.T) {
	s := newScenario(t)
	richSeed(t, s)
	r := syncOK(t, s)
	wantContains(t, r.Stdout, "Imported 1 project, 1 cluster, 2 buckets, 2 scopes, 3 collections, 2 credentials, "+
		"1 allowed CIDR, 1 hibernation schedule into ")
	projects := docProjects(t, s)
	wantNames(t, "projects", projects, "nova-dev")
	cl := docCluster(t, s, "nova-dev", "nova-dev-01")
	if cl["deletionProtection"] != true {
		t.Errorf("deletionProtection = %v, want true", cl["deletionProtection"])
	}
	h, _ := cl["hibernation"].(map[string]any)
	if h["timezone"] != "US/Pacific" || h["weekdays"] != "08:00-18:00" || h["weekend"] != "off" || len(h) != 3 {
		t.Errorf("hibernation = %v", h)
	}
	buckets := asMaps(cl["buckets"])
	wantNames(t, "buckets", buckets, "orders", "sessions")
	wantNames(t, "scopes of orders", asMaps(buckets[0]["scopes"]), "inventory", "billing")
	wantNames(t, "credentials", asMaps(cl["databaseCredentials"]), "orders_app", "reporting_ro")
}

// TestSync_CAP036_AC1_ExpiredCIDRSkipped: an expired allow-list entry is not written.
func TestSync_CAP036_AC1_ExpiredCIDRSkipped(t *testing.T) {
	s := newScenario(t)
	richSeed(t, s)
	syncOK(t, s)
	cidrs := asMaps(docCluster(t, s, "", "nova-dev-01")["allowedCidrs"])
	if len(cidrs) != 1 || cidrs[0]["cidr"] != "203.0.113.10/32" || cidrs[0]["comment"] != "Office VPN" || len(cidrs[0]) != 2 {
		t.Errorf("allowedCidrs = %v, want only the active 203.0.113.10/32 (Office VPN)", cidrs)
	}
	wantNotContains(t, s.ReadFile("capella.yaml"), "198.51.100.0/24")
}

// TestSync_CAP036_AC1_AnyIPCIDRRecordsAllowAnyIP: 0.0.0.0/0 in Capella writes allowAnyIp: true
// ahead of the schedule, and the file validates.
func TestSync_CAP036_AC1_AnyIPCIDRRecordsAllowAnyIP(t *testing.T) {
	s := newScenario(t)
	_, cid := richSeed(t, s)
	s.Fake.SeedAllowedCIDR(cid, "0.0.0.0/0", "", "active")
	syncOK(t, s)
	if docCluster(t, s, "", "nova-dev-01")["allowAnyIp"] != true {
		t.Fatalf("allowAnyIp not written:\n%s", s.ReadFile("capella.yaml"))
	}
	text := s.ReadFile("capella.yaml")
	if a, h := strings.Index(text, "allowAnyIp:"), strings.Index(text, "hibernation:"); a < 0 || h < 0 || a > h {
		t.Errorf("allowAnyIp must come before hibernation:\n%s", text)
	}
	validates(t, s)
}

// TestSync_CAP036_AC1_AdvancedCredentialCommented: an advanced credential is a reference comment
// under databaseCredentials, not an entry.
func TestSync_CAP036_AC1_AdvancedCredentialCommented(t *testing.T) {
	s := newScenario(t)
	richSeed(t, s)
	syncOK(t, s)
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "# dba is an advanced credential (userRoles)", "        # - name: dba", "admin_role")
	if strings.Contains(text, "\n# - name: dba") {
		t.Errorf("the commented-out credential is not indented under its cluster:\n%s", text)
	}
	for _, n := range names(asMaps(docCluster(t, s, "", "nova-dev-01")["databaseCredentials"])) {
		if n == "dba" {
			t.Error("dba written as a managed credential")
		}
	}
	validates(t, s)
}

// roundTripSeeds mirrors capellactl's SEEDS: each must sync, validate and plan with no changes.
var roundTripSeeds = map[string]func(*testing.T, *clitest.Scenario){
	"minimal": func(_ *testing.T, s *clitest.Scenario) { seedHealthy(s) },
	"rich":    func(t *testing.T, s *clitest.Scenario) { richSeed(t, s) },
	"single-node": func(_ *testing.T, s *clitest.Scenario) {
		seedHealthy(s, capellafake.WithField("configurationType", "singleNode"),
			capellafake.WithField("availability", capellafake.Obj{"type": "single"}),
			capellafake.WithField("serviceGroups", []any{group(1, 4, 16, capellafake.Obj{"type": "gp3", "storage": 50, "iops": 3000}, "data", "index", "query")}))
	},
	"two-groups": func(_ *testing.T, s *clitest.Scenario) {
		seedHealthy(s, capellafake.WithField("serviceGroups", []any{
			group(3, 8, 32, capellafake.Obj{"type": "io2", "storage": 100, "iops": 5000}, "query", "data", "index"),
			group(2, 4, 16, capellafake.Obj{"type": "gp3", "storage": 50, "iops": 3000}, "search"),
		}))
	},
	"azure": func(_ *testing.T, s *clitest.Scenario) {
		seedHealthy(s, capellafake.WithField("cloudProvider", capellafake.Obj{"type": "azure", "region": "eastus", "cidr": "10.1.0.0/23"}),
			capellafake.WithField("serviceGroups", []any{group(3, 4, 16, capellafake.Obj{"type": "P10", "autoExpansion": false}, "data", "index", "query")}))
	},
	"gcp-enterprise": func(_ *testing.T, s *clitest.Scenario) {
		seedHealthy(s, capellafake.WithField("cloudProvider", capellafake.Obj{"type": "gcp", "region": "us-east1", "cidr": "10.2.0.0/23"}),
			capellafake.WithField("support", capellafake.Obj{"plan": "enterprise", "timezone": "ET"}),
			capellafake.WithField("serviceGroups", []any{group(3, 4, 16, capellafake.Obj{"type": "pd-ssd", "storage": 100}, "data", "index", "query")}))
	},
}

func group(nodes, cpu, ram int, disk capellafake.Obj, services ...string) capellafake.Obj {
	sv := make([]any, len(services))
	for i, x := range services {
		sv[i] = x
	}
	return capellafake.Obj{"node": capellafake.Obj{"compute": capellafake.Obj{"cpu": cpu, "ram": ram}, "disk": disk},
		"numOfNodes": nodes, "services": sv}
}

// TestSync_CAP036_AC2_RoundTripNoChanges: every seed, minimal and --full, syncs to a file that
// validates, plans with no changes and applies with no writes.
func TestSync_CAP036_AC2_RoundTripNoChanges(t *testing.T) {
	for _, seed := range []string{"minimal", "rich", "single-node", "two-groups", "azure", "gcp-enterprise"} {
		for _, full := range []bool{false, true} {
			name := seed + "/minimal"
			args := []string{}
			if full {
				name, args = seed+"/full", []string{"--full"}
			}
			t.Run(name, func(t *testing.T) {
				s := newScenario(t)
				roundTripSeeds[seed](t, s)
				syncOK(t, s, args...)
				validates(t, s)
				planClean(t, s)
				r := mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
				wantContains(t, r.Stdout, "No changes. Capella matches capella.yaml.")
				clitest.AssertNoWrites(t, s.Fake)
			})
		}
	}
}

// TestSync_CAP036_AC2_RoundTripChildren: buckets, scopes, collections, CIDRs, credentials and the
// schedule all round-trip to a plan with no changes.
func TestSync_CAP036_AC2_RoundTripChildren(t *testing.T) {
	s := newScenario(t)
	richSeed(t, s)
	syncOK(t, s)
	r := planClean(t, s)
	wantContains(t, r.Stdout, "= schedule nova-dev-01", "= allowed-cidr nova-dev-01/203.0.113.10/32",
		"= scope nova-dev-01/orders/inventory", "= collection nova-dev-01/orders/billing/invoices",
		"= credential nova-dev-01/orders_app", "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked.")
}

// TestSync_CAP036_AC4_NoSecrets: no password is written or printed; each basic credential gets the
// "password not exported" note.
func TestSync_CAP036_AC4_NoSecrets(t *testing.T) {
	s := newScenario(t)
	richSeed(t, s)
	r := syncOK(t, s)
	text := s.ReadFile("capella.yaml")
	if strings.Contains(text, "password:") {
		t.Errorf("a password key was written:\n%s", text)
	}
	if n := strings.Count(text, "# password not exported (Capella never returns it)"); n != 2 {
		t.Errorf("%d 'password not exported' notes, want 2:\n%s", n, text)
	}
	clitest.AssertNoSecret(t, clitest.Result{Stdout: r.Stdout + text, Stderr: r.Stderr}, richPassword, s.Fake.APIKey)
}

// TestSync_CAP036_AC5_ClusterFilter: a first sync with --cluster writes only that cluster; an
// unknown --cluster fails.
func TestSync_CAP036_AC5_ClusterFilter(t *testing.T) {
	s := newScenario(t)
	pid, _ := seedHealthy(s)
	s.Fake.SeedCluster(pid, "nova-dev-02", "healthy")
	syncOK(t, s, "--cluster", "nova-dev-02")
	wantNames(t, "clusters", docClusters(t, s, ""), "nova-dev-02")

	r := s.Run("capella", "sync", "--cluster", "nope", "--force")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "cluster(s) not found in Capella: nope")
}

// TestSync_CAP036_AC6_MinimalOmitsDefaults: the default sync leaves out values equal to the
// defaults.
func TestSync_CAP036_AC6_MinimalOmitsDefaults(t *testing.T) {
	s := newScenario(t)
	_, cid := seedHealthy(s)
	s.Fake.SeedBucket(cid, "plain", capellafake.WithField("memoryAllocationInMb", 512))
	syncOK(t, s)
	b := asMaps(docCluster(t, s, "", "nova-dev-01")["buckets"])[0]
	want := map[string]any{"name": "plain", "storageBackend": "couchstore", "memoryAllocationInMb": 512, "evictionPolicy": "fullEviction"}
	if len(b) != len(want) {
		t.Errorf("bucket = %v, want only %v", b, want)
	}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("bucket %s = %v, want %v", k, b[k], v)
		}
	}
}

// TestSync_CAP036_AC6_FullHasAllFields: --full writes every field, defaults included.
func TestSync_CAP036_AC6_FullHasAllFields(t *testing.T) {
	s := newScenario(t)
	_, cid := seedHealthy(s)
	s.Fake.SeedBucket(cid, "plain", capellafake.WithField("memoryAllocationInMb", 512))
	syncOK(t, s, "--full")
	cl := docCluster(t, s, "", "nova-dev-01")
	b := asMaps(cl["buckets"])[0]
	for _, f := range []string{"type", "replicas", "durabilityLevel", "timeToLiveInSeconds", "flushEnabled", "priority", "bucketConflictResolution"} {
		if _, ok := b[f]; !ok {
			t.Errorf("--full bucket has no %s: %v", f, b)
		}
	}
	if v, ok := cl["deletionProtection"]; !ok || v != false {
		t.Errorf("--full deletionProtection = %v (present %v), want false", v, ok)
	}
	if _, ok := cl["configurationType"]; !ok {
		t.Errorf("--full cluster has no configurationType: %v", cl)
	}
}

// TestSync_CAP036_AC7_ScheduleCompacted: a regular schedule is written as weekdays/weekend.
func TestSync_CAP036_AC7_ScheduleCompacted(t *testing.T) {
	s := newScenario(t)
	richSeed(t, s)
	syncOK(t, s)
	// cbctl's YAML writer double-quotes off where capellactl single-quoted it; both read as "off".
	wantContains(t, s.ReadFile("capella.yaml"), "weekdays: 08:00-18:00", `weekend: "off"`)
}

// TestSync_CAP036_AC7_ScheduleIrregularPerDay: the most common weekday value becomes weekdays, the
// other days are overrides, and a paused schedule records paused: true.
func TestSync_CAP036_AC7_ScheduleIrregularPerDay(t *testing.T) {
	s := newScenario(t)
	_, cid := seedHealthy(s)
	days := []any{capellafake.Obj{"day": "monday", "state": "on"},
		capellafake.Obj{"day": "tuesday", "state": "custom", "from": capellafake.Obj{"hour": 9, "minute": 30}, "to": capellafake.Obj{"hour": 0, "minute": 0}}}
	for _, d := range []string{"wednesday", "thursday", "friday", "saturday", "sunday"} {
		days = append(days, capellafake.Obj{"day": d, "state": "off"})
	}
	s.Fake.SeedOnOffSchedule(cid, capellafake.Obj{"timezone": "Europe/London", "days": days}, true)
	syncOK(t, s)
	h, _ := docCluster(t, s, "", "nova-dev-01")["hibernation"].(map[string]any)
	if h["weekdays"] != "off" || h["monday"] != "on" || h["tuesday"] != "09:30-24:00" || h["weekend"] != "off" || h["paused"] != true {
		t.Errorf("hibernation = %v", h)
	}
	if _, ok := h["friday"]; ok {
		t.Errorf("friday written although it equals weekdays: %v", h)
	}
	planClean(t, s)
}

// TestSync_CAP036_AC9_TurnedOffCluster: a turned-off cluster's buckets and credentials are not
// asked for (no retries); the file notes it and still has the schedule and CIDRs.
func TestSync_CAP036_AC9_TurnedOffCluster(t *testing.T) {
	s := newScenario(t)
	_, cid := richSeed(t, s)
	s.Fake.SetClusterState(cid, "turnedOff")
	s.Fake.FailNext("GET", `/buckets$`, 500, 6) // what Capella answers while the cluster is off
	r := syncOK(t, s)
	wantContains(t, r.Stderr, "[warning] - cluster nova-dev-01: buckets and database credentials not imported (turnedOff)",
		"→ Turn it on")
	wantNotContains(t, r.Stdout+r.Stderr, "retrying")
	if n := s.Fake.Count("GET", `/clusters/`+cid+`/(buckets|users)$`); n != 0 {
		t.Errorf("%d bucket/credential reads of a turned-off cluster, want 0", n)
	}
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "# not imported: buckets and database credentials (the cluster is turnedOff;")
	cl := docCluster(t, s, "", "nova-dev-01")
	for _, k := range []string{"hibernation", "allowedCidrs"} {
		if _, ok := cl[k]; !ok {
			t.Errorf("%s missing from a turned-off cluster", k)
		}
	}
	for _, k := range []string{"buckets", "databaseCredentials"} {
		if _, ok := cl[k]; ok {
			t.Errorf("%s written for a turned-off cluster", k)
		}
	}
}

// TestSync_CAP036_EmptyProjectPointsToClusterCreate: an empty project writes clusters: [] with the
// cluster-create template and the next step.
func TestSync_CAP036_EmptyProjectPointsToClusterCreate(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("alpha")
	r := syncOK(t, s)
	if c := docProjects(t, s)[0]["clusters"]; c == nil || len(c.([]any)) != 0 {
		t.Errorf("clusters = %v, want []", c)
	}
	wantContains(t, r.Stdout, "Next: cbctl capella cluster create NAME")
	wantContains(t, s.ReadFile("capella.yaml"), "To create one, run: cbctl capella cluster create NAME")
}
