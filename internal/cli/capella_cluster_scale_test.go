package cli

import (
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

func nodesOf(t *testing.T, c *capellaapi.Cluster) int {
	t.Helper()
	return totalNodes(c.ServiceGroups)
}

func TestClusterScale(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		out := runCap(t, "cluster", "scale", "c1", "--nodes", "5").mustOK(t)
		out.wantStdout(t, "scaled data,index,query: 3 → 5")
		c := clusterOf(t, f, pid, id)
		if nodesOf(t, c) != 5 || c.CurrentState != "healthy" {
			t.Fatalf("cluster = %d nodes, state %s", nodesOf(t, c), c.CurrentState)
		}
	})
	t.Run("step up json", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		var r scaleResult
		runCap(t, "-o", "json", "cluster", "scale", "c1", "--step", "+1").mustOK(t).jsonOut(t, &r)
		if r.Status != "scaled" || r.From != 3 || r.To != 4 || r.Group != "data,index,query" {
			t.Fatalf("result = %+v", r)
		}
		if n := nodesOf(t, clusterOf(t, f, pid, id)); n != 4 {
			t.Fatalf("nodes = %d", n)
		}
	})
	t.Run("step down at limit is a no-op", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		f.ResetLog()
		runCap(t, "cluster", "scale", "c1", "--step=-1").mustOK(t).wantStdout(t, "already at minimum (3)")
		if n := f.Count("PUT", `/clusters/[^/]+$`); n != 0 {
			t.Fatalf("a PUT was sent for a no-op (%d)", n)
		}
	})
	t.Run("unchanged", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "scale", "c1", "--nodes", "3").mustOK(t).wantStdout(t, "already at 3 nodes")
	})
	t.Run("no-wait", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		var r scaleResult
		runCap(t, "-o", "json", "--no-wait", "cluster", "scale", "c1", "--nodes", "4").mustOK(t).jsonOut(t, &r)
		if r.Status != "accepted" {
			t.Fatalf("status = %s", r.Status)
		}
		if s := clusterOf(t, f, pid, id).CurrentState; s != "scaling" {
			t.Fatalf("state = %s, want scaling", s)
		}
	})
	t.Run("waits for a transitional cluster first", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		f.SetClusterState(id, "healthy")
		runCap(t, "cluster", "scale", "c1", "--nodes", "4").mustOK(t)
		if n := nodesOf(t, clusterOf(t, f, pid, id)); n != 4 {
			t.Fatalf("nodes = %d", n)
		}
	})
	t.Run("transitional with no-wait is blocked", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		f.SetClusterState(id, "deploying")
		runCap(t, "--no-wait", "cluster", "scale", "c1", "--nodes", "4").mustFail(t, ExitBlocked, "is deploying")
	})
	t.Run("stale etag is retried once", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		f.FailNext("PUT", `/clusters/[^/]+$`, 412, 1)
		runCap(t, "cluster", "scale", "c1", "--nodes", "4").mustOK(t)
		if n := nodesOf(t, clusterOf(t, f, pid, id)); n != 4 {
			t.Fatalf("nodes = %d", n)
		}
		if n := f.Count("PUT", `/clusters/[^/]+$`); n != 2 {
			t.Fatalf("PUT count = %d, want 2", n)
		}
	})
	t.Run("out of range", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "scale", "c1", "--nodes", "28").mustFail(t, ExitConfigInvalid, "outside the limits")
		runCap(t, "cluster", "scale", "c1", "--nodes", "2").mustFail(t, ExitConfigInvalid, "outside the limits")
	})
	t.Run("flag validation", func(t *testing.T) {
		newFake(t)
		runCap(t, "cluster", "scale", "c1", "--nodes", "4", "--step", "1").mustFail(t, ExitConfigInvalid, "mutually exclusive")
		runCap(t, "cluster", "scale", "c1").mustFail(t, ExitConfigInvalid, "neither --nodes nor --step")
		runCap(t, "cluster", "scale", "c1", "--step", "2").mustFail(t, ExitConfigInvalid, "+1 or -1")
		runCap(t, "cluster", "scale", "c1", "--nodes", "0").mustFail(t, ExitConfigInvalid, "at least 1")
	})
	t.Run("unknown group", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "scale", "c1", "--nodes", "4", "--group", "search").mustFail(t, ExitConfigInvalid, "no single service group")
	})
	t.Run("free tier is blocked", func(t *testing.T) {
		f := newFake(t)
		seedFree(t, f, "freebie")
		runCap(t, "cluster", "scale", "freebie", "--nodes", "3").mustFail(t, ExitBlocked, "free-tier clusters have 1 node")
	})
	t.Run("scale in is guarded by bucket replicas", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "scale", "c1", "--nodes", "5").mustOK(t)
		if _, err := f.Client().CreateBucket(t.Context(), pid, id, capellaapi.CreateBucketRequest{
			Name: "b", Type: "couchbase", MemoryAllocationInMb: 100, BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 3}); err != nil {
			t.Fatal(err)
		}
		runCap(t, "cluster", "scale", "c1", "--nodes", "3").mustFail(t, ExitBlocked, "bucket b has 3 replicas")
		if n := nodesOf(t, clusterOf(t, f, pid, id)); n != 5 {
			t.Fatalf("nodes = %d, want the cluster untouched", n)
		}
	})
}

