package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// Ports of capellactl's tests/scenario/test_multi_project.py (CAP-040).

// singleProjectYAML is capellactl's MINIMAL (the single-project form), matching seedHealthy.
const singleProjectYAML = `apiVersion: cbctl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: nova-dev
  description: Nova development environment
clusters:
  - name: nova-dev-01
    description: Nova development cluster
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.0.0.0/20 }
    couchbaseServer: { version: 7.6.2 }
    availability: { type: multi }
    support: { plan: basic, timezone: ET }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 3
        services: [data, index, query]
`

// seedTwoProjects mirrors capellactl's _two_projects: nova-dev/nova-dev-01 and non-prod/np-01.
func seedTwoProjects(s *clitest.Scenario) (demo, np string) {
	demo, _ = seedHealthy(s)
	np = s.Fake.SeedProject("non-prod", capellafake.WithField("description", "Non-production"))
	s.Fake.SeedCluster(np, "np-01", "healthy")
	return demo, np
}

// seedSameNamed seeds projects demo and non-prod, each with a cluster named main.
func seedSameNamed(s *clitest.Scenario) {
	demo, np := s.Fake.SeedProject("demo"), s.Fake.SeedProject("non-prod")
	s.Fake.SeedCluster(demo, "main", "healthy")
	s.Fake.SeedCluster(np, "main", "healthy")
}

func firstGroupNodes(t *testing.T, s *clitest.Scenario, project string) any {
	t.Helper()
	cl := docClusters(t, s, project)
	if len(cl) == 0 {
		t.Fatalf("no cluster in project %s", project)
	}
	return asMaps(cl[0]["serviceGroups"])[0]["numOfNodes"]
}

// TestMultiProject_CAP040_AC2_LegacyStillPlans: the single-project form validates and plans.
func TestMultiProject_CAP040_AC2_LegacyStillPlans(t *testing.T) {
	s := newScenario(t)
	seedHealthy(s)
	writeCapellaYAML(t, s, singleProjectYAML)
	mustScenario(t, s.Run("capella", "validate"), "validate")
	wantContains(t, planClean(t, s).Stdout, "nova-dev")
}

// TestMultiProject_CAP040_AC2_ConvertedOnFirstWrite: the first managed edit rewrites the file
// in the projects: form; the backup keeps the old form and the plan is clean afterwards.
func TestMultiProject_CAP040_AC2_ConvertedOnFirstWrite(t *testing.T) {
	s := newScenario(t)
	seedHealthy(s)
	writeCapellaYAML(t, s, singleProjectYAML)
	mustScenario(t, s.Run("capella", "cluster", "scale", "nova-dev-01", "--nodes", "4", "-y"), "cluster scale")
	text := s.ReadFile("capella.yaml")
	if !strings.Contains(text, "projects:") || strings.Contains(text, "\nproject:") {
		t.Errorf("file not converted to the projects: form:\n%s", text)
	}
	if n := firstGroupNodes(t, s, ""); n != 4 {
		t.Errorf("numOfNodes = %v, want 4", n)
	}
	wantContains(t, s.ReadFile("capella.yaml.bak"), "\nproject:")
	planClean(t, s)
}

// TestMultiProject_CAP040_AC3_PlanApplyTwoProjects: a plan over two projects names both, creates
// the cluster in the second, and a second apply writes nothing.
func TestMultiProject_CAP040_AC3_PlanApplyTwoProjects(t *testing.T) {
	s := newScenario(t)
	seedHealthy(s)
	syncOK(t, s)
	mustScenario(t, s.Run("capella", "project", "create", "non-prod", "--no-apply", "-y"), "project create")
	mustScenario(t, s.Run("capella", "cluster", "create", "np-01", "--project", "non-prod", "--size", "dev",
		"--region", "us-west-2", "--no-apply", "-y"), "cluster create --no-apply")
	r := s.Run("capella", "plan")
	wantContains(t, r.Stdout, "Projects nova-dev, non-prod", "= project nova-dev", "+ project non-prod", "+ cluster np-01")
	mustScenario(t, s.Run("capella", "apply", "-y", "--no-wait"), "apply")
	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "apply", "-y"), "apply again")
	wantContains(t, r.Stdout, "No changes")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestMultiProject_CAP040_AC3_TargetQualifiedCluster: --target cluster/PROJECT/NAME picks one of
