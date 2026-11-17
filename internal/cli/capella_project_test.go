package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

func TestProjectList(t *testing.T) {
	f := resEnv(t)
	f.SeedProject("alpha")
	_, err := f.Client().CreateProject(context.Background(), "beta", "second project")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "project", "list")
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) != 3 {
			t.Fatalf("want header and 2 rows, got:\n%s", res.Stdout)
		}
		if got := strings.Fields(lines[0]); strings.Join(got, " ") != "NAME ID DESCRIPTION" {
			t.Errorf("header = %q", lines[0])
		}
		wantContains(t, res.Stdout, "alpha", "beta", "second project")
	})
	t.Run("json", func(t *testing.T) {
		items := jsonArray(t, mustRun(t, "project", "list", "-o", "json"))
		if len(items) != 2 {
			t.Fatalf("got %d projects, want 2", len(items))
		}
		if first := items[0].(map[string]any); first["name"] != "alpha" || first["id"] == "" {
			t.Errorf("first project = %v", first)
		}
	})
	t.Run("API failure", func(t *testing.T) {
		f.FailNext("GET", `/projects$`, 403, 1)
		res := runResource(t, "project", "list")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "listing projects") {
			t.Fatalf("err = %v", res.Err)
		}
	})
	t.Run("no key", func(t *testing.T) {
		t.Setenv("CAPELLA_API_KEY", "")
		wantExit(t, runResource(t, "project", "list"), ExitConfigInvalid, "API key not set")
	})
}

func TestProjectGet(t *testing.T) {
	f := resEnv(t)
	id := f.SeedProject("alpha")

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "project", "get", "alpha")
		wantContains(t, res.Stdout, "id: "+id, "name: alpha")
	})
	t.Run("json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "project", "get", "alpha", "-o", "json"))
		if obj["id"] != id || obj["name"] != "alpha" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("not found", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "project", "get", "missing"), ExitConfigInvalid, "project missing not found")
		if !strings.Contains(ce.Action, "project list") {
			t.Errorf("action = %q", ce.Action)
		}
	})
}

func TestProjectCreate(t *testing.T) {
	f := resEnv(t)

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "project", "create", "alpha", "--description", "first")
		wantContains(t, res.Stdout, "created project alpha (")
		p, err := f.Client().ListProjects(context.Background())
		if err != nil || len(p) != 1 || p[0].Description != "first" {
			t.Fatalf("projects = %+v, %v", p, err)
		}
	})
	t.Run("json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "project", "create", "beta", "-o", "json"))
		if obj["name"] != "beta" || obj["id"] == "" || obj["id"] == nil {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("already exists", func(t *testing.T) {
		res := runResource(t, "project", "create", "alpha")
		wantExit(t, res, ExitBlocked, "project alpha already exists")
	})
	t.Run("conflict from Capella", func(t *testing.T) {
		f.FailNext("POST", `/projects$`, 409, 1)
		wantExit(t, runResource(t, "project", "create", "gamma"), ExitBlocked, "project gamma already exists")
	})
	t.Run("API failure", func(t *testing.T) {
		f.FailNext("POST", `/projects$`, 422, 1)
		res := runResource(t, "project", "create", "delta")
		if res.Err == nil || !errors.Is(res.Err, capellaapi.ErrValidation) {
			t.Fatalf("err = %v, want a validation error", res.Err)
		}
	})
}

func TestProjectUpdate(t *testing.T) {
	f := resEnv(t)
	id := f.SeedProject("alpha")
	c := f.Client()

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "project", "update", "alpha", "--description", "new text")
		wantContains(t, res.Stdout, "updated project alpha")
		p, _, err := c.GetProject(context.Background(), id)
		if err != nil || p.Description != "new text" || p.Name != "alpha" {
			t.Fatalf("project = %+v, %v", p, err)
		}
	})
	t.Run("json and clearing the description", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "project", "update", "alpha", "--description", "", "-o", "json"))
		if obj["name"] != "alpha" {
			t.Errorf("object = %v", obj)
		}
		p, _, _ := c.GetProject(context.Background(), id)
		if p.Description != "" {
			t.Errorf("description = %q, want cleared", p.Description)
		}
	})
	t.Run("nothing to update", func(t *testing.T) {
		f.ResetLog()
		wantExit(t, runResource(t, "project", "update", "alpha"), ExitConfigInvalid, "nothing to update")
		if n := len(f.RequestLog()); n != 0 {
			t.Errorf("%d requests sent, want none", n)
		}
	})
	t.Run("stale version is retried once", func(t *testing.T) {
		f.ResetLog()
		f.FailNext("PUT", `/projects/`+id+`$`, 412, 1)
		mustRun(t, "project", "update", "alpha", "--description", "after retry")
		var seq []string
		for _, r := range f.RequestLog() {
			if strings.HasSuffix(r.Path, "/projects/"+id) {
				seq = append(seq, r.Method)
			}
		}
		if got := strings.Join(seq, " "); got != "GET PUT GET PUT" {
			t.Errorf("requests on the project = %q, want GET PUT GET PUT", got)
		}
	})
	t.Run("not found", func(t *testing.T) {
		wantExit(t, runResource(t, "project", "update", "missing", "--description", "x"), ExitConfigInvalid, "not found")
	})
}

