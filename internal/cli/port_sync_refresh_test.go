package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// Ports of capellactl's tests/scenario/test_sync.py (CAP-038).

// syncedHealthy seeds nova-dev/nova-dev-01 and syncs it into capella.yaml (capellactl's
// "imported" fixture).
func syncedHealthy(t *testing.T) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s = newScenario(t)
	pid, cid = seedHealthy(s)
	syncOK(t, s)
	return s, pid, cid
}

// editDoc loads capella.yaml as plain YAML, lets edit change it and writes it back.
func editDoc(t *testing.T, s *clitest.Scenario, edit func(doc map[string]any)) {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s.ReadFile("capella.yaml")), &doc); err != nil {
		t.Fatalf("capella.yaml is not YAML: %v", err)
	}
	edit(doc)
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal capella.yaml: %v", err)
	}
	writeCapellaYAML(t, s, string(out))
}

// firstCluster returns the first cluster mapping of a projects: document.
func firstCluster(doc map[string]any) map[string]any {
	return asMaps(asMaps(doc["projects"])[0]["clusters"])[0]
}

// seedBucketAndCredential mirrors capellactl's _seed_bucket_and_credential.
func seedBucketAndCredential(s *clitest.Scenario, cid string) {
	s.Fake.SeedBucket(cid, "orders")
	seedReader(s, cid, "app", "orders")
}

// TestSync_CAP038_AC9_RefreshesWithDiff: sync prints the -/+ diff of what changed in Capella,
// rewrites the file and keeps a backup.
func TestSync_CAP038_AC9_RefreshesWithDiff(t *testing.T) {
	s, pid, cid := syncedHealthy(t)
	s.Fake.SetClusterField(cid, "description", "changed in the Capella UI")
	s.Fake.SeedCluster(pid, "made-in-ui", "healthy")
	r := syncOK(t, s)
	wantContains(t, r.Stdout, "-        description: Nova development cluster",
		"+        description: changed in the Capella UI", "+        name: made-in-ui")
	wantNames(t, "clusters", docClusters(t, s, ""), "nova-dev-01", "made-in-ui")
	s.ReadFile("capella.yaml.bak")
}

// TestSync_CAP038_AC9_KeepsLocalOnlyValues: organizationId references, credentialsOutput,
// allowAnyIp and password references survive a sync, with a note.
func TestSync_CAP038_AC9_KeepsLocalOnlyValues(t *testing.T) {
	s, _, cid := syncedHealthy(t)
	seedBucketAndCredential(s, cid)
	syncOK(t, s)
	editDoc(t, s, func(doc map[string]any) {
		doc["organizationId"] = "${CAPELLA_ORG_ID}"
		doc["credentialsOutput"] = "./secrets/creds"
		cl := firstCluster(doc)
		cl["allowAnyIp"] = true
		asMaps(cl["databaseCredentials"])[0]["password"] = "${APP_PASSWORD}"
	})
	s.Fake.SetClusterField(cid, "description", "changed")
	r := syncOK(t, s)
	wantContains(t, r.Stdout+r.Stderr, "kept password reference for nova-dev-01/app")
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "password reference kept from the previous capella.yaml")
	doc := fileDoc(t, s)
	if doc["organizationId"] != "${CAPELLA_ORG_ID}" || doc["credentialsOutput"] != "./secrets/creds" {
		t.Errorf("organizationId = %v, credentialsOutput = %v", doc["organizationId"], doc["credentialsOutput"])
	}
	cl := docCluster(t, s, "", "nova-dev-01")
	if cl["allowAnyIp"] != true || cl["description"] != "changed" {
		t.Errorf("allowAnyIp = %v, description = %v", cl["allowAnyIp"], cl["description"])
	}
	if pw := asMaps(cl["databaseCredentials"])[0]["password"]; pw != "${APP_PASSWORD}" {
		t.Errorf("password = %v, want ${APP_PASSWORD}", pw)
	}
}

// TestSync_CAP038_ClusterFilterKeepsOthers: sync --cluster refreshes only that cluster.
func TestSync_CAP038_ClusterFilterKeepsOthers(t *testing.T) {
	s, pid, cid := syncedHealthy(t)
	other := s.Fake.SeedCluster(pid, "other", "healthy")
	syncOK(t, s)
	s.Fake.SetClusterField(other, "description", "changed")
	s.Fake.SetClusterField(cid, "description", "first changed")
	syncOK(t, s, "--cluster", "other")
	if d := docCluster(t, s, "", "other")["description"]; d != "changed" {
		t.Errorf("other description = %v, want changed", d)
	}
	if d := docCluster(t, s, "", "nova-dev-01")["description"]; d != "Nova development cluster" {
		t.Errorf("nova-dev-01 description = %v, want the old value", d)
	}
}

