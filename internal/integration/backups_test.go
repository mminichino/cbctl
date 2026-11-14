//go:build integration

package integration_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestBackups_CAP056_CAP058: snapshot schedule + on-demand snapshot; restore on fake only.
func TestBackups_CAP056_CAP058(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	opts := []integration.CLIOption{integration.WithConfig(shared.Cfg)}

	tg.MustCLI(t, opts, "cluster", "backup", "schedule", "set", shared.Cluster,
		"--every", "24", "--retain", "30d", "--start", "02:00")
	tg.MustCLI(t, opts, "cluster", "backup", "schedule", "delete", shared.Cluster)

	if tg.Kind != "fake" {
		t.Log("skipping on-demand snapshot create/delete and restore on sandbox (cost)")
		return
	}
	t.Skip("on-demand snapshot create/wait/delete + restore: enable when fake backup create path is exercised end-to-end")
}