func TestProjectDelete(t *testing.T) {
	t.Run("empty project", func(t *testing.T) {
		f := resEnv(t)
		f.SeedProject("alpha")
		res := mustRun(t, "project", "delete", "alpha", "--yes")
		wantContains(t, res.Stdout, "deleted project alpha")
		if p, _ := f.Client().ListProjects(context.Background()); len(p) != 0 {
			t.Errorf("projects left: %+v", p)
		}
	})
	t.Run("json", func(t *testing.T) {
		f := resEnv(t)
		f.SeedProject("alpha")
		obj := jsonObject(t, mustRun(t, "project", "delete", "alpha", "-y", "-o", "json"))
		if obj["status"] != "deleted" || obj["project"] != "alpha" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("no terminal and no --yes is blocked", func(t *testing.T) {
		f := resEnv(t)
		f.SeedProject("alpha")
		wantExit(t, runResource(t, "project", "delete", "alpha"), ExitBlocked, "no terminal")
		if f.Count("DELETE", `/projects/`) != 0 {
			t.Error("a DELETE was sent")
		}
	})
	t.Run("declined at the prompt", func(t *testing.T) {
		f := resEnv(t)
		f.SeedProject("alpha")
		fakeTTY(t)
		res := runResourceIn(t, "n\n", "project", "delete", "alpha")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stdout, "cancelled")
		if f.Count("DELETE", `/projects/`) != 0 {
			t.Error("a DELETE was sent")
		}
	})
	t.Run("accepted at the prompt", func(t *testing.T) {
		f := resEnv(t)
		f.SeedProject("alpha")
		fakeTTY(t)
		res := runResourceIn(t, "alpha\n", "project", "delete", "alpha")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stderr, "Type the project name to confirm deletion (alpha)")
		wantContains(t, res.Stdout, "deleted project alpha")
	})
	t.Run("not empty", func(t *testing.T) {
		fx := newFixture(t)
		res := runResource(t, "project", "delete", fx.ProjectName, "--yes")
		ce := wantExitErr(t, res, ExitError, "project proj is not empty")
		if !strings.Contains(ce.Action, "--cascade") {
			t.Errorf("action = %q", ce.Action)
		}
	})
	t.Run("not found", func(t *testing.T) {
		resEnv(t)
		// A project already gone is a no-op (capellactl CAP-010 AC2), not an error.
		res := mustRun(t, "project", "delete", "missing", "-y")
		if !strings.Contains(res.Stdout+res.Stderr, "project missing not found; nothing to do") {
			t.Errorf("output = %q / %q", res.Stdout, res.Stderr)
		}
	})
}

