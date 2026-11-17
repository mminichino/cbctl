package capellafake_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/secrets"
)

func clusterReq(name string) capellaapi.CreateClusterRequest {
	cpu, ram, storage, iops := 4, 16, 50, 3000
	return capellaapi.CreateClusterRequest{
		Name:          name,
		CloudProvider: capellaapi.CloudProvider{Type: "aws", Region: "us-east-1", CIDR: "10.1.0.0/24"},
		ServiceGroups: []capellaapi.ServiceGroupRequest{{
			NumOfNodes: 3, Services: []string{"data", "index", "query"},
			Node: capellaapi.NodeConfig{
				Compute: &capellaapi.Compute{CPU: cpu, RAM: ram},
				Disk:    &capellaapi.Disk{Type: "gp3", Storage: &storage, IOPS: &iops},
			},
		}},
		Availability: capellaapi.Availability{Type: "multi"},
		Support:      capellaapi.Support{Plan: "basic", Timezone: "ET"},
	}
}

// fixture creates a project and a healthy cluster.
func fixture(t *testing.T) (*capellafake.Fake, *capellaapi.Client, string, string) {
	t.Helper()
	f := capellafake.New(t)
	c := f.Client()
	p := f.SeedProject("proj")
	cl := f.SeedCluster(p, "clu", "healthy")
	return f, c, p, cl
}

func TestAuthAndOrganizations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)

	bad, err := capellaapi.New("wrong-key", capellaapi.WithBaseURL(f.URL), capellaapi.WithOrgID(f.OrgID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.ListOrganizations(ctx); !errors.Is(err, capellaapi.ErrUnauthorized) {
		t.Fatalf("wrong key: err = %v, want ErrUnauthorized", err)
	}

	c := f.Client()
	orgs, err := c.ListOrganizations(ctx)
	if err != nil || len(orgs) != 1 || orgs[0].ID != f.OrgID {
		t.Fatalf("ListOrganizations = %v, %v", orgs, err)
	}
	other := f.AddOrganization("second")
	if orgs, _ = c.ListOrganizations(ctx); len(orgs) != 2 {
		t.Fatalf("after AddOrganization: %d orgs", len(orgs))
	}
	if org, err := c.GetOrganization(ctx); err != nil || org.ID != f.OrgID {
		t.Fatalf("GetOrganization = %v, %v", org, err)
	}

	// Resources of an organization the key cannot use are forbidden.
	c.SetOrgID(other)
	if _, err := c.ListProjects(ctx); !errors.Is(err, capellaapi.ErrForbidden) {
		t.Fatalf("other org projects: err = %v, want ErrForbidden", err)
	}
}

func TestProjectCRUD(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client()

	id, err := c.CreateProject(ctx, "alpha", "first")
	if err != nil {
		t.Fatal(err)
	}
	// Capella allows two projects with the same name (parity with capellactl's fake).
	if dup, err := c.CreateProject(ctx, "alpha", ""); err != nil || dup == id {
		t.Fatalf("duplicate project name: id %q (first %q), err %v; want a second project", dup, id, err)
	}
	p, etag, err := c.GetProject(ctx, id)
	if err != nil || p.Name != "alpha" || p.Description != "first" || etag != "1" {
		t.Fatalf("GetProject = %+v, %q, %v", p, etag, err)
	}
	if err := c.UpdateProject(ctx, id, "alpha", "second", etag); err != nil {
		t.Fatal(err)
	}
	// The version moved on, so the old ETag is stale.
	if err := c.UpdateProject(ctx, id, "alpha", "third", etag); !errors.Is(err, capellaapi.ErrPreconditionFailed) {
		t.Fatalf("stale ETag: err = %v, want ErrPreconditionFailed", err)
	}
	if _, etag, _ = c.GetProject(ctx, id); etag != "2" {
		t.Fatalf("etag = %q, want 2", etag)
	}
	if err := c.UpdateProject(ctx, id, "alpha", "third", ""); err != nil {
		t.Fatalf("update without If-Match: %v", err)
	}

	if _, _, err := c.GetProject(ctx, "nope"); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("unknown project: err = %v, want ErrNotFound", err)
	}

	// A project with a cluster cannot be deleted.
	f.SeedCluster(id, "c1", "healthy")
	if err := c.DeleteProject(ctx, id); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("delete non-empty: err = %v, want ErrConflict", err)
	}
	empty, _ := c.CreateProject(ctx, "empty", "")
	if err := c.DeleteProject(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetProject(ctx, empty); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client()
	f.SetPageSize(2)
	for i := 0; i < 5; i++ {
		f.SeedProject(fmt.Sprintf("p%d", i))
	}
	projects, err := c.ListProjects(ctx)
	if err != nil || len(projects) != 5 {
		t.Fatalf("ListProjects = %d items, %v", len(projects), err)
	}
	// 5 items at 2 per page is 3 list requests.
	if n := f.Count("GET", `/projects$`); n != 3 {
		t.Fatalf("list requests = %d, want 3", n)
	}
	for _, r := range f.RequestLog() {
		if r.Method == "GET" && !strings.Contains(r.RawQuery, "perPage=100") {
			t.Errorf("client did not send perPage=100: %q", r.RawQuery)
		}
	}
}

func TestClusterLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	f.SettlePolls(2)
	c := f.Client()
	p, _ := c.CreateProject(ctx, "proj", "")

	id, err := c.CreateCluster(ctx, p, clusterReq("c1"))
	if err != nil {
		t.Fatal(err)
	}
	// Capella allows two clusters with the same name in a project (in a separate project here so
	// the lifecycle below sees one cluster).
	p2, _ := c.CreateProject(ctx, "dups", "")
	reqA, reqB := clusterReq("same"), clusterReq("same")
	reqA.CloudProvider.CIDR, reqB.CloudProvider.CIDR = "10.2.0.0/24", "10.3.0.0/24" // unique in the org
	a, errA := c.CreateCluster(ctx, p2, reqA)
	b, errB := c.CreateCluster(ctx, p2, reqB)
	if errA != nil || errB != nil || a == b {
		t.Fatalf("duplicate cluster name: ids %q %q, errs %v %v; want two clusters", a, b, errA, errB)
	}

	var states []string
	for i := 0; i < 4; i++ {
		cl, err := c.GetCluster(ctx, p, id)
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, cl.CurrentState)
	}
	if got, want := strings.Join(states, ","), "deploying,deploying,healthy,healthy"; got != want {
		t.Fatalf("states = %s, want %s", got, want)
	}

	cl, _ := c.GetCluster(ctx, p, id)
	if cl.ETag != "1" || cl.ConfigurationType != "multiNode" || cl.ConnectionString == "" {
		t.Fatalf("cluster = %+v", cl)
	}

	// Scale: the group change moves the cluster to scaling and bumps the version.
	groups := cl.ServiceGroups
	groups[0].NumOfNodes = 4
	req := capellaapi.UpdateClusterRequest{Name: cl.Name, Support: cl.Support, ServiceGroups: groups}
	if err := c.UpdateCluster(ctx, p, id, req, "99"); !errors.Is(err, capellaapi.ErrPreconditionFailed) {
		t.Fatalf("stale PUT: err = %v, want ErrPreconditionFailed", err)
	}
	if err := c.UpdateCluster(ctx, p, id, req, cl.ETag); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateCluster(ctx, p, id, req, ""); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("PUT while scaling: err = %v, want ErrConflict", err)
	}
	if got, err := c.AsClusterGetter().GetCluster(ctx, p, id); err != nil || got.CurrentState != "scaling" {
		t.Fatalf("after PUT: %+v, %v", got, err)
	}
	f.SetClusterState(id, "healthy")
	cl, _ = c.GetCluster(ctx, p, id)
	if cl.ETag != "2" || cl.ServiceGroups[0].NumOfNodes != 4 {
		t.Fatalf("after scale: etag %q nodes %d", cl.ETag, cl.ServiceGroups[0].NumOfNodes)
	}

	stats, err := c.GetClusterStats(ctx, p, id)
	if err != nil || stats.TotalMemoryInMb == 0 || stats.MaxReplicas != 3 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}

func TestClusterDeleteAndProtection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	f.SettlePolls(1)
	c := f.Client()
	p := f.SeedProject("proj")
	id := f.SeedCluster(p, "c1", "healthy")

	if err := c.SetDeletionProtection(ctx, p, id, true); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCluster(ctx, p, id, true); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("protected delete: err = %v, want ErrConflict", err)
	}
	if err := c.SetDeletionProtection(ctx, p, id, false); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCluster(ctx, p, id, false); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCluster(ctx, p, id, false); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("second delete: err = %v, want ErrConflict", err)
	}

	// The waiter sees destroying, then 404.
	clk := engine.NewFakeClock(time.Time{})
	state, err := engine.WaitForCluster(ctx, c.AsClusterGetter(), p, id, engine.OpDelete, time.Hour, clk, "c1", engine.WaitHooks{})
	if err != nil || state != "deleted" {
		t.Fatalf("WaitForCluster = %q, %v", state, err)
	}
	if _, err := c.GetCluster(ctx, p, id); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
	if err := c.DeleteProject(ctx, p); err != nil {
		t.Fatalf("project now empty: %v", err)
	}
}

