//go:build integration

package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestDataModel_CAP023_AC8_CAP025_AC8: bucket + scope + collection create/update/delete idempotency.
func TestDataModel_CAP023_AC8_CAP025_AC8(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	shared := integration.SharedProject(t, tg)

	cfg := filepath.Join(t.TempDir(), "capella.yaml")
	shared.ImportInto(t, tg, cfg, true)
	opts := []integration.CLIOption{integration.WithConfig(cfg)}

	cluster, bucket := shared.Cluster, "cbctl-it-bucket"

	tg.MustCLI(t, opts, "bucket", "add", cluster, bucket, "--memory", "100")
	r := tg.CLIOpts(t, append(opts, integration.NoApprove()), "plan", "--detailed-exitcode")
	if r.Code != 0 {
		t.Fatalf("plan after bucket add: exit %d\n%s", r.Code, r.Output)
	}

	tg.MustCLI(t, opts, "scope", "add", cluster, bucket, "inventory")
	tg.MustCLI(t, opts, "collection", "add", cluster, bucket, "inventory", "items")
	tg.MustCLI(t, opts, "collection", "add", cluster, bucket, "inventory", "suppliers", "--max-ttl", "-1")

	r = tg.CLIOpts(t, append(opts, integration.NoApprove()), "plan", "--detailed-exitcode")
	if r.Code != 0 {
		t.Fatalf("plan after scopes/collections: exit %d\n%s", r.Code, r.Output)
	}

	tg.MustCLI(t, opts, "collection", "delete", cluster, bucket, "inventory", "items")
	tg.MustCLI(t, opts, "scope", "delete", cluster, bucket, "inventory")
	tg.MustCLI(t, opts, "bucket", "delete", cluster, bucket)
}
