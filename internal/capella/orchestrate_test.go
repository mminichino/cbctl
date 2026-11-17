package capella

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/engine"
)

// legacyAPI is a minimal Capella v4 stand-in for the legacy backend: one organization, one user,
// one project and the clusters the test configures. clusterGet decides each single-cluster GET.
type legacyAPI struct {
	t          *testing.T
	mu         sync.Mutex
	clusters   []map[string]any
	listStatus int
	clusterGet func(n int) (int, any)
	gets       atomic.Int32
	deletes    atomic.Int32
}

func (a *legacyAPI) serve(w http.ResponseWriter, r *http.Request) {
	const org = "/v4/organizations/org-1"
	write := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == org:
		write(200, map[string]any{"id": "org-1", "name": "org"})
	case p == org+"/users/u1":
		write(200, map[string]any{"id": "u1", "email": "dev@example.com"})
	case p == org+"/projects/p1":
		write(200, map[string]any{"id": "p1", "name": "default"})
	case p == org+"/projects/p1/clusters":
		if a.listStatus != 0 {
			write(a.listStatus, map[string]any{"code": a.listStatus, "message": "boom"})
			return
		}
		write(200, map[string]any{"data": a.clusters, "cursor": map[string]any{"pages": map[string]any{"page": 1, "last": 1}}})
	case strings.HasPrefix(p, org+"/projects/p1/clusters/c1/"):
		write(200, map[string]any{"certificate": ""}) // certificate and other sub-resources
	case strings.HasPrefix(p, org+"/projects/p1/clusters/"):
		if r.Method == http.MethodDelete {
			a.deletes.Add(1)
			write(202, nil)
			return
		}
		n := int(a.gets.Add(1))
		status, body := a.clusterGet(n)
		write(status, body)
	default:
		a.t.Errorf("unexpected %s %s", r.Method, p)
		write(404, map[string]any{"code": 404, "message": "not found"})
	}
}

func newLegacyAPI(t *testing.T) (*legacyAPI, *config.Config) {
	t.Helper()
	a := &legacyAPI{t: t, clusters: []map[string]any{{"id": "c1", "name": "db1", "currentState": "healthy"}}}
	a.clusterGet = func(int) (int, any) {
		return 200, map[string]any{"id": "c1", "name": "db1", "currentState": "destroying"}
	}
	srv := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(srv.Close)
	cfg := config.New()
	cfg.SetProp(config.PropCapellaToken, "test-token")
	cfg.SetProp(config.PropCapellaAPIHost, srv.URL)
	cfg.SetProp(config.PropCapellaOrgID, "org-1")
	cfg.SetProp(config.PropCapellaProjectID, "p1")
	cfg.SetProp(config.PropCapellaUserID, "u1")
	cfg.SetProp(config.PropCapellaDatabase, "db1")
	return a, cfg
}

func TestClusterExists(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		_, cfg := newLegacyAPI(t)
		b := NewBackend()
		b.Clock = engine.NewFakeClock(time.Time{})
		ok, err := b.ClusterExists(context.Background(), cfg)
		if err != nil || !ok {
			t.Fatalf("ClusterExists = %v, %v; want true, nil", ok, err)
		}
	})
	t.Run("missing is false without error", func(t *testing.T) {
		a, cfg := newLegacyAPI(t)
		a.clusters = nil
		b := NewBackend()
		b.Clock = engine.NewFakeClock(time.Time{})
		ok, err := b.ClusterExists(context.Background(), cfg)
		if err != nil || ok {
			t.Fatalf("ClusterExists = %v, %v; want false, nil", ok, err)
		}
	})
	t.Run("server error is an error, not false", func(t *testing.T) {
		a, cfg := newLegacyAPI(t)
		a.listStatus = http.StatusInternalServerError
		b := NewBackend()
		b.Clock = engine.NewFakeClock(time.Time{}) // retries back off instantly
		ok, err := b.ClusterExists(context.Background(), cfg)
		if err == nil {
			t.Fatalf("ClusterExists = %v, nil; want an error for HTTP 500", ok)
		}
	})
	t.Run("nil context is refused", func(t *testing.T) {
		_, cfg := newLegacyAPI(t)
		if _, err := NewBackend().ClusterExists(nil, cfg); err == nil { //nolint:staticcheck // nil ctx is the case under test
			t.Fatal("want an error for a nil context")
		}
	})
}

func TestDestroyClusterWait(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		a, cfg := newLegacyAPI(t)
		a.clusterGet = func(n int) (int, any) {
			if n < 3 {
				return 200, map[string]any{"id": "c1", "name": "db1", "currentState": "destroying"}
			}
			return 404, map[string]any{"code": 404, "message": "not found"}
		}
		b := NewBackend()
		b.Clock = engine.NewFakeClock(time.Time{})
		if err := b.DestroyCluster(context.Background(), cfg); err != nil {
			t.Fatalf("DestroyCluster: %v", err)
		}
		if a.deletes.Load() != 1 {
			t.Fatalf("DELETE calls = %d, want 1", a.deletes.Load())
		}
	})
	t.Run("403 ends the wait at once", func(t *testing.T) {
		a, cfg := newLegacyAPI(t)
		a.clusterGet = func(int) (int, any) {
			return 403, map[string]any{"code": 1003, "message": "forbidden"}
		}
		b := NewBackend()
		b.Clock = engine.NewFakeClock(time.Time{})
		err := b.DestroyCluster(context.Background(), cfg)
		if err == nil {
			t.Fatal("want an error for HTTP 403 during the wait")
		}
		if got := a.gets.Load(); got != 1 {
			t.Fatalf("cluster GETs = %d, want 1 (no polling after 403)", got)
		}
	})
	t.Run("wait timeout is typed", func(t *testing.T) {
		_, cfg := newLegacyAPI(t)
		b := NewBackend()
		b.Clock = engine.NewFakeClock(time.Time{})
		b.WaitTimeout = time.Minute
		err := b.DestroyCluster(context.Background(), cfg)
		var wt *engine.WaitTimeout
		if !errors.As(err, &wt) {
			t.Fatalf("err = %v, want *engine.WaitTimeout", err)
		}
	})
	t.Run("context deadline stops the wait", func(t *testing.T) {
		_, cfg := newLegacyAPI(t)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		b := NewBackend() // real clock: the first sleep (10s) outlives the deadline
		start := time.Now()
		err := b.DestroyCluster(ctx, cfg)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("wait took %s after a 300ms deadline", el)
		}
	})
	t.Run("cancel stops the wait", func(t *testing.T) {
		_, cfg := newLegacyAPI(t)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(200 * time.Millisecond); cancel() }()
		err := NewBackend().DestroyCluster(ctx, cfg)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// TestWaitReadyHealthy checks the legacy wait returns on the first healthy poll.
func TestWaitReadyHealthy(t *testing.T) {
	a, cfg := newLegacyAPI(t)
	a.clusterGet = func(int) (int, any) {
		return 200, map[string]any{"id": "c1", "name": "db1", "currentState": "healthy"}
	}
	b := NewBackend()
	b.Clock = engine.NewFakeClock(time.Time{})
	project, err := b.resolveProject(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveProject: %v", err)
	}
	cl := NewCluster(project)
	if err := cl.wait("c1", "db1", engine.OpReady); err != nil {
		t.Fatalf("wait for healthy: %v", err)
	}
}
