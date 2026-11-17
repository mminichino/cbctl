package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/resources"
)

const clusterPutPath = "PUT /v4/organizations/*/projects/*/clusters/*"

// computeDoc is doc with the data group's compute changed.
func computeDoc(doc, compute string) string {
	return strings.Replace(doc, "compute: {cpu: 4, ram: 16}", compute, 1)
}

// onePut is the only cluster PUT body; the test fails unless there is exactly one.
func onePut(t *testing.T, s *clitest.Scenario) map[string]any {
	t.Helper()
	puts := clusterPutBodies(s)
	if len(puts) != 1 {
		t.Fatalf("cluster PUTs = %d, want 1: %v", len(puts), puts)
	}
	return puts[0]
}

// TestScaleIn_CAP016_AC1_GuardsPassPutSent: 5 → 4 nodes passes both guards, prints the checks,
// lists the cluster as destructive in the prompt and sends one PUT.
func TestScaleIn_CAP016_AC1_GuardsPassPutSent(t *testing.T) {
	s, cid := nodeScenario(t, scaledDoc("5", "data, index, query"))
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 12288), capellafake.WithField("replicas", 1))
	writeCapellaYAML(t, s, scaledDoc("4", "data, index, query"))
	s.TTY(true)
	s.Answers("y")
	r := mustScenario(t, s.Run("capella", "apply"), "apply scale-in")
	wantContains(t, r.Stdout,
		"~ cluster envclu",
		"scale in",
		"serviceGroups[data,index,query].numOfNodes:  5 → 4",
		"checks: memory ok (12,288 / ~32,768 MB) · replicas ok (max 1, needs 2 data nodes)",
	)
	// cbctl writes the prompt on stderr so stdout stays the plan.
	wantContains(t, r.Stderr, "Destructive changes:\n  cluster envclu")
	put := onePut(t, s)
	if g := putGroups(t, put); g[0]["numOfNodes"] != float64(4) {
		t.Errorf("PUT numOfNodes = %v, want 4", g[0]["numOfNodes"])
	}
}

// TestScaleIn_CAP016_AC3_ReplicaGuardBlocks: 3 replicas need 4 data nodes, so a declarative
// 4 → 3 is blocked with exit 4 and nothing is written.
func TestScaleIn_CAP016_AC3_ReplicaGuardBlocks(t *testing.T) {
	s, cid := nodeScenario(t, scaledDoc("4", "data, index, query"))
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 1024), capellafake.WithField("replicas", 3))
	writeCapellaYAML(t, s, scenarioDoc)
	r := s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "scale-in blocked: bucket 'orders' has 3 replicas and needs at least 4 data nodes")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestVertical_CAP017_AC1_ComputeChangeBody: a cpu/ram change is a "scale up"; the PUT keeps the
// node count and the disk.
func TestVertical_CAP017_AC1_ComputeChangeBody(t *testing.T) {
	s, _ := nodeScenario(t, scenarioDoc)
	writeCapellaYAML(t, s, computeDoc(scenarioDoc, "compute: {cpu: 8, ram: 32}"))
	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply compute")
	wantContains(t, r.Stdout, "scale up")
	g := putGroups(t, onePut(t, s))[0]
	node, _ := g["node"].(map[string]any)
	if !jsonEqual(node["compute"], map[string]any{"cpu": 8, "ram": 32}) || g["numOfNodes"] != float64(3) {
		t.Errorf("PUT group = %v", g)
	}
	if !jsonEqual(node["disk"], map[string]any{"type": "gp3", "storage": 50, "iops": 3000}) {
		t.Errorf("PUT disk = %v", node["disk"])
	}
}

// TestVertical_CAP017_AC2_DiskChangeByProvider: a disk change is sent in the provider's shape.
func TestVertical_CAP017_AC2_DiskChangeByProvider(t *testing.T) {
	cases := []struct {
		provider, region, before, after string
		want                            map[string]any
	}{
		{"aws", "us-east-1", "{type: gp3, storage: 50, iops: 3000}", "{type: gp3, storage: 100, iops: 6000}",
			map[string]any{"type": "gp3", "storage": 100, "iops": 6000}},
		{"azure", "eastus", "{type: P10}", "{type: P20}", map[string]any{"type": "P20"}},
		{"gcp", "us-east1", "{type: pd-ssd, storage: 50}", "{type: pd-ssd, storage: 200}",
			map[string]any{"type": "pd-ssd", "storage": 200}},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			doc := strings.Replace(scenarioDoc, "type: aws\n      region: us-east-1", "type: "+tc.provider+"\n      region: "+tc.region, 1)
			doc = strings.Replace(doc, "disk: {type: gp3, storage: 50, iops: 3000}", "disk: "+tc.before, 1)
			s, _ := nodeScenario(t, doc)
			writeCapellaYAML(t, s, strings.Replace(doc, "disk: "+tc.before, "disk: "+tc.after, 1))
			mustScenario(t, s.Run("capella", "apply", "--yes"), "apply disk")
			node, _ := putGroups(t, onePut(t, s))[0]["node"].(map[string]any)
			if !jsonEqual(node["disk"], tc.want) {
				t.Errorf("PUT disk = %v, want %v", node["disk"], tc.want)
			}
		})
	}
}