// two same-named clusters.
func TestMultiProject_CAP040_AC3_TargetQualifiedCluster(t *testing.T) {
	s := newScenario(t)
	seedSameNamed(s)
	syncOK(t, s)
	text := s.ReadFile("capella.yaml")
	i := strings.Index(text, "name: non-prod")
	writeCapellaYAML(t, s, text[:i]+strings.Replace(text[i:], "numOfNodes: 3", "numOfNodes: 4", 1))
	r := s.Run("capella", "plan", "--target", "cluster/non-prod/main")
	wantContains(t, r.Stdout, "~ cluster main")
	if n := strings.Count(r.Stdout, "cluster main"); n != 1 {
		t.Errorf("%d 'cluster main' lines, want 1:\n%s", n, r.Stdout)
	}
	r = s.Run("capella", "plan", "--target", "cluster/demo/main")
	wantNotContains(t, r.Stdout, "~ cluster main")
}

// TestMultiProject_CAP040_AC5_SyncAllProjects: a first sync records every project in order,
// with the counts, and plans clean.
func TestMultiProject_CAP040_AC5_SyncAllProjects(t *testing.T) {
	s := newScenario(t)
	seedTwoProjects(s)
	s.Fake.SeedProject("sandbox")
	r := syncOK(t, s)
	wantContains(t, r.Stdout, "Imported 3 projects, 2 clusters")
	wantNames(t, "projects", docProjects(t, s), "nova-dev", "non-prod", "sandbox")
	planClean(t, s)
}

// TestMultiProject_CAP040_AC6_SyncProjectThenAll: sync --project refreshes only that project; a
// plain sync then refreshes the rest.
func TestMultiProject_CAP040_AC6_SyncProjectThenAll(t *testing.T) {
	s := newScenario(t)
	_, devCID := seedHealthy(s)
	np := s.Fake.SeedProject("non-prod", capellafake.WithField("description", "Non-production"))
	npCID := s.Fake.SeedCluster(np, "np-01", "healthy")
	syncOK(t, s)
	s.Fake.SetClusterField(devCID, "description", "changed nova-dev-01")
	s.Fake.SetClusterField(npCID, "description", "changed np-01")
	syncOK(t, s, "--project", "non-prod")
	if d := docCluster(t, s, "", "np-01")["description"]; d != "changed np-01" {
		t.Errorf("np-01 description = %v", d)
	}
	if d := docCluster(t, s, "", "nova-dev-01")["description"]; d == "changed nova-dev-01" {
		t.Error("sync --project non-prod refreshed nova-dev")
	}
	syncOK(t, s)
	if d := docCluster(t, s, "", "nova-dev-01")["description"]; d != "changed nova-dev-01" {
		t.Errorf("nova-dev-01 description = %v after a full sync", d)
	}
	wantContains(t, syncOK(t, s).Stdout, "already in sync")
}

// TestMultiProject_CAP040_AC7_ClusterCreateProjectChoice: with several projects, cluster create
// without --project exits 3 with no terminal; on a terminal it asks and writes the cluster under
// the chosen project.
func TestMultiProject_CAP040_AC7_ClusterCreateProjectChoice(t *testing.T) {
	s := newScenario(t)
	seedTwoProjects(s)
	syncOK(t, s)
	r := s.Run("capella", "cluster", "create", "new-01", "--size", "dev", "--region", "us-west-2", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "declares several projects", "add --project NAME (nova-dev, non-prod)")

	s.TTY(true)
	r = mustScenario(t, s.RunWith("2\n", "capella", "cluster", "create", "new-01", "--size", "dev", "--region", "us-west-2",
		"--no-apply", "-y"), "cluster create on a terminal")
	wantContains(t, r.Stderr, "1. nova-dev", "2. non-prod", "Project of the new cluster")
	wantNames(t, "non-prod clusters", docClusters(t, s, "non-prod"), "np-01", "new-01")
}

