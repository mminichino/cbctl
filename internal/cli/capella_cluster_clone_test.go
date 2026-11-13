package cli

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
)

// seedDataModel gives the cluster a bucket with a scope and collection, an allowed CIDR and a credential.
func seedDataModel(t *testing.T, f *capellafake.Fake, pid, id string) {
	t.Helper()
	ctx, c := t.Context(), f.Client()
	if _, err := c.CreateBucket(ctx, pid, id, capellaapi.CreateBucketRequest{Name: "orders", Type: "couchbase", MemoryAllocationInMb: 128,
		BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateScope(ctx, pid, id, capellaapi.BucketID("orders"), "eu"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateCollection(ctx, pid, id, capellaapi.BucketID("orders"), "eu", "invoices", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateAllowedCIDR(ctx, pid, id, "203.0.113.0/24", "office", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.CreateCredential(ctx, pid, id, capellaapi.CreateCredentialRequest{Name: "app", Password: "Source-Pa55word!",
		Access: []capellaapi.AccessEntry{{Privileges: []string{"data_reader"}}}}); err != nil {
		t.Fatal(err)
	}
}

func clusterNamed(t *testing.T, f *capellafake.Fake, pid, name string) *capellaapi.Cluster {
	t.Helper()
	list, err := f.Client().ListClusters(t.Context(), pid)
	if err != nil {
		t.Fatal(err)
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	t.Fatalf("cluster %s not found", name)
	return nil
}

func TestClusterClone(t *testing.T) {
	t.Run("data model and region", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		seedDataModel(t, f, pid, id)
		var r cloneResult
		runCap(t, "-o", "json", "cluster", "clone", "c1", "c2", "--region", "us-west-2").mustOK(t).jsonOut(t, &r)
		if r.Status != "created" || r.Buckets != 1 || r.Scopes != 1 || r.Collections != 1 || r.Region != "us-west-2" {
			t.Fatalf("result = %+v", r)
		}
		if r.CIDR == "" || r.AllowedCIDR != 0 || r.Credentials != 0 {
			t.Fatalf("result = %+v", r)
		}
		c := clusterNamed(t, f, pid, "c2")
		full := clusterOf(t, f, pid, c.ID)
		if got := clusterRegion(full); got != "us-west-2" {
			t.Fatalf("region = %s", got)
		}
		if totalNodes(full.ServiceGroups) != 3 || full.CurrentState != "healthy" {
			t.Fatalf("clone = %d nodes, state %s", totalNodes(full.ServiceGroups), full.CurrentState)
		}
		buckets, err := f.Client().ListBuckets(t.Context(), pid, c.ID)
		if err != nil || len(buckets) != 1 || buckets[0].Name != "orders" {
			t.Fatalf("buckets = %+v, %v", buckets, err)
		}
		colls, err := f.Client().ListCollections(t.Context(), pid, c.ID, capellaapi.BucketID("orders"), "eu")
		if err != nil || len(colls) != 1 {
			t.Fatalf("collections = %+v, %v", colls, err)
		}
		if cidrs, _ := f.Client().ListAllowedCIDRs(t.Context(), pid, c.ID); len(cidrs) != 0 {
			t.Fatalf("allowed CIDRs copied without --with-cidrs: %+v", cidrs)
		}
	})
	t.Run("suggested cidr avoids the source network", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		var r cloneResult
		runCap(t, "-o", "json", "cluster", "clone", "c1", "c2", "--region", "us-west-2").mustOK(t).jsonOut(t, &r)
		src := clusterOf(t, f, pid, id).CloudProvider.CIDR
		if r.CIDR == src {
			t.Fatalf("clone reuses the source CIDR %s", src)
		}
	})
	t.Run("overlapping cidr is refused", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		src := clusterOf(t, f, pid, id).CloudProvider.CIDR
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "us-west-2", "--cidr", src).mustFail(t, ExitConfigInvalid, "overlaps the network of cluster c1")
	})
	t.Run("explicit cidr", func(t *testing.T) {
		f := newFake(t)
		pid, _ := seedCluster(f, "alpha", "c1")
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "us-west-2", "--cidr", "10.99.0.0/24").mustOK(t).wantStdout(t, "10.99.0.0/24")
		if got := clusterNamed(t, f, pid, "c2").CloudProvider.CIDR; got != "10.99.0.0/24" {
			t.Fatalf("cidr = %s", got)
		}
	})
	t.Run("credentials are printed once", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		seedDataModel(t, f, pid, id)
		out := runCap(t, "-o", "json", "cluster", "clone", "c1", "c2", "--region", "us-west-2", "--with-cidrs", "--with-credentials").mustOK(t)
		var doc struct {
			AllowedCIDRs int `json:"allowedCidrs"`
			Credentials  int `json:"credentials"`
			Passwords    []struct {
				Name, Password string
			} `json:"credentialPasswords"`
		}
		out.jsonOut(t, &doc)
		if doc.AllowedCIDRs != 1 || doc.Credentials != 1 || len(doc.Passwords) != 1 || doc.Passwords[0].Name != "app" || len(doc.Passwords[0].Password) < 12 {
			t.Fatalf("doc = %+v", doc)
		}
		if strings.Contains(out.Stderr, doc.Passwords[0].Password) || strings.Contains(out.Stdout, "Source-Pa55word!") {
			t.Fatal("a password leaked outside the one-time field")
		}
	})
	t.Run("no data model", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		seedDataModel(t, f, pid, id)
		var r cloneResult
		runCap(t, "-o", "json", "cluster", "clone", "c1", "c2", "--region", "us-west-2", "--no-data-model").mustOK(t).jsonOut(t, &r)
		if r.Buckets != 0 {
			t.Fatalf("result = %+v", r)
		}
	})
	t.Run("no-wait copies only the cluster", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		seedDataModel(t, f, pid, id)
		var r cloneResult
		runCap(t, "-o", "json", "--no-wait", "cluster", "clone", "c1", "c2", "--region", "us-west-2").mustOK(t).jsonOut(t, &r)
		if r.Status != "accepted" || r.Buckets != 0 || len(r.NotCopied) == 0 {
			t.Fatalf("result = %+v", r)
		}
		clusterNamed(t, f, pid, "c2")
	})
	t.Run("same region needs confirmation", func(t *testing.T) {
		f := newFake(t)
		pid, id := seedCluster(f, "alpha", "c1")
		region := clusterRegion(clusterOf(t, f, pid, id))
		runCap(t, "cluster", "clone", "c1", "c2", "--region", region).mustFail(t, ExitBlocked, "no terminal")
		runCap(t, "--yes", "cluster", "clone", "c1", "c2", "--region", region).mustOK(t)
		clusterNamed(t, f, pid, "c2")
	})
	t.Run("validation", func(t *testing.T) {
		f := newFake(t)
		pid, _ := seedCluster(f, "alpha", "c1")
		f.SeedCluster(pid, "taken", "healthy")
		runCap(t, "cluster", "clone", "c1", "c2").mustFail(t, ExitConfigInvalid, "region of the clone is required")
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "x", "--cidr", "nope").mustFail(t, ExitConfigInvalid, "invalid --cidr")
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "x", "--support-plan", "gold").mustFail(t, ExitConfigInvalid, "unknown support plan")
		runCap(t, "cluster", "clone", "c1", "taken", "--region", "x").mustFail(t, ExitBlocked, "already exists")
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "x", "--to-project", "nowhere").mustFail(t, ExitConfigInvalid, "project nowhere does not exist")
	})
	t.Run("to another project", func(t *testing.T) {
		f := newFake(t)
		seedCluster(f, "alpha", "c1")
		other := f.SeedProject("beta")
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "us-west-2", "--to-project", "beta").mustOK(t)
		clusterNamed(t, f, other, "c2")
	})
	t.Run("not healthy is blocked", func(t *testing.T) {
		f := newFake(t)
		_, id := seedCluster(f, "alpha", "c1")
		f.SetClusterState(id, "scaling")
		runCap(t, "cluster", "clone", "c1", "c2", "--region", "us-west-2").mustFail(t, ExitBlocked, "is scaling")
	})
	t.Run("free tier is blocked", func(t *testing.T) {
		f := newFake(t)
		seedFree(t, f, "freebie")
		runCap(t, "cluster", "clone", "freebie", "c2", "--region", "us-west-2").mustFail(t, ExitBlocked, "free-tier")
	})
}

