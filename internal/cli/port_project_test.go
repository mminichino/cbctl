package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/settings"
)

// Ports of capellactl's project, project create/delete, configure, configure check,
// deletion protection and allowed CIDR scenario tests (area "project").

// portNovaDev is capellactl's MINIMAL fixture: one project with one cluster.
const portNovaDev = `apiVersion: capellactl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: nova-dev
  description: Nova development environment
clusters:
  - name: nova-dev-01
    description: Nova development cluster
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.10.0.0/24 }
    availability: { type: multi }
    support: { plan: developer pro, timezone: PT }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 3
        services: [data, index, query]
`

// portProjectOnly declares the project and no clusters.
const portProjectOnly = `apiVersion: capellactl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: nova-dev
  description: Nova development environment
`

// portSeedNovaDev seeds the Capella state that matches portNovaDev.
func portSeedNovaDev(f *capellafake.Fake, fields ...capellafake.Field) (pid, cid string) {
	pid = f.SeedProject("nova-dev", capellafake.WithField("description", "Nova development environment"))
	base := []capellafake.Field{
		capellafake.WithField("description", "Nova development cluster"),
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.10.0.0/24"}),
		capellafake.WithField("support", map[string]any{"plan": "developer pro", "timezone": "PT"}),
	}
	cid = f.SeedCluster(pid, "nova-dev-01", "healthy", append(base, fields...)...)
	return pid, cid
}

func portOut(r clitest.Result) string { return r.Stdout + r.Stderr }

func portWantExit(t *testing.T, r clitest.Result, code int) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.Code, code, r.Stdout, r.Stderr)
	}
}

func portLacks(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(s, p) {
			t.Errorf("output contains %q:\n%s", p, s)
		}
	}
}

func portBody(t *testing.T, c capellafake.Call) map[string]any {
	t.Helper()
	m, ok := c.Body.(map[string]any)
	if !ok {
		t.Fatalf("%s: body is not a JSON object: %v", c, c.Body)
	}
	return m
}

func portWantWritePaths(t *testing.T, f *capellafake.Fake, want ...string) []capellafake.Call {
	t.Helper()
	w := f.Writes()
	if len(w) != len(want) {
		t.Fatalf("writes = %v, want %d (%v)", w, len(want), want)
	}
	for i, glob := range want {
		method, pat, _ := strings.Cut(glob, " ")
		if w[i].Method != method || !portMatch(pat, w[i].Path) {
			t.Errorf("write %d = %s %s, want %s", i, w[i].Method, w[i].Path, glob)
		}
	}
	return w
}

// portMatch matches a path against a glob where * stands for one path segment.
func portMatch(glob, p string) bool {
	g, s := strings.Split(glob, "/"), strings.Split(p, "/")
	if len(g) != len(s) {
		return false
	}
	for i := range g {
		if g[i] != "*" && g[i] != s[i] {
			return false
		}
	}
	return true
}

func portProjects(t *testing.T, s *clitest.Scenario) []map[string]any {
	t.Helper()
	r := s.Run("capella", "project", "list", "-o", "json")
	portWantExit(t, r, 0)
	var list []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &list); err != nil {
		t.Fatalf("project list -o json: %v\n%s", err, r.Stdout)
	}
	return list
}

func portProjectNames(t *testing.T, s *clitest.Scenario) []string {
	t.Helper()
	var names []string
	for _, p := range portProjects(t, s) {
		names = append(names, p["name"].(string))
	}
	return names
}

// --- tests/scenario/test_project.py (CAP-009) ---

func TestProject_CAP009_AC1_CreateWhenMissing(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, portNovaDev)
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "+ project nova-dev")
	w := portWantWritePaths(t, s.Fake,
		"POST /v4/organizations/org-1/projects",
		"POST /v4/organizations/org-1/projects/*/clusters")
	body := portBody(t, w[0])
	if body["name"] != "nova-dev" || body["description"] != "Nova development environment" || len(body) != 2 {
		t.Errorf("project create body = %v", body)
	}
}

func TestProject_CAP009_AC2_UpdateDescriptionWithIfMatch(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("nova-dev", capellafake.WithField("description", "old"))
	writeCapellaYAML(t, s, portProjectOnly)
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "~ project nova-dev", `description:  "old" → "Nova development environment"`)
	w := portWantWritePaths(t, s.Fake, "PUT /v4/organizations/org-1/projects/*")
	body := portBody(t, w[0])
	if body["name"] != "nova-dev" || body["description"] != "Nova development environment" {
		t.Errorf("project update body = %v", body)
	}
	if w[0].Header.Get("If-Match") == "" {
		t.Errorf("project update sent no If-Match header: %v", w[0].Header)
	}
}