func TestProjectDeleteCascade(t *testing.T) {
	t.Run("waits for each cluster, then deletes the project", func(t *testing.T) {
		fx := newFixture(t)
		fx.Fake.SeedCluster(fx.ProjectID, "second", "healthy")
		res := mustRun(t, "project", "delete", fx.ProjectName, "--cascade", "--yes")
		wantContains(t, res.Stdout, "deleted project proj", "cluster clu: destroying")
		wantContains(t, res.Stdout, "cluster second: destroying")
		if p, _ := fx.Client.ListProjects(context.Background()); len(p) != 0 {
			t.Errorf("project not deleted: %+v", p)
		}
		if n := fx.Fake.Count("DELETE", `/clusters/[^/]+$`); n != 2 {
			t.Errorf("%d cluster deletes, want 2", n)
		}
		for _, r := range fx.Fake.RequestLog() {
			if r.Method == "DELETE" && strings.Contains(r.Path, "/clusters/") && !strings.Contains(r.RawQuery, "retainsnapshotbackups=true") {
				t.Errorf("cluster delete %q should retain snapshot backups by default", r.RawQuery)
			}
		}
	})
	t.Run("json lists the deleted clusters", func(t *testing.T) {
		fx := newFixture(t)
		obj := jsonObject(t, mustRun(t, "project", "delete", fx.ProjectName, "--cascade", "-y", "-o", "json"))
		if obj["status"] != "deleted" {
			t.Fatalf("object = %v", obj)
		}
		got, _ := obj["deletedClusters"].([]any)
		if len(got) != 1 || got[0] != "clu" {
			t.Errorf("deletedClusters = %v", obj["deletedClusters"])
		}
	})
	t.Run("--discard-snapshots", func(t *testing.T) {
		fx := newFixture(t)
		mustRun(t, "project", "delete", fx.ProjectName, "--cascade", "--discard-snapshots", "-y")
		found := false
		for _, r := range fx.Fake.RequestLog() {
			if r.Method == "DELETE" && strings.Contains(r.RawQuery, "retainsnapshotbackups=false") {
				found = true
			}
		}
		if !found {
			t.Error("no cluster delete with retainsnapshotbackups=false")
		}
	})
	t.Run("deletion protection aborts before anything is deleted", func(t *testing.T) {
		fx := newFixture(t)
		other := fx.Fake.SeedCluster(fx.ProjectID, "aaa-unprotected", "healthy")
		if err := fx.Client.SetDeletionProtection(context.Background(), fx.ProjectID, fx.ClusterID, true); err != nil {
			t.Fatal(err)
		}
		fx.Fake.ResetLog()
		ce := wantExitErr(t, runResource(t, "project", "delete", fx.ProjectName, "--cascade", "-y"), ExitBlocked, "cluster clu has deletion protection on")
		if !strings.Contains(ce.Action, "cluster protect clu --off") {
			t.Errorf("action = %q", ce.Action)
		}
		if n := fx.Fake.Count("DELETE", `.`); n != 0 {
			t.Errorf("%d DELETEs sent, want none", n)
		}
		if st := fx.Fake.ClusterState(other); st != "healthy" {
			t.Errorf("unprotected cluster state = %q, want healthy", st)
		}
	})
	t.Run("--no-wait returns after the accepted deletes", func(t *testing.T) {
		fx := newFixture(t)
		obj := jsonObject(t, mustRun(t, "project", "delete", fx.ProjectName, "--cascade", "-y", "--no-wait", "-o", "json"))
		if obj["status"] != "accepted" {
			t.Errorf("object = %v", obj)
		}
		if n := fx.Fake.Count("GET", `/clusters/[^/]+$`); n != 0 {
			t.Errorf("%d polls with --no-wait, want 0", n)
		}
		if n := fx.Fake.Count("DELETE", `/projects/[^/]+$`); n != 0 {
			t.Errorf("project deleted with --no-wait while clusters still exist")
		}
		if st := fx.Fake.ClusterState(fx.ClusterID); st != "destroying" {
			t.Errorf("cluster state = %q, want destroying", st)
		}
	})
	t.Run("a stuck cluster times out with exit 5", func(t *testing.T) {
		fx := newFixture(t)
		fx.Fake.SettlePolls(1000)
		res := runResource(t, "project", "delete", fx.ProjectName, "--cascade", "-y", "--timeout", "30s")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "continues in Capella") {
			t.Fatalf("err = %v", res.Err)
		}
		if code := exitCodeFor(res.Err); code != ExitTimeout {
			t.Errorf("exit code = %d, want %d", code, ExitTimeout)
		}
		if fx.Fake.Count("DELETE", `/projects/[^/]+$`) != 0 {
			t.Error("project deleted although the cluster is still being deleted")
		}
	})
	t.Run("free-tier cluster", func(t *testing.T) {
		f := resEnv(t)
		pid := f.SeedProject("free-proj")
		id, err := f.Client().CreateFreeCluster(context.Background(), pid, map[string]any{"name": "freebie"})
		if err != nil {
			t.Fatal(err)
		}
		f.SetClusterState(id, "healthy")
		mustRun(t, "project", "delete", "free-proj", "--cascade", "-y")
		if f.Count("DELETE", `/clusters/freeTier/`) != 1 {
			t.Error("free-tier delete endpoint was not used")
		}
	})
}

// exitCodeFor returns the exit code HandleError picks for err, discarding what it prints.
func exitCodeFor(err error) ExitCode {
	old := errWriter
	errWriter = io.Discard
	defer func() { errWriter = old }()
	return HandleError(err)
}
