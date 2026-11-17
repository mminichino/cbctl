package capella

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
)

func stubSRV(t *testing.T, fn func(string, bool) ([]string, error)) {
	t.Helper()
	prev := lookupSRVHostnames
	lookupSRVHostnames = fn
	t.Cleanup(func() { lookupSRVHostnames = prev })
}

// The SRV poll runs on the injected clock: it sleeps exactly until SRVLookupTimeout and then
// falls back to the bare hostname, with no real waiting.
func TestResolveSRVTargets_PollsOnClockThenFallsBack(t *testing.T) {
	var lookups int
	stubSRV(t, func(string, bool) ([]string, error) { lookups++; return nil, errors.New("no such host") })
	clk := engine.NewFakeClock(time.Time{})
	c := &Connectivity{SRVLookupTimeout: 10 * time.Second, SRVLookupPollInterval: 4 * time.Second, Clock: clk}

	got := c.resolveSRVTargets(context.Background(), "cb.example.cloud", true, managerPortTLS, true)
	if len(got) != 1 || got[0] != (HostPort{Host: "cb.example.cloud", Port: managerPortTLS}) {
		t.Fatalf("targets = %v", got)
	}
	sleeps := clk.Sleeps()
	want := []time.Duration{4 * time.Second, 4 * time.Second, 2 * time.Second}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", sleeps, want)
		}
	}
	if lookups != 4 {
		t.Fatalf("lookups = %d, want 4", lookups)
	}
}

func TestResolveSRVTargets_StopsOnCancel(t *testing.T) {
	var lookups int
	stubSRV(t, func(string, bool) ([]string, error) { lookups++; return nil, errors.New("no such host") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Connectivity{SRVLookupTimeout: time.Hour, SRVLookupPollInterval: time.Second, Clock: engine.NewFakeClock(time.Time{})}
	got := c.resolveSRVTargets(ctx, "cb.example.cloud", false, managerPortPlain, true)
	if lookups != 1 || len(got) != 1 || got[0].Port != managerPortPlain {
		t.Fatalf("lookups=%d targets=%v, want one lookup and the bare host", lookups, got)
	}
}

func TestResolveSRVTargets_UsesRecords(t *testing.T) {
	stubSRV(t, func(host string, tls bool) ([]string, error) {
		if !tls {
			t.Error("tls not passed through")
		}
		return []string{"n1.example.cloud", "n2.example.cloud"}, nil
	})
	clk := engine.NewFakeClock(time.Time{})
	c := &Connectivity{SRVLookupTimeout: time.Minute, SRVLookupPollInterval: time.Second, Clock: clk}
	got := c.ResolveTargets("couchbases://cb.example.cloud", true, true)
	if len(got) != 2 || got[0].Host != "n1.example.cloud" || got[1].Port != managerPortTLS {
		t.Fatalf("targets = %v", got)
	}
	if len(clk.Sleeps()) != 0 {
		t.Fatalf("slept %v with records available", clk.Sleeps())
	}
}

// CheckConnectivity measures its deadline on the clock: an unreachable target gives up after
// the timeout of fake time and reports false.
func TestCheckConnectivity_TimesOutOnClock(t *testing.T) {
	stubSRV(t, func(string, bool) ([]string, error) { return nil, errors.New("no such host") })
	clk := engine.NewFakeClock(time.Time{})
	c := &Connectivity{SRVLookupTimeout: 0, SRVLookupPollInterval: time.Second, Clock: clk}
	// Port 1 on loopback refuses immediately.
	if c.CheckConnectivity(context.Background(), "127.0.0.1:1", false, 5*time.Second) {
		t.Fatal("CheckConnectivity reported an unreachable port as reachable")
	}
	var total time.Duration
	for _, d := range clk.Sleeps() {
		total += d
	}
	if total != 5*time.Second {
		t.Fatalf("slept %v in total, want the 5s timeout", total)
	}
}
