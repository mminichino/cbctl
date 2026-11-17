package engine_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
)

// TestWaitForClusterWithClient drives WaitForCluster through the real capellaapi client against an
// httptest server, proving AsClusterGetter and ErrNotFound line up.
func TestWaitForClusterWithClient(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T, states []string) (*capellaapi.Client, *int) {
		t.Helper()
		polls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			i := polls
			polls++
			if i >= len(states) {
				i = len(states) - 1
			}
			if states[i] == "404" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"gone"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"c1","name":"demo","currentState":"` + states[i] + `"}`))
		}))
		t.Cleanup(srv.Close)
		c, err := capellaapi.New("test-key-0000", capellaapi.WithBaseURL(srv.URL), capellaapi.WithOrgID("o1"),
			capellaapi.WithMaxAttempts(1))
		if err != nil {
			t.Fatal(err)
		}
		return c, &polls
	}

	t.Run("create reaches healthy", func(t *testing.T) {
		t.Parallel()
		c, polls := newClient(t, []string{"deploying", "deploying", "healthy"})
		got, err := engine.WaitForCluster(context.Background(), c.AsClusterGetter(), "p1", "c1", engine.OpCreate,
			time.Hour, engine.NewFakeClock(time.Unix(0, 0)), "", engine.WaitHooks{})
		if err != nil || got != "healthy" || *polls != 3 {
			t.Errorf("got=%q err=%v polls=%d", got, err, *polls)
		}
	})

	t.Run("delete succeeds on 404", func(t *testing.T) {
		t.Parallel()
		c, _ := newClient(t, []string{"destroying", "404"})
		got, err := engine.WaitForCluster(context.Background(), c.AsClusterGetter(), "p1", "c1", engine.OpDelete,
			time.Hour, engine.NewFakeClock(time.Unix(0, 0)), "", engine.WaitHooks{})
		if err != nil || got != "deleted" {
			t.Errorf("got=%q err=%v", got, err)
		}
	})

	t.Run("404 on create is capellaapi.ErrNotFound", func(t *testing.T) {
		t.Parallel()
		c, _ := newClient(t, []string{"404"})
		_, err := engine.WaitForCluster(context.Background(), c.AsClusterGetter(), "p1", "c1", engine.OpCreate,
			time.Hour, engine.NewFakeClock(time.Unix(0, 0)), "", engine.WaitHooks{})
		if !errors.Is(err, capellaapi.ErrNotFound) {
			t.Errorf("err = %v, want capellaapi.ErrNotFound", err)
		}
	})
}
