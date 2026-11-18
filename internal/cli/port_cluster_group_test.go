package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// svcGroup is a Capella service group body with a gp3 50 GB disk.
func svcGroup(nodes, cpu, ram int, services ...string) map[string]any {
	sv := make([]any, len(services))
	for i, s := range services {
		sv[i] = s
	}
	return map[string]any{
		"node":       map[string]any{"compute": map[string]any{"cpu": cpu, "ram": ram}, "disk": map[string]any{"type": "gp3", "storage": 50, "iops": 3000}},
		"numOfNodes": nodes, "services": sv,
	}
}

// syncedGroups seeds cluster envclu (with groups, or the fake's default 3 × 4/16 data group)
// and writes capella.yaml with sync, as capellactl's fixtures import it. The log is empty.
func syncedGroups(t *testing.T, groups []any, seed func(f *capellafake.Fake, cid string)) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	pid := s.Fake.SeedProject("envproj")
	var fields []capellafake.Field
	if groups != nil {
		fields = append(fields, capellafake.WithField("serviceGroups", groups))
	}
	cid := s.Fake.SeedCluster(pid, "envclu", "healthy", fields...)
	if seed != nil {
		seed(s.Fake, cid)
	}
	mustScenario(t, s.Run("capella", "sync", "--project", "envproj"), "sync")
	s.Fake.ResetCalls()
	return s, cid
}

func threeGroups(t *testing.T) (*clitest.Scenario, string) {
	t.Helper()
	return syncedGroups(t, []any{svcGroup(3, 8, 32, "data", "index", "query"), svcGroup(2, 4, 16, "search"),
		svcGroup(2, 4, 16, "index", "search")}, nil)
}

func readFile(t *testing.T, s *clitest.Scenario) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, "capella.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// putServices is the services of each group of the only cluster PUT.
func putServices(t *testing.T, s *clitest.Scenario) []string {
	t.Helper()
	var out []string
	for _, g := range putGroups(t, onePut(t, s)) {
		var sv []string
		for _, x := range g["services"].([]any) {
			sv = append(sv, x.(string))
		}
		out = append(out, strings.Join(sv, ","))
	}
	return out
}