// TestVertical_CAP017_AC3_DiskShrinkBlocked: a smaller disk is blocked (exit 4, no writes).
func TestVertical_CAP017_AC3_DiskShrinkBlocked(t *testing.T) {
	s, _ := nodeScenario(t, strings.Replace(scenarioDoc, "storage: 50", "storage: 100", 1))
	writeCapellaYAML(t, s, scenarioDoc)
	r := s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "node.disk.storage: 100 → 50 GB  disk size cannot be reduced")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestVertical_CAP017_AC5_CombinedSinglePut: scale out one group and scale up another in one PUT.
func TestVertical_CAP017_AC5_CombinedSinglePut(t *testing.T) {
	s, _ := nodeScenario(t, withSearch(scenarioDoc))
	doc := computeDoc(withSearch(scenarioDoc), "compute: {cpu: 8, ram: 32}")
	writeCapellaYAML(t, s, strings.Replace(doc, "numOfNodes: 2", "numOfNodes: 4", 1))
	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply combined")
	wantContains(t, r.Stdout, "scale out / scale up")
	onePut(t, s)
}

// TestVertical_CAP017_AC6_RamDecreaseMemoryGuard: 32 → 16 GB per data node halves the data
// service, so the bucket memory must fit half of today's total.
func TestVertical_CAP017_AC6_RamDecreaseMemoryGuard(t *testing.T) {
	big := computeDoc(scenarioDoc, "compute: {cpu: 8, ram: 32}") // total 49,152 MB → ~24,576 after
	t.Run("blocked", func(t *testing.T) {
		s, cid := nodeScenario(t, big)
		s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 30000), capellafake.WithField("replicas", 1))
		writeCapellaYAML(t, s, scenarioDoc)
		r := s.Run("capella", "apply", "--yes")
		wantExit(t, r, cli.ExitBlocked)
		wantContains(t, r.Stdout, "buckets allocate 30,000 MB but data service would have ~24,576 MB after scale-in (32 → 16 GB per node)")
		clitest.AssertNoWrites(t, s.Fake)
	})
	t.Run("fits", func(t *testing.T) {
		s, cid := nodeScenario(t, big)
		s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 8192), capellafake.WithField("replicas", 1))
		writeCapellaYAML(t, s, scenarioDoc)
		r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply ram decrease")
		wantContains(t, r.Stdout, "checks: memory ok (8,192 / ~24,576 MB)")
		clitest.AssertWrites(t, s.Fake, clusterPutPath)
	})
}

// TestVertical_CAP017_AC7_RerunZeroWrites: re-applying after a compute change writes nothing.
func TestVertical_CAP017_AC7_RerunZeroWrites(t *testing.T) {
	s, _ := nodeScenario(t, scenarioDoc)
	writeCapellaYAML(t, s, computeDoc(scenarioDoc, "compute: {cpu: 8, ram: 32}"))
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply compute")
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "apply", "--yes"), "rerun")
	clitest.AssertNoWrites(t, s.Fake)
}

// describedDoc is scenarioDoc with a cluster description.
func describedDoc(desc string) string {
	return strings.Replace(scenarioDoc, "    deletionProtection: false\n", "    description: "+desc+"\n    deletionProtection: false\n", 1)
}

