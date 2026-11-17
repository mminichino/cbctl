package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/config/capellafile"
)

// syncedTwoProjects seeds two projects with a cluster each, syncs them into capella.yaml and
// returns the scenario with the projects' and clusters' IDs.
func syncedTwoProjects(t *testing.T) (s *clitest.Scenario, devPID, devCID, qaPID string) {
	t.Helper()
	s = newScenario(t)
	devPID = s.Fake.SeedProject("nova-dev")
	devCID = s.Fake.SeedCluster(devPID, "dev-01", "healthy")
	qaPID = s.Fake.SeedProject("nova-qa")
	s.Fake.SeedCluster(qaPID, "qa-01", "healthy")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	return s, devPID, devCID, qaPID
}

func fileProjects(t *testing.T, s *clitest.Scenario) []string {
	t.Helper()
	cfg, err := capellafile.LoadText(s.ReadFile("capella.yaml"), "capella.yaml", s.Dir, func(string) string { return "" })
	if err != nil {
		t.Fatalf("capella.yaml does not load: %v\n%s", err, s.ReadFile("capella.yaml"))
	}
	return cfg.ProjectNames()
}

func syncOK(t *testing.T, s *clitest.Scenario, args ...string) clitest.Result {
	t.Helper()
	r := mustScenario(t, s.Run(append([]string{"capella", "sync"}, args...)...), "sync "+strings.Join(args, " "))
	clitest.AssertNoWrites(t, s.Fake) // sync is GET-only
	return r
}

// TestSync_CAP038_AC13_RemovesDeletedProject: a deployed project gone from Capella is dropped.
func TestSync_CAP038_AC13_RemovesDeletedProject(t *testing.T) {
	s, devPID, _, _ := syncedTwoProjects(t)
	s.Fake.RemoveProject(devPID)
	r := syncOK(t, s)
	wantContains(t, r.Stderr, "project nova-dev no longer exists in Capella; removed from capella.yaml with its clusters")
	if got := strings.Join(fileProjects(t, s), ","); got != "nova-qa" {
		t.Fatalf("projects = %s, want nova-qa", got)
	}
	wantContains(t, syncOK(t, s).Stdout, "already in sync")
}

// TestSync_CAP038_AC13_KeepsNeverDeployed: a project only in the file is kept as it is.
func TestSync_CAP038_AC13_KeepsNeverDeployed(t *testing.T) {
	s, _, _, _ := syncedTwoProjects(t)
	text := s.ReadFile("capella.yaml") + "  - name: nova-new\n    clusters: []\n"
	writeCapellaYAML(t, s, text)
	if !contains(fileProjects(t, s), "nova-new") {
		t.Fatalf("test setup: nova-new not in the file:\n%s", text)
	}
	r := syncOK(t, s)
	wantContains(t, r.Stderr, "project nova-new is not in Capella yet; kept as it is")
	if !contains(fileProjects(t, s), "nova-new") {
		t.Fatal("never-deployed project removed by sync")
	}
}

// TestSync_CAP038_AC13_RemovesDeletedCluster: with and without --cluster.
func TestSync_CAP038_AC13_RemovesDeletedCluster(t *testing.T) {
	for _, args := range [][]string{nil, {"--cluster", "dev-01"}} {
		t.Run(strings.Join(append([]string{"args"}, args...), " "), func(t *testing.T) {
			s, devPID, devCID, _ := syncedTwoProjects(t)
			s.Fake.SeedCluster(devPID, "other", "healthy")
			s.Fake.SeedBucket(devCID, "orders")
			syncOK(t, s)
			s.Fake.DetachCluster(devCID)
			r := syncOK(t, s, args...)
			wantContains(t, r.Stderr, "cluster dev-01 no longer exists in Capella; removed from capella.yaml with its buckets")
			text := s.ReadFile("capella.yaml")
			if strings.Contains(text, "dev-01") || strings.Contains(text, "orders") {
				t.Errorf("dev-01 or its bucket still in the file:\n%s", text)
			}
			if !strings.Contains(text, "name: other") {
				t.Errorf("the other cluster was dropped:\n%s", text)
			}
		})
	}
}

// TestSync_CAP048_AC5_AdoptsRenamedProject: a project renamed in Capella keeps its entry, under
// the new name and its id.
func TestSync_CAP048_AC5_AdoptsRenamedProject(t *testing.T) {
	s, devPID, _, _ := syncedTwoProjects(t)
	s.Fake.RenameProject(devPID, "nova-development")
	r := syncOK(t, s)
	got := fileProjects(t, s)
	if !contains(got, "nova-development") || contains(got, "nova-dev") {
		t.Fatalf("projects = %v, want nova-development instead of nova-dev\n%s", got, r.Stderr)
	}
	if !strings.Contains(s.ReadFile("capella.yaml"), devPID) {
		t.Error("the renamed project is not pinned by id")
	}
	r = mustScenario(t, s.Run("capella", "plan", "--detailed-exitcode"), "plan after rename")
	wantContains(t, r.Stdout, "0 to add")
}

