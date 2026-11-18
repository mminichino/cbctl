package cli_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// importedScenario is capellactl's `imported` fixture: project nova-dev with the healthy
// cluster nova-dev-01, synced into capella.yaml, with the call log cleared.
func importedScenario(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	return s
}

// fileCluster returns cluster name from the scenario's capella.yaml (either file form).
func fileCluster(t *testing.T, s *clitest.Scenario, name string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s.ReadFile("capella.yaml")), &doc); err != nil {
		t.Fatalf("capella.yaml: %v", err)
	}
	var clusters []any
	if c, ok := doc["clusters"].([]any); ok {
		clusters = c
	}
	if ps, ok := doc["projects"].([]any); ok {
		for _, p := range ps {
			if c, ok := p.(map[string]any)["clusters"].([]any); ok {
				clusters = append(clusters, c...)
			}
		}
	}
	for _, c := range clusters {
		if m, ok := c.(map[string]any); ok && m["name"] == name {
			return m
		}
	}
	t.Fatalf("cluster %s not in capella.yaml:\n%s", name, s.ReadFile("capella.yaml"))
	return nil
}

// groupsOf returns the serviceGroups of a decoded cluster.
func groupsOf(c map[string]any) []map[string]any {
	var out []map[string]any
	list, _ := c["serviceGroups"].([]any)
	for _, g := range list {
		if m, ok := g.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// jsonNorm round-trips v through JSON so YAML and JSON documents compare equal.
func jsonNorm(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	if g, w := jsonNorm(t, got), jsonNorm(t, want); !reflect.DeepEqual(g, w) {
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		t.Errorf("%s =\n  %s\nwant\n  %s", what, gb, wb)
	}
}

// clusterPosts returns the bodies of the cluster create POSTs.
func clusterPosts(s *clitest.Scenario) []map[string]any {
	var out []map[string]any
	for _, c := range writesTo(s, "POST", "/clusters") {
		if m, ok := c.Body.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// writesTo returns the write calls of method whose path ends with suffix.
func writesTo(s *clitest.Scenario, method, suffix string) []capellafake.Call {
	var out []capellafake.Call
	for _, c := range s.Fake.Writes() {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			out = append(out, c)
		}
	}
	return out
}

// clusterPuts returns the cluster update PUTs.
func clusterPuts(s *clitest.Scenario) []capellafake.Call {
	var out []capellafake.Call
	for _, c := range s.Fake.Writes() {
		if c.Method == "PUT" && strings.Contains(c.Path, "/clusters/") && strings.Count(c.Path[strings.Index(c.Path, "/clusters/")+1:], "/") == 1 {
			out = append(out, c)
		}
	}
	return out
}

// lines joins wizard answers, one per line.
func lines(answers ...string) string { return strings.Join(answers, "\n") + "\n" }

// runWizard runs cluster create on a terminal with the wizard answers on stdin and the plan
// confirmation answered "y".
func runWizard(t *testing.T, s *clitest.Scenario, answers []string, args ...string) clitest.Result {
	t.Helper()
	s.TTY(true)
	defer s.TTY(false)
	s.Answers("y")
	return s.RunWith(lines(answers...), append([]string{"capella", "cluster", "create"}, args...)...)
}

var gp3Default = map[string]any{"type": "gp3", "storage": 50, "iops": 3000}

var _ = cli.ExitConfigInvalid

func TestClusterCreate_CAP051_AC6_InteractiveTwoGroups(t *testing.T) {
	s := importedScenario(t)
	r := runWizard(t, s, []string{"provisioned", "aws", "custom", "multiple", // a start of the answer is enough
		"data", "3", "4", "", "", "", // group 1: data, 3 nodes, size 4 (8/32), default disk
		"y",
		"index,query", "2", "2", "", "100", "", // group 2: 4/32 with gp3 100 GB
		"n", "", "", ""}, "nova-prod-01")
	mustScenario(t, r, "cluster create")
	wantContains(t, r.Stderr, "Service group 2  (nodes so far: 3 of 27)", "Node size (vCPU / RAM GB)")
	wantContains(t, r.Stdout+r.Stderr, "2 groups - 5 nodes (data 3 × 8/32 - index,query 2 × 4/32)")
	groups := groupsOf(fileCluster(t, s, "nova-prod-01"))
	type row struct {
		Services any
		Nodes    any
		Compute  any
	}
	var got []row
	for _, g := range groups {
		got = append(got, row{g["services"], g["numOfNodes"], g["node"].(map[string]any)["compute"]})
	}
	wantJSON(t, "groups", got, []row{
		{[]string{"data"}, 3, map[string]int{"cpu": 8, "ram": 32}},
		{[]string{"index", "query"}, 2, map[string]int{"cpu": 4, "ram": 32}},
	})
	wantJSON(t, "group 2 disk", groups[1]["node"].(map[string]any)["disk"],
		map[string]any{"type": "gp3", "storage": 100, "iops": 4370})
}

func TestClusterCreate_CAP051_AC6_InteractiveReasksBadAnswer(t *testing.T) {
	s := importedScenario(t)
	r := runWizard(t, s, []string{"provisioned", "aws", "custom", "multiple groups",
		"dat", "data", "2", "3", "4", "", "", "", "y",
		"data", "cache", "search", "25", "2", "1", "", "", "",
		"n", "", "", ""}, "nova-prod-01")
	mustScenario(t, r, "cluster create")
	wantContains(t, r.Stderr, "unknown service 'dat'; did you mean data?", "this group needs 3–27 nodes",
		"a group already runs data", "unknown service 'cache'", "this group needs 2–24 nodes")
	var got []any
	for _, g := range groupsOf(fileCluster(t, s, "nova-prod-01")) {
		got = append(got, g["services"])
	}
	wantJSON(t, "services", got, [][]string{{"data"}, {"search"}})
}

func TestClusterCreate_CAP051_AC6_OneGroupKeepsQuestions(t *testing.T) {
	s := importedScenario(t)
	r := runWizard(t, s, []string{"provisioned", "aws", "custom", "one group", "3", "8", "32", "", "", "", "", "", ""},
		"nova-qa-01")
	mustScenario(t, r, "cluster create")
	if !strings.Contains(r.Stderr, "vCPUs per node") || strings.Contains(r.Stderr, "Node size") {
		t.Errorf("one group asks vCPUs per node, not the size menu:\n%s", r.Stderr)
	}
	wantJSON(t, "compute", groupsOf(fileCluster(t, s, "nova-qa-01"))[0]["node"].(map[string]any)["compute"],
		map[string]int{"cpu": 8, "ram": 32})
}

func TestClusterCreate_CAP051_AC6_DefaultIsUnusedServices(t *testing.T) {
	s := importedScenario(t)
	r := runWizard(t, s, []string{"provisioned", "aws", "custom", "multiple",
		"data", "3", "4", "", "", "", "y",
		"index,query", "2", "1", "", "", "", "y",
		"", "2", "1", "", "", "", // Enter: one group with every service not used yet
		"n", "", "", ""}, "nova-prod-01")
	mustScenario(t, r, "cluster create")
	wantContains(t, r.Stderr, "Services [data,index,query,search,analytics,eventing]:",
		"Services [search,analytics,eventing]:")
	if !strings.Contains(r.Stderr, "Services [index,query,search,analytics,eventing]:") {
		t.Errorf("second group default:\n%s", r.Stderr)
	}
	groups := groupsOf(fileCluster(t, s, "nova-prod-01"))
	wantJSON(t, "group 3 services", groups[2]["services"], []string{"analytics", "eventing", "search"})
}

func TestClusterCreate_CAP051_AC9_InteractiveOffersKnownSizes(t *testing.T) {
	s := importedScenario(t)
	r := runWizard(t, s, []string{"provisioned", "aws", "custom", "multiple groups", "data", "3", "18", "6", "30",
		"", "", "", "n", "", "", ""}, "nova-prod-01")
	mustScenario(t, r, "cluster create")
	wantContains(t, r.Stderr, " 1) 4/16", "17) 64/512", "18) other")
	menu := strings.SplitN(strings.SplitN(r.Stderr, "Node size", 2)[1], "Choose", 2)[0]
	if strings.Contains(menu, "2/8") {
		t.Errorf("the single-node size 2/8 is offered for a group:\n%s", menu)
	}
	wantJSON(t, "compute", groupsOf(fileCluster(t, s, "nova-prod-01"))[0]["node"].(map[string]any)["compute"],
		map[string]int{"cpu": 6, "ram": 30})
}

func TestDiskTypes_CAP060_AC2_CreatePicker(t *testing.T) {
	for _, tc := range []struct {
		provider string
		answers  []string
		disk     map[string]any
		shown    string
	}{
		{"aws", []string{"2", "200", ""}, map[string]any{"type": "io2", "storage": 200, "iops": 5740}, "IOPS (3000–64000; recommended 5740"},
		{"azure", []string{"3"}, map[string]any{"type": "P15"}, "P15: 256 GB, 1100 IOPS (set by the type)"},
		{"gcp", []string{"1", "100"}, map[string]any{"type": "pd-ssd", "storage": 100}, "up to 3000 read and 3000 write IOPS"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			s := importedScenario(t)
			answers := append([]string{"provisioned", tc.provider, "custom", "one group", "3", "8", "32"}, tc.answers...)
			r := runWizard(t, s, append(answers, "", "", ""), "nova-qa-01")
			mustScenario(t, r, "cluster create")
			wantContains(t, r.Stderr, "Disk type:", tc.shown)
			if strings.Index(r.Stderr, "Disk type:") < strings.Index(r.Stderr, "RAM GB per node") {
				t.Errorf("the disk is asked before the node size:\n%s", r.Stderr)
			}
			posts := clusterPosts(s)
			if len(posts) != 1 {
				t.Fatalf("cluster POSTs = %d, want 1", len(posts))
			}
			g := posts[0]["serviceGroups"].([]any)[0].(map[string]any)
			wantJSON(t, "disk", g["node"].(map[string]any)["disk"], tc.disk)
		})
	}
}

func TestDiskTypes_CAP060_AC2_GroupAddPicker(t *testing.T) {
	s := importedScenario(t)
	s.TTY(true)
	s.Answers("y")
	r := s.RunWith("2\n2\n100\n\n", "capella", "cluster", "group", "add", "nova-dev-01", "--services", "search")
	mustScenario(t, r, "group add")
	wantContains(t, r.Stderr, "Disk type:", "Choose [1]") // the data group's gp3 is the default
	puts := clusterPuts(s)
	if len(puts) != 1 {
		t.Fatalf("PUTs = %d, want 1", len(puts))
	}
	groups := puts[0].Body.(map[string]any)["serviceGroups"].([]any)
	wantJSON(t, "new group disk", groups[1].(map[string]any)["node"].(map[string]any)["disk"],
		map[string]any{"type": "io2", "storage": 100, "iops": 4370})
}

func TestPrompts_CAP042_AC2_AskUsesDefaultOrNamesFlag(t *testing.T) {
	s := importedScenario(t)
	// Without a terminal an optional value takes its default and stdin is never read.
	r := s.RunReader(context.Background(), clitest.ForbiddenStdin(t, "bucket add"),
		"capella", "bucket", "add", "nova-dev-01", "orders", "-y")
	mustScenario(t, r, "bucket add")
	if b := writesTo(s, "POST", "/buckets"); len(b) != 1 || b[0].Body.(map[string]any)["memoryAllocationInMb"] != float64(1024) {
		t.Errorf("bucket POST = %v, want the default 1024 MB", b)
	}
	// A required value names its flag.
	r = s.RunReader(context.Background(), clitest.ForbiddenStdin(t, "group add"),
		"capella", "cluster", "group", "add", "nova-dev-01", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "missing --services: there is no terminal to ask for it", "--services")
}

func TestDiskTypes_CAP060_AC6_Help(t *testing.T) {
	s := newScenario(t)
	for _, args := range [][]string{{"cluster", "create"}, {"cluster", "update"}, {"cluster", "group", "add"}} {
		r := mustScenario(t, s.Run(append(append([]string{"capella"}, args...), "--help")...), "help")
		if !strings.Contains(strings.Join(strings.Fields(r.Stdout), " "), "cbctl capella cluster disk-types") {
			t.Errorf("%v --help does not point to 'cbctl capella cluster disk-types':\n%s", args, r.Stdout)
		}
	}
	r := mustScenario(t, s.Run("capella", "guide", "scaling"), "guide")
	wantContains(t, r.Stdout, "Disk types", "cbctl capella cluster disk-types")
}
