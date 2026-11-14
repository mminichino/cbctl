//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

const itCredPassword = "Cbctl!It-Passw0rd"

// TestCredentials_CAP027_AC8: create credential; SDK connect is sandbox-only.
func TestCredentials_CAP027_AC8(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	if tg.Kind == "fake" {
		t.Skip("connecting with the SDK needs a real cluster; credential create exercised in lifecycle on fake")
	}
	if os.Getenv("CAPELLA_SANDBOX_CLIENT_CIDR") == "" {
		t.Skip("needs CAPELLA_SANDBOX_CLIENT_CIDR: this machine's address, allowed on the shared cluster")
	}
	tg.RequireClusters(t)

	shared := integration.SharedProject(t, tg)
	cfg := filepath.Join(t.TempDir(), "capella.yaml")
	shared.ImportInto(t, tg, cfg, true)
	opts := []integration.CLIOption{
		integration.WithConfig(cfg),
		integration.WithEnv("IT_CRED_PASSWORD", itCredPassword),
	}
	name := "cbctl_it_app"
	tg.MustCLI(t, opts, "credential", "create", shared.Cluster, name,
		"--privileges", "read+write", "--bucket", shared.Bucket, "--password-env", "IT_CRED_PASSWORD")
	r := tg.CLIOpts(t, append(opts, integration.NoApprove()), "plan", "--detailed-exitcode")
	if r.Code != 0 {
		t.Fatalf("plan after credential create: exit %d\n%s", r.Code, r.Output)
	}
	t.Cleanup(func() {
		tg.CLIOpts(t, opts, "credential", "delete", shared.Cluster, name)
	})
	t.Skip("SDK KV put/get against Capella connection string: extend with gocb when sandbox is available")
}