func TestProject_CAP009_AC3_OmittedDescriptionUntouched(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("nova-dev", capellafake.WithField("description", "kept"))
	writeCapellaYAML(t, s, "apiVersion: capellactl/v1\norganizationId: ${CAPELLA_ORG_ID}\nproject:\n  name: nova-dev\n")
	r := s.Run("capella", "plan", "--detailed-exitcode")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "= project nova-dev")
	r = s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	clitest.AssertNoWrites(t, s.Fake)
}

func TestProject_CAP009_AC4_AmbiguousBlocked(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("nova-dev")
	s.Fake.SeedProject("nova-dev")
	writeCapellaYAML(t, s, portProjectOnly)
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 4)
	wantContains(t, portOut(r), "cbctl capella sync --force")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestProject_CAP009_AC5_PinnedIDRename(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-old", capellafake.WithField("description", "Nova development environment"))
	writeCapellaYAML(t, s, strings.Replace(portProjectOnly, "  name: nova-dev\n", "  name: nova-dev\n  id: "+pid+"\n", 1))
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, `name:  "nova-old" → "nova-dev"`)
	if got := portProjectNames(t, s); len(got) != 1 || got[0] != "nova-dev" {
		t.Errorf("projects after rename = %v, want [nova-dev]", got)
	}
}

func TestProject_CAP009_AC5_PinnedIDMissingNeverCreates(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, strings.Replace(portProjectOnly, "  name: nova-dev\n", "  name: nova-dev\n  id: p-404\n", 1))
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 1)
	wantContains(t, portOut(r), "p-404", "not found")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestProjectApply_TargetsProjectOnly(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, portNovaDev)
	r := s.Run("capella", "project", "apply", "nova-dev", "-y")
	portWantExit(t, r, 0)
	portWantWritePaths(t, s.Fake, "POST /v4/organizations/org-1/projects")
	portLacks(t, r.Stdout, "+ cluster nova-dev-01")
}

func TestProject_CAP009_AC6_ListTableRows(t *testing.T) {
	s := newScenario(t)
	ids := map[string]string{}
	for _, n := range []string{"alpha", "bravo", "charlie"} {
		ids[n] = s.Fake.SeedProject(n, capellafake.WithField("description", n+" project"))
	}
	r := s.Run("capella", "project", "list")
	portWantExit(t, r, 0)
	lines := r.Lines()
	if len(lines) != 4 {
		t.Fatalf("project list lines = %d, want header + 3:\n%s", len(lines), r.Stdout)
	}
	wantContains(t, lines[0], "NAME", "ID", "DESCRIPTION")
	for i, n := range []string{"alpha", "bravo", "charlie"} {
		wantContains(t, lines[i+1], n, ids[n], n+" project")
	}
	if got := portProjectNames(t, s); strings.Join(got, ",") != "alpha,bravo,charlie" {
		t.Errorf("project list -o json names = %v", got)
	}
}

// --- tests/scenario/test_project_create.py (CAP-039) ---

const portEmptyFile = "apiVersion: cbctl/v1\nprojects: []\n"

func TestProjectCreate_CAP039_AC1_Interactive(t *testing.T) {
	t.Run("direct asks the description", func(t *testing.T) {
		s := newScenario(t)
		s.TTY(true)
		r := s.RunWith("QA environment\n", "capella", "project", "create", "nova-qa")
		portWantExit(t, r, 0)
		w := portWantWritePaths(t, s.Fake, "POST /v4/organizations/org-1/projects")
		if b := portBody(t, w[0]); b["name"] != "nova-qa" || b["description"] != "QA environment" {
			t.Errorf("body = %v", b)
		}
		if _, err := os.Stat(filepath.Join(s.Dir, "capella.yaml")); err == nil {
			t.Error("direct project create wrote capella.yaml")
		}
	})
	t.Run("managed asks the description and confirms", func(t *testing.T) {
		s := newScenario(t)
		writeCapellaYAML(t, s, portEmptyFile)
		s.TTY(true)
		s.Answers("y")
		r := s.RunWith("QA environment\n", "capella", "project", "create", "nova-qa")
		portWantExit(t, r, 0)
		wantContains(t, r.Stdout, "+ project nova-qa")
		w := portWantWritePaths(t, s.Fake, "POST /v4/organizations/org-1/projects")
		if b := portBody(t, w[0]); b["description"] != "QA environment" {
			t.Errorf("body = %v", b)
		}
		wantContains(t, s.ReadFile("capella.yaml"), "name: nova-qa", "description: QA environment")
	})
}

func TestProjectCreate_CAP039_AC2_NeedsAutoApprove(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, portEmptyFile)
	r := s.Run("capella", "project", "create", "nova-qa", "--description", "QA")
	portWantExit(t, r, 4)
	wantContains(t, portOut(r), "--yes")
	clitest.AssertNoWrites(t, s.Fake)
	if got := s.ReadFile("capella.yaml"); got != portEmptyFile {
		t.Errorf("capella.yaml changed without approval:\n%s", got)
	}
}

