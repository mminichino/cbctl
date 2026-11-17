//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// lifecycleExtra is a whole environment. The schedule is always on (00:00-24:00, weekend on), so
// it never turns the cluster off while the suite runs.
const lifecycleExtra = `
    deletionProtection: true
    hibernation:
      timezone: US/Pacific
      weekdays: "00:00-24:00"
      weekend: "on"
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

// lifecycleEdits change a bucket, a CIDR comment and the schedule's time zone.
var lifecycleEdits = [][2]string{
	{"memoryAllocationInMb: 256", "memoryAllocationInMb: 512"},
	{"comment: cbctl nightly", "comment: cbctl nightly, edited"},
	{"timezone: US/Pacific", "timezone: US/Eastern"},
}

// TestLifecycle_CAP032_AC1: validate → plan → apply → apply (zero writes) → drift clean → edit
// (bucket memory, CIDR comment, schedule time zone) → apply writes all three → apply converges →
// protect --off → destroy → project gone.
func TestLifecycle_CAP032_AC1(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)

	project := tg.ProjectName("life")
	tg.Cleanup(t, project)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "capella.yaml")
	cluster := "life1-" + tg.Run
	body := integration.SingleNodeConfig(project, cluster, integration.UniqueCIDR(tg.Run, 'l'), lifecycleExtra)
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

	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, e := range lifecycleEdits {
		if !strings.Contains(text, e[0]) {
			t.Fatalf("capella.yaml has no %q:\n%s", e[0], text)
		}
		text = strings.Replace(text, e[0], e[1], 1)
	}
	if err := os.WriteFile(cfg, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	r = tg.MustCLI(t, opts, "apply")
	writes := r.Writes()
	for _, part := range []string{"/buckets/", "/onOffSchedule", "/allowedcidrs"} {
		found := false
		for _, w := range writes {
			if strings.Contains(w.Path, part) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("apply after the edits sent no write to %s; writes: %v", part, writes)
		}
	}

	r = tg.MustCLI(t, opts, "apply")
	if w := r.Writes(); len(w) != 0 {
		t.Fatalf("the edits did not converge; a re-run wrote: %v\n%s", w, r.Output)
	}

	tg.MustCLI(t, opts, "cluster", "protect", cluster, "--off")
	tg.MustCLI(t, opts, "destroy", "--discard-snapshots")

	r = tg.CLIOpts(t, []integration.CLIOption{integration.NoApprove()}, "project", "list")
	if r.Code != 0 {
		t.Fatalf("project list: exit %d\n%s", r.Code, r.Output)
	}
	if strings.Contains(r.Stdout, project) {
		t.Fatalf("project %s still listed after destroy:\n%s", project, r.Stdout)
	}
}
