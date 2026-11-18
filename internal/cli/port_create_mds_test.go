package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

var mdsGroups = []string{"--group", "data:3x8/32", "--group", "index,query:2x8/32", "--group", "search:2x4/16"}

// createCluster runs 'capella cluster create NAME ARGS... -y'.
func createCluster(s *clitest.Scenario, name string, args ...string) clitest.Result {
	return s.Run(append([]string{"capella", "cluster", "create", name, "-y"}, args...)...)
}

func groupServices(groups []map[string]any) []any {
	var out []any
	for _, g := range groups {
		out = append(out, g["services"])
	}
	return out
}

func postGroups(t *testing.T, s *clitest.Scenario) []map[string]any {
	t.Helper()
	posts := clusterPosts(s)
	if len(posts) != 1 {
		t.Fatalf("cluster POSTs = %d, want 1", len(posts))
	}
	return groupsOf(posts[0])
}

func TestClusterCreate_CAP051_AC1_CreateBodyMatchesFixture(t *testing.T) {
	s := importedScenario(t)
	r := mustScenario(t, createCluster(s, "nova-prod-01", append([]string{"--provider", "aws"}, mdsGroups...)...), "create")
	node := func(cpu, ram int) map[string]any {
		return map[string]any{"compute": map[string]int{"cpu": cpu, "ram": ram}, "disk": gp3Default}
	}
	wantJSON(t, "serviceGroups", postGroups(t, s), []map[string]any{
		{"node": node(8, 32), "numOfNodes": 3, "services": []string{"data"}},
		{"node": node(8, 32), "numOfNodes": 2, "services": []string{"index", "query"}},
		{"node": node(4, 16), "numOfNodes": 2, "services": []string{"search"}},
	})
	wantContains(t, r.Stdout+r.Stderr, "cluster nova-prod-01: healthy")
	wantJSON(t, "file groups", groupServices(groupsOf(fileCluster(t, s, "nova-prod-01"))),
		[][]string{{"data"}, {"index", "query"}, {"search"}})
}

func TestClusterCreate_CAP051_AC1_PlanLinesPerGroup(t *testing.T) {
	s := importedScenario(t)
	r := mustScenario(t, createCluster(s, "nova-prod-01", append(mdsGroups, "--no-apply")...), "create")
	wantContains(t, r.Stdout,
		"+ cluster nova-prod-01  aws us-east-1 - multi-AZ - 3 service groups - 7 nodes - developer pro",
		"+ serviceGroups[data]         3 × 8 vCPU/32 GB gp3 50 GB",
		"+ serviceGroups[index,query]  2 × 8 vCPU/32 GB gp3 50 GB",
		"+ serviceGroups[search]       2 × 4 vCPU/16 GB gp3 50 GB",
		"3 groups - 7 nodes (data 3 × 8/32 - index,query 2 × 8/32 - search 2 × 4/16)")
}

