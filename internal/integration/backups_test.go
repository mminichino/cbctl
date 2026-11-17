//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestBackups_CAP056_CAP058: cluster snapshot schedule set/delete, then an on-demand snapshot that
// is created, restored and deleted. On the sandbox the snapshot steps are left out (cost and time:
// a restore replaces the shared cluster's data); on the fake every step runs.
func TestBackups_CAP056_CAP058(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	opts := []integration.CLIOption{integration.WithConfig(shared.Cfg)}

	tg.MustCLI(t, opts, "cluster", "backup", "schedule", "set", shared.Cluster,
		"--every", "24", "--retain", "30d", "--start", "02:00")
	tg.MustCLI(t, opts, "cluster", "backup", "schedule", "delete", shared.Cluster)

	if tg.Kind != "fake" {
		t.Log("on-demand snapshot create/restore/delete not run on the sandbox (cost; a restore replaces the shared cluster's data)")
		return
	}

	jsonOpts := append(append([]integration.CLIOption(nil), opts...), integration.Debug(false))
	r := tg.MustCLI(t, jsonOpts, "-o", "json", "cluster", "backup", "create", shared.Cluster, "--retain", "7d")
	var created struct {
		BackupID string `json:"backupId"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &created); err != nil {
		t.Fatalf("backup create -o json: %v\n%s", err, r.Output)
	}
	if created.BackupID == "" || created.Status != "complete" {
		t.Fatalf("backup create: %+v, want an id and status complete\n%s", created, r.Output)
	}

	r = tg.MustCLI(t, opts, "cluster", "backup", "list", shared.Cluster)
	if !strings.Contains(r.Stdout, created.BackupID) {
		t.Fatalf("backup list does not show %s:\n%s", created.BackupID, r.Output)
	}

	r = tg.MustCLI(t, jsonOpts, "-o", "json", "cluster", "backup", "restore", shared.Cluster, created.BackupID)
	var restored struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &restored); err != nil {
		t.Fatalf("backup restore -o json: %v\n%s", err, r.Output)
	}
	if restored.Status != "complete" {
		t.Fatalf("backup restore status %q, want complete\n%s", restored.Status, r.Output)
	}

	tg.MustCLI(t, opts, "cluster", "backup", "delete", shared.Cluster, created.BackupID)
	r = tg.MustCLI(t, opts, "cluster", "backup", "list", shared.Cluster)
	if strings.Contains(r.Stdout, created.BackupID) {
		t.Fatalf("backup %s still listed after delete:\n%s", created.BackupID, r.Output)
	}
}
