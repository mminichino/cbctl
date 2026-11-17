package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// searchGroup is a second service group for scenarioDoc (2 search nodes).
const searchGroup = `      - node:
          compute: {cpu: 4, ram: 16}
          disk: {type: gp3, storage: 50, iops: 3000}
        numOfNodes: 2
        services: [search]
`

// withSearch adds searchGroup after the data group of doc.
func withSearch(doc string) string {
	return strings.Replace(doc, "    deletionProtection: false\n", searchGroup+"    deletionProtection: false\n", 1)
}

// nodeScenario applies doc (normally scaledDoc) and returns the scenario with the cluster ID and
// an empty request log.
func nodeScenario(t *testing.T, doc string) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, doc)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply")
	cls, err := s.Fake.Client().ListClusters(t.Context(), projectOf(t, s))
	if err != nil || len(cls) != 1 {
		t.Fatalf("clusters = %v, %v", cls, err)
	}
	s.Fake.ResetCalls()
	return s, cls[0].ID
}

// groupNodes is the node count of every service group of the cluster, in Capella's order.
func groupNodes(t *testing.T, s *clitest.Scenario, cid string) []int {
	t.Helper()
	cl, err := s.Fake.Client().GetCluster(t.Context(), projectOf(t, s), cid)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int, len(cl.ServiceGroups))
	for i, g := range cl.ServiceGroups {
		out[i] = g.NumOfNodes
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// clusterPuts are the cluster PUT bodies the fake received.
func clusterPutBodies(s *clitest.Scenario) []map[string]any {
	var out []map[string]any
	for _, c := range s.Fake.Calls("PUT", "/v4/organizations/*/projects/*/clusters/*") {
		if b, ok := c.Body.(map[string]any); ok {
			out = append(out, b)
		}
	}
	return out
}

func putGroups(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, _ := body["serviceGroups"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, g := range raw {
		m, ok := g.(map[string]any)
		if !ok {
			t.Fatalf("service group %v is not an object", g)
		}
		out = append(out, m)
	}
	return out
}

// TestStep_CAP018_AC2_StepDownRunsGuards: --step -1 runs the scale-in checks and then scales.
func TestStep_CAP018_AC2_StepDownRunsGuards(t *testing.T) {
	s, cid := nodeScenario(t, scaledDoc("4", "data, index, query"))
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "scale", "envclu", "--step", "-1", "--min", "3"), "step -1")
	wantContains(t, r.Stdout, "checks: memory ok", "scaled data,index,query: 4 → 3")
	if got := groupNodes(t, s, cid); !sameInts(got, []int{3}) {
		t.Fatalf("nodes = %v, want [3]", got)
	}
}

// TestStep_CAP018_AC2_StepDownGuardBlocks: a bucket with 3 replicas blocks 4 → 3; -o json
// reports status blocked and nothing is written.
func TestStep_CAP018_AC2_StepDownGuardBlocks(t *testing.T) {
	s, cid := nodeScenario(t, scaledDoc("4", "data, index, query"))
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("replicas", 3))
	before := s.ReadFile("capella.yaml")
	r := s.Run("capella", "--yes", "-o", "json", "cluster", "scale", "envclu", "--step", "-1")
	wantExit(t, r, cli.ExitBlocked)
	clitest.AssertJSONSchema(t, "scale", []byte(r.Stdout))
	wantContains(t, r.Stdout, `"status": "blocked"`)
	clitest.AssertNoWrites(t, s.Fake)
	if s.ReadFile("capella.yaml") != before {
		t.Error("capella.yaml changed on a blocked step")
	}
}

// TestStep_CAP018_AC3_AtMaxNoop: --step +1 at --max is a successful no-op.
func TestStep_CAP018_AC3_AtMaxNoop(t *testing.T) {
	s, _ := nodeScenario(t, scaledDoc("6", "data, index, query"))
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "scale", "envclu", "--step", "+1", "--max", "6"), "step at max")
	wantContains(t, r.Stdout, "already at maximum (6)")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestStep_CAP018_AC4_AllGroupsIndividual: --all-groups steps each group by one, in one PUT.
func TestStep_CAP018_AC4_AllGroupsIndividual(t *testing.T) {
	s, cid := nodeScenario(t, withSearch(scenarioDoc))
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "scale", "envclu", "--step", "+1", "--all-groups"), "step all groups")
	wantContains(t, r.Stdout, "scaled data,index,query: 3 → 4", "scaled search: 2 → 3")
	puts := clusterPutBodies(s)
	if len(puts) != 1 {
		t.Fatalf("cluster PUTs = %d, want 1", len(puts))
	}
	var got []int
	for _, g := range putGroups(t, puts[0]) {
		n, _ := g["numOfNodes"].(float64)
		got = append(got, int(n))
	}
	if !sameInts(got, []int{4, 3}) {
		t.Fatalf("PUT node counts = %v, want [4 3]", got)
	}
	if got := groupNodes(t, s, cid); !sameInts(got, []int{4, 3}) {
		t.Fatalf("nodes = %v", got)
	}
}