func TestPower(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	f.SettlePolls(0)
	c := f.Client()
	p := f.SeedProject("proj")
	id := f.SeedCluster(p, "c1", "healthy")

	if err := c.TurnClusterOn(ctx, p, id, false); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("turn on a healthy cluster: err = %v, want ErrConflict", err)
	}
	if err := c.TurnClusterOff(ctx, p, id); err != nil {
		t.Fatal(err)
	}
	if got := f.ClusterState(id); got != "turningOff" {
		t.Fatalf("state = %q, want turningOff", got)
	}
	if cl, _ := c.GetCluster(ctx, p, id); cl.CurrentState != "turnedOff" {
		t.Fatalf("settled state = %q, want turnedOff", cl.CurrentState)
	}
	if err := c.TurnClusterOff(ctx, p, id); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("turn off twice: err = %v, want ErrConflict", err)
	}
	if err := c.TurnClusterOn(ctx, p, id, true); err != nil {
		t.Fatal(err)
	}
	if cl, _ := c.GetCluster(ctx, p, id); cl.CurrentState != "healthy" {
		t.Fatalf("state = %q, want healthy", cl.CurrentState)
	}
}

func TestFreeTier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	f.SettlePolls(0)
	c := f.Client()
	p := f.SeedProject("proj")
	body := capellaapi.Object{"name": "free1", "cloudProvider": capellaapi.Object{"type": "aws", "region": "us-east-1"}}

	id, err := c.CreateFreeCluster(ctx, p, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateFreeCluster(ctx, p, capellaapi.Object{"name": "free2"}); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("second free cluster: err = %v, want ErrValidation", err)
	}
	obj, etag, err := c.GetFreeCluster(ctx, p, id)
	if err != nil || etag != "1" {
		t.Fatalf("GetFreeCluster = %v, %q, %v", obj, etag, err)
	}
	if support, _ := obj["support"].(map[string]any); support["plan"] != "free" {
		t.Fatalf("support = %v", obj["support"])
	}
	if err := c.UpdateFreeCluster(ctx, p, id, capellaapi.Object{"name": "free1", "description": "d"}, etag); err != nil {
		t.Fatal(err)
	}
	// Free clusters show up in the normal listing too.
	if cls, _ := c.ListClusters(ctx, p); len(cls) != 1 || cls[0].Support.Plan != "free" {
		t.Fatalf("ListClusters = %+v", cls)
	}

	bid, err := c.CreateFreeBucket(ctx, p, id, capellaapi.Object{"name": "b1", "memoryAllocationInMb": 100})
	if err != nil || bid != capellaapi.BucketID("b1") {
		t.Fatalf("CreateFreeBucket = %q, %v", bid, err)
	}
	buckets, stats, err := c.ListFreeBuckets(ctx, p, id)
	if err != nil || len(buckets) != 1 || stats.TotalMemoryInMb == 0 {
		t.Fatalf("ListFreeBuckets = %+v, %+v, %v", buckets, stats, err)
	}
	if err := c.UpdateFreeBucket(ctx, p, id, bid, capellaapi.Object{"memoryAllocationInMb": 200}); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := c.GetFreeBucket(ctx, p, id, bid); b.MemoryAllocationInMb != 200 {
		t.Fatalf("memory = %d, want 200", b.MemoryAllocationInMb)
	}
	if err := c.DeleteFreeBucket(ctx, p, id, bid); err != nil {
		t.Fatal(err)
	}

	if err := c.TurnFreeClusterOff(ctx, p, id); err != nil {
		t.Fatal(err)
	}
	if cl, _ := c.GetCluster(ctx, p, id); cl.CurrentState != "turnedOff" {
		t.Fatalf("state = %q", cl.CurrentState)
	}
	if err := c.TurnFreeClusterOn(ctx, p, id); err != nil {
		t.Fatal(err)
	}
	if cl, _ := c.GetCluster(ctx, p, id); cl.CurrentState != "healthy" {
		t.Fatalf("state = %q, want healthy", cl.CurrentState)
	}
	if err := c.DeleteFreeCluster(ctx, p, id); err != nil {
		t.Fatal(err)
	}
}