// TestMultiProject_CAP040_AC7_SameClusterNameNeedsProject: a cluster name in two projects needs
// --project; with it, only that project's entry changes.
func TestMultiProject_CAP040_AC7_SameClusterNameNeedsProject(t *testing.T) {
	s := newScenario(t)
	seedSameNamed(s)
	syncOK(t, s)
	r := s.Run("capella", "cluster", "scale", "main", "--nodes", "4", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "main", "demo, non-prod", "--project")
	mustScenario(t, s.Run("capella", "cluster", "scale", "main", "--nodes", "4", "--project", "non-prod", "-y"), "scale --project")
	if n := firstGroupNodes(t, s, "non-prod"); n != 4 {
		t.Errorf("non-prod main numOfNodes = %v, want 4", n)
	}
	if n := firstGroupNodes(t, s, "demo"); n != 3 {
		t.Errorf("demo main numOfNodes = %v, want 3", n)
	}
	wantContains(t, mustScenario(t, s.Run("capella", "cluster", "get", "main", "--project", "demo"), "cluster get").Stdout, "main")
}

// TestMultiProject_CAP040_AC7_ClusterListEveryProject: cluster list covers every project, sorted
// by project, with --project filtering and projectName in JSON.
func TestMultiProject_CAP040_AC7_ClusterListEveryProject(t *testing.T) {
	s := newScenario(t)
	seedTwoProjects(s)
	syncOK(t, s)
	r := mustScenario(t, s.Run("capella", "cluster", "list"), "cluster list")
	wantNotContains(t, r.Stdout+r.Stderr, "pass --project")
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "np-01") || !strings.Contains(lines[2], "nova-dev-01") {
		t.Errorf("cluster list not ordered by project:\n%s", r.Stdout)
	}
	r = mustScenario(t, s.Run("capella", "cluster", "list", "--project", "non-prod"), "cluster list --project")
	if l := strings.Split(strings.TrimSpace(r.Stdout), "\n"); len(l) != 2 || !strings.Contains(l[1], "np-01") {
		t.Errorf("cluster list --project non-prod:\n%s", r.Stdout)
	}
	r = mustScenario(t, s.Run("capella", "cluster", "list", "-o", "json"), "cluster list -o json")
	var data []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &data); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r.Stdout)
	}
	if len(data) != 2 || data[0]["projectName"] != "non-prod" || data[0]["name"] != "np-01" ||
		data[1]["projectName"] != "nova-dev" || data[1]["name"] != "nova-dev-01" {
		t.Errorf("cluster list JSON = %v", data)
	}
}

// TestMultiProject_CAP040_AC8_ProjectListInFile: project list marks the projects capella.yaml
// records.
func TestMultiProject_CAP040_AC8_ProjectListInFile(t *testing.T) {
	s := newScenario(t)
	seedTwoProjects(s)
	s.Fake.SeedProject("sandbox")
	syncOK(t, s, "--project", "nova-dev", "--project", "non-prod")
	r := mustScenario(t, s.Run("capella", "project", "list"), "project list")
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	wantContains(t, lines[0], "IN FILE")
	got := map[string]string{}
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		got[f[0]] = f[len(f)-1]
	}
	want := map[string]string{"nova-dev": "yes", "non-prod": "yes", "sandbox": "-"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("project list IN FILE = %v, want %v\n%s", got, want, r.Stdout)
			break
		}
	}
}

// bumpNodes sets numOfNodes to n for the first cluster after "name: project" in capella.yaml.
func bumpNodes(t *testing.T, s *clitest.Scenario, project string, n string) {
	t.Helper()
	text := s.ReadFile("capella.yaml")
	i := strings.Index(text, "name: "+project+"\n")
	if i < 0 {
		t.Fatalf("project %s not in capella.yaml", project)
	}
	writeCapellaYAML(t, s, text[:i]+strings.Replace(text[i:], "numOfNodes: 3", "numOfNodes: "+n, 1))
}

