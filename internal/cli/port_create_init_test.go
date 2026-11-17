package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
)

// initDefaults answers every interview question after the project with its default: cluster
// name, provider, region, network, size preset, support plan, deletion protection,
// hibernation, allowed IP ranges and bucket.
func initDefaults() []string { return []string{"", "", "", "", "", "", "", "", "", ""} }

// runInit runs 'capella init' on a terminal with the scripted interview answers.
func runInit(s *clitest.Scenario, answers []string, args ...string) clitest.Result {
	s.TTY(true)
	defer s.TTY(false)
	s.Answers(answers...)
	return s.Run(append([]string{"capella", "init"}, args...)...)
}

func hasGET(s *clitest.Scenario, path string) bool {
	for _, c := range s.Fake.Calls("GET", path) {
		if c.Path == path {
			return true
		}
	}
	return false
}

func TestInit_CAP035_AC2_ListsOrgsAndProjects(t *testing.T) {
	s := newScenario(t)
	other := s.Fake.AddOrganization("Other Corp")
	first := s.Fake.SeedProject("nova-dev")
	second := s.Fake.SeedProject("nova-dev")
	s.Fake.SeedProject("nova-qa")
	// Start new, organization 1, then project 2: the second nova-dev, whose name is ambiguous,
	// so the file pins its id.
	r := mustScenario(t, runInit(s, append([]string{"", "1", "2"}, initDefaults()...)), "init")
	wantContains(t, r.Stderr, "Organizations:", "  1. ", "(org-1)", "  2. Other Corp ("+other+")",
		"Existing projects:", "  3. nova-qa")
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "project:\n  id: "+second+"\n  name: nova-dev\n")
	if strings.Contains(text, first) {
		t.Errorf("the first nova-dev's id is in the file:\n%s", text)
	}
	r = mustScenario(t, runInit(s, append([]string{"", "1", "3"}, initDefaults()...), "--force"), "init --force")
	text = s.ReadFile("capella.yaml")
	wantContains(t, text, "project:\n  name: nova-qa\n") // a unique name: no id pin
	if !hasGET(s, "/v4/organizations") || !hasGET(s, "/v4/organizations/org-1/projects") {
		t.Errorf("init did not list the organizations and projects: %v", s.Fake.Calls("GET", "*"))
	}
	clitest.AssertNoWrites(t, s.Fake)
}

func TestInit_CAP035_AC2_ExistingClusterNameReasked(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	s.Fake.SeedCluster(pid, "nova-dev-01", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.10.0.0/24"}))
	// Start new, project 1, then the default cluster name (taken), then nova-dev-02.
	answers := append([]string{"", "1", "", "nova-dev-02"}, initDefaults()[1:]...)
	r := mustScenario(t, runInit(s, answers), "init")
	wantContains(t, r.Stderr, "cluster nova-dev-01 already exists in project nova-dev")
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "- name: nova-dev-02", "cidr: 10.0.0.0/24") // the first free /24
	clitest.AssertNoWrites(t, s.Fake)
}

// TestInit_CAP035_AC2_FullInterview walks every branch of the interview: a project name shared
// by two projects, a custom size with a re-asked answer, hibernation, an any-IP allow-list entry,
// a bucket with scopes and an app credential.
func TestInit_CAP035_AC2_FullInterview(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("nova-dev")
	second := s.Fake.SeedProject("nova-dev")
	answers := []string{"", "nova-dev", "2", // start new; the name is shared, so pick the second
		"", "gcp", "", "", // cluster, provider, region (gcp default), network
		"custom", "x", "3", "8", "32", // custom size; "x" is re-asked
		"", "n", "y", "", "", // support plan, protection, hibernation with the default window and timezone
		"0.0.0.0/0", "y", // allow any IP after the warning
		"orders", "", "", "inv:items,stock", // bucket, memory, backend, scopes
		"app", "", ""} // credential, access, password variable
	r := mustScenario(t, runInit(s, answers), "init")
	wantContains(t, r.Stderr, "2 projects are named nova-dev:", "x is not a positive number", "lets any IP address connect")
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "id: "+second, "type: gcp", "region: us-east1", "cpu: 8", "ram: 32", "weekdays:",
		"0.0.0.0/0", "name: orders", "name: inv", "name: app", "${CBCTL_PWD_NOVA_DEV_01_APP}")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestInit_CAP035_AC2_ProjectChoices(t *testing.T) {
	t.Run("a new project name", func(t *testing.T) {
		s := newScenario(t)
		s.Fake.SeedProject("nova-dev")
		r := mustScenario(t, runInit(s, append([]string{"", "nova-new"}, initDefaults()...)), "init")
		wantContains(t, r.Stderr, "Project nova-new does not exist yet; apply will create it.")
		wantContains(t, s.ReadFile("capella.yaml"), "project:\n  name: nova-new\n", "- name: nova-new-01")
	})
	t.Run("an ambiguous --project without a terminal", func(t *testing.T) {
		s := newScenario(t)
		s.Fake.SeedProject("nova-dev")
		s.Fake.SeedProject("nova-dev")
		r := s.Run("capella", "init", "--non-interactive", "--project", "nova-dev", "--cluster", "c1")
		wantExit(t, r, 3)
		wantContains(t, r.Stderr, "2 projects are named nova-dev", "--project-id")
	})
	t.Run("--project-id picks one of two", func(t *testing.T) {
		s := newScenario(t)
		s.Fake.SeedProject("nova-dev")
		second := s.Fake.SeedProject("nova-dev")
		mustScenario(t, s.Run("capella", "init", "--non-interactive", "--project-id", second, "--cluster", "c1"), "init")
		wantContains(t, s.ReadFile("capella.yaml"), "id: "+second)
	})
	t.Run("an unknown --project-id", func(t *testing.T) {
		s := newScenario(t)
		r := s.Run("capella", "init", "--non-interactive", "--project-id", "nope", "--cluster", "c1")
		wantExit(t, r, 3)
		wantContains(t, r.Stderr, "project nope not found")
	})
}
