//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

const lifecycleExtra = `
    deletionProtection: false
    allowedCidrs:
      - cidr: 203.0.113.10/32
        comment: cbctl nightly
    buckets:
      - name: orders
        memoryAllocationInMb: 256
        scopes:
          - name: inventory
            collections:
              - name: items
    databaseCredentials:
      - name: ci_app
        access:
          - privileges: [data_reader]
            resources:
              buckets:
                - name: orders
credentialsOutput: ./.cbctl/credentials
`

// TestLifecycle_CAP032_AC1: validate → plan → apply → second apply (no writes) → drift clean → destroy.
func TestLifecycle_CAP032_AC1(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)

	project := tg.ProjectName("life")
	tg.Cleanup(t, project)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "capella.yaml")
	body := integration.SingleNodeConfig(project, "life1-"+tg.Run, integration.UniqueCIDR(tg.Run, 'l'), lifecycleExtra)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := filepath.Join(dir, ".cbctl", "credentials")
	if err := os.MkdirAll(creds, 0o700); err != nil {
		t.Fatal(err)
	}

	opts := []integration.CLIOption{integration.WithConfig(cfg)}

	r := tg.MustCLI(t, opts, "validate")
	if !strings.Contains(r.Stdout, "is valid") {
		t.Fatalf("validate:\n%s", r.Output)
	}

	tg.MustCLI(t, opts, "plan")

	r = tg.MustCLI(t, opts, "apply")
	if len(r.Writes()) == 0 {
		t.Fatalf("first apply created nothing (no write request lines); output:\n%s", r.Output)
	}

	if tg.Fake != nil {
		tg.Fake.ResetLog()
	}
	r = tg.MustCLI(t, opts, "apply")
	if !strings.Contains(r.Stdout, "No changes") {
		t.Fatalf("second apply is not a no-op:\n%s", r.Output)
	}
	if writes := r.Writes(); len(writes) != 0 {
		t.Fatalf("second apply wrote to Capella: %v", writes)
	}

	r = tg.MustCLI(t, opts, "drift")
	if !strings.Contains(r.Stdout, "No drift") {
		t.Fatalf("drift not clean:\n%s", r.Output)
	}

	tg.MustCLI(t, opts, "destroy", "--discard-snapshots")

	r = tg.CLIOpts(t, []integration.CLIOption{integration.NoApprove()}, "project", "list")
	if r.Code != 0 {
		t.Fatalf("project list: exit %d\n%s", r.Code, r.Output)
	}
	if strings.Contains(r.Stdout, project) {
		t.Fatalf("project %s still listed after destroy:\n%s", project, r.Stdout)
	}
}
