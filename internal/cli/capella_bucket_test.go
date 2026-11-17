package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// lastBody returns the body of the last request with the given method whose path contains part.
func lastBody(fx *resFixture, method, part string) map[string]any {
	var body string
	for _, r := range fx.Fake.RequestLog() {
		if r.Method == method && strings.Contains(r.Path, part) {
			body = r.Body
		}
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(body), &m) // an empty or invalid body leaves m empty and fails the caller's check
	return m
}

func (fx *resFixture) bucket(t *testing.T, name string) *capellaapi.Bucket {
	t.Helper()
	b, _, err := fx.Client.GetBucket(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID(name))
	if err != nil {
		t.Fatalf("reading bucket %s: %v", name, err)
	}
	return b
}

func TestBucketList(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")
	fx.addBucket(t, "travel-sample")

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "bucket", "list", "clu")
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) != 3 {
			t.Fatalf("want header and 2 rows:\n%s", res.Stdout)
		}
		want := "NAME TYPE STORAGE MEMORY_MB REPLICAS DURABILITY TTL FLUSH"
		if got := strings.Join(strings.Fields(lines[0]), " "); got != want {
			t.Errorf("header = %q", got)
		}
		wantContains(t, res.Stdout, "orders", "travel-sample", "couchbase", "couchstore")
	})
	t.Run("json", func(t *testing.T) {
		items := jsonArray(t, mustRun(t, "bucket", "list", "clu", "-o", "json"))
		if len(items) != 2 || items[0].(map[string]any)["name"] != "orders" {
			t.Errorf("items = %v", items)
		}
	})
	t.Run("empty json is an empty array", func(t *testing.T) {
		fx.Fake.SeedCluster(fx.ProjectID, "empty", "healthy")
		if items := jsonArray(t, mustRun(t, "bucket", "list", "empty", "-o", "json")); len(items) != 0 {
			t.Errorf("items = %v", items)
		}
	})
	t.Run("unknown cluster", func(t *testing.T) {
		wantExit(t, runResource(t, "bucket", "list", "nope"), ExitConfigInvalid, "cluster nope not found")
	})
	t.Run("API failure", func(t *testing.T) {
		fx.Fake.FailNext("GET", `/buckets$`, 403, 1)
		res := runResource(t, "bucket", "list", "clu")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "listing buckets of clu") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestBucketGet(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "bucket", "get", "clu", "orders")
		wantContains(t, res.Stdout, "name: orders", "type: couchbase", "memoryAllocationInMb: 100", "stats.itemCount: 0")
	})
	t.Run("json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "bucket", "get", "clu", "orders", "-o", "json"))
		if obj["name"] != "orders" || obj["id"] != capellaapi.BucketID("orders") || obj["storageBackend"] != "couchstore" {
			t.Errorf("object = %v", obj)
		}
		if _, ok := obj["stats"].(map[string]any); !ok {
			t.Errorf("no stats in %v", obj)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "bucket", "get", "clu", "ghost"), ExitConfigInvalid, "bucket ghost not found")
		if !strings.Contains(ce.Action, "bucket list clu") {
			t.Errorf("action = %q", ce.Action)
		}
	})
}