// TestGroupCmd_CAP052_AC1_List: the table, the total, the JSON services and an unknown cluster.
func TestGroupCmd_CAP052_AC1_List(t *testing.T) {
	s, _ := threeGroups(t)
	r := mustScenario(t, s.Run("capella", "cluster", "group", "list", "envclu"), "list")
	lines := strings.Split(r.Stdout, "\n")
	// cbctl has no S.NO column (its tables never number rows).
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "SERVICES NODES vCPU RAM GB DISK" {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); strings.Join(f[:4], " ") != "data,index,query 3 8 32" || !strings.Contains(lines[1], "gp3 50 GB 3000 IOPS") {
		t.Errorf("row 1 = %q", lines[1])
	}
	if f := strings.Fields(lines[3]); f[0] != "index,search" {
		t.Errorf("row 3 = %q", lines[3])
	}
	wantContains(t, r.Stdout, "total nodes: 7 of 27")

	r = mustScenario(t, s.Run("capella", "-o", "json", "cluster", "group", "list", "envclu"), "list json")
	var groups []struct {
		Services string `json:"services"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &groups); err != nil {
		t.Fatalf("json: %v\n%s", err, r.Stdout)
	}
	var got []string
	for _, g := range groups {
		got = append(got, g.Services)
	}
	if strings.Join(got, " ") != "data,index,query search index,search" {
		t.Errorf("json services = %v", got)
	}

	r = s.Run("capella", "cluster", "group", "list", "nope-01")
	if r.Code == 0 || !strings.Contains(r.Stderr, "nope-01") {
		t.Errorf("unknown cluster: exit %d\n%s", r.Code, r.Stderr)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestGroupCmd_CAP052_AC2_AddBody: the plan line, "scale out", one PUT with both groups, and the file.
func TestGroupCmd_CAP052_AC2_AddBody(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "group", "add", "envclu", "--services", "search", "--nodes", "2"), "add")
	wantContains(t, r.Stdout, "+ serviceGroups[search]  2 × 4 vCPU/16 GB gp3 50 GB", "scale out")
	groups := putGroups(t, onePut(t, s))
	want := []any{svcGroup(3, 4, 16, "data", "index", "query"), svcGroup(2, 4, 16, "search")}
	if !jsonEqual(groups, want) {
		t.Errorf("PUT serviceGroups = %v, want %v", groups, want)
	}
	if f := readFile(t, s); !strings.Contains(f, "services: [search]") {
		t.Errorf("file has no search group:\n%s", f)
	}
}

// TestGroupCmd_CAP052_AC2_FileUpdatedNoDrift: the added group is written in normal form (sorted
// services, given compute and disk); apply and sync then find nothing to do.
func TestGroupCmd_CAP052_AC2_FileUpdatedNoDrift(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "group", "add", "envclu", "--services", "query,index", "--nodes", "2",
		"--cpu", "16", "--ram", "64", "--disk-size", "100"), "add")
	wantContains(t, readFile(t, s), "compute: {cpu: 16, ram: 64}", "storage: 100", "services: [index, query]")
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "--yes", "apply"), "apply")
	wantContains(t, r.Stdout, "No changes")
	clitest.AssertNoWrites(t, s.Fake)
	r = mustScenario(t, s.Run("capella", "sync"), "sync")
	wantContains(t, r.Stdout, "already in sync")
}

// TestGroupCmd_CAP052_AC3_AddRefused: an existing group of another size, a node count over the
// limit and an unknown service exit 3 and change nothing.
func TestGroupCmd_CAP052_AC3_AddRefused(t *testing.T) {
	cases := []struct {
		name string
		args []string
		msg  string
	}{
		{"existing group", []string{"--services", "data,index,query", "--nodes", "5"},
			"group data,index,query already exists with 3 nodes\n  -> change its node count with 'cbctl capella cluster scale envclu --group data,index,query"},
		{"node limit", []string{"--services", "search", "--nodes", "25"}, "outside the limits of an extra group (2-24)"},
		{"unknown service", []string{"--services", "cache"}, "unknown service 'cache'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := syncedGroups(t, nil, nil)
			before := readFile(t, s)
			r := s.Run(append([]string{"capella", "--yes", "cluster", "group", "add", "envclu"}, tc.args...)...)
			wantExit(t, r, cli.ExitConfigInvalid)
			wantContains(t, r.Stderr, tc.msg)
			if readFile(t, s) != before {
				t.Error("capella.yaml changed")
			}
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}

// TestGroupCmd_CAP052_AC3_AddRefusedSingleNode: a singleNode cluster has exactly one group.
func TestGroupCmd_CAP052_AC3_AddRefusedSingleNode(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "ci-01", "--size", "dev"), "create dev")
	s.Fake.ResetCalls()
	r := s.Run("capella", "--yes", "cluster", "group", "add", "ci-01", "--services", "search")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "singleNode clusters have exactly one service group")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestGroupCmd_CAP052_AC3_AddRefusedTotalAndFreeTier: more than 27 nodes in total exits 3; a
// free-tier cluster exits 4; neither writes.
func TestGroupCmd_CAP052_AC3_AddRefusedTotalAndFreeTier(t *testing.T) {
	s, _ := threeGroups(t)
	before := readFile(t, s)
	r := s.Run("capella", "--yes", "cluster", "group", "add", "envclu", "--services", "eventing", "--nodes", "24")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "a cluster can have at most 27 nodes (declared 31)")
	if readFile(t, s) != before {
		t.Error("capella.yaml changed")
	}
	clitest.AssertNoWrites(t, s.Fake)

	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "try-01", "--free-tier"), "create free")
	s.Fake.ResetCalls()
	r = s.Run("capella", "--yes", "cluster", "group", "add", "try-01", "--services", "search")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "[error] - free-tier clusters have 1 node and a fixed size")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestGroupCmd_CAP052_AC4_RemoveBody: one PUT with the other groups; the file loses the group.
func TestGroupCmd_CAP052_AC4_RemoveBody(t *testing.T) {
	s, _ := threeGroups(t)
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "group", "remove", "envclu", "search"), "remove")
	wantContains(t, r.Stdout, "- serviceGroups[search]")
	if got := putServices(t, s); strings.Join(got, " ") != "data,index,query index,search" {
		t.Errorf("PUT groups = %v", got)
	}
	f := readFile(t, s)
	if strings.Contains(f, "services: [search]") || !strings.Contains(f, "services: [index, search]") {
		t.Errorf("file groups after remove:\n%s", f)
	}
}

// TestGroupCmd_CAP052_AC4_RemoveMemoryGuard: removing a data group the buckets need is blocked.
func TestGroupCmd_CAP052_AC4_RemoveMemoryGuard(t *testing.T) {
	// 6 data nodes × 16 GB: 49,152 MB of bucket quota, 24,576 after removing one group.
	s, _ := syncedGroups(t, []any{svcGroup(3, 4, 16, "data", "index", "query"), svcGroup(3, 4, 16, "data")}, func(f *capellafake.Fake, cid string) {
		f.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 30000), capellafake.WithField("replicas", 1))
	})
	r := s.Run("capella", "--yes", "cluster", "group", "remove", "envclu", "data")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "scale-in blocked: buckets allocate")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestGroupCmd_CAP052_AC6_TurnedOffBlocked: a turned-off cluster cannot be scaled (exit 4, the
// file is unchanged).
func TestGroupCmd_CAP052_AC6_TurnedOffBlocked(t *testing.T) {
	s, cid := syncedGroups(t, nil, nil)
	s.Fake.SetTransition(cid, []string{"turnedOff"}, 0)
	before := readFile(t, s)
	r := s.Run("capella", "--yes", "cluster", "group", "add", "envclu", "--services", "search")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "turned off")
	if readFile(t, s) != before {
		t.Error("capella.yaml changed")
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestGroupCmd_CAP052_AC7_HelpExamples: every verb's help has an example; the scaling guide shows
// group add.
func TestGroupCmd_CAP052_AC7_HelpExamples(t *testing.T) {
	s := newScenario(t)
	for _, verb := range []string{"list", "add", "remove"} {
		r := mustScenario(t, s.Run("capella", "cluster", "group", verb, "--help"), verb+" --help")
		wantContains(t, r.Stdout, "Examples:", "cbctl capella cluster group "+verb+" prod-01")
	}
	r := mustScenario(t, s.Run("capella", "guide", "scaling"), "guide scaling")
	wantContains(t, r.Stdout, "cbctl capella cluster group add NAME --services search --nodes 2")
}
