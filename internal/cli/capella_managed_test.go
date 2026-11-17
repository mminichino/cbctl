package cli

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

func TestManagedFlagConflicts(t *testing.T) {
	resEnv(t)
	envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")

	wantExit(t, runResource(t, "project", "create", "x", "--direct", "--no-apply"), ExitConfigInvalid, "mutually exclusive")

	dir := t.TempDir()
	t.Chdir(dir)
	wantExit(t, runResource(t, "project", "create", "x", "--no-apply"), ExitConfigInvalid, "--no-apply needs capella.yaml")
}

func TestManagedProjectLifecycle(t *testing.T) {
	f := resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")

	res := mustRun(t, "project", "create", "extra", "--description", "first", "--yes")
	wantContains(t, res.Stdout, "project extra")
	text := string(mustRead(t, path))
	if !strings.Contains(text, "name: extra") || !strings.Contains(text, "first") {
		t.Fatalf("project was not added to the file:\n%s", text)
	}
	if projects, _ := f.Client().ListProjects(t.Context()); !hasProject(projects, "extra") {
		t.Errorf("project extra was not created in Capella")
	}

	mustRun(t, "project", "update", "extra", "--description", "second", "--yes")
	if text := string(mustRead(t, path)); !strings.Contains(text, "second") || strings.Contains(text, "first") {
		t.Errorf("description was not updated in the file:\n%s", text)
	}

	wantExit(t, runResource(t, "project", "update", "ghost", "--description", "x", "--yes"), ExitConfigInvalid, "project ghost is not in capella.yaml")

	mustRun(t, "project", "delete", "extra", "--yes")
	if text := string(mustRead(t, path)); strings.Contains(text, "name: extra") {
		t.Errorf("project was not removed from the file:\n%s", text)
	}
	if projects, _ := f.Client().ListProjects(t.Context()); hasProject(projects, "extra") {
		t.Errorf("project extra still exists in Capella")
	}
}

func TestManagedNoApplyAndDirect(t *testing.T) {
	f := resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")

	res := mustRun(t, "project", "create", "later", "--no-apply", "--yes")
	wantContains(t, res.Stdout, "Not applied (--no-apply)")
	if !strings.Contains(string(mustRead(t, path)), "name: later") {
		t.Error("--no-apply must still record the project in the file")
	}
	if projects, _ := f.Client().ListProjects(t.Context()); hasProject(projects, "later") {
		t.Error("--no-apply must not create the project in Capella")
	}

	before := string(mustRead(t, path))
	res = mustRun(t, "project", "create", "direct-only", "--direct")
	wantContains(t, res.Stderr, "capella.yaml was not updated")
	if string(mustRead(t, path)) != before {
		t.Error("--direct must leave the file alone")
	}
	if projects, _ := f.Client().ListProjects(t.Context()); !hasProject(projects, "direct-only") {
		t.Error("--direct must create the project in Capella")
	}
}

func TestManagedInvalidFileChangesNothing(t *testing.T) {
	f := resEnv(t)
	path := envDir(t, strings.Replace(envDoc, "region: us-east-1", "region: nowhere-1", 1))
	t.Setenv("CBCTL_CONFIG", "")
	before := string(mustRead(t, path))

	res := runResource(t, "cluster", "protect", "envclu", "--on", "--yes")
	wantExit(t, res, ExitConfigInvalid, "already had")
	if string(mustRead(t, path)) != before {
		t.Error("the file changed although it was invalid")
	}
	noWrites(t, f)
}

func TestManagedClusterProtectAndScale(t *testing.T) {
	f := resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	mustRun(t, "apply", "--yes")
	f.ResetLog()

	wantContains(t, mustRun(t, "cluster", "protect", "envclu", "--on", "--yes").Stdout, "Plan:")
	if !strings.Contains(string(mustRead(t, path)), "deletionProtection: true") {
		t.Errorf("protection not recorded:\n%s", mustRead(t, path))
	}

	f.ResetLog()
	wantContains(t, mustRun(t, "cluster", "protect", "envclu", "--on", "--yes").Stdout, "already on")
	noWrites(t, f)

	wantExit(t, runResource(t, "cluster", "scale", "envclu", "--nodes", "2", "--yes"), ExitConfigInvalid, "outside the limits")
	wantExit(t, runResource(t, "cluster", "scale", "ghost", "--nodes", "4", "--yes"), ExitConfigInvalid, "cluster ghost is not in capella.yaml")

	mustRun(t, "cluster", "scale", "envclu", "--nodes", "4", "--yes")
	if !strings.Contains(string(mustRead(t, path)), "numOfNodes: 4") {
		t.Errorf("node count not recorded:\n%s", mustRead(t, path))
	}
}

func hasProject(projects []capellaapi.Project, name string) bool {
	for _, p := range projects {
		if p.Name == name {
			return true
		}
	}
	return false
}
