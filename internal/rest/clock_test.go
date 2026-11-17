package rest

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
)

// The wait loops pace on Client.Clock, so their retry branches run instantly in tests and
// record exactly the intervals production would sleep.
func TestWaitLoops_UseInjectedClock(t *testing.T) {
	var calls atomic.Int32
	ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		// Fail twice, then report a node.
		if calls.Add(1) <= 2 {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[{"hostname":"127.0.0.1:8091"}]}`))
	})
	clk := engine.NewFakeClock(time.Time{})
	c := NewClient()
	c.Clock = clk

	if err := c.WaitForCluster(ep, "u", "p", 5); err != nil {
		t.Fatalf("WaitForCluster: %v", err)
	}
	if got := clk.Sleeps(); len(got) != 2 || got[0] != pollInterval || got[1] != pollInterval {
		t.Fatalf("sleeps = %v, want two of %v", got, pollInterval)
	}
}

func TestWaitForNodeAPI_ExhaustsAttemptsOnFakeClock(t *testing.T) {
	ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	clk := engine.NewFakeClock(time.Time{})
	c := NewClient()
	c.Clock = clk
	if err := c.WaitForNodeAPI(ep, 4); err == nil {
		t.Fatal("WaitForNodeAPI succeeded against a failing node")
	}
	// No sleep after the last attempt.
	if got := len(clk.Sleeps()); got != 3 {
		t.Fatalf("sleeps = %d, want 3", got)
	}
}

func TestClientSleep_DefaultsToRealClock(t *testing.T) {
	c := &Client{}
	start := time.Now()
	c.sleep(time.Millisecond)
	if time.Since(start) < time.Millisecond {
		t.Fatal("nil Clock should fall back to a real sleep")
	}
}

// A query service that never answers is reported as a warning (provisioning still succeeds);
// a ready one prints nothing.
func TestWarnIfQueryNotReady(t *testing.T) {
	t.Run("not ready warns", func(t *testing.T) {
		ep := closedEndpoint(t)
		ep.QueryPort = ep.AdminPort
		c := NewClient()
		c.Clock = engine.NewFakeClock(time.Time{})
		var errBuf strings.Builder
		logging.WithOutputs(io.Discard, &errBuf, func() { c.warnIfQueryNotReady(ep, "u", "p") })
		if !strings.Contains(errBuf.String(), "query service on 127.0.0.1 is not ready yet") {
			t.Fatalf("stderr = %q", errBuf.String())
		}
	})
	t.Run("ready is silent", func(t *testing.T) {
		f := newFakeCB(t)
		ep := f.endpoint()
		ep.QueryPort = ep.AdminPort
		var errBuf strings.Builder
		logging.WithOutputs(io.Discard, &errBuf, func() { NewClient().warnIfQueryNotReady(ep, "u", "p") })
		if errBuf.Len() != 0 {
			t.Fatalf("stderr = %q", errBuf.String())
		}
	})
}