func TestProjectCreate_CAP039_AC3_ExistingName(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("nova-qa")
	r := s.Run("capella", "project", "create", "nova-qa", "--description", "QA")
	portWantExit(t, r, 4)
	wantContains(t, portOut(r), "project nova-qa already exists", "cbctl capella sync --project nova-qa")
	clitest.AssertNoWrites(t, s.Fake)
	if _, err := os.Stat(filepath.Join(s.Dir, "capella.yaml")); err == nil {
		t.Error("refused create wrote capella.yaml")
	}
}

func TestProjectCreate_CAP039_AC4_AddsToExistingFile(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	mustScenario(t, s.Run("capella", "sync"), "sync")
	before := s.ReadFile("capella.yaml")
	r := s.Run("capella", "project", "create", "nova-qa", "--description", "QA", "-y")
	portWantExit(t, r, 0)
	after := s.ReadFile("capella.yaml")
	if !strings.Contains(s.ReadFile("capella.yaml.bak"), "name: nova-dev-01") {
		t.Error("capella.yaml.bak does not keep the earlier projects")
	}
	if !strings.HasPrefix(after, strings.TrimSuffix(before, "\n")) {
		t.Errorf("the first project changed:\n--- before\n%s\n--- after\n%s", before, after)
	}
	dev, qa := strings.Index(after, "name: nova-dev"), strings.Index(after, "name: nova-qa")
	if dev < 0 || qa < 0 || dev > qa {
		t.Errorf("nova-qa not appended after nova-dev:\n%s", after)
	}
	if !strings.Contains(after, "name: nova-dev-01") {
		t.Errorf("existing cluster lost:\n%s", after)
	}

	// --config names another file; it is edited instead of ./capella.yaml.
	r = s.Run("capella", "--config", "other.yaml", "project", "create", "nova-perf", "--description", "perf", "-y")
	portWantExit(t, r, 0)
	wantContains(t, s.ReadFile("other.yaml"), "name: nova-perf")
	portLacks(t, s.ReadFile("capella.yaml"), "nova-perf")

	// A refused create leaves no new file behind.
	r = s.Run("capella", "--config", "refused.yaml", "project", "create", "nova-x", "--description", "x")
	portWantExit(t, r, 4)
	if _, err := os.Stat(filepath.Join(s.Dir, "refused.yaml")); err == nil {
		t.Error("refused create left refused.yaml behind")
	}
}

func TestProjectCreate_CAP039_AC5_ThenClusterCreate(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, portEmptyFile)
	portWantExit(t, s.Run("capella", "project", "create", "nova-qa", "--description", "QA", "-y"), 0)
	portWantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
	r := s.Run("capella", "cluster", "create", "nova-qa-01", "--project", "nova-qa", "--size", "small", "-y")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "nova-qa-01")
	if got := s.Fake.Calls("POST", "/v4/organizations/org-1/projects/*/clusters"); len(got) != 1 {
		t.Errorf("cluster creates = %d, want 1", len(got))
	}
	wantContains(t, s.ReadFile("capella.yaml"), "name: nova-qa-01")
	r = s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "No changes. Capella matches capella.yaml.")
}

func TestProjectCreate_CAP039_AC6_HelpAndReadme(t *testing.T) {
	s := newScenario(t)
	r := s.Run("capella", "project", "--help")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "create", "delete", "list")
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantContains(t, string(readme), "cbctl capella project create")
}

// --- tests/scenario/test_project_delete.py (CAP-010) ---

// portTwoProjects is capellactl's delete fixture: nova-dev (with a cluster) and an empty non-prod.
func portTwoProjects(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	s.Fake.SeedProject("non-prod")
	return s
}

func TestProjectDelete_CAP010_AC2_AbsentNoop(t *testing.T) {
	s := portTwoProjects(t)
	r := s.Run("capella", "project", "delete", "nope", "-y")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "project nope not found; nothing to do")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestProjectDelete_CAP010_AC5_WrongNameCancels(t *testing.T) {
	s := portTwoProjects(t)
	s.TTY(true)
	s.Answers("non-pord")
	r := s.Run("capella", "project", "delete", "non-prod")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "nothing was deleted")
	clitest.AssertNoWrites(t, s.Fake)

	s.TTY(false)
	r = s.Run("capella", "project", "delete", "non-prod")
	portWantExit(t, r, 4)
	clitest.AssertNoWrites(t, s.Fake)
	if got := portProjectNames(t, s); len(got) != 2 {
		t.Errorf("projects = %v, want both kept", got)
	}
}

func TestProjectDelete_CAP010_AC6_API409Message(t *testing.T) {
	s := portTwoProjects(t)
	s.Fake.FailNextOpts("DELETE", "/v4/organizations/*/projects/*", capellafake.FaultOptions{Status: 409, Code: 1010, Message: "project is not empty"})
	r := s.Run("capella", "project", "delete", "non-prod", "-y")
	portWantExit(t, r, 1)
	wantContains(t, portOut(r), "non-prod")
	if !strings.Contains(portOut(r), "not empty") {
		t.Errorf("409 not explained:\n%s", portOut(r))
	}
}

