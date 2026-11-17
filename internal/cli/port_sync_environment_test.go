package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
)

// Ports of capellactl's tests/scenario/test_environment.py (CAP-030) and the CAP-048 AC4 case of
// tests/scenario/test_track_by_id.py.

// exampleFile is capellactl's reference environment (docs/release-1.0.0/examples/capella.yaml).
const exampleFile = "../config/capellafile/testdata/capellactl/example-capella.yaml"

var writeKinds = []struct {
	method string
	rx     *regexp.Regexp
	kind   string
}{
	{"POST", regexp.MustCompile(`/projects$`), "project"},
	{"POST", regexp.MustCompile(`/clusters$`), "cluster"},
	{"PUT", regexp.MustCompile(`/deletionProtection$`), "deletion-protection"},
	{"POST", regexp.MustCompile(`/onOffSchedule$`), "schedule"},
	{"POST", regexp.MustCompile(`/allowedcidrs$`), "cidr"},
	{"POST", regexp.MustCompile(`/buckets$`), "bucket"},
	{"POST", regexp.MustCompile(`/scopes$`), "scope"},
	{"POST", regexp.MustCompile(`/collections$`), "collection"},
	{"POST", regexp.MustCompile(`/users$`), "credential"},
	{"DELETE", regexp.MustCompile(`/users/[^/]+$`), "credential"},
	{"DELETE", regexp.MustCompile(`/collections/[^/]+$`), "collection"},
	{"DELETE", regexp.MustCompile(`/scopes/[^/]+$`), "scope"},
	{"DELETE", regexp.MustCompile(`/buckets/[^/]+$`), "bucket"},
	{"DELETE", regexp.MustCompile(`/allowedcidrs/[^/]+$`), "cidr"},
	{"DELETE", regexp.MustCompile(`/onOffSchedule$`), "schedule"},
	{"DELETE", regexp.MustCompile(`/clusters/[^/]+$`), "cluster"},
	{"DELETE", regexp.MustCompile(`/projects/[^/]+$`), "project"},
}

// kindRuns names the kind of each write and folds repeats: [project cluster cluster] → [project cluster].
func kindRuns(calls []capellafake.Call) []string {
	var out []string
	for _, c := range calls {
		for _, k := range writeKinds {
			if c.Method == k.method && k.rx.MatchString(c.Path) {
				if len(out) == 0 || out[len(out)-1] != k.kind {
					out = append(out, k.kind)
				}
				break
			}
		}
	}
	return out
}

// builtExample applies capellactl's reference environment to an empty fake.
func builtExample(t *testing.T) (*clitest.Scenario, clitest.Result) {
	t.Helper()
	s := newScenario(t)
	b, err := os.ReadFile(exampleFile)
	if err != nil {
		t.Fatal(err)
	}
	writeCapellaYAML(t, s, string(b))
	s.WriteFile(filepath.Join("secrets", "reporting_ro.txt"), "R3port!ng-pw")
	s.Env["ORDERS_APP_PASSWORD"] = "Orders#App-123"
	r := mustScenario(t, s.Run("capella", "apply", "-y"), "apply the example")
	return s, r
}

func unprotectAll(t *testing.T, s *clitest.Scenario) {
	t.Helper()
	ps, err := s.Fake.Client().ListProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		cs, err := s.Fake.Client().ListClusters(t.Context(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			s.Fake.SetClusterField(c.ID, "deletionProtection", false)
		}
	}
}

// TestEnvironment_CAP030_AC1_FullBuildOrder: apply builds parents before children, kind by kind.
func TestEnvironment_CAP030_AC1_FullBuildOrder(t *testing.T) {
	s, _ := builtExample(t)
	want := "project,cluster,deletion-protection,schedule,cidr,bucket,scope,collection,credential"
	if got := strings.Join(kindRuns(s.Fake.Writes()), ","); got != want {
		t.Errorf("write order = %s\nwant          %s", got, want)
	}
}

