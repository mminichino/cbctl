package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// uxFullDoc declares every kind apply orders: allowed CIDR, buckets, scopes, collections and a
// credential.
const uxFullDoc = scenarioDoc + `    allowedCidrs:
      - cidr: 10.1.0.0/16
    buckets:
      - name: events
        memoryAllocationInMb: 512
        scopes:
          - name: inventory
      - name: orders
        memoryAllocationInMb: 1024
        storageBackend: magma
        scopes:
          - name: billing
            collections:
              - name: items
                maxTTL: -1
    databaseCredentials:
      - name: app
        password: ${APP_PW}
        access:
          - privileges: [read]
            resources:
              buckets:
                - name: orders
`

const uxPassword = "Canary-Pw-0123456789!"

// uxApplied applies scenarioDoc+extra to an empty fake and returns the scenario and the file as
// apply pinned it (with ids).
func uxApplied(t *testing.T, extra string) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioDoc+extra)
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	s.Fake.ResetCalls()
	return s, s.ReadFile("capella.yaml")
}

// uxClusterID returns the id of the only cluster of the only project.
func uxClusterID(t *testing.T, s *clitest.Scenario) string {
	t.Helper()
	ps, err := s.Fake.Client().ListProjects(t.Context())
	if err != nil || len(ps) == 0 {
		t.Fatalf("projects: %v %v", ps, err)
	}
	cs, err := s.Fake.Client().ListClusters(t.Context(), ps[0].ID)
	if err != nil || len(cs) == 0 {
		t.Fatalf("clusters: %v %v", cs, err)
	}
	return cs[0].ID
}

// lastSegment is the last path element of each write, e.g. "buckets".
func uxWriteKinds(s *clitest.Scenario, method string) []string {
	var out []string
	for _, c := range s.Fake.Writes() {
		if method == "" || c.Method == method {
			out = append(out, c.Path[strings.LastIndex(c.Path, "/")+1:])
		}
	}
	return out
}

// TestApply_CAP006_AC3_WriteOrder: a full file is created parents first: project, cluster,
// allowed CIDRs, buckets, scopes, collections, and credentials last.
func TestApply_CAP006_AC3_WriteOrder(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	s.Env["APP_PW"] = uxPassword
	writeCapellaYAML(t, s, uxFullDoc)
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	got := strings.Join(uxWriteKinds(s, "POST"), ",")
	want := "projects,clusters,allowedcidrs,buckets,buckets,scopes,scopes,collections,users"
	if got != want {
		t.Fatalf("POST order = %s, want %s", got, want)
	}
}

func uxRegionChange(t *testing.T) *clitest.Scenario {
	t.Helper()
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, strings.Replace(base, "us-east-1", "us-west-2", 1)+
		"    buckets:\n      - name: orders\n        memoryAllocationInMb: 1024\n        scopes:\n"+
		"          - name: inv\n            collections:\n              - name: items\n")
	return s
}

// TestApply_CAP006_AC4_BlockedPreventsAnyWrite: a blocked change (region) refuses the whole
// apply with exit 4 and makes no write, not even the unrelated bucket.
func TestApply_CAP006_AC4_BlockedPreventsAnyWrite(t *testing.T) {
	s := uxRegionChange(t)
	r := s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "! cluster envclu  immutable: cloudProvider.region (requires a new cluster)")
	wantContains(t, r.Stderr, "[error] - Apply refused: 1 blocked change(s); nothing was changed\n  -> Resolve each change marked !")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestApply_CAP006_AC4_TargetExcludesBlocked: --target applies only the bucket subtree and skips
// the blocked cluster.
func TestApply_CAP006_AC4_TargetExcludesBlocked(t *testing.T) {
	s := uxRegionChange(t)
	mustScenario(t, s.Run("capella", "apply", "--yes", "--target", "bucket/envclu/orders"))
	if got := strings.Join(uxWriteKinds(s, ""), ","); got != "buckets,scopes,collections" {
		t.Fatalf("writes = %s", got)
	}
}

func uxScaleOut(t *testing.T, nodes string) *clitest.Scenario {
	t.Helper()
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, strings.Replace(base, "numOfNodes: 3", "numOfNodes: "+nodes, 1))
	return s
}