func TestProjectDelete_CAP010_AC7_RerunNoop(t *testing.T) {
	s := portTwoProjects(t)
	portWantExit(t, s.Run("capella", "project", "delete", "non-prod", "-y"), 0)
	s.Fake.ResetLog()
	r := s.Run("capella", "project", "delete", "non-prod", "-y")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "project non-prod not found; nothing to do")
	clitest.AssertNoWrites(t, s.Fake)
}

// --- tests/scenario/test_configure.py (CAP-037) ---

// portNoEnvAuth clears the env credentials so configure and the commands after it use profiles.
func portNoEnvAuth(s *clitest.Scenario) {
	s.Env["CAPELLA_API_KEY"] = ""
	s.Env["CAPELLA_ORG_ID"] = ""
}

func TestConfigure_CAP037_AC1_SavesProfileAndKeychain(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	org2 := s.Fake.AddOrganization("Nova Labs")
	s.TTY(true)
	s.Answers("n") // do not discover projects now
	r := s.RunWith(s.Fake.APIKey+"\n2\n", "configure")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), `Saved profile "default" (organization Nova Labs `+org2+`)`, "Later: cbctl capella sync")
	got, err := s.Keyring.Get("default")
	if err != nil || got != s.Fake.APIKey {
		t.Errorf("keychain default = %q, %v", got, err)
	}
	path := filepath.Join(s.Dir, "profiles.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantContains(t, string(raw), org2)
	portLacks(t, string(raw), s.Fake.APIKey)
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("profiles.yaml mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	calls := s.Fake.Calls("GET", "/v4/organizations")
	if len(calls) == 0 || calls[0].Header.Get("Authorization") != "Bearer "+s.Fake.APIKey {
		t.Errorf("organizations call = %v", calls)
	}
}

func TestConfigure_CAP037_AC1_KeyNotEchoed(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin")
	portWantExit(t, r, 0)
	clitest.AssertNoSecret(t, r, s.Fake.APIKey)
	portLacks(t, s.ReadFile("profiles.yaml"), s.Fake.APIKey)
}

func TestConfigure_CAP037_AC3_ShowSources(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portWantExit(t, s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin"), 0)
	r := s.Run("configure", "show")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "org-1", "profile default", "keychain")
	clitest.AssertNoSecret(t, r, s.Fake.APIKey)

	s.Env["CAPELLA_ORG_ID"] = "org-env"
	r = s.Run("configure", "show")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "org-env", "CAPELLA_ORG_ID")
}

func TestConfigure_CAP037_AC6_List(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portWantExit(t, s.RunWith(s.Fake.APIKey+"\n", "configure", "--profile", "dev", "--api-key-stdin"), 0)
	r := s.Run("configure", "list")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "dev", "org-1")
	clitest.AssertNoSecret(t, r, s.Fake.APIKey)
}

func TestConfigure_CAP037_AC6_RemoveProfileAndKey(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portWantExit(t, s.RunWith(s.Fake.APIKey+"\n", "configure", "--profile", "dev", "--api-key-stdin"), 0)
	r := s.Run("configure", "remove", "--profile", "dev", "-y")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), `Removed profile "dev" (keychain entry deleted)`)
	if _, err := s.Keyring.Get("dev"); err == nil {
		t.Error("keychain entry of dev was kept")
	}
	portLacks(t, s.ReadFile("profiles.yaml"), "dev:")
	r = s.Run("configure", "remove", "--profile", "dev", "-y")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "nothing to remove")
}

func TestConfigure_CAP037_AC7_APIKeyStdin(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portWantExit(t, s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin"), 0)
	org2 := s.Fake.AddOrganization("Nova Labs")
	r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--profile", "two", "--api-key-stdin")
	portWantExit(t, r, 3)
	wantContains(t, portOut(r), "cbctl configure --org-id ID", "Nova Labs ("+org2+")")
	r = s.RunWith(s.Fake.APIKey+"\n", "configure", "--profile", "two", "--org-id", org2, "--api-key-stdin")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), `Saved profile "two" (organization Nova Labs `+org2+")")
}

func TestConfigure_CAP037_AC7_RejectionShowsHintAndChecklist(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	r := s.RunWith("Bearer wrong-key-abcd\n", "configure", "--api-key-stdin")
	portWantExit(t, r, 1)
	out := portOut(r)
	wantContains(t, out, "rejected by Capella (401); nothing was saved",
		"note: removed a leading 'Bearer ' from the pasted key",
		"has 14 characters and ends in …abcd", "allowed IP list", "not the API Key ID or name",
		"curl -s -o /dev/null", "--api-key-stdin")
	clitest.AssertNoSecret(t, r, "wrong-key-abcd")
	if _, err := os.Stat(filepath.Join(s.Dir, "profiles.yaml")); err == nil {
		t.Error("a rejected key saved profiles.yaml")
	}
	if _, err := s.Keyring.Get("default"); err == nil {
		t.Error("a rejected key reached the keychain")
	}
}