func TestBucketAdd(t *testing.T) {
	fx := newFixture(t)

	t.Run("defaults", func(t *testing.T) {
		fx.Fake.ResetLog()
		res := mustRun(t, "bucket", "add", "clu", "orders")
		wantContains(t, res.Stdout, "created bucket orders in clu ("+capellaapi.BucketID("orders")+")")
		body := lastBody(fx, "POST", "/buckets")
		if body["name"] != "orders" || body["type"] != "couchbase" || body["storageBackend"] != nil ||
			body["memoryAllocationInMb"] != float64(1024) || body["replicas"] != float64(1) ||
			body["durabilityLevel"] != "none" || body["bucketConflictResolution"] != "seqno" {
			t.Errorf("POST body = %v", body)
		}
		if _, has := body["vbuckets"]; has {
			t.Errorf("vBuckets sent although not given: %v", body)
		}
	})
	t.Run("json with options", func(t *testing.T) {
		fx.Fake.ResetLog()
		obj := jsonObject(t, mustRun(t, "bucket", "add", "clu", "big", "--memory", "512", "--replicas", "2",
			"--storage-backend", "magma", "--vbuckets", "128", "--durability", "majority", "--ttl", "3600", "--flush",
			"--conflict-resolution", "lww", "--eviction-policy", "fullEviction", "--priority", "5", "-o", "json"))
		if obj["status"] != "created" || obj["name"] != "big" || obj["cluster"] != "clu" || obj["id"] != capellaapi.BucketID("big") {
			t.Errorf("object = %v", obj)
		}
		body := lastBody(fx, "POST", "/buckets")
		for k, want := range map[string]any{
			"memoryAllocationInMb": float64(512), "replicas": float64(2), "storageBackend": "magma", "vbuckets": float64(128),
			"durabilityLevel": "majority", "timeToLiveInSeconds": float64(3600), "flushEnabled": true,
			"bucketConflictResolution": "lww", "evictionPolicy": "fullEviction", "priority": float64(5),
		} {
			if body[k] != want {
				t.Errorf("POST body[%s] = %v, want %v", k, body[k], want)
			}
		}
	})
	t.Run("ephemeral", func(t *testing.T) {
		fx.Fake.ResetLog()
		mustRun(t, "bucket", "add", "clu", "sessions", "--type", "ephemeral", "--eviction-policy", "nruEviction", "--durability", "majority")
		body := lastBody(fx, "POST", "/buckets")
		if body["type"] != "ephemeral" || body["evictionPolicy"] != "nruEviction" {
			t.Errorf("POST body = %v", body)
		}
		if _, has := body["storageBackend"]; has {
			t.Errorf("storageBackend sent for an ephemeral bucket: %v", body)
		}
	})
	t.Run("twice is a no-op", func(t *testing.T) {
		fx.Fake.ResetLog()
		wantContains(t, mustRun(t, "bucket", "add", "clu", "orders").Stdout, "bucket orders already exists in clu")
		if fx.Fake.Count("POST", `/buckets`) != 0 {
			t.Error("a POST was sent for an existing bucket")
		}
		if obj := jsonObject(t, mustRun(t, "bucket", "add", "clu", "orders", "-o", "json")); obj["status"] != "already exists" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("conflict from Capella is a no-op", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/buckets$`, 409, 1)
		wantContains(t, mustRun(t, "bucket", "add", "clu", "raced").Stdout, "already exists")
	})
	t.Run("API failure", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/buckets$`, 422, 1)
		res := runResource(t, "bucket", "add", "clu", "bad")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "creating bucket bad") {
			t.Fatalf("err = %v", res.Err)
		}
	})
	t.Run("single-node cluster defaults to no replicas", func(t *testing.T) {
		pid := fx.ProjectID
		id, err := fx.Client.CreateCluster(context.Background(), pid, capellaapi.CreateClusterRequest{
			Name: "solo", CloudProvider: capellaapi.CloudProvider{Type: "aws", Region: "us-east-1"},
			ConfigurationType: "singleNode",
			ServiceGroups:     []capellaapi.ServiceGroupRequest{{NumOfNodes: 1, Services: []string{"data"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		fx.Fake.SetClusterState(id, "healthy")
		fx.Fake.ResetLog()
		mustRun(t, "bucket", "add", "solo", "b1")
		if body := lastBody(fx, "POST", "/buckets"); body["replicas"] != float64(0) {
			t.Errorf("replicas = %v, want 0", body["replicas"])
		}
		fx.Fake.ResetLog()
		mustRun(t, "bucket", "add", "solo", "b2", "--replicas", "1")
		if body := lastBody(fx, "POST", "/buckets"); body["replicas"] != float64(1) {
			t.Errorf("explicit replicas = %v, want 1", body["replicas"])
		}
	})

	invalid := []struct {
		name string
		args []string
		msg  string
	}{
		{"bad type", []string{"--type", "memcached"}, `invalid --type "memcached"`},
		{"bad storage backend", []string{"--storage-backend", "rocksdb"}, `invalid --storage-backend "rocksdb"`},
		{"bad durability", []string{"--durability", "always"}, `invalid --durability "always"`},
		{"bad conflict resolution", []string{"--conflict-resolution", "newest"}, `invalid --conflict-resolution "newest"`},
		{"bad memory", []string{"--memory", "0"}, "invalid --memory 0"},
		{"bad replicas", []string{"--replicas", "4"}, "invalid --replicas 4"},
		{"bad ttl", []string{"--ttl", "-1"}, "invalid --ttl -1"},
		{"bad vbuckets", []string{"--storage-backend", "magma", "--vbuckets", "256"}, "invalid --vbuckets 256"},
		{"vbuckets need magma", []string{"--vbuckets", "128"}, "only allowed with --storage-backend magma"},
		{"couchbase rejects ephemeral eviction", []string{"--eviction-policy", "noEviction"}, "not valid for couchbase buckets"},
		{"ephemeral rejects storage backend", []string{"--type", "ephemeral", "--storage-backend", "magma"}, "not allowed for ephemeral"},
		{"ephemeral rejects vbuckets", []string{"--type", "ephemeral", "--vbuckets", "128"}, "not allowed for ephemeral"},
		{"ephemeral rejects couchbase eviction", []string{"--type", "ephemeral", "--eviction-policy", "fullEviction"}, "not valid for ephemeral"},
		{"ephemeral rejects persisting durability", []string{"--type", "ephemeral", "--durability", "persistToMajority"}, "needs disk persistence"},
	}
	for _, tc := range invalid {
		t.Run("invalid: "+tc.name, func(t *testing.T) {
			fx.Fake.ResetLog()
			wantExit(t, runResource(t, append([]string{"bucket", "add", "clu", "x"}, tc.args...)...), ExitConfigInvalid, tc.msg)
			if n := len(fx.Fake.RequestLog()); n != 0 {
				t.Errorf("%d requests sent for invalid flags", n)
			}
		})
	}
}

func freeFixture(t *testing.T) *resFixture {
	t.Helper()
	fx := newFixture(t)
	id, err := fx.Client.CreateFreeCluster(context.Background(), fx.ProjectID, map[string]any{"name": "freebie"})
	if err != nil {
		t.Fatal(err)
	}
	fx.Fake.SetClusterState(id, "healthy")
	return fx
}

func TestBucketFreeTier(t *testing.T) {
	fx := freeFixture(t)

	t.Run("add sends name and memory to the free-tier endpoint", func(t *testing.T) {
		fx.Fake.ResetLog()
		mustRun(t, "bucket", "add", "freebie", "orders", "--memory", "128")
		if fx.Fake.Count("POST", `/buckets/freeTier$`) != 1 {
			t.Fatal("the free-tier bucket endpoint was not used")
		}
		body := lastBody(fx, "POST", "/buckets/freeTier")
		if len(body) != 2 || body["name"] != "orders" || body["memoryAllocationInMb"] != float64(128) {
			t.Errorf("POST body = %v, want name and memory only", body)
		}
	})
	t.Run("list uses the free-tier endpoint", func(t *testing.T) {
		fx.Fake.ResetLog()
		wantContains(t, mustRun(t, "bucket", "list", "freebie").Stdout, "orders")
		if fx.Fake.Count("GET", `/buckets/freeTier$`) != 1 {
			t.Error("the free-tier list endpoint was not used")
		}
	})
	t.Run("add twice is a no-op", func(t *testing.T) {
		wantContains(t, mustRun(t, "bucket", "add", "freebie", "orders").Stdout, "already exists")
	})
	t.Run("add refuses unsupported flags", func(t *testing.T) {
		fx.Fake.ResetLog()
		ce := wantExitErr(t, runResource(t, "bucket", "add", "freebie", "more", "--replicas", "2", "--durability", "majority"),
			ExitConfigInvalid, "not supported on free-tier clusters")
		if !strings.Contains(ce.Message, "--replicas") || !strings.Contains(ce.Message, "--durability") {
			t.Errorf("error should name the refused flags: %s", ce.Message)
		}
		if !strings.Contains(ce.Action, "only --memory can be set on a free-tier cluster") {
			t.Errorf("action = %q", ce.Action)
		}
		if fx.Fake.Count("POST", `/buckets`) != 0 {
			t.Error("a bucket was posted")
		}
	})
	t.Run("update sends memory only", func(t *testing.T) {
		fx.Fake.ResetLog()
		mustRun(t, "bucket", "update", "freebie", "orders", "--memory", "200")
		body := lastBody(fx, "PUT", "/buckets/freeTier/")
		if body["memoryAllocationInMb"] != float64(200) {
			t.Errorf("PUT body = %v", body)
		}
		for _, k := range []string{"replicas", "durabilityLevel", "timeToLiveInSeconds", "flushEnabled"} {
			if _, has := body[k]; has {
				t.Errorf("free-tier PUT carries %s: %v", k, body)
			}
		}
	})
	t.Run("update refuses unsupported flags", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "bucket", "update", "freebie", "orders", "--replicas", "2"), ExitConfigInvalid, "--replicas not supported")
		if !strings.Contains(ce.Action, "only --memory and --cross-cluster-versioning") {
			t.Errorf("action = %q", ce.Action)
		}
	})
	t.Run("get and delete use the free-tier endpoints", func(t *testing.T) {
		fx.Fake.ResetLog()
		mustRun(t, "bucket", "get", "freebie", "orders")
		mustRun(t, "bucket", "delete", "freebie", "orders", "-y")
		if fx.Fake.Count("GET", `/buckets/freeTier/`) < 2 || fx.Fake.Count("DELETE", `/buckets/freeTier/`) != 1 {
			t.Errorf("log: %+v", fx.Fake.RequestLog())
		}
	})
}

func TestBucketUpdate(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")

	t.Run("memory keeps the other settings", func(t *testing.T) {
		fx.Fake.ResetLog()
		res := mustRun(t, "bucket", "update", "clu", "orders", "--memory", "300")
		wantContains(t, res.Stdout, "updated bucket orders in clu")
		body := lastBody(fx, "PUT", "/buckets/")
		if body["memoryAllocationInMb"] != float64(300) || body["replicas"] != float64(1) || body["durabilityLevel"] != "none" {
			t.Errorf("PUT body = %v, want unchanged settings sent back", body)
		}
		if b := fx.bucket(t, "orders"); b.MemoryAllocationInMb != 300 || b.Replicas != 1 {
			t.Errorf("bucket = %+v", b)
		}
	})
	t.Run("json with several settings", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "bucket", "update", "clu", "orders", "--replicas", "2", "--durability", "majority",
			"--ttl", "60", "--flush", "--priority", "3", "--cross-cluster-versioning", "-o", "json"))
		if obj["status"] != "updated" || obj["name"] != "orders" {
			t.Errorf("object = %v", obj)
		}
		b := fx.bucket(t, "orders")
		if b.Replicas != 2 || b.DurabilityLevel != "majority" || b.TimeToLiveInSeconds != 60 || !flushEnabled(b) {
			t.Errorf("bucket = %+v", b)
		}
		body := lastBody(fx, "PUT", "/buckets/")
		if body["enableCrossClusterVersioning"] != true || body["priority"] != float64(3) {
			t.Errorf("PUT body = %v", body)
		}
	})
	t.Run("--no-flush turns flushing off", func(t *testing.T) {
		mustRun(t, "bucket", "update", "clu", "orders", "--no-flush")
		if flushEnabled(fx.bucket(t, "orders")) {
			t.Error("flush still enabled")
		}
	})
	t.Run("a zero replica count is a change", func(t *testing.T) {
		mustRun(t, "bucket", "update", "clu", "orders", "--replicas", "0")
		if fx.bucket(t, "orders").Replicas != 0 {
			t.Error("replicas not set to 0")
		}
	})
	t.Run("stale version is retried once", func(t *testing.T) {
		fx.Fake.ResetLog()
		fx.Fake.FailNext("PUT", `/buckets/`, 412, 1)
		mustRun(t, "bucket", "update", "clu", "orders", "--memory", "400")
		var seq []string
		for _, r := range fx.Fake.RequestLog() {
			if strings.Contains(r.Path, "/buckets/") {
				seq = append(seq, r.Method)
			}
		}
		if got := strings.Join(seq, " "); got != "GET PUT GET PUT" {
			t.Errorf("requests = %q, want GET PUT GET PUT", got)
		}
	})
	t.Run("nothing to update", func(t *testing.T) {
		fx.Fake.ResetLog()
		wantExit(t, runResource(t, "bucket", "update", "clu", "orders"), ExitConfigInvalid, "nothing to update")
		if n := len(fx.Fake.RequestLog()); n != 0 {
			t.Errorf("%d requests sent", n)
		}
	})
	t.Run("unknown bucket", func(t *testing.T) {
		wantExit(t, runResource(t, "bucket", "update", "clu", "ghost", "--memory", "200"), ExitConfigInvalid, "bucket ghost not found")
	})
	t.Run("conflicting flags", func(t *testing.T) {
		wantExit(t, runResource(t, "bucket", "update", "clu", "orders", "--flush", "--no-flush"), ExitConfigInvalid, "both --flush and --no-flush")
		wantExit(t, runResource(t, "bucket", "update", "clu", "orders", "--cross-cluster-versioning", "--no-cross-cluster-versioning"), ExitConfigInvalid, "both --cross-cluster-versioning")
	})
	t.Run("invalid values", func(t *testing.T) {
		wantExit(t, runResource(t, "bucket", "update", "clu", "orders", "--durability", "always"), ExitConfigInvalid, "invalid --durability")
		wantExit(t, runResource(t, "bucket", "update", "clu", "orders", "--replicas", "9"), ExitConfigInvalid, "invalid --replicas 9")
	})
	t.Run("API failure", func(t *testing.T) {
		fx.Fake.FailNext("PUT", `/buckets/`, 422, 1)
		res := runResource(t, "bucket", "update", "clu", "orders", "--memory", "500")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "updating bucket orders") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestBucketDelete(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")

	t.Run("no terminal and no --yes is blocked", func(t *testing.T) {
		wantExit(t, runResource(t, "bucket", "delete", "clu", "orders"), ExitBlocked, "no terminal")
		fx.bucket(t, "orders")
	})
	t.Run("declined", func(t *testing.T) {
		fakeTTY(t)
		res := runResourceIn(t, "n\n", "bucket", "delete", "clu", "orders")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stdout, "cancelled")
		fx.bucket(t, "orders")
	})
	t.Run("confirmed", func(t *testing.T) {
		fx.addBucket(t, "scratch")
		fakeTTY(t)
		res := runResourceIn(t, "scratch\n", "bucket", "delete", "clu", "scratch")
		if res.Err != nil {
			t.Fatalf("%v\n%s", res.Err, res.Stderr)
		}
		wantContains(t, res.Stdout, "deleted bucket scratch from clu")
	})
	t.Run("unknown", func(t *testing.T) {
		wantContains(t, mustRun(t, "bucket", "delete", "clu", "ghost", "-y").Stdout, "bucket clu/ghost not found, nothing to do")
	})
	t.Run("deletion protection blocks", func(t *testing.T) {
		ctx := context.Background()
		if err := fx.Client.SetDeletionProtection(ctx, fx.ProjectID, fx.ClusterID, true); err != nil {
			t.Fatal(err)
		}
		ce := wantExitErr(t, runResource(t, "bucket", "delete", "clu", "orders", "-y"), ExitBlocked, "deletion protection")
		if !strings.Contains(ce.Action, "cluster protect clu --off") {
			t.Errorf("action = %q", ce.Action)
		}
		fx.bucket(t, "orders")
		if err := fx.Client.SetDeletionProtection(ctx, fx.ProjectID, fx.ClusterID, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		res := mustRun(t, "bucket", "delete", "clu", "orders", "--yes")
		wantContains(t, res.Stdout, "deleted bucket orders from clu")
		if _, _, err := fx.Client.GetBucket(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID("orders")); err == nil {
			t.Error("bucket still exists")
		}
	})
	t.Run("json", func(t *testing.T) {
		fx.addBucket(t, "tmp")
		obj := jsonObject(t, mustRun(t, "bucket", "delete", "clu", "tmp", "-y", "-o", "json"))
		if obj["status"] != "deleted" || obj["name"] != "tmp" || obj["cluster"] != "clu" {
			t.Errorf("object = %v", obj)
		}
	})
}

func TestLegacyBucketLeavesStillRegistered(t *testing.T) {
	for _, group := range []string{"bucket", "collection"} {
		cmd, _, err := newCapellaCmd().Find([]string{group})
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for _, c := range cmd.Commands() {
			have[c.Name()] = true
		}
		want := []string{"create", "exists", "list", "add", "delete", "update"}
		if group == "bucket" {
			want = append(want, "get")
		}
		for _, w := range want {
			if !have[w] {
				t.Errorf("%s has no %q subcommand", group, w)
			}
		}
	}
}
