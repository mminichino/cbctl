//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestPower_CAP019_AC2: power --off → --off again (nothing to do, no writes) → --on on the shared
// cluster. The same steps run on the fake and on the sandbox.
func TestPower_CAP019_AC2(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	opts := []integration.CLIOption{integration.WithConfig(shared.Cfg)}

	on := false
	t.Cleanup(func() {
		if on {
			return
		}
		if r := tg.CLIOpts(t, opts, "cluster", "power", shared.Cluster, "--on"); r.Code != 0 {
			t.Errorf("power --on in cleanup: exit %d\n%s", r.Code, r.Output)
		}
	})

	r := tg.MustCLI(t, opts, "cluster", "power", shared.Cluster, "--off")
	if !strings.Contains(r.Output, "is off") {
		t.Fatalf("power --off output:\n%s", r.Output)
	}
	r = tg.MustCLI(t, opts, "cluster", "power", shared.Cluster, "--off")
	if !strings.Contains(r.Output, "already off") {
		t.Fatalf("second power --off output:\n%s", r.Output)
	}
	if writes := r.Writes(); len(writes) != 0 {
		t.Fatalf("second power --off wrote to Capella: %v", writes)
	}
	r = tg.MustCLI(t, opts, "cluster", "power", shared.Cluster, "--on")
	on = true
	if !strings.Contains(r.Output, "is on") {
		t.Fatalf("power --on output:\n%s", r.Output)
	}
}