func TestConfigure_NonInteractivePrintsNextStep(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portSeedNovaDev(s.Fake)
	r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "Next: cbctl capella sync")
	if _, err := os.Stat(filepath.Join(s.Dir, "capella.yaml")); err == nil {
		t.Error("non-interactive configure wrote capella.yaml without --import")
	}
	r = s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin", "--import")
	portWantExit(t, r, 0)
	wantContains(t, s.ReadFile("capella.yaml"), "name: nova-dev", "name: nova-dev-01")
}

func TestConfigure_OffersImportAndWritesCapellaYAML(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portSeedNovaDev(s.Fake)
	s.TTY(true)
	s.Answers("y")
	r := s.RunWith(s.Fake.APIKey+"\n", "configure")
	portWantExit(t, r, 0)
	wantContains(t, s.ReadFile("capella.yaml"), "name: nova-dev-01")
	s.TTY(false)
	r = s.Run("capella", "plan", "--detailed-exitcode")
	portWantExit(t, r, 0)
}

// --- tests/scenario/test_configure_check.py (CAP-047) ---

var portOtherOrgFile = strings.Replace(portNovaDev, "${CAPELLA_ORG_ID}", "org-other", 1)

func TestConfigureCheck_CAP047_AC1_OrgMismatchReported(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portOtherOrgFile)
	s.Fake.ResetLog()
	r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "Saved profile",
		"capella.yaml is for organization org-other, but this key is for cbctl-fake-org (org-1)",
		"cbctl capella sync --force", "cbctl configure --profile NAME")
	if s.ReadFile("capella.yaml") != portOtherOrgFile {
		t.Error("capella.yaml changed")
	}
	clitest.AssertNoWrites(t, s.Fake)
}

func TestConfigureCheck_CAP047_AC2_ReimportOnYes(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portOtherOrgFile)
	s.TTY(true)
	s.Answers("y")
	r := s.RunWith(s.Fake.APIKey+"\n", "configure")
	portWantExit(t, r, 0)
	if s.ReadFile("capella.yaml.bak") != portOtherOrgFile {
		t.Error("capella.yaml.bak does not hold the file for the other organization")
	}
	after := s.ReadFile("capella.yaml")
	wantContains(t, after, "organizationId: org-1", "name: nova-dev")
	portLacks(t, after, "org-other")
}

func TestConfigureCheck_CAP047_AC2_DefaultKeepsFile(t *testing.T) {
	for _, answer := range []string{"n", ""} {
		t.Run("answer "+answer, func(t *testing.T) {
			s := newScenario(t)
			portNoEnvAuth(s)
			portSeedNovaDev(s.Fake)
			writeCapellaYAML(t, s, portOtherOrgFile)
			s.TTY(true)
			s.Answers(answer)
			r := s.RunWith(s.Fake.APIKey+"\n", "configure")
			portWantExit(t, r, 0)
			if s.ReadFile("capella.yaml") != portOtherOrgFile {
				t.Error("capella.yaml changed")
			}
			if _, err := os.Stat(filepath.Join(s.Dir, "capella.yaml.bak")); err == nil {
				t.Error("capella.yaml.bak written")
			}
		})
	}
}

func TestConfigureCheck_CAP047_AC3_MissingProjectReported(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, `apiVersion: cbctl/v1
organizationId: org-1
projects:
  - name: nova-dev
  - name: free-proj
`)
	r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin")
	portWantExit(t, r, 0)
	out := portOut(r)
	wantContains(t, out, "capella.yaml declares project free-proj, which is not in organization cbctl-fake-org (projects: nova-dev)")
	portLacks(t, out, "project nova-dev, which", "is for organization")
}

