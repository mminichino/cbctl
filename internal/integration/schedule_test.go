//go:build integration

package integration_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestSchedule_CAP020_AC2: hibernation schedule create/pause/resume/delete.
func TestSchedule_CAP020_AC2(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	opts := []integration.CLIOption{integration.WithConfig(shared.Cfg)}

	tg.MustCLI(t, opts, "cluster", "schedule", "set", shared.Cluster,
		"--timezone", "US/Pacific", "--weekdays", "08:00-18:00", "--weekend", "off")
	tg.MustCLI(t, opts, "cluster", "schedule", "pause", shared.Cluster)
	tg.MustCLI(t, opts, "cluster", "schedule", "resume", shared.Cluster)
	tg.MustCLI(t, opts, "cluster", "schedule", "delete", shared.Cluster)
}