// TestClusterUpdate_CAP012_AC1_FullBodyOverlay: the PUT is the observed body with the file's
// changes overlaid, sent with If-Match.
func TestClusterUpdate_CAP012_AC1_FullBodyOverlay(t *testing.T) {
	s, cid := nodeScenario(t, describedDoc("Nova development cluster"))
	doc := strings.Replace(describedDoc("Nova dev, now on enterprise"), "plan: developer pro", "plan: enterprise", 1)
	writeCapellaYAML(t, s, doc)
	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply update")
	wantContains(t, r.Stdout, `support.plan:  "developer pro" → "enterprise"`)
	calls := s.Fake.Calls("PUT", "/v4/organizations/*/projects/*/clusters/"+cid)
	if len(calls) != 1 {
		t.Fatalf("cluster PUTs = %v", calls)
	}
	if calls[0].Header.Get("If-Match") == "" {
		t.Error("cluster PUT has no If-Match header")
	}
	body, _ := calls[0].Body.(map[string]any)
	if body["name"] != "envclu" || body["description"] != "Nova dev, now on enterprise" {
		t.Errorf("PUT name/description = %v / %v", body["name"], body["description"])
	}
	if !jsonEqual(body["support"], map[string]any{"plan": "enterprise", "timezone": "PT"}) {
		t.Errorf("PUT support = %v", body["support"])
	}
	observed := []any{map[string]any{
		"node":       map[string]any{"compute": map[string]any{"cpu": 4, "ram": 16}, "disk": map[string]any{"type": "gp3", "storage": 50, "iops": 3000}},
		"numOfNodes": 3, "services": []any{"data", "index", "query"},
	}}
	if !jsonEqual(body["serviceGroups"], observed) {
		t.Errorf("PUT serviceGroups = %v, want the observed groups %v", body["serviceGroups"], observed)
	}
}

