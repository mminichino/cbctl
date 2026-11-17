package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// TestProject_CAP009_AC6_ListTableAndJSON ports project list text + JSON onto Scenario.
func TestProject_CAP009_AC6_ListTableAndJSON(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("alpha")
	if _, err := s.Fake.Client().CreateProject(t.Context(), "beta", "second project"); err != nil {
		t.Fatal(err)
	}
	s.Fake.ResetCalls()

	r := mustScenario(t, s.Run("capella", "project", "list"), "project list")
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header and 2 rows, got:\n%s", r.Stdout)
	}
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "NAME ID DESCRIPTION" {
		t.Errorf("header = %q", lines[0])
	}
	wantContains(t, r.Stdout, "alpha", "beta", "second project")
	clitest.AssertNoWrites(t, s.Fake)

	items, ok := mustScenario(t, s.Run("capella", "project", "list", "-o", "json"), "project list -o json").JSON(t).([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("json list = %v", items)
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "alpha" || first["id"] == "" {
		t.Errorf("first project = %v", first)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestProject_CAP009_AC6_GetByName ports project get onto Scenario.
func TestProject_CAP009_AC6_GetByName(t *testing.T) {
	s := newScenario(t)
	id := s.Fake.SeedProject("alpha")
	s.Fake.ResetCalls()

	r := mustScenario(t, s.Run("capella", "project", "get", "alpha"), "project get")
	wantContains(t, r.Stdout, "id: "+id, "name: alpha")
	clitest.AssertNoWrites(t, s.Fake)

	obj := mustScenario(t, s.Run("capella", "project", "get", "alpha", "-o", "json"), "project get -o json").JSON(t).(map[string]any)
	if obj["id"] != id || obj["name"] != "alpha" {
		t.Errorf("object = %v", obj)
	}

	r = s.Run("capella", "project", "get", "missing")
	if r.Code != int(cli.ExitConfigInvalid) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitConfigInvalid, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "project missing not found")
}
