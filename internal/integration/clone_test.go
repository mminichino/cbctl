//go:build integration

package integration_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestClone_CAP049_AC9: in the shared project, create a single-node cluster with a bucket, scope
// and collection, clone it to another region, check that the plan is clean, then delete both.
func TestClone_CAP049_AC9(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)

	cfg := filepath.Join(t.TempDir(), "capella.yaml")
	shared.ImportInto(t, tg, cfg, false)
	opts := []integration.CLIOption{integration.WithConfig(cfg)}
	source, clone := "cbctl-it-clone-src", "cbctl-it-clone-dst"
	t.Cleanup(func() {
		for _, name := range []string{clone, source} {
			if r := tg.CLIOpts(t, opts, "cluster", "delete", name, "--project", shared.Project, "--discard-snapshots"); r.Code != 0 {
				t.Errorf("delete %s: exit %d\n%s", name, r.Code, r.Output)
			}
		}
	})

	tg.MustCLI(t, opts, "cluster", "create", source, "--size", "dev", "--support-plan", "basic",
		"--region", "us-east-1", "--project", shared.Project)
	for _, args := range [][]string{
		{"bucket", "add", source, "demo", "--memory", "128"},
		{"scope", "add", source, "demo", "inv"},
		{"collection", "add", source, "demo", "inv", "items"},
	} {
		tg.MustCLI(t, opts, append(args, "--project", shared.Project)...)
	}
	tg.MustCLI(t, opts, "cluster", "clone", source, clone, "--region", "us-west-2", "--project", shared.Project)

	r := tg.CLIOpts(t, append(opts, integration.NoApprove()), "plan", "--detailed-exitcode")
	if r.Code != 0 {
		t.Fatalf("plan after clone: exit %d, want 0 (clean)\n%s", r.Code, r.Output)
	}

	r = tg.MustCLI(t, []integration.CLIOption{integration.NoApprove(), integration.Debug(false)},
		"-o", "json", "cluster", "get", clone, "--project", shared.Project)
	var got struct {
		Region string `json:"region"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
		t.Fatalf("cluster get -o json: %v\n%s", err, r.Output)
	}
	if got.Region != "us-west-2" || got.State != "healthy" {
		t.Fatalf("clone %s: region %q state %q, want us-west-2 healthy", clone, got.Region, got.State)
	}
}
