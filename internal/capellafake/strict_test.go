package capellafake_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
)

// recordingTB captures Errorf so a test can assert that the fake failed it.
type recordingTB struct {
	testing.TB
	mu   sync.Mutex
	errs []string
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errs...)
}

func getStatus(t *testing.T, f *capellafake.Fake, path string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // only the status matters
	return resp.StatusCode
}

// TestFake_CAP004_AC1_UnhandledCallFailsLoudly: an unknown route fails the test (and still
// answers 404, as Capella would), so a mistyped path is never read as "absent".
func TestFake_CAP004_AC1_UnhandledCallFailsLoudly(t *testing.T) {
	rec := &recordingTB{TB: t}
	f := capellafake.New(rec)
	if got := getStatus(t, f, "/v4/organizations/"+f.OrgID+"/nope"); got != http.StatusNotFound {
		t.Errorf("status = %d, want 404", got)
	}
	errs := rec.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "capellafake: unhandled GET /v4/organizations/"+f.OrgID+"/nope") {
		t.Errorf("errors = %q", errs)
	}

	// A known route that answers 404 for a missing resource is not a failure.
	rec2 := &recordingTB{TB: t}
	f2 := capellafake.New(rec2)
	if got := getStatus(t, f2, "/v4/organizations/"+f2.OrgID+"/projects/no-such-project"); got != http.StatusNotFound {
		t.Errorf("missing project status = %d", got)
	}
	if errs := rec2.errors(); len(errs) != 0 {
		t.Errorf("missing resource failed the test: %q", errs)
	}

	// Lenient mode: plain 404, no failure.
	rec3 := &recordingTB{TB: t}
	f3 := capellafake.New(rec3, capellafake.WithLenientRoutes())
	if got := getStatus(t, f3, "/v4/nope"); got != http.StatusNotFound {
		t.Errorf("lenient status = %d", got)
	}
	if errs := rec3.errors(); len(errs) != 0 {
		t.Errorf("lenient fake failed the test: %q", errs)
	}
}

// TestFake_CAP004_AC3_DuplicateNames: Capella allows duplicate project and cluster names, but
// child resources (buckets, scopes, collections, credentials, CIDRs) conflict.
func TestFake_CAP004_AC3_DuplicateNames(t *testing.T) {
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client()
	p1, err1 := c.CreateProject(ctx, "p", "")
	p2, err2 := c.CreateProject(ctx, "p", "")
	if err1 != nil || err2 != nil || p1 == p2 {
		t.Fatalf("projects: %q %q %v %v", p1, p2, err1, err2)
	}
	ps, err := c.ListProjects(ctx)
	if err != nil || len(ps) != 2 || ps[0].Name != "p" || ps[1].Name != "p" {
		t.Fatalf("list = %+v, %v", ps, err)
	}
	if err := c.UpdateProject(ctx, p2, "p", "renamed onto itself", ""); err != nil {
		t.Errorf("update to a shared name: %v", err)
	}

	cid := f.SeedCluster(p1, "c", "healthy")
	f.SeedBucket(cid, "b")
	if _, err := c.CreateBucket(ctx, p1, cid, capellaapi.CreateBucketRequest{Name: "b"}); !errors.Is(err, capellaapi.ErrConflict) {
		t.Errorf("duplicate bucket: err = %v, want ErrConflict", err)
	}
}
