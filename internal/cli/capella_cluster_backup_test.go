package cli

import (
	"strings"
	"testing"
)

func TestClusterBackupSchedule(t *testing.T) {
	f := newFake(t)
	seedCluster(f, "alpha", "c1")

	runCap(t, "cluster", "backup", "schedule", "options", "c1").mustOK(t).wantStdout(t, "12", "24")
	runCap(t, "cluster", "backup", "schedule", "set", "c1").mustFail(t, ExitConfigInvalid, "nothing to set")
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--copy-to", "us-west-2", "--no-copies").mustFail(t, ExitConfigInvalid, "mutually exclusive")
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--start", "2am").mustFail(t, ExitConfigInvalid, "HH:MM")
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--every", "12").mustFail(t, ExitConfigInvalid, "add both")
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--every", "5", "--retain", "30d").mustFail(t, ExitConfigInvalid, "5")
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--every", "12", "--retain", "2h").mustFail(t, ExitConfigInvalid, "retention")

	var r backupScheduleResult
	runCap(t, "-o", "json", "cluster", "backup", "schedule", "set", "c1", "--every", "12", "--retain", "30d", "--start", "02:00").mustOK(t).jsonOut(t, &r)
	if r.Status != "created" || !strings.Contains(r.Summary, "12") || !strings.Contains(r.Summary, "30 days") {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--every", "12", "--retain", "30d", "--start", "02:00").mustOK(t).wantStdout(t, "already matches")

	runCap(t, "-o", "json", "cluster", "backup", "schedule", "set", "c1", "--every", "24", "--copy-to", "us-west-2").mustOK(t).jsonOut(t, &r)
	if r.Status != "updated" {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "cluster", "backup", "schedule", "show", "c1").mustOK(t).wantStdout(t, "us-west-2")
	runCap(t, "cluster", "backup", "schedule", "set", "c1", "--no-copies").mustOK(t)

	runCap(t, "cluster", "backup", "schedule", "delete", "c1").mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "--yes", "cluster", "backup", "schedule", "delete", "c1").mustOK(t).wantStdout(t, "deleted the backup schedule of c1")
	runCap(t, "--yes", "cluster", "backup", "schedule", "delete", "c1").mustOK(t).wantStdout(t, "has no backup schedule")
}

func TestClusterBackupLifecycle(t *testing.T) {
	f := newFake(t)
	seedCluster(f, "alpha", "c1")

	runCap(t, "cluster", "backup", "regions", "c1").mustOK(t).wantStdout(t, "us-west-2")
	runCap(t, "cluster", "backup", "create", "c1", "--copy-to", "mars-1").mustFail(t, ExitConfigInvalid, "mars-1")

	var r backupResult
	runCap(t, "-o", "json", "cluster", "backup", "create", "c1", "--retain", "7d").mustOK(t).jsonOut(t, &r)
	if r.Status != "complete" || r.BackupID == "" {
		t.Fatalf("result = %+v", r)
	}
	id := r.BackupID
	runCap(t, "cluster", "backup", "list", "c1").mustOK(t).wantStdout(t, id)
	runCap(t, "cluster", "backup", "list").mustOK(t).wantStdout(t, "c1")

	runCap(t, "cluster", "backup", "retain", "c1", id).mustFail(t, ExitConfigInvalid, "--retain is required")
	runCap(t, "-o", "json", "cluster", "backup", "retain", "c1", id, "--retain", "90d").mustOK(t).jsonOut(t, &r)
	if r.Status != "changed" {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "cluster", "backup", "retain", "c1", id, "--retain", "90d").mustOK(t).wantStdout(t, "no changes")
	runCap(t, "cluster", "backup", "retain", "c1", "nope", "--retain", "90d").mustFail(t, ExitConfigInvalid, "not found")

	runCap(t, "cluster", "backup", "restore", "c1", id).mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "-o", "json", "--yes", "cluster", "backup", "restore", "c1", id).mustOK(t).jsonOut(t, &r)
	if r.Status != "complete" || r.RestoreID == "" {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "cluster", "backup", "restores", "c1").mustOK(t).wantStdout(t, r.RestoreID)

	runCap(t, "cluster", "backup", "delete", "c1", id).mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "-o", "json", "--yes", "cluster", "backup", "delete", "c1", id).mustOK(t).jsonOut(t, &r)
	if r.Status != "deleted" {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "--yes", "cluster", "backup", "delete", "c1", id).mustOK(t).wantStdout(t, "not found; nothing to do")
}

func TestClusterBackupNoWaitAndBlocked(t *testing.T) {
	f := newFake(t)
	_, id := seedCluster(f, "alpha", "c1")
	var r backupResult
	runCap(t, "-o", "json", "--no-wait", "cluster", "backup", "create", "c1").mustOK(t).jsonOut(t, &r)
	if r.Status != "accepted" || r.BackupID == "" {
		t.Fatalf("result = %+v", r)
	}
	f.SetClusterState(id, "turnedOff")
	runCap(t, "--yes", "cluster", "backup", "restore", "c1", r.BackupID).mustFail(t, ExitBlocked, "cluster power c1 --on")

	seedFree(t, f, "freebie")
	runCap(t, "cluster", "backup", "list", "freebie").mustFail(t, ExitBlocked, "free-tier")
}
