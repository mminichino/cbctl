package cli

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
)

func seedBucket(t *testing.T, f *capellafake.Fake, pid, id, name string) {
	t.Helper()
	if _, err := f.Client().CreateBucket(t.Context(), pid, id, capellaapi.CreateBucketRequest{Name: name, Type: "couchbase", MemoryAllocationInMb: 128,
		BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestBucketBackupSchedule(t *testing.T) {
	f := newFake(t)
	pid, id := seedCluster(f, "alpha", "c1")
	seedBucket(t, f, pid, id, "orders")

	runCap(t, "bucket", "backup", "schedule", "show", "c1", "orders").mustOK(t).wantStdout(t, "has no backup schedule")
	runCap(t, "bucket", "backup", "schedule", "set", "c1", "orders", "--day", "someday").mustFail(t, ExitConfigInvalid, "unknown day")
	runCap(t, "bucket", "backup", "schedule", "set", "c1", "orders", "--start-at", "24").mustFail(t, ExitConfigInvalid, "0-23")
	runCap(t, "bucket", "backup", "schedule", "set", "c1", "orders", "--incremental-every", "5").mustFail(t, ExitConfigInvalid, "not offered")
	runCap(t, "bucket", "backup", "schedule", "set", "c1", "orders", "--retention", "forever").mustFail(t, ExitConfigInvalid, "unknown retention")

	var r bucketBackupResult
	runCap(t, "-o", "json", "bucket", "backup", "schedule", "set", "c1", "orders", "--day", "friday", "--start-at", "2", "--retention", "90days").mustOK(t).jsonOut(t, &r)
	if r.Status != "created" || !strings.Contains(r.Summary, "weekly full on friday at 02:00 UTC") || !strings.Contains(r.Summary, "kept 90 days") {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "bucket", "backup", "schedule", "set", "c1", "orders", "--day", "friday", "--start-at", "2", "--retention", "90days").mustOK(t).wantStdout(t, "already matches")
	runCap(t, "-o", "json", "bucket", "backup", "schedule", "set", "c1", "orders", "--incremental-every", "12", "--cost-optimized").mustOK(t).jsonOut(t, &r)
	if r.Status != "updated" || !strings.Contains(r.Summary, "incremental every 12 h") || !strings.Contains(r.Summary, "cost-optimized") {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "bucket", "backup", "schedule", "show", "c1", "orders").mustOK(t).wantStdout(t, "friday")

	runCap(t, "bucket", "backup", "schedule", "delete", "c1", "orders").mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "--yes", "bucket", "backup", "schedule", "delete", "c1", "orders").mustOK(t).wantStdout(t, "deleted the backup schedule of orders")
	runCap(t, "--yes", "bucket", "backup", "schedule", "delete", "c1", "orders").mustOK(t).wantStdout(t, "has no backup schedule")
}

func TestBucketBackupLifecycle(t *testing.T) {
	f := newFake(t)
	pid, id := seedCluster(f, "alpha", "c1")
	seedBucket(t, f, pid, id, "orders")

	var r bucketBackupResult
	runCap(t, "-o", "json", "bucket", "backup", "create", "c1", "orders").mustOK(t).jsonOut(t, &r)
	if r.BackupID == "" || r.Bucket != "orders" {
		t.Fatalf("result = %+v", r)
	}
	backup := r.BackupID

	runCap(t, "bucket", "backup", "list", "c1").mustOK(t).wantStdout(t, "orders")
	runCap(t, "bucket", "backup", "list", "c1", "orders").mustOK(t).wantStdout(t, backup)
	runCap(t, "bucket", "backup", "get", "c1", backup).mustOK(t).wantStdout(t, "orders")
	runCap(t, "bucket", "backup", "get", "c1", "nope").mustFail(t, ExitConfigInvalid, "not found")
	runCap(t, "bucket", "backup", "cycle", "list", "c1", "orders").mustOK(t)

	runCap(t, "bucket", "backup", "restore", "c1", backup, "--services", "index").mustFail(t, ExitConfigInvalid, "data, query or data,query")
	runCap(t, "bucket", "backup", "restore", "c1", backup, "--replace-ttl", "sometimes").mustFail(t, ExitConfigInvalid, "unknown --replace-ttl")
	runCap(t, "bucket", "backup", "restore", "c1", backup, "--replace-ttl-with", "2026-01-01").mustFail(t, ExitConfigInvalid, "--replace-ttl all|expired")
	runCap(t, "bucket", "backup", "restore", "c1", backup).mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "--yes", "bucket", "backup", "restore", "c1", backup, "--replace-ttl", "none").mustOK(t).wantStdout(t, "restore of orders into c1 started")

	runCap(t, "bucket", "backup", "export", "c1", backup).mustOK(t).wantStdout(t, "is complete")

	runCap(t, "bucket", "backup", "delete", "c1", backup).mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "--yes", "bucket", "backup", "delete", "c1", backup).mustOK(t).wantStdout(t, "deleted the backup cycle of orders")
	runCap(t, "--yes", "bucket", "backup", "delete", "c1", backup).mustOK(t).wantStdout(t, "nothing to do")
}

func TestBucketBackupFreeTier(t *testing.T) {
	f := newFake(t)
	seedFree(t, f, "freebie")
	runCap(t, "bucket", "backup", "list", "freebie").mustFail(t, ExitBlocked, "free-tier")
}