func TestCIDRs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c, p, cl := fixture(t)

	id, err := c.CreateAllowedCIDR(ctx, p, cl, "203.0.113.0/24", "office", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateAllowedCIDR(ctx, p, cl, "203.0.113.0/24", "", ""); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate: err = %v, want ErrConflict", err)
	}
	if _, err := c.CreateAllowedCIDR(ctx, p, cl, "198.51.100.1/32", "", "2030-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListAllowedCIDRs(ctx, p, cl)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].Type != "permanent" || list[1].Type != "temporary" || list[0].Status != "active" {
		t.Fatalf("types = %q/%q status %q", list[0].Type, list[1].Type, list[0].Status)
	}
	got, _, err := c.GetAllowedCIDR(ctx, p, cl, id)
	if err != nil || got.Comment != "office" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if err := c.DeleteAllowedCIDR(ctx, p, cl, id); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAllowedCIDR(ctx, p, cl, id); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("delete twice: err = %v, want ErrNotFound", err)
	}
}

func TestBucketsScopesCollections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c, p, cl := fixture(t)

	// Names with characters that are awkward in a path are safe as base64url IDs.
	name := "travel/sample+é"
	bid, err := c.CreateBucket(ctx, p, cl, capellaapi.CreateBucketRequest{
		Name: name, Type: "couchbase", StorageBackend: "couchstore", MemoryAllocationInMb: 128,
		BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 1,
	})
	if err != nil || bid != capellaapi.BucketID(name) {
		t.Fatalf("CreateBucket = %q, %v", bid, err)
	}
	if _, err := c.CreateBucket(ctx, p, cl, capellaapi.CreateBucketRequest{Name: name, MemoryAllocationInMb: 128}); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate bucket: err = %v, want ErrConflict", err)
	}
	if _, err := c.CreateBucket(ctx, p, cl, capellaapi.CreateBucketRequest{Name: "small", MemoryAllocationInMb: 10}); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("tiny bucket: err = %v, want ErrValidation", err)
	}
	b, etag, err := c.GetBucket(ctx, p, cl, bid)
	if err != nil || b.Name != name || b.MemoryAllocationInMb != 128 || etag != "1" {
		t.Fatalf("GetBucket = %+v, %q, %v", b, etag, err)
	}
	if err := c.UpdateBucket(ctx, p, cl, bid, capellaapi.Object{"memoryAllocationInMb": 256, "name": "ignored"}, etag); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateBucket(ctx, p, cl, bid, capellaapi.Object{"replicas": 2}, etag); !errors.Is(err, capellaapi.ErrPreconditionFailed) {
		t.Fatalf("stale bucket update: err = %v, want ErrPreconditionFailed", err)
	}
	if b, _, _ = c.GetBucket(ctx, p, cl, bid); b.MemoryAllocationInMb != 256 || b.Name != name {
		t.Fatalf("after update: %+v", b)
	}
	if _, _, err := c.GetBucket(ctx, p, cl, capellaapi.BucketID("missing")); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("missing bucket: err = %v, want ErrNotFound", err)
	}

	scopes, err := c.ListScopes(ctx, p, cl, bid)
	if err != nil || len(scopes) != 1 || scopes[0].Name != "_default" {
		t.Fatalf("initial scopes = %+v, %v", scopes, err)
	}
	if err := c.CreateScope(ctx, p, cl, bid, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateScope(ctx, p, cl, bid, "inventory"); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate scope: err = %v, want ErrConflict", err)
	}

	ttl := 3600
	if err := c.CreateCollection(ctx, p, cl, bid, "inventory", "hotels", &ttl); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateCollection(ctx, p, cl, bid, "inventory", "hotels", nil); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate collection: err = %v, want ErrConflict", err)
	}
	if err := c.CreateCollection(ctx, p, cl, bid, "nope", "x", nil); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("collection in missing scope: err = %v, want ErrNotFound", err)
	}
	scopes, _ = c.ListScopes(ctx, p, cl, bid)
	if len(scopes) != 2 || len(scopes[1].Collections) != 1 || scopes[1].Collections[0].MaxTTL != 3600 {
		t.Fatalf("scopes = %+v", scopes)
	}
	colls, err := c.ListCollections(ctx, p, cl, bid, "inventory")
	if err != nil || len(colls) != 1 || colls[0].Name != "hotels" {
		t.Fatalf("collections = %+v, %v", colls, err)
	}
	coll, etag, err := c.GetCollection(ctx, p, cl, bid, "inventory", "hotels")
	if err != nil || coll.MaxTTL != 3600 {
		t.Fatalf("GetCollection = %+v, %v", coll, err)
	}
	if err := c.UpdateCollection(ctx, p, cl, bid, "inventory", "hotels", 60, etag); err != nil {
		t.Fatal(err)
	}
	if coll, _, _ = c.GetCollection(ctx, p, cl, bid, "inventory", "hotels"); coll.MaxTTL != 60 {
		t.Fatalf("maxTTL = %d, want 60", coll.MaxTTL)
	}
	if err := c.DeleteCollection(ctx, p, cl, bid, "inventory", "hotels"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCollection(ctx, p, cl, bid, "_default", "_default"); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("delete _default: err = %v, want ErrValidation", err)
	}
	if err := c.DeleteScope(ctx, p, cl, bid, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteBucket(ctx, p, cl, bid); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteBucket(ctx, p, cl, bid); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("delete twice: err = %v, want ErrNotFound", err)
	}
}

func TestCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c, p, cl := fixture(t)
	access := []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources:  capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "travel-sample"}}},
	}}

	// No password supplied: Capella generates one and returns it exactly once.
	id, pw, err := c.CreateCredential(ctx, p, cl, capellaapi.CreateCredentialRequest{Name: "app", Access: access})
	if err != nil {
		t.Fatal(err)
	}
	if !secrets.MeetsRules(pw) {
		t.Fatalf("generated password %q breaks the rules", pw)
	}
	got, etag, err := c.GetCredential(ctx, p, cl, id)
	if err != nil || got.Name != "app" || len(got.Access) != 1 {
		t.Fatalf("GetCredential = %+v, %v", got, err)
	}

	// A supplied password is checked against the rules and not echoed back.
	if _, _, err := c.CreateCredential(ctx, p, cl, capellaapi.CreateCredentialRequest{Name: "weak", Password: "weak", Access: access}); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("weak password: err = %v, want ErrValidation", err)
	}
	good := secrets.MustGenerate(24)
	_, echoed, err := c.CreateCredential(ctx, p, cl, capellaapi.CreateCredentialRequest{Name: "given", Password: good, Access: access})
	if err != nil || echoed != "" {
		t.Fatalf("supplied password: echoed %q, err %v", echoed, err)
	}
	if _, _, err := c.CreateCredential(ctx, p, cl, capellaapi.CreateCredentialRequest{Name: "app", Access: access}); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate: err = %v, want ErrConflict", err)
	}

	update := capellaapi.UpdateCredentialRequest{Access: []capellaapi.AccessEntry{{Privileges: []string{"data_writer"}}}}
	if err := c.UpdateCredential(ctx, p, cl, id, update, etag); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateCredential(ctx, p, cl, id, update, etag); !errors.Is(err, capellaapi.ErrPreconditionFailed) {
		t.Fatalf("stale update: err = %v, want ErrPreconditionFailed", err)
	}
	if err := c.UpdateCredential(ctx, p, cl, id, capellaapi.UpdateCredentialRequest{Password: "weak"}, ""); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("weak update: err = %v, want ErrValidation", err)
	}
	creds, err := c.ListCredentials(ctx, p, cl)
	if err != nil || len(creds) != 2 || creds[0].Access[0].Privileges[0] != "data_writer" {
		t.Fatalf("ListCredentials = %+v, %v", creds, err)
	}
	if err := c.DeleteCredential(ctx, p, cl, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetCredential(ctx, p, cl, id); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestOnOffSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, c, p, cl := fixture(t)

	if _, _, err := c.GetSchedule(ctx, p, cl); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("no schedule: err = %v, want ErrNotFound", err)
	}
	sched := capellaapi.Object{"timezone": "UTC", "days": []any{capellaapi.Object{"day": "monday", "state": "off"}}}
	if err := c.CreateSchedule(ctx, p, cl, sched); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateSchedule(ctx, p, cl, sched); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate: err = %v, want ErrConflict", err)
	}
	got, etag, err := c.GetSchedule(ctx, p, cl)
	if err != nil || got["timezone"] != "UTC" || etag != "1" {
		t.Fatalf("GetSchedule = %v, %q, %v", got, etag, err)
	}
	sched["timezone"] = "PST"
	if err := c.UpdateSchedule(ctx, p, cl, sched, etag); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateSchedule(ctx, p, cl, sched, etag); !errors.Is(err, capellaapi.ErrPreconditionFailed) {
		t.Fatalf("stale: err = %v, want ErrPreconditionFailed", err)
	}
	if err := c.PauseSchedule(ctx, p, cl); err != nil || !f.SchedulePaused(cl) {
		t.Fatalf("pause: err %v paused %v", err, f.SchedulePaused(cl))
	}
	if err := c.ResumeSchedule(ctx, p, cl); err != nil || f.SchedulePaused(cl) {
		t.Fatalf("resume: err %v paused %v", err, f.SchedulePaused(cl))
	}
	if err := c.DeleteSchedule(ctx, p, cl); err != nil {
		t.Fatal(err)
	}
	if err := c.PauseSchedule(ctx, p, cl); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("pause without schedule: err = %v, want ErrNotFound", err)
	}
}

