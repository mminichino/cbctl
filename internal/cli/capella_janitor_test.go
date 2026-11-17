package cli

import (
	"strings"
	"testing"
	"time"
)

func TestJanitor(t *testing.T) {
	f := newFake(t)
	f.SettlePolls(0)
	// Fake stamps audit.createdAt with wall-clock now; advance the CLI clock so Stale
	// treats the CI projects as older than 24 hours.
	setClock(t, time.Now().UTC().Add(48*time.Hour))

	a := f.SeedProject("cbctl-ci-aaa")
	f.SeedCluster(a, "one", "healthy")
	two := f.SeedCluster(a, "two", "healthy")
	f.SeedProject("cbctl-ci-bbb")
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

	runCap(t, "janitor", "--prefix", "cb").mustFail(t, ExitConfigInvalid, "at least 4 characters")
	runCap(t, "janitor", "--prefix", "cbctl").mustFail(t, ExitConfigInvalid, "must end with")
	runCap(t, "janitor", "--prefix", "cbctl-ci-").mustFail(t, ExitBlocked, "no terminal")
	if len(remaining()) != 3 {
		t.Fatalf("projects = %v", remaining())
	}
	runCap(t, "--yes", "janitor", "--prefix", "zzz-", "--older-than-hours", "0").mustOK(t).wantStdout(t, "no zzz-*")

	var r janitorResult
	runCap(t, "-o", "json", "--yes", "janitor", "--prefix", "cbctl-ci-", "--older-than-hours", "24").mustOK(t).jsonOut(t, &r)
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

func TestJanitorDryRunFailOnLeftovers(t *testing.T) {
	f := newFake(t)
	setClock(t, time.Now().UTC().Add(48*time.Hour))
	f.SeedProject("cbctl-ci-old")

	runCap(t, "--yes", "janitor", "--dry-run", "--fail-on-leftovers").mustFail(t, ExitError, "leftover")
	ps, err := f.Client().ListProjects(t.Context())
	if err != nil || len(ps) != 1 {
		t.Fatalf("dry-run deleted something: %v %v", ps, err)
	}
}

func TestJanitorBudgetAndFlags(t *testing.T) {
	runCap(t, "--yes", "janitor", "--timeout-minutes", "0").mustFail(t, ExitConfigInvalid, "--timeout-minutes")
	runCap(t, "--yes", "janitor", "--max-total-minutes", "-1").mustFail(t, ExitConfigInvalid, "--max-total-minutes")

	f := newFake(t)
	f.SettlePolls(0)
	setClock(t, time.Now().UTC().Add(48*time.Hour))
	a := f.SeedProject("cbctl-ci-a")
	f.SeedCluster(a, "stuck", "healthy")
	b := f.SeedProject("cbctl-ci-b")
	f.SeedCluster(b, "stuck2", "healthy")
	// Every cluster delete fails, so the first project uses the whole budget.
	f.FailNext("DELETE", `/clusters/`, 409, 1000)

	var r janitorResult
	res := runCap(t, "-o", "json", "--yes", "janitor", "--max-total-minutes", "2")
	res.mustFail(t, ExitError, "skipped")
	res.jsonOut(t, &r)
	if r.Failed != 1 || len(r.Skipped) != 1 {
		t.Fatalf("result = %+v, want one failure and one skipped", r)
	}
}