func TestClusterUpdate(t *testing.T) {
	t.Run("description and plan", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		out := runCap(t, "cluster", "update", "c1", "--description", "hello", "--support-plan", "enterprise").mustOK(t)
		out.wantStdout(t, "cluster c1 updated")
		c := clusterOf(t, f, pid, id)
		if c.Description != "hello" || c.Support.Plan != "enterprise" {
			t.Fatalf("cluster = %+v", c)
		}
	})
	t.Run("compute and disk", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "update", "c1", "--cpu", "8", "--ram", "32", "--disk-size", "100", "--disk-iops", "4000").mustOK(t)
		g := clusterOf(t, f, pid, id).ServiceGroups[0].Node
		if g.Compute.CPU != 8 || g.Compute.RAM != 32 || *g.Disk.Storage != 100 || *g.Disk.IOPS != 4000 {
			t.Fatalf("node = %+v disk %+v", g.Compute, g.Disk)
		}
	})
	t.Run("disk type change gets recommended iops", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "update", "c1", "--disk-type", "io2").mustOK(t)
		d := clusterOf(t, f, pid, id).ServiceGroups[0].Node.Disk
		if d.Type != "io2" || d.Storage == nil || d.IOPS == nil {
			t.Fatalf("disk = %+v", d)
		}
	})
	t.Run("unchanged is a no-op", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		f.ResetLog()
		runCap(t, "cluster", "update", "c1", "--support-plan", "basic").mustOK(t).wantStdout(t, "already matches")
		if n := f.Count("PUT", `/clusters/[^/]+$`); n != 0 {
			t.Fatalf("PUT count = %d", n)
		}
	})
	t.Run("no-wait", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		var r clusterUpdateResult
		runCap(t, "-o", "json", "--no-wait", "cluster", "update", "c1", "--ram", "32").mustOK(t).jsonOut(t, &r)
		if r.Status != "accepted" {
			t.Fatalf("status = %s", r.Status)
		}
		if s := clusterOf(t, f, pid, id).CurrentState; s != "scaling" {
			t.Fatalf("state = %s", s)
		}
	})
	t.Run("validation", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "update", "c1").mustFail(t, ExitConfigInvalid, "nothing to update")
		runCap(t, "cluster", "update", "c1", "--support-plan", "gold").mustFail(t, ExitConfigInvalid, "unknown support plan")
		runCap(t, "cluster", "update", "c1", "--support-timezone", "XX").mustFail(t, ExitConfigInvalid, "unknown support timezone")
		runCap(t, "cluster", "update", "c1", "--disk-type", "nope").mustFail(t, ExitConfigInvalid, "unknown aws disk type")
		runCap(t, "cluster", "update", "c1", "--disk-auto-expansion").mustFail(t, ExitConfigInvalid, "Azure disks only")
	})
	t.Run("free tier takes only a description", func(t *testing.T) {
		f := newFake(t)
		seedFree(t, f, "freebie")
		runCap(t, "cluster", "update", "freebie", "--description", "mine").mustOK(t).wantStdout(t, "updated (description)")
		runCap(t, "cluster", "update", "freebie", "--cpu", "4").mustFail(t, ExitBlocked, "free-tier clusters have 1 node")
	})
}