// TestMultiProject_CAP040_AC3_DriftAcrossProjects: drift reads every project and reports only the
// cluster changed in Capella, under its own project.
func TestMultiProject_CAP040_AC3_DriftAcrossProjects(t *testing.T) {
	s := newScenario(t)
	seedHealthy(s)
	np := s.Fake.SeedProject("non-prod")
	npCID := s.Fake.SeedCluster(np, "np-01", "healthy", capellafake.WithField("description", "np"))
	syncOK(t, s)
	wantContains(t, mustScenario(t, s.Run("capella", "drift", "--detailed-exitcode"), "drift (clean)").Stdout, "No drift")

	s.Fake.SetClusterField(npCID, "description", "changed in the UI")
	s.Fake.ResetCalls()
	r := s.Run("capella", "drift", "--detailed-exitcode")
	wantExit(t, r, cli.ExitChanges)
	wantContains(t, r.Stdout, "~ cluster np-01", "description")
	wantNotContains(t, r.Stdout, "~ cluster nova-dev-01")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestMultiProject_CAP040_AC3_ApplyTargetOneProject: apply --target cluster/PROJECT/NAME changes
// only that project's cluster; the other project's pending change stays in the plan.
func TestMultiProject_CAP040_AC3_ApplyTargetOneProject(t *testing.T) {
	s := newScenario(t)
	seedSameNamed(s)
	syncOK(t, s)
	bumpNodes(t, s, "demo", "4")
	bumpNodes(t, s, "non-prod", "5")
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "apply", "--target", "cluster/non-prod/main", "-y"), "apply --target")
	npID := projectIDByName(t, s, "non-prod")
	writes := s.Fake.Writes()
	if len(writes) == 0 {
		t.Fatal("apply --target sent nothing")
	}
	for _, c := range writes {
		if !strings.Contains(c.Path, "/projects/"+npID+"/") {
			t.Errorf("apply --target cluster/non-prod/main wrote outside non-prod: %s %s", c.Method, c.Path)
		}
	}
	r := s.Run("capella", "plan", "--detailed-exitcode")
	wantExit(t, r, cli.ExitChanges)
	wantContains(t, r.Stdout, "~ cluster main", "3 → 4")
	if strings.Count(r.Stdout, "~ cluster main") != 1 {
		t.Errorf("want only demo's main pending:\n%s", r.Stdout)
	}
}

// TestMultiProject_CAP040_AC6_SyncProjectFilterLeavesOthers: sync --project refreshes one project
// of a two-project file; the other keeps its old value and plan then shows only that difference.
func TestMultiProject_CAP040_AC6_SyncProjectFilterLeavesOthers(t *testing.T) {
	s := newScenario(t)
	_, devCID := seedHealthy(s)
	np := s.Fake.SeedProject("non-prod")
	npCID := s.Fake.SeedCluster(np, "np-01", "healthy", capellafake.WithField("description", "np"))
	syncOK(t, s)
	s.Fake.SetClusterField(devCID, "description", "dev changed")
	s.Fake.SetClusterField(npCID, "description", "np changed")
	r := syncOK(t, s, "--project", "nova-dev")
	wantContains(t, r.Stdout, "+        description: dev changed")
	wantNotContains(t, r.Stdout, "np changed")
	r = s.Run("capella", "plan", "--detailed-exitcode")
	wantExit(t, r, cli.ExitChanges)
	wantContains(t, r.Stdout, "~ cluster np-01")
	wantNotContains(t, r.Stdout, "~ cluster nova-dev-01")
}

func projectIDByName(t *testing.T, s *clitest.Scenario, name string) string {
	t.Helper()
	ps, err := s.Fake.Client().ListProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("project %s not in Capella", name)
	return ""
}
