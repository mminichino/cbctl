package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

func (fx *resFixture) collections(t *testing.T, bucket, scope string) []capellaapi.Collection {
	t.Helper()
	colls, err := fx.Client.ListCollections(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID(bucket), scope)
	if err != nil {
		t.Fatal(err)
	}
	return colls
}

func TestParseServerVersion(t *testing.T) {
	tests := []struct {
		in     string
		want   [3]int
		wantOK bool
	}{
		{"7.6.2", [3]int{7, 6, 2}, true},
		{"7.2.4-build", [3]int{7, 2, 4}, true},
		{"8.0", [3]int{8, 0, 0}, true},
		{"", [3]int{}, false},
		{"unknown", [3]int{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseServerVersion(tc.in)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("parseServerVersion(%q) = %v, %v", tc.in, got, ok)
			}
		})
	}
	if !versionBefore([3]int{7, 2, 4}, collectionMaxTTLVersion) || versionBefore([3]int{7, 6, 0}, collectionMaxTTLVersion) ||
		versionBefore([3]int{8, 0, 0}, collectionMaxTTLVersion) {
		t.Error("versionBefore compares wrongly")
	}
}

func collectionFixture(t *testing.T) *resFixture {
	t.Helper()
	fx := newFixture(t)
	fx.addBucket(t, "orders")
	fx.addScope(t, "orders", "inv")
	return fx
}

func TestCollectionList(t *testing.T) {
	fx := collectionFixture(t)
	if err := fx.Client.CreateCollection(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID("orders"), "inv", "items", intPtr(3600)); err != nil {
		t.Fatal(err)
	}

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "collection", "list", "clu", "orders", "inv")
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) != 2 {
			t.Fatalf("want header and 1 row:\n%s", res.Stdout)
		}
		if got := strings.Join(strings.Fields(lines[0]), " "); got != "NAME MAX_TTL" {
			t.Errorf("header = %q", got)
		}
		if got := strings.Join(strings.Fields(lines[1]), " "); got != "items 3600" {
			t.Errorf("row = %q", got)
		}
	})
	t.Run("json", func(t *testing.T) {
		items := jsonArray(t, mustRun(t, "collection", "list", "clu", "orders", "inv", "-o", "json"))
		if len(items) != 1 || items[0].(map[string]any)["name"] != "items" {
			t.Errorf("items = %v", items)
		}
	})
	t.Run("unknown scope", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "list", "clu", "orders", "ghost"), ExitConfigInvalid, "scope ghost not found")
	})
	t.Run("unknown bucket", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "list", "clu", "ghost", "inv"), ExitConfigInvalid, "bucket ghost not found")
	})
}