func TestConfigureCheck_CAP047_AC4_MatchingFileQuiet(t *testing.T) {
	for name, doc := range map[string]string{
		"literal": strings.Replace(portNovaDev, "${CAPELLA_ORG_ID}", "org-1", 1),
		"env ref": portNovaDev,
	} {
		t.Run(name, func(t *testing.T) {
			s := newScenario(t)
			portNoEnvAuth(s)
			if name == "env ref" {
				s.Env["CAPELLA_ORG_ID"] = "org-1"
			}
			portSeedNovaDev(s.Fake)
			writeCapellaYAML(t, s, doc)
			s.Fake.ResetLog()
			r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin")
			portWantExit(t, r, 0)
			wantContains(t, portOut(r), "capella.yaml already exists; to refresh it from Capella: cbctl capella sync")
			portLacks(t, portOut(r), "[warning]")
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}

func TestConfigureCheck_CAP047_AC4_UnresolvedOrgRefQuiet(t *testing.T) {
	s := newScenario(t)
	portNoEnvAuth(s)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portNovaDev)
	r := s.RunWith(s.Fake.APIKey+"\n", "configure", "--api-key-stdin")
	portWantExit(t, r, 0)
	portLacks(t, portOut(r), "is for organization", "[warning]")
}

// portNoKeyring is a keychain that is not available (no OS keychain on the machine).
type portNoKeyring struct{}

func (portNoKeyring) Get(string) (string, error) { return "", errors.New("no keychain") }
func (portNoKeyring) Set(string, string) error   { return errors.New("no keychain") }
func (portNoKeyring) Delete(string) error        { return errors.New("no keychain") }

func TestConfigure_CAP037_AC4_NoKeychainNeverWritesKey(t *testing.T) {
	f := capellafake.New(t)
	dir := t.TempDir()
	env := map[string]string{"CAPELLA_API_BASE_URL": f.URL, "CBCTL_HOME": dir, "CAPELLA_API_KEY": "", "CAPELLA_ORG_ID": "", "CBCTL_CONFIG": ""}
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"configure", "--api-key-stdin"}, cli.Deps{
		Env:            func(k string) string { return env[k] },
		LookupEnv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Stdin:          strings.NewReader(f.APIKey + "\n"),
		Stdout:         &stdout,
		Stderr:         &stderr,
		IsTTY:          func(io.Reader) bool { return false },
		Cwd:            func() (string, error) { return dir, nil },
		Home:           dir,
		Keyring:        portNoKeyring{},
		LegacyHome:     filepath.Join(dir, "legacy"),
		LegacyKeyring:  settings.NewMemKeyring(),
		ResetRedaction: true,
	})
	out := stdout.String() + stderr.String()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	wantContains(t, out, "no OS keychain available; set CAPELLA_API_KEY")
	portLacks(t, out, f.APIKey)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), f.APIKey) {
			t.Errorf("%s holds the API key", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.yaml")); err != nil {
		t.Errorf("the profile was not saved: %v", err)
	}
}

// --- tests/scenario/test_deletion_protection.py (CAP-013) ---

func TestDeletionProtection_CAP013_AC2_AfterCreateBeforeBuckets(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("nova-dev", capellafake.WithField("description", "Nova development environment"))
	writeCapellaYAML(t, s, portNovaDev+"    deletionProtection: true\n    buckets:\n      - name: orders\n        memoryAllocationInMb: 1024\n")
	r := s.Run("capella", "plan")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "+ deletion-protection nova-dev-01", "(after cluster is healthy)")
	r = s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	w := portWantWritePaths(t, s.Fake,
		"POST /v4/organizations/org-1/projects/*/clusters",
		"PUT /v4/organizations/org-1/projects/*/clusters/*/deletionProtection",
		"POST /v4/organizations/org-1/projects/*/clusters/*/buckets")
	if b := portBody(t, w[1]); b["deletionProtection"] != true {
		t.Errorf("protection body = %v", b)
	}
	healthy := strings.Index(r.Stderr, "cluster nova-dev-01")
	protect := strings.Index(r.Stderr, "deletion-protection nova-dev-01")
	if healthy < 0 || protect < 0 || healthy > protect {
		t.Errorf("protection not applied after the cluster:\n%s", r.Stderr)
	}
}

func TestDeletionProtection_CAP013_AC3_OmittedUnchangedPlan(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprint(on), func(t *testing.T) {
			s := newScenario(t)
			portSeedNovaDev(s.Fake, capellafake.WithField("deletionProtection", on))
			writeCapellaYAML(t, s, portNovaDev)
			r := s.Run("capella", "plan", "--detailed-exitcode")
			portWantExit(t, r, 0)
			portLacks(t, r.Stdout, "deletion-protection")
		})
	}
}

func TestDeletionProtection_CAP013_AC5_AlreadyOffNoop(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portNovaDev)
	r := s.Run("capella", "cluster", "protect", "nova-dev-01", "--off")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "deletion protection is already off for nova-dev-01; nothing to do")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestDeletionProtection_CAP013_AC5_OffRecordsInFile(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake, capellafake.WithField("deletionProtection", true))
	writeCapellaYAML(t, s, portNovaDev+"    deletionProtection: true\n")
	r := s.Run("capella", "cluster", "protect", "nova-dev-01", "--off", "-y")
	portWantExit(t, r, 0)
	wantContains(t, s.ReadFile("capella.yaml"), "deletionProtection: false")
	w := portWantWritePaths(t, s.Fake, "PUT /v4/organizations/org-1/projects/*/clusters/*/deletionProtection")
	if b := portBody(t, w[0]); b["deletionProtection"] != false || len(b) != 1 {
		t.Errorf("body = %v", b)
	}
}

