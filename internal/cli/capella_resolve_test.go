package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
)

func fakeRuntime(t *testing.T) (*runtime, *capellafake.Fake) {
	t.Helper()
	f := capellafake.New(t)
	return &runtime{Ctx: context.Background(), Client: f.Client()}, f
}

func wantCLIError(t *testing.T, err error, code ExitCode, msg, action string) {
	t.Helper()
	var ce *CLIError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *CLIError", err)
	}
	if ce.Code != code || ce.Message != msg || ce.Action != action {
		t.Fatalf("CLIError = {%d %q %q}, want {%d %q %q}", ce.Code, ce.Message, ce.Action, code, msg, action)
	}
}

func TestResolveProject(t *testing.T) {
	rt, f := fakeRuntime(t)
	alpha := f.SeedProject("alpha")
	f.SeedProject("Alpha") // exact, case-sensitive match
	f.SeedProject("with space/ünï")

	tests := []struct {
		name   string
		in     string
		wantID string
	}{
		{"exact", "alpha", alpha},
		{"unicode and spaces", "with space/ünï", ""},
		{"by id", alpha, alpha},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := rt.resolveProject(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantID != "" && p.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", p.ID, tc.wantID)
			}
			if tc.wantID == "" && p.Name != tc.in {
				t.Errorf("Name = %q, want %q", p.Name, tc.in)
			}
		})
	}

	t.Run("case sensitive", func(t *testing.T) {
		p, err := rt.resolveProject("Alpha")
		if err != nil || p.Name != "Alpha" || p.ID == alpha {
			t.Fatalf("got %+v, %v", p, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		_, err := rt.resolveProject("ALPHA")
		wantCLIError(t, err, ExitConfigInvalid, "project ALPHA not found", "check the name with 'cbctl capella project list'")
	})
	t.Run("API failure is not a CLIError", func(t *testing.T) {
		f.FailNext("GET", `/projects$`, 403, 1)
		_, err := rt.resolveProject("alpha")
		if !errors.Is(err, capellaapi.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}

func TestResolveProjectDuplicateNames(t *testing.T) {
	// The API forbids duplicate project names, so exercise the picker directly.
	projects := []capellaapi.Project{{ID: "a", Name: "dup"}, {ID: "b", Name: "dup"}}
	_, err := pickProject(projects, "dup")
	wantCLIError(t, err, ExitConfigInvalid, "several projects are named dup (a, b)", "use the project ID")
}

func TestResolveCluster(t *testing.T) {
	rt, f := fakeRuntime(t)
	pa := f.SeedProject("proj-a")
	pb := f.SeedProject("proj-b")
	solo := f.SeedCluster(pa, "solo", "healthy")
	dupA := f.SeedCluster(pa, "dup", "healthy")
	dupB := f.SeedCluster(pb, "dup", "turnedOff")

	t.Run("unique name across projects", func(t *testing.T) {
		p, c, err := rt.resolveCluster("solo", "")
		if err != nil {
			t.Fatal(err)
		}
		if p.ID != pa || c.ID != solo {
			t.Errorf("got project %q cluster %q", p.ID, c.ID)
		}
	})
	t.Run("scoped to a project", func(t *testing.T) {
		p, c, err := rt.resolveCluster("dup", "proj-b")
		if err != nil {
			t.Fatal(err)
		}
		if p.ID != pb || c.ID != dupB || c.CurrentState != "turnedOff" {
			t.Errorf("got project %q cluster %q state %q", p.ID, c.ID, c.CurrentState)
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		_, _, err := rt.resolveCluster("dup", "")
		wantCLIError(t, err, ExitConfigInvalid, "several clusters are named dup (projects proj-a, proj-b)", "add --project NAME")
	})
	t.Run("not found", func(t *testing.T) {
		_, _, err := rt.resolveCluster("nope", "")
		wantCLIError(t, err, ExitConfigInvalid, "cluster nope not found", "check the name with 'cbctl capella cluster list'")
	})
	t.Run("not found in the named project", func(t *testing.T) {
		_, _, err := rt.resolveCluster("solo", "proj-b")
		wantCLIError(t, err, ExitConfigInvalid, "cluster solo not found", "check the name with 'cbctl capella cluster list'")
	})
	t.Run("project not found", func(t *testing.T) {
		_, _, err := rt.resolveCluster("solo", "missing")
		wantCLIError(t, err, ExitConfigInvalid, "project missing not found", "check the name with 'cbctl capella project list'")
	})
	t.Run("by ID", func(t *testing.T) {
		p, c, err := rt.resolveCluster(dupB, "")
		if err != nil || p.ID != pb || c.ID != dupB {
			t.Fatalf("got %+v %+v %v", p, c, err)
		}
		// An ID is unambiguous even when the name is not.
		if _, c, err = rt.resolveCluster(dupA, "proj-a"); err != nil || c.ID != dupA {
			t.Fatalf("got %+v %v", c, err)
		}
	})
	t.Run("name wins over ID on a tie", func(t *testing.T) {
		pc := f.SeedProject("proj-c")
		named := f.SeedCluster(pc, solo, "healthy") // a cluster whose name is solo's ID
		_, c, err := rt.resolveCluster(solo, "")
		if err != nil {
			t.Fatal(err)
		}
		if c.ID != named {
			t.Errorf("resolved %q, want the cluster named %q (%q)", c.ID, solo, named)
		}
	})
	t.Run("listing failure surfaces the API error", func(t *testing.T) {
		f.FailNext("GET", `/clusters$`, 403, 1)
		_, _, err := rt.resolveCluster("solo", "proj-a")
		if !errors.Is(err, capellaapi.ErrForbidden) || !strings.Contains(err.Error(), "proj-a") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestResolveBucket(t *testing.T) {
	rt, f := fakeRuntime(t)
	p := f.SeedProject("proj")
	cl := f.SeedCluster(p, "clu", "healthy")
	c := f.Client()

	names := []string{"travel-sample", "with.dot", "per%cent", "a_b", "slash/plus+é"}
	for _, n := range names {
		if _, err := c.CreateBucket(context.Background(), p, cl, capellaapi.CreateBucketRequest{Name: n, MemoryAllocationInMb: 100}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			b, err := rt.resolveBucket(p, cl, n)
			if err != nil {
				t.Fatal(err)
			}
			if b.Name != n || b.ID != capellaapi.BucketID(n) {
				t.Errorf("got %+v", b)
			}
		})
	}
	t.Run("not found", func(t *testing.T) {
		_, err := rt.resolveBucket(p, cl, "missing")
		wantCLIError(t, err, ExitConfigInvalid, "bucket missing not found", "check the name with 'cbctl capella bucket list CLUSTER'")
	})
	t.Run("other errors pass through", func(t *testing.T) {
		f.FailNext("GET", `/buckets/`, 403, 1)
		_, err := rt.resolveBucket(p, cl, "travel-sample")
		if !errors.Is(err, capellaapi.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}