const uxClusterPUT = "/v4/organizations/*/projects/*/clusters/*"

// TestApply_CAP006_AC6_412ReplansOnce: a 412 on the cluster PUT is re-read and retried once.
func TestApply_CAP006_AC6_412ReplansOnce(t *testing.T) {
	s := uxScaleOut(t, "4")
	s.Fake.FailNextOpts("PUT", uxClusterPUT, capellafake.FaultOptions{Status: 412, Message: "etag mismatch"})
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	if n := len(s.Fake.Calls("PUT", uxClusterPUT)); n != 2 {
		t.Fatalf("PUT cluster sent %d times, want 2", n)
	}
}

// TestApply_CAP006_AC6_Double412Fails: a second 412 is a concurrent change: exit 1, re-run plan.
func TestApply_CAP006_AC6_Double412Fails(t *testing.T) {
	s := uxScaleOut(t, "4")
	s.Fake.FailNextOpts("PUT", uxClusterPUT, capellafake.FaultOptions{Status: 412, Message: "etag mismatch", Times: 2})
	r := s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "cluster envclu changed concurrently; re-run plan")
}

const uxBucketsPOST = "/v4/organizations/*/projects/*/clusters/*/buckets"

// TestApply_CAP006_AC6_CreateConflictAdoptsExisting: a 409 because someone created the bucket
// first is adopted: "already exists; using it", exit 0.
func TestApply_CAP006_AC6_CreateConflictAdoptsExisting(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+"    buckets:\n      - name: orders\n        memoryAllocationInMb: 1024\n")
	s.Fake.FailNextOpts("POST", uxBucketsPOST, capellafake.FaultOptions{Status: 409, Code: 6001,
		Message: "bucket already exists", ApplyThenFail: true})
	r := mustScenario(t, s.Run("capella", "apply", "--yes"))
	wantContains(t, r.Stderr, "bucket envclu/orders already exists; using it")
}

// TestApply_CAP006_AC6_AmbiguousPostAdoptsCreated: a POST whose response is lost but which
// landed is adopted after one POST.
func TestApply_CAP006_AC6_AmbiguousPostAdoptsCreated(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+"    buckets:\n      - name: orders\n        memoryAllocationInMb: 1024\n")
	s.Fake.FailNextOpts("POST", uxBucketsPOST, capellafake.FaultOptions{ApplyThenDrop: true})
	r := mustScenario(t, s.Run("capella", "apply", "--yes"))
	wantContains(t, r.Stderr, "was created despite the lost response; using it")
	if n := len(s.Fake.Calls("POST", uxBucketsPOST)); n != 1 {
		t.Fatalf("POST buckets sent %d times, want 1", n)
	}
}

// TestApply_CAP006_AC7_JSONSchema: apply -o json reports status ok, a done status per action and
// the new cluster's connection string.
func TestApply_CAP006_AC7_JSONSchema(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioDoc)
	r := mustScenario(t, s.Run("capella", "-o", "json", "--yes", "apply"))
	doc, _ := r.JSON(t).(map[string]any)
	res, _ := doc["result"].(map[string]any)
	if res["status"] != "ok" {
		t.Fatalf("result = %v", doc)
	}
	actions, _ := res["actions"].([]any)
	if len(actions) == 0 {
		t.Fatalf("no actions: %v", res)
	}
	for _, a := range actions {
		if st := a.(map[string]any)["status"]; st != "done" {
			t.Errorf("action %v status %v", a, st)
		}
	}
	cs, _ := res["connectionStrings"].([]any)
	if len(cs) != 1 || !strings.HasPrefix(cs[0].(string), "connectionString (envclu): ") {
		t.Errorf("connectionStrings = %v", res["connectionStrings"])
	}
}

// uxPartial is a healthy cluster with bucket events/inventory; the file scales out to 5 and adds
// bucket orders.
func uxPartial(t *testing.T) *clitest.Scenario {
	t.Helper()
	s, base := uxApplied(t, "")
	id := uxClusterID(t, s)
	s.Fake.SeedBucket(id, "events", capellafake.WithField("memoryAllocationInMb", 512))
	s.Fake.SeedScope(id, "events", "inventory", nil)
	s.Fake.ResetCalls()
	writeCapellaYAML(t, s, strings.Replace(base, "numOfNodes: 3", "numOfNodes: 5", 1)+
		"    buckets:\n      - name: events\n        memoryAllocationInMb: 512\n        scopes:\n          - name: inventory\n"+
		"      - name: orders\n        memoryAllocationInMb: 1024\n")
	return s
}

