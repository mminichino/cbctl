package cli

import (
	"strings"
	"testing"
)

func TestClusterPower(t *testing.T) {
	t.Run("off needs confirmation without a terminal", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "power", "c1", "--off").mustFail(t, ExitBlocked, "no terminal")
		if s := f.ClusterState(id); s != "healthy" {
			t.Fatalf("state = %s", s)
		}
	})
	t.Run("off then on", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		runCap(t, "--yes", "cluster", "power", "c1", "--off").mustOK(t).wantStdout(t, "cluster c1 is off")
		if s := f.ClusterState(id); s != "turnedOff" {
			t.Fatalf("state = %s", s)
		}
		var r powerResult
		runCap(t, "-o", "json", "cluster", "power", "c1", "--on").mustOK(t).jsonOut(t, &r)
		if r.Status != "changed" || r.State != "on" {
			t.Fatalf("result = %+v", r)
		}
		if s := f.ClusterState(id); s != "healthy" {
			t.Fatalf("state = %s", s)
		}
	})
	t.Run("already in the target state", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		f.ResetLog()
		runCap(t, "cluster", "power", "c1", "--on").mustOK(t).wantStdout(t, "already on")
		if n := f.Count("POST", `/clusters/[^/]+/activationState`); n != 0 {
			t.Fatalf("power call sent for a no-op (%d)", n)
		}
	})
	t.Run("no-wait", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		var r powerResult
		runCap(t, "-o", "json", "--yes", "--no-wait", "cluster", "power", "c1", "--off").mustOK(t).jsonOut(t, &r)
		if r.Status != "accepted" || r.State != "turningOff" {
			t.Fatalf("result = %+v", r)
		}
		if s := f.ClusterState(id); s != "turningOff" {
			t.Fatalf("state = %s", s)
		}
	})
	t.Run("flag validation", func(t *testing.T) {
		newFake(t)
		runCap(t, "cluster", "power", "c1").mustFail(t, ExitConfigInvalid, "neither --on nor --off")
		runCap(t, "cluster", "power", "c1", "--on", "--off").mustFail(t, ExitConfigInvalid, "mutually exclusive")
		runCap(t, "cluster", "power", "c1", "--off", "--with-app-service").mustFail(t, ExitConfigInvalid, "only applies with --on")
	})
	t.Run("unhealthy cluster is blocked", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		f.SetClusterState(id, "deployment_failed")
		runCap(t, "--yes", "cluster", "power", "c1", "--off").mustFail(t, ExitBlocked, "deployment_failed")
	})
}

func TestClusterProtect(t *testing.T) {
	f := newFake(t)
	pid, id := seedCluster(f, "alpha", "c1")
	protected := func() bool {
		c := clusterOf(t, f, pid, id)
		return c.DeletionProtection != nil && *c.DeletionProtection
	}

	runCap(t, "cluster", "protect", "c1").mustFail(t, ExitConfigInvalid, "neither --on nor --off")
	runCap(t, "cluster", "protect", "c1", "--on", "--off").mustFail(t, ExitConfigInvalid, "mutually exclusive")

	var r protectResult
	runCap(t, "-o", "json", "cluster", "protect", "c1", "--on").mustOK(t).jsonOut(t, &r)
	if r.Status != "changed" || !r.DeletionProtection || !protected() {
		t.Fatalf("result = %+v protected %v", r, protected())
	}
	runCap(t, "cluster", "protect", "c1", "--on").mustOK(t).wantStdout(t, "already on")

	runCap(t, "cluster", "protect", "c1", "--off").mustFail(t, ExitBlocked, "no terminal")
	if !protected() {
		t.Fatal("protection dropped without confirmation")
	}
	runCap(t, "--yes", "cluster", "protect", "c1", "--off").mustOK(t).wantStdout(t, "protection off for c1")
	if protected() {
		t.Fatal("protection still on")
	}
}

func TestClusterDelete(t *testing.T) {
	t.Run("needs confirmation without a terminal", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "delete", "c1").mustFail(t, ExitBlocked, "no terminal")
		clusterOf(t, f, pid, id)
	})
	t.Run("deleted with the summary", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		out := runCap(t, "--yes", "cluster", "delete", "c1").mustOK(t)
		out.wantStdout(t, "cluster c1", "3 nodes", "snapshot backups are kept", "deleted cluster c1 (snapshot backups retained)")
		if _, err := f.Client().GetCluster(t.Context(), pid, id); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("cluster still there: %v", err)
		}
	})
	t.Run("discard snapshots is flagged", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		runCap(t, "--yes", "cluster", "delete", "c1", "--discard-snapshots").mustOK(t).wantStdout(t, "WARNING: snapshot backups will be deleted", "snapshot backups deleted")
	})
	t.Run("protected is blocked", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		if err := f.Client().SetDeletionProtection(t.Context(), pid, id, true); err != nil {
			t.Fatal(err)
		}
		runCap(t, "--yes", "cluster", "delete", "c1").mustFail(t, ExitBlocked, "deletion protection")
		clusterOf(t, f, pid, id)
	})
	t.Run("absent is a no-op", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		var r clusterDeleteResult
		runCap(t, "-o", "json", "--yes", "cluster", "delete", "ghost").mustOK(t).jsonOut(t, &r)
		if r.Status != "absent" {
			t.Fatalf("result = %+v", r)
		}
	})
	t.Run("no-wait then again waits", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		var r clusterDeleteResult
		runCap(t, "-o", "json", "--yes", "--no-wait", "cluster", "delete", "c1").mustOK(t).jsonOut(t, &r)
		if r.Status != "accepted" {
			t.Fatalf("result = %+v", r)
		}
		if s := f.ClusterState(id); s != "destroying" {
			t.Fatalf("state = %s", s)
		}
		runCap(t, "--yes", "cluster", "delete", "c1").mustOK(t).wantStdout(t, "deleted cluster c1")
	})
	t.Run("free tier", func(t *testing.T) {
		f := newFake(t)
		seedFree(t, f, "freebie")
		runCap(t, "--yes", "cluster", "delete", "freebie").mustOK(t).wantStdout(t, "deleted cluster freebie")
	})
}