func TestClusterCreate_CAP051_AC3_ConflictingFlags(t *testing.T) {
	for _, tc := range []struct {
		extra []string
		flags string
	}{
		{[]string{"--nodes", "3"}, "--nodes"},
		{[]string{"--cpu", "8", "--ram", "32"}, "--cpu, --ram"},
		{[]string{"--services", "data"}, "--services"},
		{[]string{"--size", "small"}, "--size"},
	} {
		t.Run(tc.flags, func(t *testing.T) {
			s := importedScenario(t)
			before := s.ReadFile("capella.yaml")
			r := createCluster(s, "bad-01", append(append([]string{}, mdsGroups...), tc.extra...)...)
			wantExit(t, r, cli.ExitConfigInvalid)
			wantContains(t, r.Stderr, "--group sets nodes, size and services per group, so "+tc.flags+" cannot be given with it",
				"remove "+tc.flags)
			if s.ReadFile("capella.yaml") != before {
				t.Error("capella.yaml changed")
			}
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}

func TestClusterCreate_CAP051_AC3_DuplicateGroupKey(t *testing.T) {
	s := importedScenario(t)
	before := s.ReadFile("capella.yaml")
	r := createCluster(s, "bad-01", "--group", "data:3x8/32", "--group", "index,query:2x8/32", "--group", "query,index:2x4/16")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "two groups run index,query; each group needs a different set of services")
	if s.ReadFile("capella.yaml") != before {
		t.Error("capella.yaml changed")
	}
	clitest.AssertNoWrites(t, s.Fake)
}

func TestClusterCreate_CAP051_AC4_Limits(t *testing.T) {
	for _, tc := range []struct {
		groups []string
		msg    string
	}{
		{[]string{"index,query:3x8/32", "search:2x4/16"}, "at least one service group must run the data service"},
		{[]string{"data:2x8/32"}, "must be between 3 and 27"},
		{[]string{"data:3x8/32", "search:25x4/16"}, "additional service groups allow 2–24 nodes"},
		{[]string{"data:20x8/32", "search:8x4/16"}, "a cluster can have at most 27 nodes (declared 28)"},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			s := importedScenario(t)
			var args []string
			for _, g := range tc.groups {
				args = append(args, "--group", g)
			}
			r := createCluster(s, "bad-01", args...)
			wantExit(t, r, cli.ExitConfigInvalid)
			wantContains(t, r.Stderr, tc.msg)
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}

func TestClusterCreate_CAP051_AC4_SingleNodeOneGroup(t *testing.T) {
	s := importedScenario(t)
	r := createCluster(s, "bad-01", "--single-node", "--group", "data:1x4/16", "--group", "search:2x4/16")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "singleNode clusters have exactly one service group")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestClusterCreate_CAP051_AC4_DataGroupFirst(t *testing.T) {
	s := importedScenario(t)
	mustScenario(t, createCluster(s, "nova-prod-01", "--group", "search:2x4/16", "--group", "data:3x8/32"), "create")
	wantJSON(t, "POST groups", groupServices(postGroups(t, s)), [][]string{{"data"}, {"search"}})
}

func TestClusterCreate_CAP051_AC5_DiskPerGroup(t *testing.T) {
	for _, tc := range []struct {
		provider, region string
		extra            []string
		disks            []map[string]any
	}{
		{"aws", "us-east-1", []string{"--disk-size", "100"},
			[]map[string]any{{"type": "gp3", "storage": 100, "iops": 4370}, {"type": "gp3", "storage": 200, "iops": 5740}}},
		{"gcp", "us-east1", []string{"--disk-size", "100"},
			[]map[string]any{{"type": "pd-ssd", "storage": 100}, {"type": "pd-ssd", "storage": 200}}},
		{"azure", "eastus", nil, []map[string]any{{"type": "P6", "autoExpansion": true}, {"type": "P6"}}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			s := importedScenario(t)
			first, second := "data:3x8/32", "index,query:2x8/32:size=200"
			if tc.provider == "azure" {
				first, second = "data:3x8/32:auto-expansion=true", "index,query:2x8/32"
			}
			args := append([]string{"--provider", tc.provider, "--region", tc.region}, tc.extra...)
			mustScenario(t, createCluster(s, "nova-prod-01", append(args, "--group", first, "--group", second)...), "create")
			var disks []any
			for _, g := range postGroups(t, s) {
				disks = append(disks, g["node"].(map[string]any)["disk"])
			}
			wantJSON(t, "disks", disks, tc.disks)
		})
	}
}

func TestClusterCreate_CAP051_AC5_AutoExpansionAzureOnly(t *testing.T) {
	s := importedScenario(t)
	r := createCluster(s, "bad-01", "--group", "data:3x8/32:auto-expansion=true")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "autoExpansion is for azure disks only")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestClusterCreate_CAP051_AC7_RerunZeroWrites(t *testing.T) {
	s := importedScenario(t)
	r := mustScenario(t, s.Run(append([]string{"capella", "cluster", "create", "nova-prod-01", "-y", "-o", "json"}, mdsGroups...)...), "create")
	var doc struct {
		Plan struct {
			Actions []struct {
				Notes []string `json:"notes"`
			} `json:"actions"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(r.Stdout[strings.Index(r.Stdout, "{"):]), &doc); err != nil {
		t.Fatalf("JSON: %v\n%s", err, r.Stdout)
	}
	var keys []string
	for _, a := range doc.Plan.Actions {
		for _, n := range a.Notes {
			if strings.HasPrefix(n, "+ serviceGroups[") {
				keys = append(keys, strings.SplitN(n, "]", 2)[0])
			}
		}
	}
	wantJSON(t, "group notes", keys, []string{"+ serviceGroups[data", "+ serviceGroups[index,query", "+ serviceGroups[search"})
	s.Fake.ResetCalls()
	wantContains(t, mustScenario(t, s.Run("capella", "apply", "-y"), "apply").Stdout, "No changes")
	r = createCluster(s, "nova-prod-01", mdsGroups...)
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "already exists")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestClusterCreate_CAP051_AC7_NoApplyThenApply(t *testing.T) {
	s := importedScenario(t)
	mustScenario(t, createCluster(s, "nova-prod-01", append(mdsGroups, "--no-apply")...), "create")
	clitest.AssertNoWrites(t, s.Fake)
	mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	if n := len(postGroups(t, s)); n != 3 {
		t.Errorf("POST serviceGroups = %d, want 3", n)
	}
}

func TestClusterCreate_CAP051_AC8_HelpAndOptions(t *testing.T) {
	s := importedScenario(t)
	r := mustScenario(t, s.Run("capella", "cluster", "create", "--help"), "help")
	wantContains(t, r.Stdout, "SERVICES:NODESxCPU/RAM[:DISK]", "--group data:3x8/32 --group index,query:2x8/32",
		"cluster clone SOURCE NEW_NAME --region REGION --no-data-model")
	r = mustScenario(t, s.Run("capella", "cluster", "options"), "options")
	wantContains(t, r.Stdout, "services         data, index, query, search, analytics, eventing",
		"first group 3-27 nodes, other groups 2-24, at most 27 per cluster, one group runs data",
		"aws    4/16, 4/32, 8/16, 8/32")
	wantContains(t, mustScenario(t, s.Run("capella", "guide", "scaling"), "guide").Stdout, "--group data:3x8/32")
}

func TestClusterCreate_CAP051_AC9_UnknownSizeWarns(t *testing.T) {
	s := importedScenario(t)
	r := mustScenario(t, createCluster(s, "nova-prod-01", "--group", "data:3x8/30", "--group", "search:2x4/16", "--no-apply"), "create")
	warning := "! serviceGroups[data]: 8 vCPU/30 GB is not a known aws size; Capella may reject it (see 'cbctl capella cluster options')"
	wantContains(t, r.Stdout, warning)
	if strings.Contains(r.Stdout, "serviceGroups[search]: 4 vCPU") {
		t.Errorf("a known size warns:\n%s", r.Stdout)
	}
}

func TestClusterCreate_CAP051_AC10_DeploymentFailedNotDeleted(t *testing.T) {
	s := importedScenario(t)
	s.Fake.FailDeployNext()
	r := createCluster(s, "nova-prod-01", mdsGroups...)
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "deploymentFailed")
	for _, c := range s.Fake.Writes() {
		if c.Method == "DELETE" {
			t.Errorf("the failed cluster was deleted: %s", c.Path)
		}
	}
}

func TestClusterCreate_CAP053_AC1_OptionInBody(t *testing.T) {
	all := []string{"couchbaseServer", "zones", "cmekId", "enablePrivateDNSResolution"}
	for _, tc := range []struct {
		args  []string
		field string
		value any
	}{
		{[]string{"--server-version", "8.0"}, "couchbaseServer", map[string]any{"version": "8.0"}},
		{[]string{"--availability", "single", "--zone", "use1-az1"}, "zones", []string{"use1-az1"}},
		{[]string{"--cmek-id", "key-1"}, "cmekId", "key-1"},
		{[]string{"--private-dns"}, "enablePrivateDNSResolution", true},
	} {
		t.Run(tc.field, func(t *testing.T) {
			s := importedScenario(t)
			mustScenario(t, createCluster(s, "nova-adv-01", tc.args...), "create")
			posts := clusterPosts(s)
			if len(posts) != 1 {
				t.Fatalf("cluster POSTs = %d", len(posts))
			}
			wantJSON(t, tc.field, posts[0][tc.field], tc.value)
			for _, f := range all {
				if _, ok := posts[0][f]; ok && f != tc.field {
					t.Errorf("body has %s: %v", f, posts[0][f])
				}
			}
			wantJSON(t, "file "+tc.field, fileCluster(t, s, "nova-adv-01")[tc.field], tc.value)
			s.Fake.ResetCalls()
			wantContains(t, mustScenario(t, s.Run("capella", "apply", "-y"), "apply").Stdout, "No changes")
		})
	}
}