// TestSync_CAP054_AC3_SkipProjectRemembered: --skip-project drops a project now and later.
func TestSync_CAP054_AC3_SkipProjectRemembered(t *testing.T) {
	s, _, _, _ := syncedTwoProjects(t)
	r := syncOK(t, s, "--skip-project", "nova-qa")
	wantContains(t, r.Stderr, "project nova-qa removed from capella.yaml and skipped from now on")
	wantContains(t, s.ReadFile("capella.yaml"), "skipProjects:", "- nova-qa")
	if contains(fileProjects(t, s), "nova-qa") {
		t.Fatal("nova-qa still in the file")
	}
	// A later sync without the flag keeps skipping it.
	syncOK(t, s)
	if contains(fileProjects(t, s), "nova-qa") {
		t.Fatal("nova-qa came back without --project")
	}
	// Naming it brings it back.
	syncOK(t, s, "--project", "nova-qa")
	if !contains(fileProjects(t, s), "nova-qa") {
		t.Fatal("--project nova-qa did not add it back")
	}
}

// TestSync_CAP054_AC3_SkipOnFirstSync: --skip-project also works when the file is created.
func TestSync_CAP054_AC3_SkipOnFirstSync(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("a")
	s.Fake.SeedProject("b")
	syncOK(t, s, "--skip-project", "b")
	if got := strings.Join(fileProjects(t, s), ","); got != "a" {
		t.Fatalf("projects = %s, want a", got)
	}
	wantContains(t, s.ReadFile("capella.yaml"), "skipProjects:")
}

// TestSync_CAP054_AC3_UnknownProjectExit3: --project naming nothing known exits 3.
func TestSync_CAP054_AC3_UnknownProjectExit3(t *testing.T) {
	s, _, _, _ := syncedTwoProjects(t)
	before := s.ReadFile("capella.yaml")
	r := s.Run("capella", "sync", "--project", "nope")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "not in capella.yaml or in Capella: nope")
	if s.ReadFile("capella.yaml") != before {
		t.Error("the file changed")
	}

	// On a first sync, an unknown project lists the ones there are.
	s2 := newScenario(t)
	s2.Fake.SeedProject("real")
	r = s2.Run("capella", "sync", "--project", "nope")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "project nope not found (projects: real)")
}

// TestSync_CAP054_AC1_AddsMissingProjects: a project created in Capella is added.
func TestSync_CAP054_AC1_AddsMissingProjects(t *testing.T) {
	s, _, _, _ := syncedTwoProjects(t)
	pid := s.Fake.SeedProject("nova-prod")
	s.Fake.SeedCluster(pid, "prod-01", "healthy")
	r := s.Run("capella", "sync", "--dry-run")
	wantExit(t, r, cli.ExitChanges)
	wantContains(t, r.Stdout, "nova-prod")
	r = syncOK(t, s)
	wantContains(t, r.Stdout, "added project nova-prod (1 cluster)")
}

// TestSync_CAP054_AC2_ExistingForeignFile: sync will not silently replace a file that is not
// a cbctl file; on a terminal it asks, and a yes keeps a backup.
func TestSync_CAP054_AC2_ExistingForeignFile(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("p")
	s.WriteFile("capella.yaml", "something: else\n")

	r := s.Run("capella", "sync")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "already exists and is not a cbctl file", "--force")

	s.TTY(true)
	s.Answers("n")
	r = mustScenario(t, s.Run("capella", "sync"), "sync declined")
	wantContains(t, r.Stdout, "Sync cancelled; nothing was written.")
	if s.ReadFile("capella.yaml") != "something: else\n" {
		t.Fatal("declined sync changed the file")
	}

	s.Answers("y")
	mustScenario(t, s.Run("capella", "sync"), "sync confirmed")
	if s.ReadFile("capella.yaml.bak") != "something: else\n" {
		t.Error("no backup of the replaced file")
	}
	if got := fileProjects(t, s); len(got) != 1 || got[0] != "p" {
		t.Errorf("projects = %v", got)
	}
}

// TestSync_CAP038_AC14_ReadErrorKeepsPrevious: buckets that cannot be read now keep what the
// file had, and sync says so.
func TestSync_CAP038_AC14_ReadErrorKeepsPrevious(t *testing.T) {
	s, _, devCID, _ := syncedTwoProjects(t)
	s.Fake.SeedBucket(devCID, "orders")
	syncOK(t, s)
	s.Fake.FailNext("GET", `/clusters/`+devCID+`/buckets$`, 500, 100)
	r := syncOK(t, s)
	if !strings.Contains(s.ReadFile("capella.yaml"), "name: orders") {
		t.Fatalf("bucket lost after a read error:\n%s\n%s", s.ReadFile("capella.yaml"), r.Stderr)
	}
	wantContains(t, r.Stdout+r.Stderr, "kept from capella.yaml")
}

// TestSync_CAP038_AC14_TurnedOffKeepsChildren: a turned-off cluster keeps its buckets.
func TestSync_CAP038_AC14_TurnedOffKeepsChildren(t *testing.T) {
	s, _, devCID, _ := syncedTwoProjects(t)
	s.Fake.SeedBucket(devCID, "orders")
	syncOK(t, s)
	s.Fake.SetClusterState(devCID, "turnedOff")
	r := syncOK(t, s)
	wantContains(t, r.Stdout+r.Stderr, "kept from capella.yaml (turnedOff)")
	if !strings.Contains(s.ReadFile("capella.yaml"), "name: orders") {
		t.Fatal("turned-off cluster lost its bucket")
	}
	mustScenario(t, s.Run("capella", "sync", "--dry-run"), "dry-run while off")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