func TestCollectionAdd(t *testing.T) {
	fx := collectionFixture(t)

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "collection", "add", "clu", "orders", "inv", "items")
		wantContains(t, res.Stdout, "created collection items in clu/orders/inv")
		if got := fx.collections(t, "orders", "inv"); len(got) != 1 || got[0].Name != "items" {
			t.Errorf("collections = %+v", got)
		}
	})
	t.Run("json with --max-ttl", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "collection", "add", "clu", "orders", "inv", "suppliers", "--max-ttl", "60", "-o", "json"))
		if obj["status"] != "created" || obj["collection"] != "suppliers" || obj["maxTTL"] != float64(60) {
			t.Errorf("object = %v", obj)
		}
		for _, c := range fx.collections(t, "orders", "inv") {
			if c.Name == "suppliers" && c.MaxTTL != 60 {
				t.Errorf("maxTTL = %d", c.MaxTTL)
			}
		}
	})
	t.Run("--max-ttl -1 is sent", func(t *testing.T) {
		fx.Fake.ResetLog()
		mustRun(t, "collection", "add", "clu", "orders", "inv", "forever", "--max-ttl", "-1")
		var body string
		for _, r := range fx.Fake.RequestLog() {
			if r.Method == "POST" {
				body = r.Body
			}
		}
		if !strings.Contains(body, `"maxTTL":-1`) {
			t.Errorf("POST body = %s", body)
		}
	})
	t.Run("twice is a no-op", func(t *testing.T) {
		fx.Fake.ResetLog()
		wantContains(t, mustRun(t, "collection", "add", "clu", "orders", "inv", "items").Stdout, "collection items already exists")
		if fx.Fake.Count("POST", `/collections`) != 0 {
			t.Error("a POST was sent for an existing collection")
		}
		obj := jsonObject(t, mustRun(t, "collection", "add", "clu", "orders", "inv", "items", "-o", "json"))
		if obj["status"] != "already exists" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("conflict from Capella is a no-op", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/collections$`, 409, 1)
		wantContains(t, mustRun(t, "collection", "add", "clu", "orders", "inv", "raced").Stdout, "already exists")
	})
	t.Run("invalid --max-ttl", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "add", "clu", "orders", "inv", "x", "--max-ttl", "-5"), ExitConfigInvalid, "invalid --max-ttl -5")
	})
	t.Run("unknown scope", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "add", "clu", "orders", "ghost", "x"), ExitConfigInvalid, "scope ghost not found")
	})
	t.Run("API failure", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/collections$`, 422, 1)
		res := runResource(t, "collection", "add", "clu", "orders", "inv", "bad")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "creating collection bad") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestCollectionUpdate(t *testing.T) {
	fx := collectionFixture(t)
	fx.addCollection(t, "orders", "inv", "items")

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "collection", "update", "clu", "orders", "inv", "items", "--max-ttl", "7200")
		wantContains(t, res.Stdout, "updated collection items in clu/orders/inv")
		if got := fx.collections(t, "orders", "inv"); got[0].MaxTTL != 7200 {
			t.Errorf("maxTTL = %d", got[0].MaxTTL)
		}
	})
	t.Run("json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "collection", "update", "clu", "orders", "inv", "items", "--max-ttl", "-1", "-o", "json"))
		if obj["status"] != "updated" || obj["maxTTL"] != float64(-1) {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("nothing to update", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "update", "clu", "orders", "inv", "items"), ExitConfigInvalid, "nothing to update")
	})
	t.Run("max-ttl 0 is a change", func(t *testing.T) {
		mustRun(t, "collection", "update", "clu", "orders", "inv", "items", "--max-ttl", "0")
	})
	t.Run("unknown collection", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "update", "clu", "orders", "inv", "ghost", "--max-ttl", "5"), ExitConfigInvalid, "collection ghost not found")
	})
	t.Run("stale version is retried once", func(t *testing.T) {
		fx.Fake.ResetLog()
		fx.Fake.FailNext("PUT", `/collections/`, 412, 1)
		mustRun(t, "collection", "update", "clu", "orders", "inv", "items", "--max-ttl", "99")
		var seq []string
		for _, r := range fx.Fake.RequestLog() {
			if strings.Contains(r.Path, "/collections/items") {
				seq = append(seq, r.Method)
			}
		}
		if got := strings.Join(seq, " "); got != "GET PUT GET PUT" {
			t.Errorf("requests = %q, want GET PUT GET PUT", got)
		}
	})
	t.Run("needs Couchbase Server 7.6.0", func(t *testing.T) {
		ctx := context.Background()
		id, err := fx.Client.CreateCluster(ctx, fx.ProjectID, capellaapi.CreateClusterRequest{
			Name: "old", CloudProvider: capellaapi.CloudProvider{Type: "aws", Region: "us-east-1"},
			CouchbaseServer: &capellaapi.CouchbaseServer{Version: "7.2.4"},
			ServiceGroups:   []capellaapi.ServiceGroupRequest{{NumOfNodes: 3, Services: []string{"data"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		fx.Fake.SetClusterState(id, "healthy")
		_, err = fx.Client.CreateBucket(ctx, fx.ProjectID, id, capellaapi.CreateBucketRequest{Name: "b", Type: "couchbase", MemoryAllocationInMb: 100})
		if err != nil {
			t.Fatal(err)
		}
		fx.Fake.ResetLog()
		res := runResource(t, "collection", "update", "old", "b", "_default", "_default", "--max-ttl", "5")
		wantExit(t, res, ExitBlocked, "requires Couchbase Server 7.6.0 or later")
		if fx.Fake.Count("PUT", `.`) != 0 {
			t.Error("a PUT was sent to an old server")
		}
	})
}

func TestCollectionDelete(t *testing.T) {
	fx := collectionFixture(t)
	fx.addCollection(t, "orders", "inv", "items")

	t.Run("_default is refused before any request", func(t *testing.T) {
		fx.Fake.ResetLog()
		wantExit(t, runResource(t, "collection", "delete", "clu", "orders", "_default", "_default", "-y"), ExitConfigInvalid, "cannot be deleted")
		if n := len(fx.Fake.RequestLog()); n != 0 {
			t.Errorf("%d requests sent", n)
		}
	})
	t.Run("no terminal and no --yes is blocked", func(t *testing.T) {
		wantExit(t, runResource(t, "collection", "delete", "clu", "orders", "inv", "items"), ExitBlocked, "no terminal")
		if len(fx.collections(t, "orders", "inv")) != 1 {
			t.Error("collection was deleted")
		}
	})
	t.Run("declined", func(t *testing.T) {
		fakeTTY(t)
		res := runResourceIn(t, "n\n", "collection", "delete", "clu", "orders", "inv", "items")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stdout, "cancelled")
		if len(fx.collections(t, "orders", "inv")) != 1 {
			t.Error("collection was deleted")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		wantContains(t, mustRun(t, "collection", "delete", "clu", "orders", "inv", "ghost", "-y").Stdout, "collection clu/orders/inv/ghost not found, nothing to do")
	})
	t.Run("delete", func(t *testing.T) {
		res := mustRun(t, "collection", "delete", "clu", "orders", "inv", "items", "--yes")
		wantContains(t, res.Stdout, "deleted collection items from clu/orders/inv")
		if len(fx.collections(t, "orders", "inv")) != 0 {
			t.Error("collection still exists")
		}
	})
	t.Run("json", func(t *testing.T) {
		fx.addCollection(t, "orders", "inv", "tmp")
		obj := jsonObject(t, mustRun(t, "collection", "delete", "clu", "orders", "inv", "tmp", "-y", "-o", "json"))
		if obj["status"] != "deleted" || obj["collection"] != "tmp" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("deletion protection blocks", func(t *testing.T) {
		fx.addCollection(t, "orders", "inv", "guarded")
		ctx := context.Background()
		if err := fx.Client.SetDeletionProtection(ctx, fx.ProjectID, fx.ClusterID, true); err != nil {
			t.Fatal(err)
		}
		wantExit(t, runResource(t, "collection", "delete", "clu", "orders", "inv", "guarded", "-y"), ExitBlocked, "deletion protection")
	})
}