// TestPlan_CAP005_AC1_SymbolsAndSummary: the real scale-out diff line, a + bucket summary, an =
// scope and the summary line, from a seeded Capella.
func TestPlan_CAP005_AC1_SymbolsAndSummary(t *testing.T) {
	s := uxPartial(t)
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout,
		"  ~ cluster envclu   scale out\n      serviceGroups[data,index,query].numOfNodes:  3 -> 5\n",
		"  + bucket envclu/orders  couchbase - 1024 MB - 1 replica - durability none\n",
		"  = scope envclu/events/inventory\n",
		"Plan: 1 to add, 1 to change, 0 to delete, 0 blocked.")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestPlanner_CAP005_AC7_TargetFilter: plan --target bucket/C/B shows only that bucket.
func TestPlanner_CAP005_AC7_TargetFilter(t *testing.T) {
	s := uxPartial(t)
	r := mustScenario(t, s.Run("capella", "plan", "--target", "bucket/envclu/orders"))
	var body []string
	for _, l := range strings.Split(r.Stdout, "\n") {
		if strings.HasPrefix(l, "  ") {
			body = append(body, l)
		}
	}
	if len(body) != 1 || body[0] != "  + bucket envclu/orders  couchbase - 1024 MB - 1 replica - durability none" {
		t.Fatalf("plan lines = %q", body)
	}
}

// TestPlan_CAP005_AC8_DetailedExitcodeBlocked: 0 when clean, 2 with changes, 4 with a blocked
// change.
func TestPlan_CAP005_AC8_DetailedExitcodeBlocked(t *testing.T) {
	s, base := uxApplied(t, "")
	for _, tc := range []struct {
		name, doc string
		want      cli.ExitCode
	}{
		{"clean", base, cli.ExitOK},
		{"changes", strings.Replace(base, "numOfNodes: 3", "numOfNodes: 4", 1), cli.ExitChanges},
		{"blocked", strings.Replace(base, "us-east-1", "eu-west-1", 1), cli.ExitBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeCapellaYAML(t, s, tc.doc)
			wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), tc.want)
		})
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestPlanner_CAP005_AC6_ChildrenOfNewCluster: children of a cluster still to be created are
// planned with "(after ... is ...)" notes and no cluster-scoped API call is made.
func TestPlanner_CAP005_AC6_ChildrenOfNewCluster(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("envproj")
	s.Env["APP_PW"] = uxPassword
	writeCapellaYAML(t, s, uxFullDoc)
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout,
		"  + bucket envclu/orders  couchbase - magma - 1024 MB - 1 replica - durability none  (after cluster is healthy)",
		"  + credential envclu/app",
		"  + scope envclu/orders/billing  (after bucket is created)")
	for _, c := range s.Fake.RequestLog() {
		if strings.Contains(c.Path, "/clusters/") {
			t.Errorf("cluster-scoped call for a cluster that does not exist: %s %s", c.Method, c.Path)
		}
	}
}

// TestPlanner_CAP005_AC6_UnsupportedKindsDeferred: every kind in capellactl's example file is
// planned for real against a seeded cluster: magma bucket, scopes, maxTTL -1 collection and the
// schedule summary, and nothing is deferred.
func TestPlanner_CAP005_AC6_UnsupportedKindsDeferred(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+hib+
		"    buckets:\n      - name: orders\n        memoryAllocationInMb: 1024\n        storageBackend: magma\n"+
		"        durabilityLevel: majority\n        scopes:\n          - name: inventory\n"+
		"            collections:\n              - name: suppliers\n                maxTTL: -1\n")
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout,
		"  + bucket envclu/orders  couchbase - magma - 1024 MB - 1 replica - durability majority",
		"  + scope envclu/orders/inventory  (after bucket is created)",
		"  + collection envclu/orders/inventory/suppliers",
		"  + schedule envclu")
	wantNotContains(t, r.Stdout+r.Stderr, "deferred")
}