func TestSnapshots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, c, p, cl := fixture(t)
	f.SettlePolls(1)

	if s, err := c.GetSnapshotSchedule(ctx, p, cl); err != nil || s != nil {
		t.Fatalf("no schedule: %v, %v", s, err)
	}
	opts, err := c.GetSnapshotScheduleOptions(ctx, p, cl)
	if err != nil || opts["minimumRetention"] == nil {
		t.Fatalf("options = %v, %v", opts, err)
	}
	if err := c.UpsertSnapshotSchedule(ctx, p, cl, capellaapi.Object{"interval": 5, "retention": 48}); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("bad interval: err = %v, want ErrValidation", err)
	}
	if err := c.UpsertSnapshotSchedule(ctx, p, cl, capellaapi.Object{"interval": 12, "retention": 48}); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.GetSnapshotSchedule(ctx, p, cl); s["interval"] != float64(12) {
		t.Fatalf("schedule = %v", s)
	}
	if err := c.DeleteSnapshotSchedule(ctx, p, cl); err != nil {
		t.Fatal(err)
	}
	if regions, err := c.ListSnapshotRegions(ctx, p, cl); err != nil || len(regions) == 0 {
		t.Fatalf("regions = %v, %v", regions, err)
	}

	id, err := c.CreateSnapshot(ctx, p, cl, capellaapi.Object{"retention": 72})
	if err != nil || id == "" {
		t.Fatalf("CreateSnapshot = %q, %v", id, err)
	}
	status := func() string {
		list, err := c.ListSnapshots(ctx, p, cl)
		if err != nil || len(list) != 1 {
			t.Fatalf("ListSnapshots = %v, %v", list, err)
		}
		return list[0]["progress"].(map[string]any)["status"].(string)
	}
	if got := status(); got != "pending" {
		t.Fatalf("first list status = %q, want pending", got)
	}
	if got := status(); got != "complete" {
		t.Fatalf("second list status = %q, want complete", got)
	}
	if err := c.UpdateSnapshotRetention(ctx, p, cl, id, 96); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateSnapshotRetention(ctx, p, cl, "nope", 96); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: err = %v, want ErrNotFound", err)
	}
	if prj, err := c.ListProjectSnapshots(ctx, p); err != nil || len(prj) != 1 {
		t.Fatalf("project snapshots = %v, %v", prj, err)
	}

	rid, err := c.RestoreSnapshot(ctx, p, cl, id, capellaapi.Object{})
	if err != nil || rid == "" {
		t.Fatalf("RestoreSnapshot = %q, %v", rid, err)
	}
	if restores, err := c.ListSnapshotRestores(ctx, p, cl); err != nil || len(restores) != 1 || restores[0]["snapshot"] == "" || restores[0]["snapshot"] == id {
		t.Fatalf("restores = %v, %v", restores, err)
	}
	if err := c.DeleteSnapshot(ctx, p, cl, id); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.ListSnapshots(ctx, p, cl); len(list) != 0 {
		t.Fatalf("after delete: %v", list)
	}

	f.SetClusterState(cl, "turnedOff")
	if _, err := c.CreateSnapshot(ctx, p, cl, nil); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("snapshot of an off cluster: err = %v, want ErrConflict", err)
	}
}

