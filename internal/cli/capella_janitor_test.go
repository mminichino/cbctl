package cli

import (
	"strings"
	"testing"
)

func TestJanitor(t *testing.T) {
	f := newFake(t)
	a := f.SeedProject("cbctl-it-aaa")
	f.SeedCluster(a, "one", "healthy")
	two := f.SeedCluster(a, "two", "healthy")
	f.SeedProject("cbctl-it-bbb")
	keep := f.SeedProject("production")
	f.SeedCluster(keep, "prod", "healthy")
	if err := f.Client().SetDeletionProtection(t.Context(), a, two, true); err != nil {
		t.Fatal(err)
	}
	remaining := func() []string {
		ps, err := f.Client().ListProjects(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, p := range ps {
			names = append(names, p.Name)
		}
		return names
	}

	runCap(t, "janitor").mustFail(t, ExitConfigInvalid, "--prefix is required")
	runCap(t, "janitor", "--prefix", "cb").mustFail(t, ExitConfigInvalid, "at least 3 characters")
	runCap(t, "janitor", "--prefix", "cbctl-it-").mustFail(t, ExitBlocked, "no terminal")
	if len(remaining()) != 3 {
		t.Fatalf("projects = %v", remaining())
	}
	runCap(t, "--yes", "janitor", "--prefix", "zzz-").mustOK(t).wantStdout(t, "nothing to do")

	var r janitorResult
	runCap(t, "-o", "json", "--yes", "janitor", "--prefix", "cbctl-it-").mustOK(t).jsonOut(t, &r)
	if r.Status != "deleted" || len(r.Projects) != 2 {
		t.Fatalf("result = %+v", r)
	}
	if got := strings.Join(remaining(), ","); got != "production" {
		t.Fatalf("projects left = %s", got)
	}
	left, err := f.Client().ListClusters(t.Context(), keep)
	if err != nil || len(left) != 1 {
		t.Fatalf("the unrelated project lost its cluster: %v, %v", left, err)
	}
}
