//go:build integration

package integration_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestScaling_CAP015_AC2: 3→4 data nodes then back to 3 on the shared cluster.
func TestScaling_CAP015_AC2(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	opts := []integration.CLIOption{integration.WithConfig(shared.Cfg)}

	tg.MustCLI(t, opts, "cluster", "scale", shared.Cluster, "--nodes", "4")
	tg.MustCLI(t, opts, "cluster", "scale", shared.Cluster, "--nodes", "3")
}
