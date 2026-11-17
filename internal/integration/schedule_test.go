//go:build integration

package integration_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestSchedule_CAP020_AC2: on/off schedule set → apply has nothing to do → per-day update →
// pause → resume → delete. The schedule is always on, so it never turns the shared cluster off.
func TestSchedule_CAP020_AC2(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	cfg := filepath.Join(t.TempDir(), "capella.yaml")
	shared.ImportInto(t, tg, cfg, false)
	opts := []integration.CLIOption{integration.WithConfig(cfg)}
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		if r := tg.CLIOpts(t, opts, "cluster", "schedule", "delete", shared.Cluster); r.Code != 0 {
			t.Errorf("schedule delete in cleanup: exit %d\n%s", r.Code, r.Output)
		}
	})

	tg.MustCLI(t, opts, "cluster", "schedule", "set", shared.Cluster,
		"--timezone", "US/Pacific", "--weekdays", "00:00-24:00", "--weekend", "on")
	r := tg.MustCLI(t, opts, "apply")
	if !strings.Contains(r.Output, "No changes") {
		t.Fatalf("apply after schedule set is not a no-op:\n%s", r.Output)
	}
	if w := r.Writes(); len(w) != 0 {
		t.Fatalf("apply after schedule set wrote to Capella: %v", w)
	}
	r = tg.MustCLI(t, opts, "cluster", "schedule", "set", shared.Cluster, "--sunday", "08:00-20:00")
	if !strings.Contains(r.Output, "sunday") {
		t.Fatalf("schedule set --sunday does not show the sunday change:\n%s", r.Output)
	}
	tg.MustCLI(t, opts, "cluster", "schedule", "pause", shared.Cluster)
	tg.MustCLI(t, opts, "cluster", "schedule", "resume", shared.Cluster)
	tg.MustCLI(t, opts, "cluster", "schedule", "delete", shared.Cluster)
	deleted = true
}