func TestClusterGroup(t *testing.T) {
	f := newFake(t)
	pid, id := seedCluster(f, "alpha", "c1")
	groups := func() int { return len(clusterOf(t, f, pid, id).ServiceGroups) }

	runCap(t, "cluster", "group", "list", "c1").mustOK(t).wantStdout(t, "SERVICES", "data,index,query", "total nodes: 3 of 27")

	runCap(t, "cluster", "group", "add", "c1").mustFail(t, ExitConfigInvalid, "services of the new group are required")
	runCap(t, "cluster", "group", "add", "c1", "--services", "search", "--nodes", "1").mustFail(t, ExitConfigInvalid, "outside the limits")

	var r groupChangeResult
	runCap(t, "-o", "json", "cluster", "group", "add", "c1", "--services", "search", "--nodes", "2", "--cpu", "8", "--ram", "32").mustOK(t).jsonOut(t, &r)
	if r.Status != "added" || r.Group != "search" || r.Nodes != 2 || groups() != 2 {
		t.Fatalf("result = %+v groups %d", r, groups())
	}
	runCap(t, "cluster", "group", "list", "c1").mustOK(t).wantStdout(t, "search", "total nodes: 5 of 27")

	runCap(t, "cluster", "group", "add", "c1", "--services", "search").mustOK(t).wantStdout(t, "already exists")
	if groups() != 2 {
		t.Fatalf("groups = %d", groups())
	}

	runCap(t, "--yes", "cluster", "group", "remove", "c1", "search").mustFail(t, ExitBlocked, "--allow-service-removal")
	runCap(t, "cluster", "group", "remove", "c1", "search", "--allow-service-removal").mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "--yes", "cluster", "group", "remove", "c1", "search", "--allow-service-removal").mustOK(t).wantStdout(t, "removed")
	if groups() != 1 {
		t.Fatalf("groups = %d", groups())
	}
	runCap(t, "--yes", "cluster", "group", "remove", "c1", "search").mustOK(t).wantStdout(t, "nothing to remove")
	runCap(t, "--yes", "cluster", "group", "remove", "c1", "data,index,query").mustFail(t, ExitBlocked, "needs a group that runs data")
}