// TestEnvironment_CAP030_AC1_SummaryTable: the summary has a create/done row per kind and the
// cluster's connection string.
func TestEnvironment_CAP030_AC1_SummaryTable(t *testing.T) {
	_, r := builtExample(t)
	out := r.Stdout + r.Stderr
	for _, row := range [][2]string{{"project", "nova-dev"}, {"cluster", "nova-dev-01"}, {"schedule", "nova-dev-01"},
		{"bucket", "nova-dev-01/orders"}, {"collection", "nova-dev-01/orders/inventory/items"},
		{"credential", "nova-dev-01/orders_app"}} {
		rx := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(row[0]) + `\s+` + regexp.QuoteMeta(row[1]) + `\s+create\s+done\b`)
		if !rx.MatchString(out) {
			t.Errorf("no %s %s create done row:\n%s", row[0], row[1], out)
		}
	}
	// Capella v4 (and cbctl's fake) return the hostname without capellactl's fake couchbases:// scheme.
	wantContains(t, out, "connectionString (nova-dev-01): cb.")
}

// TestEnvironment_CAP030_AC4_DestroyReverseOrder: destroy deletes children before parents, drops
// the deleted ids from the file, and the next plan creates everything again.
func TestEnvironment_CAP030_AC4_DestroyReverseOrder(t *testing.T) {
	s, _ := builtExample(t)
	unprotectAll(t, s)
	s.Fake.ResetCalls()
	s.TTY(true)
	s.Answers("nova-dev")
	r := mustScenario(t, s.Run("capella", "destroy"), "destroy")
	want := "credential,collection,scope,bucket,cidr,schedule,cluster,project"
	if got := strings.Join(kindRuns(s.Fake.Writes()), ","); got != want {
		t.Errorf("delete order = %s\nwant           %s", got, want)
	}
	wantContains(t, r.Stdout+r.Stderr, "Destroy complete")
	if ps, _ := s.Fake.Client().ListProjects(t.Context()); len(ps) != 0 {
		t.Errorf("projects left: %v", ps)
	}
	// The ids of what was deleted are dropped (the example's "# id:" comment stays).
	if regexp.MustCompile(`(?m)^\s*-?\s*id:`).MatchString(s.ReadFile("capella.yaml")) {
		t.Errorf("ids left after destroy:\n%s", s.ReadFile("capella.yaml"))
	}
	s.TTY(false)
	r = mustScenario(t, s.Run("capella", "plan"), "plan after destroy")
	wantContains(t, r.Stdout, "+ project nova-dev", "+ cluster nova-dev-01")
}

// TestEnvironment_CAP030_AC6_DestroyKeepsProjectWithUndeclared: a project that also holds a
// cluster the file does not declare is kept, and that cluster is not touched.
func TestEnvironment_CAP030_AC6_DestroyKeepsProjectWithUndeclared(t *testing.T) {
	s, _ := builtExample(t)
	unprotectAll(t, s)
	pid := projectOf(t, s)
	other := s.Fake.SeedCluster(pid, "someone-else", "healthy")
	r := mustScenario(t, s.Run("capella", "destroy", "-y"), "destroy -y")
	wantContains(t, r.Stdout+r.Stderr, "project nova-dev kept: contains undeclared cluster someone-else")
	cs, err := s.Fake.Client().ListClusters(t.Context(), pid)
	if err != nil {
		t.Fatalf("project nova-dev was deleted: %v", err)
	}
	if len(cs) != 1 || cs[0].ID != other {
		t.Errorf("clusters left = %v, want only someone-else", cs)
	}
}

// TestTrackByID_CAP048_AC4_UnpinnedRenameBlocksCreate: a file without ids whose project was
// renamed in Capella is blocked (pointing at sync) instead of creating a second project.
func TestTrackByID_CAP048_AC4_UnpinnedRenameBlocksCreate(t *testing.T) {
	s := newScenario(t)
	pid, _ := seedHealthy(s)
	writeCapellaYAML(t, s, singleProjectYAML)
	s.Fake.RenameProject(pid, "nova-dev-renamed")
	r := s.Run("capella", "plan")
	wantContains(t, r.Stdout+r.Stderr, "project nova-dev-renamed has its cluster nova-dev-01", "cbctl capella sync",
		"0 to add", "1 blocked")
	wantNotContains(t, r.Stdout, "+ project nova-dev", "+ cluster")
	s.Fake.ResetCalls()
	wantExit(t, s.Run("capella", "apply", "-y"), 4)
	clitest.AssertNoWrites(t, s.Fake)
}