func TestDeletionProtection_CAP013_AC5_OnAndConfirmation(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portNovaDev)
	// Turning protection on needs no confirmation, even without a terminal.
	r := s.Run("capella", "cluster", "protect", "nova-dev-01", "--on")
	portWantExit(t, r, 0)
	portWantWritePaths(t, s.Fake, "PUT /v4/organizations/org-1/projects/*/clusters/*/deletionProtection")
	s.Fake.ResetLog()

	r = s.Run("capella", "cluster", "protect", "nova-dev-01", "--off")
	portWantExit(t, r, 4)
	wantContains(t, portOut(r), "--yes")
	s.TTY(true)
	s.Answers("n")
	r = s.Run("capella", "cluster", "protect", "nova-dev-01", "--off")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "Cancelled; capella.yaml and Capella are unchanged.")
	clitest.AssertNoWrites(t, s.Fake)
	wantContains(t, s.ReadFile("capella.yaml"), "deletionProtection: true")
}

// --- tests/scenario/test_allowed_cidrs.py (CAP-022) ---

const portCIDRPath = "/v4/organizations/org-1/projects/*/clusters/*/allowedcidrs"

func TestAllowedCIDR_CAP022_AC1_CreateMissing(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portNovaDev+`    allowedCidrs:
      - cidr: 203.0.113.10/32
        comment: Office VPN egress
      - cidr: 198.51.100.0/24
        comment: CI runners
        expiresAt: "2026-12-31T23:59:59Z"
`)
	r := s.Run("capella", "plan")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout,
		`+ allowed-cidr nova-dev-01/203.0.113.10/32  "Office VPN egress" · permanent`,
		`+ allowed-cidr nova-dev-01/198.51.100.0/24  "CI runners" · expires 2026-12-31T23:59:59Z`)
	portWantExit(t, s.Run("capella", "apply", "-y"), 0)
	w := portWantWritePaths(t, s.Fake, "POST "+portCIDRPath, "POST "+portCIDRPath)
	if b := portBody(t, w[0]); len(b) != 2 || b["cidr"] != "203.0.113.10/32" || b["comment"] != "Office VPN egress" {
		t.Errorf("first body = %v", b)
	}
	if b := portBody(t, w[1]); len(b) != 3 || b["expiresAt"] != "2026-12-31T23:59:59Z" {
		t.Errorf("second body = %v", b)
	}
}

func TestAllowedCIDR_CAP022_AC2_BareIPMatches32(t *testing.T) {
	s := newScenario(t)
	_, cid := portSeedNovaDev(s.Fake)
	s.Fake.SeedCIDR(cid, "203.0.113.10/32", "Office VPN")
	writeCapellaYAML(t, s, portNovaDev+"    allowedCidrs:\n      - cidr: 203.0.113.10\n        comment: Office VPN\n")
	r := s.Run("capella", "plan", "--detailed-exitcode")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "= allowed-cidr nova-dev-01/203.0.113.10/32")
}

func portReplaceSetup(t *testing.T, duplicates bool) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	_, cid := portSeedNovaDev(s.Fake)
	s.Fake.SeedCIDR(cid, "203.0.113.10/32", "a")
	s.Fake.AllowDuplicateCIDRs(duplicates)
	writeCapellaYAML(t, s, portNovaDev+"    allowedCidrs:\n      - cidr: 203.0.113.10/32\n        comment: b\n")
	return s
}

func portCIDRComments(t *testing.T, s *clitest.Scenario) string {
	t.Helper()
	r := s.Run("capella", "cidr", "list", "nova-dev-01", "-o", "json")
	portWantExit(t, r, 0)
	var list []map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &list); err != nil {
		t.Fatalf("cidr list -o json: %v\n%s", err, r.Stdout)
	}
	var got []string
	for _, c := range list {
		got = append(got, fmt.Sprintf("%v=%v", c["cidr"], c["comment"]))
	}
	return strings.Join(got, ",")
}

func TestAllowedCIDR_CAP022_AC3_ReplaceCreateThenDelete(t *testing.T) {
	s := portReplaceSetup(t, true)
	r := s.Run("capella", "plan")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "± allowed-cidr nova-dev-01/203.0.113.10/32  (replace: API has no update)",
		`comment:  "a" → "b"`, "Plan: 1 to add, 0 to change, 0 to delete, 0 blocked.")
	r = s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	portLacks(t, portOut(r), "briefly interrupted")
	w := portWantWritePaths(t, s.Fake, "POST "+portCIDRPath, "DELETE "+portCIDRPath+"/*")
	if b := portBody(t, w[0]); b["comment"] != "b" {
		t.Errorf("create body = %v", b)
	}
	if got := portCIDRComments(t, s); got != "203.0.113.10/32=b" {
		t.Errorf("CIDRs after replace = %s", got)
	}
}

