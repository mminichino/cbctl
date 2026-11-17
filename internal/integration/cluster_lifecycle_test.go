//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/integration"
)

// TestClusterLifecycle_CAP014_AC7: protect → delete blocked (exit 4) → protect --off → delete → absent.
func TestClusterLifecycle_CAP014_AC7(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)

	project := tg.ProjectName("clu-life")
	// Run-unique: a leftover project owning a fixed cluster name blocks apply via rename detection.
	cluster := "life-" + tg.Run
	tg.Cleanup(t, project)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "capella.yaml")
	extra := `
    deletionProtection: false
`
	body := integration.SingleNodeConfig(project, cluster, integration.UniqueCIDR(tg.Run, 'c'), extra)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := []integration.CLIOption{integration.WithConfig(cfg)}

	tg.MustCLI(t, opts, "apply")

	// Imperative Capella-side protect/delete (--direct) matches CAP-014 AC7.
	tg.MustCLI(t, opts, "cluster", "protect", cluster, "--on", "--direct")
	r := tg.CLIOpts(t, opts, "cluster", "delete", cluster, "--direct")
	if r.Code != int(cli.ExitBlocked) {
		t.Fatalf("protected delete: exit %d, want %d\n%s", r.Code, cli.ExitBlocked, r.Output)
	}

	tg.MustCLI(t, opts, "cluster", "protect", cluster, "--off", "--direct")
	r = tg.MustCLI(t, opts, "cluster", "delete", cluster, "--discard-snapshots", "--direct")
	if !strings.Contains(r.Stdout, "deleted cluster "+cluster) {
		t.Fatalf("delete output:\n%s", r.Output)
	}

	r = tg.MustCLI(t, opts, "cluster", "delete", cluster, "--discard-snapshots", "--direct")
	if !strings.Contains(r.Stdout, "not found") {
		t.Fatalf("second delete should be absent:\n%s", r.Output)
	}
	if writes := r.Writes(); len(writes) != 0 {
		t.Fatalf("second delete wrote to Capella: %v", writes)
	}
}
