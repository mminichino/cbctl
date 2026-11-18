package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"go.yaml.in/yaml/v3"
)

// richPassword is a credential password that must never reach the file or the output.
const richPassword = "Existing!Secret-77"

func weekdaySchedule() capellafake.Obj {
	days := []any{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday"} {
		days = append(days, capellafake.Obj{"day": d, "state": "custom",
			"from": capellafake.Obj{"hour": 8, "minute": 0}, "to": capellafake.Obj{"hour": 18, "minute": 0}})
	}
	days = append(days, capellafake.Obj{"day": "saturday", "state": "off"}, capellafake.Obj{"day": "sunday", "state": "off"})
	return capellafake.Obj{"timezone": "US/Pacific", "days": days}
}

// seedHealthy mirrors capellactl's seed_healthy: project nova-dev with cluster nova-dev-01.
func seedHealthy(s *clitest.Scenario, fields ...capellafake.Field) (pid, cid string) {
	pid = s.Fake.SeedProject("nova-dev", capellafake.WithField("description", "Nova development environment"))
	cid = s.Fake.SeedCluster(pid, "nova-dev-01", "healthy",
		append([]capellafake.Field{capellafake.WithField("description", "Nova development cluster")}, fields...)...)
	return pid, cid
}

// richSeed mirrors capellactl's _rich fixture: a protected cluster with an on/off schedule, an
// active and an expired CIDR, two buckets with scopes and collections, two basic credentials
// and one advanced credential.
func richSeed(t *testing.T, s *clitest.Scenario) (pid, cid string) {
	t.Helper()
	pid, cid = seedHealthy(s, capellafake.WithField("deletionProtection", true))
	s.Fake.SeedOnOffSchedule(cid, weekdaySchedule(), false)
	s.Fake.SeedAllowedCIDR(cid, "203.0.113.10/32", "Office VPN", "active")
	s.Fake.SeedAllowedCIDR(cid, "198.51.100.0/24", "", "expired")
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 1024),
		capellafake.WithField("storageBackend", "magma"), capellafake.WithField("durabilityLevel", "majority"))
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"items": 0, "suppliers": -1})
	s.Fake.SeedScope(cid, "orders", "billing", map[string]int{"invoices": 0})
	s.Fake.SeedBucket(cid, "sessions", capellafake.WithField("type", "ephemeral"),
		capellafake.WithField("evictionPolicy", "nruEviction"), capellafake.WithField("memoryAllocationInMb", 256),
		capellafake.WithField("storageBackend", ""))
	s.Fake.SeedCredential(cid, "orders_app", []capellaapi.AccessEntry{{
		Privileges: []string{"data_writer", "data_reader"},
		Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders",
			Scopes: []capellaapi.ResourceScope{{Name: "inventory", Collections: []string{"items", "suppliers"}}}}}},
	}}, false)
	seedReader(s, cid, "reporting_ro", "orders")
	s.Fake.SeedAdvancedCredential(cid, "dba", []string{"admin_role"})
	return pid, cid
}

// seedReader adds a basic credential with read access to a whole bucket.
func seedReader(s *clitest.Scenario, cid, name, bucket string) {
	s.Fake.SeedCredential(cid, name, []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources:  capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: bucket}}},
	}}, false)
}

// fileDoc parses capella.yaml as plain YAML, in the projects: form.
func fileDoc(t *testing.T, s *clitest.Scenario) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s.ReadFile("capella.yaml")), &doc); err != nil {
		t.Fatalf("capella.yaml is not YAML: %v", err)
	}
	if _, ok := doc["projects"]; !ok {
		proj, _ := doc["project"].(map[string]any)
		if proj == nil {
			proj = map[string]any{}
		}
		proj["clusters"] = doc["clusters"]
		doc["projects"] = []any{proj}
	}
	return doc
}

func asMaps(v any) []map[string]any {
	l, _ := v.([]any)
	out := make([]map[string]any, 0, len(l))
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// docProjects returns the project mappings of capella.yaml.
func docProjects(t *testing.T, s *clitest.Scenario) []map[string]any {
	t.Helper()
	return asMaps(fileDoc(t, s)["projects"])
}

// docClusters returns the clusters of capella.yaml (of project, when not empty).
func docClusters(t *testing.T, s *clitest.Scenario, project string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, p := range docProjects(t, s) {
		if project == "" || p["name"] == project {
			out = append(out, asMaps(p["clusters"])...)
		}
	}
	return out
}

// docCluster returns the cluster named name, failing the test when it is not in the file.
func docCluster(t *testing.T, s *clitest.Scenario, project, name string) map[string]any {
	t.Helper()
	for _, c := range docClusters(t, s, project) {
		if c["name"] == name {
			return c
		}
	}
	t.Fatalf("cluster %s not in capella.yaml:\n%s", name, s.ReadFile("capella.yaml"))
	return nil
}

func names(items []map[string]any) []string {
	out := make([]string, len(items))
	for i, m := range items {
		out[i], _ = m["name"].(string)
	}
	return out
}

func wantNames(t *testing.T, what string, items []map[string]any, want ...string) {
	t.Helper()
	if got := strings.Join(names(items), ","); got != strings.Join(want, ",") {
		t.Errorf("%s = %s, want %s", what, got, strings.Join(want, ","))
	}
}

func wantNotContains(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(s, p) {
			t.Errorf("output contains %q:\n%s", p, s)
		}
	}
}

// validates checks that capella.yaml loads and passes validate.
func validates(t *testing.T, s *clitest.Scenario) {
	t.Helper()
	if _, err := capellafile.LoadText(s.ReadFile("capella.yaml"), "capella.yaml", s.Dir, func(string) string { return "" }); err != nil {
		t.Fatalf("capella.yaml does not load: %v\n%s", err, s.ReadFile("capella.yaml"))
	}
	mustScenario(t, s.Run("capella", "validate"), "validate")
}

// planClean runs plan --detailed-exitcode and requires no changes and no writes.
func planClean(t *testing.T, s *clitest.Scenario) clitest.Result {
	t.Helper()
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "plan", "--detailed-exitcode"), "plan --detailed-exitcode")
	clitest.AssertNoWrites(t, s.Fake)
	return r
}
