package janitor_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/ci/janitor"
	"github.com/mminichino/cbctl/internal/engine"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func TestStaleFilter(t *testing.T) {
	cases := []struct {
		stamp string
		stale bool
	}{
		{"2026-10-19T10:00:00Z", true},
		{"2026-10-19T10:00:00.123456789Z", true},
		{"2026-10-20T10:00:00+00:00", false},
		{"not a date", false},
		{"", false},
	}
	for _, tc := range cases {
		p := capellaapi.Project{Name: "cbctl-ci-1", Audit: &capellaapi.Audit{CreatedAt: tc.stamp}}
		got := janitor.Stale([]capellaapi.Project{p}, "cbctl-ci-", 24*time.Hour, now)
		if (len(got) > 0) != tc.stale {
			t.Errorf("stamp %q: stale=%v want %v", tc.stamp, len(got) > 0, tc.stale)
		}
	}
}

func TestStalePrefixAndKeepUnreadable(t *testing.T) {
	projects := []capellaapi.Project{
		{Name: "cbctl-ci-old", Audit: &capellaapi.Audit{CreatedAt: "2026-10-18T00:00:00Z"}},
		{Name: "cbctl-ci-young", Audit: &capellaapi.Audit{CreatedAt: "2026-10-20T10:00:00Z"}},
		{Name: "production", Audit: &capellaapi.Audit{CreatedAt: "2020-01-01T00:00:00Z"}},
		{Name: "cbctl-ci-noread", Audit: &capellaapi.Audit{CreatedAt: "bogus"}},
		{Name: "cbctl-ci-nil"},
	}
	got := janitor.Stale(projects, janitor.Prefix, 24*time.Hour, now)
	if len(got) != 1 || got[0].Name != "cbctl-ci-old" {
		t.Fatalf("Stale = %v", got)
	}
}

func TestDeleteProjectTreeProtectionAndWait(t *testing.T) {
	f := capellafake.New(t)
	f.SettlePolls(0)
	clk := engine.NewFakeClock(now)
	c := f.Client(capellaapi.WithClock(clk))

	pid := f.SeedProject("cbctl-ci-111-1-shared")
	cid := f.SeedCluster(pid, "cbctl-ci-111-1-shared-01", "healthy")
	if err := c.SetDeletionProtection(context.Background(), pid, cid, true); err != nil {
		t.Fatal(err)
	}

	steps, err := janitor.DeleteProjectTree(context.Background(), c, capellaapi.Project{ID: pid, Name: "cbctl-ci-111-1-shared"}, time.Hour, clk)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(steps, "\n")
	if !strings.Contains(joined, "deletion protection off") {
		t.Fatalf("steps missing protection off: %v", steps)
	}
	if !strings.Contains(joined, "deleted cluster") || !strings.Contains(joined, "deleted project") {
		t.Fatalf("steps = %v", steps)
	}
	ps, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Fatalf("projects left = %v", ps)
	}
}

func TestDeleteProjectTreeRetryOnConflict(t *testing.T) {
	f := capellafake.New(t)
	f.SettlePolls(0)
	clk := engine.NewFakeClock(now)
	c := f.Client(capellaapi.WithClock(clk), capellaapi.WithMaxAttempts(1))

	pid := f.SeedProject("cbctl-ci-retry")
	_ = f.SeedCluster(pid, "c1", "healthy")
	f.FailNext("DELETE", `/clusters/`, 409, 1)

	steps, err := janitor.DeleteProjectTree(context.Background(), c, capellaapi.Project{ID: pid, Name: "cbctl-ci-retry"}, 10*time.Minute, clk)
	if err != nil {
		t.Fatal(err)
	}
	if len(clk.Sleeps()) == 0 {
		t.Fatal("expected a retry sleep after 409")
	}
	if !strings.Contains(strings.Join(steps, "\n"), "deleted project") {
		t.Fatalf("steps = %v", steps)
	}
}

func TestDeleteProjectTreeNotFoundTolerated(t *testing.T) {
	f := capellafake.New(t)
	f.SettlePolls(0)
	clk := engine.NewFakeClock(now)
	c := f.Client(capellaapi.WithClock(clk))

	pid := f.SeedProject("cbctl-ci-gone")
	// Pretend the project is already gone at list time by deleting via API first.
	if err := c.DeleteProject(context.Background(), pid); err != nil {
		t.Fatal(err)
	}
	steps, err := janitor.DeleteProjectTree(context.Background(), c, capellaapi.Project{ID: pid, Name: "cbctl-ci-gone"}, time.Hour, clk)
	if err != nil {
		t.Fatal(err)
	}
	// ListClusters → 404 → empty steps, nil error.
	if len(steps) != 0 {
		t.Fatalf("steps = %v", steps)
	}
}

func TestDeleteProjectsNamed(t *testing.T) {
	f := capellafake.New(t)
	f.SettlePolls(0)
	clk := engine.NewFakeClock(now)
	c := f.Client(capellaapi.WithClock(clk))

	a := f.SeedProject("cbctl-ci-exact")
	_ = f.SeedCluster(a, "c1", "healthy")
	keep := f.SeedProject("cbctl-ci-exact-other")
	_ = f.SeedCluster(keep, "c2", "healthy")

	steps, err := janitor.DeleteProjectsNamed(context.Background(), c, "cbctl-ci-exact", time.Hour, clk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(steps, "\n"), "deleted project cbctl-ci-exact") {
		t.Fatalf("steps = %v", steps)
	}
	ps, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "cbctl-ci-exact-other" {
		t.Fatalf("projects = %v", ps)
	}
}

func TestDeleteProjectTreeTimeout(t *testing.T) {
	f := capellafake.New(t)
	clk := engine.NewFakeClock(now)
	c := f.Client(capellaapi.WithClock(clk), capellaapi.WithMaxAttempts(1))

	pid := f.SeedProject("cbctl-ci-stuck")
	_ = f.SeedCluster(pid, "c1", "healthy")
	// Keep refusing forever within a tiny timeout.
	f.FailNext("DELETE", `/clusters/`, 409, 100)

	_, err := janitor.DeleteProjectTree(context.Background(), c, capellaapi.Project{ID: pid, Name: "cbctl-ci-stuck"}, janitor.RetryEvery/2, clk)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, capellaapi.ErrConflict) && !strings.Contains(err.Error(), "delete cluster") {
		t.Fatalf("err = %v", err)
	}
}