func TestBucketBackups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, c, p, cl := fixture(t)
	f.SettlePolls(1)
	bid, err := c.CreateBucket(ctx, p, cl, capellaapi.CreateBucketRequest{Name: "b1", MemoryAllocationInMb: 100})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.GetBucketBackupSchedule(ctx, p, cl, bid); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("no schedule: err = %v, want ErrNotFound", err)
	}
	sched := capellaapi.Object{"weeklySchedule": capellaapi.Object{"dayOfWeek": "sunday"}}
	if err := c.CreateBucketBackupSchedule(ctx, p, cl, bid, sched); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateBucketBackupSchedule(ctx, p, cl, bid, sched); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("duplicate: err = %v, want ErrConflict", err)
	}
	if err := c.UpdateBucketBackupSchedule(ctx, p, cl, bid, sched); err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetBucketBackupSchedule(ctx, p, cl, bid); err != nil || got["weeklySchedule"] == nil {
		t.Fatalf("schedule = %v, %v", got, err)
	}
	if err := c.DeleteBucketBackupSchedule(ctx, p, cl, bid); err != nil {
		t.Fatal(err)
	}

	if err := c.CreateBucketBackup(ctx, p, cl, bid); err != nil {
		t.Fatal(err)
	}
	cycles, err := c.ListBackupCycles(ctx, p, cl, bid)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("cycles = %v, %v", cycles, err)
	}
	cycle := cycles[0]["cycleID"].(string)
	inCycle, err := c.ListCycleBackups(ctx, p, cl, bid, cycle)
	if err != nil || len(inCycle) != 1 {
		t.Fatalf("cycle backups = %v, %v", inCycle, err)
	}
	backupID := inCycle[0]["id"].(string)
	if _, err := c.ListCycleBackups(ctx, p, cl, bid, "nope"); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("unknown cycle: err = %v, want ErrNotFound", err)
	}
	if all, err := c.ListClusterBackups(ctx, p, cl); err != nil || len(all) != 1 {
		t.Fatalf("cluster backups = %v, %v", all, err)
	}
	if got, err := c.GetBackup(ctx, p, cl, backupID); err != nil || got["bucketName"] != "b1" {
		t.Fatalf("GetBackup = %v, %v", got, err)
	}

	restore := capellaapi.Object{"targetClusterID": cl, "sourceClusterID": cl, "backupID": backupID, "services": []any{"data"}}
	if err := c.RestoreBackup(ctx, p, cl, backupID, capellaapi.Object{"backupID": backupID}); !errors.Is(err, capellaapi.ErrValidation) {
		t.Fatalf("incomplete restore: err = %v, want ErrValidation", err)
	}
	if err := c.RestoreBackup(ctx, p, cl, backupID, restore); err != nil {
		t.Fatal(err)
	}

	exportID, err := c.ExportBackup(ctx, p, cl, bid, backupID)
	if err != nil || exportID == "" {
		t.Fatalf("ExportBackup = %q, %v", exportID, err)
	}
	if _, err := c.ExportBackup(ctx, p, cl, bid, backupID); !errors.Is(err, capellaapi.ErrConflict) {
		t.Fatalf("second export: err = %v, want ErrConflict", err)
	}
	var export capellaapi.Object
	for i := 0; i < 3; i++ {
		if export, err = c.GetBackupExport(ctx, p, cl, bid, backupID, exportID); err != nil {
			t.Fatal(err)
		}
	}
	if export["status"] != "complete" || export["backupDownloadURL"] == nil {
		t.Fatalf("export = %v", export)
	}

	if err := c.DeleteBackup(ctx, p, cl, backupID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteBackup(ctx, p, cl, backupID); !errors.Is(err, capellaapi.ErrNotFound) {
		t.Fatalf("delete twice: err = %v, want ErrNotFound", err)
	}
}

func TestFailNextAndRequestLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client(capellaapi.WithMaxAttempts(1))
	p := f.SeedProject("proj")

	f.FailNext("POST", `/clusters$`, 409, 2)
	for i := 0; i < 2; i++ {
		if _, err := c.CreateCluster(ctx, p, clusterReq("c1")); !errors.Is(err, capellaapi.ErrConflict) {
			t.Fatalf("attempt %d: err = %v, want injected ErrConflict", i, err)
		}
	}
	if _, err := c.CreateCluster(ctx, p, clusterReq("c1")); err != nil {
		t.Fatalf("after the injected failures: %v", err)
	}

	f.FailNext("", `/projects$`, 503, 1)
	if _, err := c.ListProjects(ctx); !errors.Is(err, capellaapi.ErrServer) {
		t.Fatalf("injected 503: err = %v, want ErrServer", err)
	}
	if _, err := c.ListProjects(ctx); err != nil {
		t.Fatal(err)
	}

	log := f.RequestLog()
	var posts []int
	for _, r := range log {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/clusters") {
			posts = append(posts, r.Status)
		}
		if r.Path == "" || r.Method == "" {
			t.Errorf("incomplete log entry %+v", r)
		}
	}
	if fmt.Sprint(posts) != "[409 409 202]" {
		t.Fatalf("POST /clusters statuses = %v, want [409 409 202]", posts)
	}
	if n := f.Count("POST", `/clusters$`); n != 3 {
		t.Fatalf("Count = %d, want 3", n)
	}
	f.ResetLog()
	if len(f.RequestLog()) != 0 {
		t.Fatal("ResetLog did not clear the log")
	}
}

func TestRequestLogRecordsIfMatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := capellafake.New(t)
	c := f.Client()
	id, _ := c.CreateProject(ctx, "p", "")
	_ = c.UpdateProject(ctx, id, "p", "d", "1")
	var found bool
	for _, r := range f.RequestLog() {
		if r.Method == "PUT" {
			found = r.IfMatch == "1" && r.Status == 204 && strings.Contains(r.Body, `"description":"d"`)
		}
	}
	if !found {
		t.Fatalf("PUT not logged with If-Match/status/body: %+v", f.RequestLog())
	}
}

// TestScheduleBoundariesLikeCapella mirrors the live probe (nightly run 44141806450): to is
// literal, hour 24 and minutes other than 0/30 are refused, "from 08:00, no to" runs until
// midnight, "from 0:00, no to" and a week on every day get 500. A patch version is refused on
// create.
func TestScheduleBoundariesLikeCapella(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c, p, cl := fixture(t)
	hm := func(h, m int) capellaapi.Object { return capellaapi.Object{"hour": h, "minute": m} }
	week := func(from, to capellaapi.Object) capellaapi.Object {
		monday := capellaapi.Object{"day": "monday", "state": "custom", "from": from}
		if to != nil {
			monday["to"] = to
		}
		days := []any{monday}
		for _, d := range []string{"tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
			days = append(days, capellaapi.Object{"day": d, "state": "on"})
		}
		return capellaapi.Object{"timezone": "UTC", "days": days}
	}
	allOn := capellaapi.Object{"timezone": "UTC", "days": []any{capellaapi.Object{"day": "monday", "state": "on"}}}
	for _, tc := range []struct {
		name string
		body capellaapi.Object
		want string // "" = accepted, else a part of the error
	}{
		{"whole day 0:00 to 0:00", week(hm(0, 0), hm(0, 0)), "Monday has a 'from' time boundary"},
		{"08:00 to 0:00 (to is literal)", week(hm(8, 0), hm(0, 0)), "Monday has a 'from' time boundary"},
		{"from after to", week(hm(18, 0), hm(8, 0)), "Monday has a 'from' time boundary"},
		{"from equals to", week(hm(8, 0), hm(8, 0)), "Monday has a 'from' time boundary"},
		{"hour 24", week(hm(8, 0), hm(24, 0)), "invalid hour value of '24'"},
		{"minute 59", week(hm(8, 0), hm(23, 59)), "invalid minute value of '59'"},
		{"from 0:00, no to", week(hm(0, 0), nil), "500"},
		{"on every day", allOn, "500"},
		{"from 08:00, no to (until midnight)", week(hm(8, 0), nil), ""},
		{"08:00 to 23:30", week(hm(8, 0), hm(23, 30)), ""},
		{"working hours", week(hm(8, 0), hm(18, 30)), ""},
	} {
		err := c.CreateSchedule(ctx, p, cl, tc.body)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if err := c.DeleteSchedule(ctx, p, cl); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q like Capella's", tc.name, err, tc.want)
		}
	}

	req := clusterReq("patch")
	req.CouchbaseServer = &capellaapi.CouchbaseServer{Version: "8.0.3"}
	if _, err := c.CreateCluster(ctx, p, req); !errors.Is(err, capellaapi.ErrValidation) || !strings.Contains(err.Error(), "'8.0.3' is not supported") {
		t.Fatalf("patch version: err = %v, want a 422", err)
	}
	req.CouchbaseServer.Version = "8.0"
	if _, err := c.CreateCluster(ctx, p, req); err != nil {
		t.Fatalf("release line: %v", err)
	}
}