// TestSync_CAP038_AC13_LastProjectDeletedLeavesValidFile: when every project is gone the file
// keeps projects: [] with the project-create template, validates and is then in sync.
func TestSync_CAP038_AC13_LastProjectDeletedLeavesValidFile(t *testing.T) {
	s, pid, _ := syncedHealthy(t)
	s.Fake.RemoveProject(pid)
	syncOK(t, s)
	text := s.ReadFile("capella.yaml")
	if p, ok := fileDoc(t, s)["projects"].([]any); !ok || len(p) != 0 {
		t.Errorf("projects = %v, want []:\n%s", fileDoc(t, s)["projects"], text)
	}
	wantContains(t, text, "cbctl capella project create NAME")
	mustScenario(t, s.Run("capella", "validate"), "validate")
	wantContains(t, syncOK(t, s).Stdout, "already in sync")
}

// TestSync_CAP038_AC9_WholeBucketWildcardNotWritten: Capella returns a whole-bucket grant as
// scopes [{name: '*'}]; sync must not write it, and a later managed edit plans clean.
func TestSync_CAP038_AC9_WholeBucketWildcardNotWritten(t *testing.T) {
	s, _, cid := syncedHealthy(t)
	s.Fake.SeedBucket(cid, "demo", capellafake.WithField("memoryAllocationInMb", 512))
	syncOK(t, s)
	s.Env["RO_PW"] = "Demo-R3ad-2026!"
	s.Env["AIRLINE_PW"] = "Demo-A1rline-2026!"
	mustScenario(t, s.Run("capella", "scope", "add", "nova-dev-01", "demo", "inventory", "-y"), "scope add")
	mustScenario(t, s.Run("capella", "collection", "add", "nova-dev-01", "demo", "inventory", "airline", "-y"), "collection add")
	// Capella stores the whole-bucket grant with a '*' scope.
	s.Fake.SeedCredential(cid, "demo_ro", []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "demo",
			Scopes: []capellaapi.ResourceScope{{Name: "*"}}}}},
	}}, false)
	s.Fake.ResetCalls()
	syncOK(t, s)
	text := s.ReadFile("capella.yaml")
	if strings.Contains(text, "'*'") || strings.Contains(text, `"*"`) {
		t.Fatalf("wildcard scope written:\n%s", text)
	}
	validates(t, s)
	mustScenario(t, s.Run("capella", "credential", "create", "nova-dev-01", "airline_app", "--privileges", "read+write",
		"--bucket", "demo", "--scope", "inventory", "--collection", "airline", "--password-env", "AIRLINE_PW", "-y"), "credential create")
	planClean(t, s)
}

// TestSync_CAP038_AC14_TurnedOffClusterKeepsBucketsAndCredentials: while the cluster is off its
// buckets and credentials (with the password reference) are kept; once on, apply --prune deletes
// nothing and the reference survives.
func TestSync_CAP038_AC14_TurnedOffClusterKeepsBucketsAndCredentials(t *testing.T) {
	s, _, cid := syncedHealthy(t)
	seedBucketAndCredential(s, cid)
	syncOK(t, s)
	editDoc(t, s, func(doc map[string]any) {
		asMaps(firstCluster(doc)["databaseCredentials"])[0]["password"] = "${APP_PASSWORD}"
	})
	before := docCluster(t, s, "", "nova-dev-01")

	s.Fake.SetClusterState(cid, "turnedOff")
	r := syncOK(t, s)
	out := r.Stdout + r.Stderr
	wantContains(t, out, "already in sync",
		"buckets, scopes and collections, database credentials kept from capella.yaml (turnedOff)")
	wantNotContains(t, out, "not imported")
	after := docCluster(t, s, "", "nova-dev-01")
	if !sameYAML(t, after["buckets"], before["buckets"]) || !sameYAML(t, after["databaseCredentials"], before["databaseCredentials"]) {
		t.Errorf("children changed while off:\nbefore %v\nafter  %v", before, after)
	}
	mustScenario(t, s.Run("capella", "sync", "--dry-run"), "dry-run while off")

	s.Fake.SetClusterState(cid, "healthy")
	s.Env["APP_PASSWORD"] = "Demo-App-2026!"
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "apply", "--prune", "-y"), "apply --prune")
	if n := s.Fake.Count("DELETE", `/(buckets|users)/`); n != 0 {
		t.Errorf("%d bucket/credential deletes after the cluster came back", n)
	}
	syncOK(t, s)
	if pw := asMaps(docCluster(t, s, "", "nova-dev-01")["databaseCredentials"])[0]["password"]; pw != "${APP_PASSWORD}" {
		t.Errorf("password = %v, want ${APP_PASSWORD}", pw)
	}
	wantNotContains(t, s.ReadFile("capella.yaml"), "kept from the previous")
}

// TestSync_CAP038_AC14_FirstSyncOfTurnedOffClusterStillNotesIt: a cluster recorded without
// children that is off says "not imported", never "kept from".
func TestSync_CAP038_AC14_FirstSyncOfTurnedOffClusterStillNotesIt(t *testing.T) {
	s, _, cid := syncedHealthy(t)
	s.Fake.SetClusterState(cid, "turnedOff")
	r := syncOK(t, s)
	wantContains(t, r.Stdout+r.Stderr, "buckets and database credentials not imported (turnedOff)")
	wantNotContains(t, r.Stdout+r.Stderr, "kept from")
}

func sameYAML(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := yaml.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := yaml.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}
