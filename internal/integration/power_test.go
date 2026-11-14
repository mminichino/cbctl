//go:build integration

package integration_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestPower_CAP019_AC2: cluster power --off / --on round trip on the shared cluster.
func TestPower_CAP019_AC2(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	if tg.Kind == "fake" {
		// Fake models power transitions; full plan-blocked-children assertion needs more surface.
		shared := integration.SharedProject(t, tg)
		opts := []integration.CLIOption{integration.WithConfig(shared.Cfg)}
		tg.MustCLI(t, opts, "cluster", "power", shared.Cluster, "--off")
		tg.MustCLI(t, opts, "cluster", "power", shared.Cluster, "--on")
		return
	}
	t.Skip("sandbox power cycle: enable once nightly shared cluster power is accepted")
}