// TestStep_CAP018_AC5_WaitsWhenBusy: a cluster still scaling is waited for first, and the step is
// counted from the node count it reaches.
func TestStep_CAP018_AC5_WaitsWhenBusy(t *testing.T) {
	s, cid := nodeScenario(t, scaledDoc("4", "data, index, query"))
	s.Fake.SettlePolls(1)
	s.Fake.SetTransition(cid, []string{"scaling", "healthy"}, 0)
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "scale", "envclu", "--step", "+1"), "step while scaling")
	out := r.Stdout + r.Stderr
	wait, scaled := strings.Index(out, "cluster envclu is scaling; waiting"), strings.Index(out, "scaled data,index,query: 4 → 5")
	if wait < 0 || scaled < 0 || r.Stderr == "" {
		t.Fatalf("missing wait or scaled line:\nstdout: %s\nstderr: %s", r.Stdout, r.Stderr)
	}
	if got := groupNodes(t, s, cid); !sameInts(got, []int{5}) {
		t.Fatalf("nodes = %v, want [5]", got)
	}
	// The wait came before the write.
	var firstGet, firstPut = -1, -1
	for i, c := range s.Fake.RequestLog() {
		if c.Method == "GET" && strings.HasSuffix(c.Path, "/clusters/"+cid) && firstGet < 0 {
			firstGet = i
		}
		if c.Method == "PUT" && firstPut < 0 {
			firstPut = i
		}
	}
	if firstGet < 0 || firstPut < 0 || firstGet > firstPut {
		t.Fatalf("GET at %d, PUT at %d: the cluster was not waited for first", firstGet, firstPut)
	}
}

// TestStep_CAP018_AC6_JSONOutput: every -o json outcome of --step matches scale.schema.json.
func TestStep_CAP018_AC6_JSONOutput(t *testing.T) {
	s, cid := nodeScenario(t, scenarioDoc)
	decode := func(r clitest.Result) any {
		t.Helper()
		clitest.AssertJSONSchema(t, "scale", []byte(r.Stdout))
		var v any
		if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	r := mustScenario(t, s.Run("capella", "--yes", "-o", "json", "cluster", "scale", "envclu", "--step", "+1"), "step json")
	want := map[string]any{"cluster": "envclu", "group": "data,index,query", "from": 3.0, "to": 4.0, "status": "scaled"}
	if got := decode(r); !jsonEqual(got, want) {
		t.Fatalf("scale doc = %v, want %v", got, want)
	}

	r = mustScenario(t, s.Run("capella", "--yes", "-o", "json", "cluster", "scale", "envclu", "--step", "+1", "--max", "4"), "step at max")
	if got := decode(r).(map[string]any)["status"]; got != "at_limit" {
		t.Fatalf("status = %v, want at_limit", got)
	}

	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("replicas", 3))
	r = s.Run("capella", "--yes", "-o", "json", "cluster", "scale", "envclu", "--step", "-1")
	wantExit(t, r, cli.ExitBlocked)
	if got := decode(r).(map[string]any)["status"]; got != "blocked" {
		t.Fatalf("status = %v, want blocked", got)
	}

	// The same through the direct command (no capella.yaml).
	r = mustScenario(t, s.Run("capella", "--yes", "-o", "json", "cluster", "scale", "envclu", "--step", "+1", "--direct"), "direct step")
	if got := decode(r).(map[string]any)["status"]; got != "scaled" {
		t.Fatalf("direct status = %v", got)
	}

	s2, _ := nodeScenario(t, withSearch(scaledDoc("4", "data, index, query")))
	r = mustScenario(t, s2.Run("capella", "--yes", "-o", "json", "cluster", "scale", "envclu", "--step", "+1", "--all-groups"), "all groups json")
	docs, ok := decode(r).([]any)
	if !ok || len(docs) != 2 {
		t.Fatalf("all-groups doc = %s", r.Stdout)
	}
	for _, d := range docs {
		if d.(map[string]any)["status"] != "scaled" {
			t.Fatalf("all-groups doc = %s", r.Stdout)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// TestJSONSchema_PTST12_PlanApplyOutput: the real -o json output of plan and apply --yes matches
// plan.schema.json and apply.schema.json, for a create, an update and a no-change run.
func TestJSONSchema_PTST12_PlanApplyOutput(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioBucketDoc)
	for _, step := range []string{"create", "update", "no change"} {
		if step == "update" {
			writeCapellaYAML(t, s, strings.Replace(scenarioBucketDoc, "numOfNodes: 3", "numOfNodes: 4", 1))
		}
		r := mustScenario(t, s.Run("capella", "-o", "json", "plan"), "plan -o json")
		clitest.AssertJSONSchema(t, "plan", []byte(r.Stdout))
		r = mustScenario(t, s.Run("capella", "-o", "json", "apply", "--yes"), "apply -o json")
		clitest.AssertJSONSchema(t, "apply", []byte(r.Stdout))
	}
}