func TestAllowedCIDR_CAP022_AC3_ReplaceFallbackOn409(t *testing.T) {
	s := portReplaceSetup(t, false)
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 0)
	wantContains(t, portOut(r), "access from 203.0.113.10/32 is briefly interrupted")
	w := portWantWritePaths(t, s.Fake, "POST "+portCIDRPath, "DELETE "+portCIDRPath+"/*", "POST "+portCIDRPath)
	if w[0].Status != 409 || w[2].Status != 201 {
		t.Errorf("statuses = %d, %d; want 409 then 201", w[0].Status, w[2].Status)
	}
	if got := portCIDRComments(t, s); got != "203.0.113.10/32=b" {
		t.Errorf("CIDRs after replace = %s", got)
	}
}

func TestAllowedCIDR_CAP022_AC4_UnmanagedNotDeleted(t *testing.T) {
	s := newScenario(t)
	_, cid := portSeedNovaDev(s.Fake)
	s.Fake.SeedCIDR(cid, "203.0.113.10/32", "Office VPN egress")
	s.Fake.SeedCIDR(cid, "192.0.2.7/32", "old laptop")
	writeCapellaYAML(t, s, portNovaDev+"    allowedCidrs:\n      - cidr: 203.0.113.10/32\n        comment: Office VPN egress\n")
	r := s.Run("capella", "plan", "--detailed-exitcode")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "Unmanaged (not in capella.yaml; use --prune to delete):", `allowed-cidr nova-dev-01/192.0.2.7/32  "old laptop"`)
	portWantExit(t, s.Run("capella", "apply", "-y"), 0)
	clitest.AssertNoWrites(t, s.Fake)
	r = s.Run("capella", "plan", "-o", "json")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, `"unmanaged"`, "192.0.2.7/32")
	portWantExit(t, s.Run("capella", "apply", "-y", "--prune"), 0)
	portWantWritePaths(t, s.Fake, "DELETE "+portCIDRPath+"/*")
	if got := portCIDRComments(t, s); got != "203.0.113.10/32=Office VPN egress" {
		t.Errorf("CIDRs after prune = %s", got)
	}
}

func TestAllowedCIDR_CAP022_AC4_UnmanagedListedWhenFileHasNone(t *testing.T) {
	s := newScenario(t)
	_, cid := portSeedNovaDev(s.Fake)
	s.Fake.SeedCIDR(cid, "192.0.2.7/32", "")
	writeCapellaYAML(t, s, portNovaDev)
	r := s.Run("capella", "plan", "--detailed-exitcode")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "allowed-cidr nova-dev-01/192.0.2.7/32")
}

func TestAllowedCIDR_CAP022_AC5_AnyIPWarning(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portNovaDev+"    allowAnyIp: true\n    allowedCidrs:\n      - cidr: 0.0.0.0/0\n")
	r := s.Run("capella", "plan")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "+ allowed-cidr nova-dev-01/0.0.0.0/0")
	wantContains(t, r.Stderr, "allowed CIDR 0.0.0.0/0 on nova-dev-01 lets any IP address connect (allowAnyIp: true)")
}

func TestAllowedCIDR_CAP022_AC5_UnconfirmedAnyIPCreateBlocked(t *testing.T) {
	s := newScenario(t)
	portSeedNovaDev(s.Fake)
	writeCapellaYAML(t, s, portNovaDev+"    allowedCidrs:\n      - cidr: 0.0.0.0/0\n")
	r := s.Run("capella", "apply", "-y")
	portWantExit(t, r, 4)
	wantContains(t, r.Stdout, "! allowed-cidr nova-dev-01/0.0.0.0/0  0.0.0.0/0 would let every IP address connect",
		"cbctl capella cidr create nova-dev-01 0.0.0.0/0 --allow-any-ip", "1 blocked")
	clitest.AssertNoWrites(t, s.Fake)
	r = s.Run("capella", "validate")
	portWantExit(t, r, 0)
	wantContains(t, r.Stderr, "allowed IP range 0.0.0.0/0 (line 19)")
}

func TestAllowedCIDR_CAP022_AC5_UnconfirmedAnyIPAlreadyInCapellaWarnsOnly(t *testing.T) {
	s := newScenario(t)
	_, cid := portSeedNovaDev(s.Fake)
	s.Fake.SeedCIDR(cid, "0.0.0.0/0", "")
	writeCapellaYAML(t, s, portNovaDev+"    allowedCidrs:\n      - cidr: 0.0.0.0/0\n")
	r := s.Run("capella", "plan", "--detailed-exitcode")
	portWantExit(t, r, 0)
	wantContains(t, r.Stdout, "= allowed-cidr nova-dev-01/0.0.0.0/0", "0 blocked")
	wantContains(t, r.Stderr, "warnings; run 'cbctl capella validate'")
	r = s.Run("capella", "validate")
	portWantExit(t, r, 0)
	wantContains(t, r.Stderr, "0.0.0.0/0 lets every IP address connect")
	// Other commands do not repeat the warning.
	r = s.Run("capella", "cidr", "list", "nova-dev-01")
	portWantExit(t, r, 0)
	portLacks(t, portOut(r), "[warning]")
}
