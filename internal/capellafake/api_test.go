package capellafake_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

func TestFake_Options_AndAccessors(t *testing.T) {
	t.Parallel()
	clk := engine.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	f := capellafake.New(t,
		capellafake.WithOrgID("org-custom"),
		capellafake.WithAPIKey("fake-api-key-0123456789"),
		capellafake.WithClock(clk),
	)
	if f.OrgID != "org-custom" || f.APIKey != "fake-api-key-0123456789" {
		t.Fatalf("options: org=%q key=%q", f.OrgID, f.APIKey)
	}
	if f.URL == "" || f.Clock() != clk || f.Handler() == nil {
		t.Fatalf("accessors: url=%q clock=%v handler=%v", f.URL, f.Clock(), f.Handler())
	}
	f.Advance(30 * time.Second)
	want := time.Date(2026, 1, 2, 3, 4, 35, 0, time.UTC)
	if got := f.Clock().Now(); !got.Equal(want) {
		t.Fatalf("Advance: now=%v want=%v", got, want)
	}
	_ = capellafake.New(t) // New(t) without options still works
}

func TestFake_Calls_Writes_ResetCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client()
	pid := f.SeedProject("p")
	if _, err := c.CreateCluster(ctx, pid, clusterReq("c1")); err != nil {
		t.Fatal(err)
	}
	posts := f.Calls("POST", "/v4/organizations/{org}/projects/{project}/clusters")
	if len(posts) != 1 || posts[0].Status != 202 {
		t.Fatalf("Calls POST clusters = %+v", posts)
	}
	if len(f.Writes()) == 0 {
		t.Fatal("Writes empty")
	}
	redact.Register("should-not-leak-secret-xyz")
	t.Cleanup(redact.Reset)
	call := capellafake.Call{Method: "POST", Path: "/x", Body: map[string]any{"password": "secret-value"}, Status: 201}
	if s := call.String(); strings.Contains(s, "secret-value") || !strings.Contains(s, "***") {
		t.Fatalf("Call.String redaction: %s", s)
	}
	f.ResetCalls()
	if n := len(f.RequestLog()); n != 0 {
		t.Fatalf("ResetCalls left %d", n)
	}
}

func TestFake_FailNextOpts_CodeMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client(capellaapi.WithMaxAttempts(1))
	f.FailNextOpts("GET", "/v4/organizations/{org}/projects", capellafake.FaultOptions{
		Status: 403, Code: 1002, Message: "Access Denied.", Times: 1,
	})
	if _, err := c.ListProjects(ctx); err == nil {
		t.Fatal("expected injected FailNextOpts error")
	}
	// Existing FailNext regexp form still works.
	f.FailNext("GET", `/projects$`, 503, 1)
	if _, err := c.ListProjects(ctx); err == nil {
		t.Fatal("want FailNext 503")
	}
}

func TestFake_FailNextOpts_DropAfterRead(t *testing.T) {
	t.Parallel()
	f := capellafake.New(t)
	f.FailNextOpts("GET", "/v4/organizations", capellafake.FaultOptions{
		DropAfterRead: true, Times: 1,
	})
	req, err := http.NewRequest(http.MethodGet, f.URL+"/v4/organizations", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.APIKey)
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		t.Fatalf("expected connection error from DropAfterRead, got status %d", res.StatusCode)
	}
}

func TestFake_Seeds_AndTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clk := engine.NewFakeClock(time.Time{})
	f := capellafake.New(t, capellafake.WithClock(clk))
	c := f.Client()
	pid := f.SeedProject("proj", capellafake.WithField("description", "seeded"))
	cid := f.SeedCluster(pid, "clu", "healthy", capellafake.WithField("description", "d"))
	if id := f.SeedBucket(cid, "b1", capellafake.WithField("memoryAllocationInMb", 200)); id == "" {
		t.Fatal("SeedBucket empty id")
	}
	f.SeedScope(cid, "b1", "app", map[string]int{"orders": 0, "items": 3600})
	if id := f.SeedCredential(cid, "appuser", []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
	}}, false); id == "" {
		t.Fatal("SeedCredential empty")
	}
	if f.SeedSnapshot(cid, "", 24, "complete") == "" || f.SeedBucketBackup(cid, "b1", "full", "manual") == "" {
		t.Fatal("SeedSnapshot/SeedBucketBackup empty")
	}
	if f.SeedFreeCluster(f.SeedProject("free"), "free-1", "healthy") == "" {
		t.Fatal("SeedFreeCluster empty")
	}

	f.SetTransition(cid, []string{"scaling", "healthy"}, 10*time.Second)
	if st := f.ClusterState(cid); st != "scaling" {
		t.Fatalf("after SetTransition: %q", st)
	}
	f.Advance(10 * time.Second)
	cl, _, err := c.GetCluster(ctx, pid, cid)
	if err != nil {
		t.Fatal(err)
	}
	if cl.CurrentState != "healthy" {
		t.Fatalf("after Advance+GET: %q", cl.CurrentState)
	}

	other := f.SeedCluster(pid, "doomed", "healthy")
	f.SettlePolls(0)
	f.FailDestroy(other)
	if err := c.DeleteCluster(ctx, pid, other); err != nil {
		t.Fatal(err)
	}
	got, _, err := c.GetCluster(ctx, pid, other)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentState != "destroyFailed" {
		t.Fatalf("FailDestroy settled state=%q", got.CurrentState)
	}
}

// TestFake_* aliases for existing behavioural coverage (Phase 4 §3.8 naming).
func TestFake_Auth_BearerRequired(t *testing.T) { TestAuthAndOrganizations(t) }
func TestFake_Projects_CRUD(t *testing.T)       { TestProjectCRUD(t) }
func TestFake_Clusters_Lifecycle(t *testing.T)   { TestClusterLifecycle(t) }
func TestFake_Faults_FailNext(t *testing.T)      { TestFailNextAndRequestLog(t) }