// TestClusterUpdate_CAP012_AC1_SettingsOnlyNote: a description change says it starts a Capella
// job (text and JSON notes) and does not call the progress "scaling".
func TestClusterUpdate_CAP012_AC1_SettingsOnlyNote(t *testing.T) {
	s, _ := nodeScenario(t, describedDoc("Nova development cluster"))
	writeCapellaYAML(t, s, describedDoc("Nova demo cluster"))
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "      description:  \"Nova development cluster\" → \"Nova demo cluster\"\n      "+resources.SettingsOnlyNote)

	r = mustScenario(t, s.Run("capella", "-o", "json", "plan"), "plan json")
	var js struct {
		Plan struct {
			Actions []struct {
				Kind  string   `json:"kind"`
				Notes []string `json:"notes"`
			} `json:"actions"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &js); err != nil {
		t.Fatalf("plan json: %v\n%s", err, r.Stdout)
	}
	found := false
	for _, a := range js.Plan.Actions {
		if a.Kind == "cluster" && slices.Contains(a.Notes, resources.SettingsOnlyNote) {
			found = true
		}
	}
	if !found {
		t.Errorf("no cluster action has the settings note:\n%s", r.Stdout)
	}

	s.Fake.SettlePolls(1)
	r = mustScenario(t, s.Run("capella", "apply", "--yes"), "apply")
	out := r.Stdout + r.Stderr
	wantContains(t, out, "cluster envclu: applying the change (Capella: scaling)")
	if strings.Contains(out, ": scaling (") {
		t.Errorf("settings-only progress says scaling:\n%s", out)
	}
}

// TestClusterUpdate_CAP012_AC1_ScalingKeepsScalingWording: a node-count change has no settings
// note and its progress says "scaling".
func TestClusterUpdate_CAP012_AC1_ScalingKeepsScalingWording(t *testing.T) {
	s, _ := nodeScenario(t, scenarioDoc)
	writeCapellaYAML(t, s, scaledDoc("4", "data, index, query"))
	s.Fake.SettlePolls(1)
	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply scale out")
	out := r.Stdout + r.Stderr
	wantContains(t, out, "cluster envclu: scaling (")
	if strings.Contains(out, resources.SettingsOnlyNote) {
		t.Errorf("scaling plan has the settings note:\n%s", out)
	}
}

// TestClusterUpdate_CAP012_AC3_ImmutableBlocked: changing an immutable field is blocked (exit 4,
// or exit 3 when the file is already invalid offline) and nothing is written.
func TestClusterUpdate_CAP012_AC3_ImmutableBlocked(t *testing.T) {
	cases := []struct{ old, new, field string }{
		{"region: us-east-1", "region: us-west-2", "cloudProvider.region"},
		{"type: aws", "type: gcp", "cloudProvider.type"},
		{"cidr: 10.0.0.0/24", "cidr: 10.20.0.0/23", "cloudProvider.cidr"},
		{"type: multi", "type: single", "availability.type"},
		{"    cloudProvider:", "    couchbaseServer: {version: '8.0'}\n    cloudProvider:", "couchbaseServer.version"},
		{"    cloudProvider:", "    cmekId: key-1\n    cloudProvider:", "cmekId"},
		{"    cloudProvider:", "    enablePrivateDNSResolution: true\n    cloudProvider:", "enablePrivateDNSResolution"},
		{"    cloudProvider:", "    configurationType: singleNode\n    cloudProvider:", "configurationType"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			s, _ := nodeScenario(t, scenarioDoc)
			doc := strings.Replace(scenarioDoc, tc.old, tc.new, 1)
			if tc.field == "cloudProvider.type" {
				doc = strings.Replace(doc, "type: gp3, storage: 50, iops: 3000", "type: pd-ssd, storage: 50", 1)
			}
			if tc.field == "configurationType" {
				doc = strings.Replace(strings.Replace(doc, "type: multi", "type: single", 1), "numOfNodes: 3", "numOfNodes: 1", 1)
			}
			writeCapellaYAML(t, s, doc)
			r := s.Run("capella", "apply", "--yes")
			defer clitest.AssertNoWrites(t, s.Fake)
			if r.Code == int(cli.ExitConfigInvalid) { // rejected offline first; still blocked
				return
			}
			wantExit(t, r, cli.ExitBlocked)
			wantContains(t, r.Stdout, "immutable:", tc.field, "(requires a new cluster)")
		})
	}
}

// TestClusterUpdate_CAP012_AC4_WaitBeforeAndAfter: the update waits for a busy cluster before the
// PUT and for the job after it.
func TestClusterUpdate_CAP012_AC4_WaitBeforeAndAfter(t *testing.T) {
	s, cid := nodeScenario(t, describedDoc("old description"))
	s.Fake.SetTransition(cid, []string{"rebalancing", "healthy"}, 0)
	s.Fake.SettlePolls(1)
	writeCapellaYAML(t, s, describedDoc("new description"))
	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply")
	putAt, gets := -1, []int{}
	for i, q := range s.Fake.RequestLog() {
		if !strings.HasSuffix(q.Path, "/clusters/"+cid) {
			continue
		}
		switch q.Method {
		case "PUT":
			putAt = i
		case "GET":
			gets = append(gets, i)
		}
	}
	if putAt < 0 || len(gets) == 0 || gets[0] > putAt || gets[len(gets)-1] < putAt {
		t.Errorf("want cluster GETs before and after the PUT (PUT at %d, GETs at %v)", putAt, gets)
	}
	wantContains(t, r.Stdout+r.Stderr, "rebalancing", "scaling")
}

// TestClusterUpdate_CAP012_AC6_RenameWithPinnedID: a pinned id renames the cluster in place.
func TestClusterUpdate_CAP012_AC6_RenameWithPinnedID(t *testing.T) {
	s, cid := nodeScenario(t, scenarioDoc)
	writeCapellaYAML(t, s, strings.Replace(scenarioDoc, "  - name: envclu", "  - id: "+cid+"\n    name: envclu2", 1))
	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply rename")
	wantContains(t, r.Stdout, `name:  "envclu" → "envclu2"`)
	cl, err := s.Fake.Client().GetCluster(t.Context(), projectOf(t, s), cid)
	if err != nil || cl.Name != "envclu2" {
		t.Errorf("cluster = %+v, %v; want renamed envclu2", cl, err)
	}
}

// TestClusterUpdate_CAP012_AC6_RenameHintWithoutID: a new name without an id plans a create and
// hints at pinning the id.
func TestClusterUpdate_CAP012_AC6_RenameHintWithoutID(t *testing.T) {
	s, cid := nodeScenario(t, scenarioDoc)
	writeCapellaYAML(t, s, strings.Replace(scenarioDoc, "  - name: envclu", "  - name: envclu2", 1))
	r := s.Run("capella", "plan")
	wantContains(t, r.Stdout, "+ cluster envclu2", "did you mean to rename envclu? pin id: "+cid+" to rename")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestClusterUpdate_CAP012_AC7_RerunZeroWrites: re-applying after a support change writes nothing.
func TestClusterUpdate_CAP012_AC7_RerunZeroWrites(t *testing.T) {
	s, _ := nodeScenario(t, scenarioDoc)
	writeCapellaYAML(t, s, strings.Replace(scenarioDoc, "plan: developer pro", "plan: enterprise", 1))
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply support")
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "apply", "--yes"), "rerun")
	clitest.AssertNoWrites(t, s.Fake)
}
