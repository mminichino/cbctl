package capellaapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

const (
	tOrg    = "org1"
	tPrj    = "prj1"
	tClu    = "clu1"
	tScope  = "scp1"
	tColl   = "col1"
	tUser   = "usr1"
	tCIDR   = "cidr1"
	tBackup = "bak1"
	tCycle  = "cyc1"
	tExport = "exp1"
	tBkt    = "travel-sample"

	tPassword = "Gen3rated#Passw0rd"
)

// hit is one request seen by the stub server.
type hit struct {
	Method  string
	Path    string // escaped path, no query
	Query   url.Values
	IfMatch string
	Body    string
}

// stubServer records every request and answers with canned JSON that is valid for every decoder.
type stubServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits []hit
	// override, when set, may answer a request itself and return true.
	override func(w http.ResponseWriter, r *http.Request) bool
}

func newStubServer(t *testing.T) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *stubServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.hits = append(s.hits, hit{
		Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.Query(),
		IfMatch: r.Header.Get("If-Match"), Body: string(body),
	})
	override := s.override
	s.mu.Unlock()
	if override != nil && override(w, r) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		if strings.HasSuffix(r.URL.Path, "/cloudsnapshotbackups/regions") {
			_, _ = io.WriteString(w, `["us-east-1","us-west-2"]`)
			return
		}
		w.Header().Set("ETag", `"Version: 3"`)
		// One envelope that satisfies list, single-object and stats decoders alike.
		_, _ = io.WriteString(w, `{"id":"x1","name":"n1","currentState":"healthy","data":[],"scopes":[],`+
			`"clusterStats":{"freeMemoryInMb":1,"totalMemoryInMb":2,"maxReplicas":3},`+
			`"freeMemoryInMb":10,"totalMemoryInMb":20,"maxReplicas":2,"interval":24}`)
	case http.MethodPost:
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"new1","backupId":"bk-new","restoreId":"rs-new","exportId":"ex-new","password":"`+tPassword+`"}`)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *stubServer) snapshot() []hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]hit(nil), s.hits...)
}

func (s *stubServer) reset() {
	s.mu.Lock()
	s.hits = nil
	s.mu.Unlock()
}

// last returns the most recent request.
func (s *stubServer) last(t *testing.T) hit {
	t.Helper()
	h := s.snapshot()
	if len(h) == 0 {
		t.Fatal("no request recorded")
	}
	return h[len(h)-1]
}

func newTestClient(t *testing.T, s *stubServer) *Client {
	t.Helper()
	c, err := New("test-key-0000", WithBaseURL(s.URL), WithOrgID(tOrg), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// templateIDs maps inventory placeholders to the IDs used by the contract test.
var templateIDs = map[string]string{
	"organizationId": tOrg,
	"projectId":      tPrj,
	"clusterId":      tClu,
	"bucketId":       BucketID(tBkt),
	"scopeName":      tScope,
	"collectionName": tColl,
	"userId":         tUser,
	"allowedCidrId":  tCIDR,
	"backupId":       tBackup,
	"cycleId":        tCycle,
	"exportId":       tExport,
}

// expand replaces every {placeholder} in an inventory path template with its test ID.
func expand(t *testing.T, tmpl string) string {
	t.Helper()
	var b strings.Builder
	for {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		j := strings.IndexByte(tmpl[i:], '}')
		if j < 0 {
			t.Fatalf("unterminated placeholder in %q", tmpl)
		}
		name := tmpl[i+1 : i+j]
		id, ok := templateIDs[name]
		if !ok {
			t.Fatalf("no test ID for placeholder {%s}", name)
		}
		b.WriteString(tmpl[:i])
		b.WriteString(id)
		tmpl = tmpl[i+j+1:]
	}
}

// contractCall invokes one Client method with the test IDs.
type contractCall struct {
	name string
	do   func(ctx context.Context, c *Client) error
}

func ignore[T any](_ T, err error) error { return err }

func contractCalls() []contractCall {
	b := BucketID(tBkt)
	ttl := 60
	return []contractCall{
		// organizations
		{"ListOrganizations", func(ctx context.Context, c *Client) error { return ignore(c.ListOrganizations(ctx)) }},
		{"GetOrganization", func(ctx context.Context, c *Client) error { return ignore(c.GetOrganization(ctx)) }},
		// projects
		{"ListProjects", func(ctx context.Context, c *Client) error { return ignore(c.ListProjects(ctx)) }},
		{"GetProject", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetProject(ctx, tPrj)
			return err
		}},
		{"CreateProject", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateProject(ctx, "prj", "desc"))
		}},
		{"UpdateProject", func(ctx context.Context, c *Client) error { return c.UpdateProject(ctx, tPrj, "prj", "desc", "3") }},
		{"DeleteProject", func(ctx context.Context, c *Client) error { return c.DeleteProject(ctx, tPrj) }},
		// clusters
		{"ListClusters", func(ctx context.Context, c *Client) error { return ignore(c.ListClusters(ctx, tPrj)) }},
		{"GetCluster", func(ctx context.Context, c *Client) error { return ignore(c.GetCluster(ctx, tPrj, tClu)) }},
		{"CreateCluster", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateCluster(ctx, tPrj, CreateClusterRequest{Name: "cl"}))
		}},
		{"UpdateCluster", func(ctx context.Context, c *Client) error {
			return c.UpdateCluster(ctx, tPrj, tClu, UpdateClusterRequest{Name: "cl"}, "3")
		}},
		{"DeleteCluster", func(ctx context.Context, c *Client) error { return c.DeleteCluster(ctx, tPrj, tClu, true) }},
		{"SetDeletionProtection", func(ctx context.Context, c *Client) error {
			return c.SetDeletionProtection(ctx, tPrj, tClu, true)
		}},
		{"TurnClusterOn", func(ctx context.Context, c *Client) error { return c.TurnClusterOn(ctx, tPrj, tClu, true) }},
		{"TurnClusterOff", func(ctx context.Context, c *Client) error { return c.TurnClusterOff(ctx, tPrj, tClu) }},
		{"GetClusterStats", func(ctx context.Context, c *Client) error { return ignore(c.GetClusterStats(ctx, tPrj, tClu)) }},
		// free tier
		{"CreateFreeCluster", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateFreeCluster(ctx, tPrj, Object{"name": "free"}))
		}},
		{"GetFreeCluster", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetFreeCluster(ctx, tPrj, tClu)
			return err
		}},
		{"UpdateFreeCluster", func(ctx context.Context, c *Client) error {
			return c.UpdateFreeCluster(ctx, tPrj, tClu, Object{"name": "free"}, "3")
		}},
		{"DeleteFreeCluster", func(ctx context.Context, c *Client) error { return c.DeleteFreeCluster(ctx, tPrj, tClu) }},
		{"TurnFreeClusterOn", func(ctx context.Context, c *Client) error { return c.TurnFreeClusterOn(ctx, tPrj, tClu) }},
		{"TurnFreeClusterOff", func(ctx context.Context, c *Client) error { return c.TurnFreeClusterOff(ctx, tPrj, tClu) }},
		{"ListFreeBuckets", func(ctx context.Context, c *Client) error {
			_, _, err := c.ListFreeBuckets(ctx, tPrj, tClu)
			return err
		}},
		{"GetFreeBucket", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetFreeBucket(ctx, tPrj, tClu, b)
			return err
		}},
		{"CreateFreeBucket", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateFreeBucket(ctx, tPrj, tClu, Object{"name": tBkt}))
		}},
		{"UpdateFreeBucket", func(ctx context.Context, c *Client) error {
			return c.UpdateFreeBucket(ctx, tPrj, tClu, b, Object{"memoryAllocationInMb": 100})
		}},
		{"DeleteFreeBucket", func(ctx context.Context, c *Client) error { return c.DeleteFreeBucket(ctx, tPrj, tClu, b) }},
		// on/off schedule
		{"GetSchedule", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetSchedule(ctx, tPrj, tClu)
			return err
		}},
		{"CreateSchedule", func(ctx context.Context, c *Client) error {
			return c.CreateSchedule(ctx, tPrj, tClu, Object{"timezone": "UTC"})
		}},
		{"UpdateSchedule", func(ctx context.Context, c *Client) error {
			return c.UpdateSchedule(ctx, tPrj, tClu, Object{"timezone": "UTC"}, "3")
		}},
		{"DeleteSchedule", func(ctx context.Context, c *Client) error { return c.DeleteSchedule(ctx, tPrj, tClu) }},
		{"PauseSchedule", func(ctx context.Context, c *Client) error { return c.PauseSchedule(ctx, tPrj, tClu) }},
		{"ResumeSchedule", func(ctx context.Context, c *Client) error { return c.ResumeSchedule(ctx, tPrj, tClu) }},
		// allowed CIDRs
		{"ListAllowedCIDRs", func(ctx context.Context, c *Client) error { return ignore(c.ListAllowedCIDRs(ctx, tPrj, tClu)) }},
		{"GetAllowedCIDR", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetAllowedCIDR(ctx, tPrj, tClu, tCIDR)
			return err
		}},
		{"CreateAllowedCIDR", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateAllowedCIDR(ctx, tPrj, tClu, "10.0.0.0/24", "office", ""))
		}},
		{"DeleteAllowedCIDR", func(ctx context.Context, c *Client) error { return c.DeleteAllowedCIDR(ctx, tPrj, tClu, tCIDR) }},
		// buckets
		{"ListBuckets", func(ctx context.Context, c *Client) error { return ignore(c.ListBuckets(ctx, tPrj, tClu)) }},
		{"GetBucket", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetBucket(ctx, tPrj, tClu, b)
			return err
		}},
		{"CreateBucket", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateBucket(ctx, tPrj, tClu, CreateBucketRequest{Name: tBkt}))
		}},
		{"UpdateBucket", func(ctx context.Context, c *Client) error {
			return c.UpdateBucket(ctx, tPrj, tClu, b, Object{"memoryAllocationInMb": 200}, "3")
		}},
		{"DeleteBucket", func(ctx context.Context, c *Client) error { return c.DeleteBucket(ctx, tPrj, tClu, b) }},
		// scopes
		{"ListScopes", func(ctx context.Context, c *Client) error { return ignore(c.ListScopes(ctx, tPrj, tClu, b)) }},
		{"GetScope", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetScope(ctx, tPrj, tClu, b, tScope)
			return err
		}},
		{"CreateScope", func(ctx context.Context, c *Client) error { return c.CreateScope(ctx, tPrj, tClu, b, tScope) }},
		{"DeleteScope", func(ctx context.Context, c *Client) error { return c.DeleteScope(ctx, tPrj, tClu, b, tScope) }},
		// collections
		{"ListCollections", func(ctx context.Context, c *Client) error {
			return ignore(c.ListCollections(ctx, tPrj, tClu, b, tScope))
		}},
		{"GetCollection", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetCollection(ctx, tPrj, tClu, b, tScope, tColl)
			return err
		}},
		{"CreateCollection", func(ctx context.Context, c *Client) error {
			return c.CreateCollection(ctx, tPrj, tClu, b, tScope, tColl, &ttl)
		}},
		{"UpdateCollection", func(ctx context.Context, c *Client) error {
			return c.UpdateCollection(ctx, tPrj, tClu, b, tScope, tColl, 120, "3")
		}},
		{"DeleteCollection", func(ctx context.Context, c *Client) error {
			return c.DeleteCollection(ctx, tPrj, tClu, b, tScope, tColl)
		}},
		// credentials
		{"ListCredentials", func(ctx context.Context, c *Client) error { return ignore(c.ListCredentials(ctx, tPrj, tClu)) }},
		{"GetCredential", func(ctx context.Context, c *Client) error {
			_, _, err := c.GetCredential(ctx, tPrj, tClu, tUser)
			return err
		}},
		{"CreateCredential", func(ctx context.Context, c *Client) error {
			_, _, err := c.CreateCredential(ctx, tPrj, tClu, CreateCredentialRequest{Name: "app"})
			return err
		}},
		{"UpdateCredential", func(ctx context.Context, c *Client) error {
			return c.UpdateCredential(ctx, tPrj, tClu, tUser, UpdateCredentialRequest{}, "3")
		}},
		{"DeleteCredential", func(ctx context.Context, c *Client) error { return c.DeleteCredential(ctx, tPrj, tClu, tUser) }},
		// cloud snapshots
		{"GetSnapshotSchedule", func(ctx context.Context, c *Client) error { return ignore(c.GetSnapshotSchedule(ctx, tPrj, tClu)) }},
		{"UpsertSnapshotSchedule", func(ctx context.Context, c *Client) error {
			return c.UpsertSnapshotSchedule(ctx, tPrj, tClu, Object{"interval": 24})
		}},
		{"DeleteSnapshotSchedule", func(ctx context.Context, c *Client) error { return c.DeleteSnapshotSchedule(ctx, tPrj, tClu) }},
		{"GetSnapshotScheduleOptions", func(ctx context.Context, c *Client) error {
			return ignore(c.GetSnapshotScheduleOptions(ctx, tPrj, tClu))
		}},
		{"ListSnapshotRegions", func(ctx context.Context, c *Client) error { return ignore(c.ListSnapshotRegions(ctx, tPrj, tClu)) }},
		{"ListSnapshots", func(ctx context.Context, c *Client) error { return ignore(c.ListSnapshots(ctx, tPrj, tClu)) }},
		{"ListProjectSnapshots", func(ctx context.Context, c *Client) error { return ignore(c.ListProjectSnapshots(ctx, tPrj)) }},
		{"CreateSnapshot", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateSnapshot(ctx, tPrj, tClu, Object{"retention": 24}))
		}},
		{"UpdateSnapshotRetention", func(ctx context.Context, c *Client) error {
			return c.UpdateSnapshotRetention(ctx, tPrj, tClu, tBackup, 48)
		}},
		{"DeleteSnapshot", func(ctx context.Context, c *Client) error { return c.DeleteSnapshot(ctx, tPrj, tClu, tBackup) }},
		{"RestoreSnapshot", func(ctx context.Context, c *Client) error {
			return ignore(c.RestoreSnapshot(ctx, tPrj, tClu, tBackup, Object{}))
		}},
		{"ListSnapshotRestores", func(ctx context.Context, c *Client) error {
			return ignore(c.ListSnapshotRestores(ctx, tPrj, tClu))
		}},
		// bucket backups
		{"GetBucketBackupSchedule", func(ctx context.Context, c *Client) error {
			return ignore(c.GetBucketBackupSchedule(ctx, tPrj, tClu, b))
		}},
		{"CreateBucketBackupSchedule", func(ctx context.Context, c *Client) error {
			return c.CreateBucketBackupSchedule(ctx, tPrj, tClu, b, Object{"type": "weekly"})
		}},
		{"UpdateBucketBackupSchedule", func(ctx context.Context, c *Client) error {
			return c.UpdateBucketBackupSchedule(ctx, tPrj, tClu, b, Object{"type": "weekly"})
		}},
		{"DeleteBucketBackupSchedule", func(ctx context.Context, c *Client) error {
			return c.DeleteBucketBackupSchedule(ctx, tPrj, tClu, b)
		}},
		{"ListBackupCycles", func(ctx context.Context, c *Client) error { return ignore(c.ListBackupCycles(ctx, tPrj, tClu, b)) }},
		{"ListCycleBackups", func(ctx context.Context, c *Client) error {
			return ignore(c.ListCycleBackups(ctx, tPrj, tClu, b, tCycle))
		}},
		{"CreateBucketBackup", func(ctx context.Context, c *Client) error { return c.CreateBucketBackup(ctx, tPrj, tClu, b) }},
		{"ListClusterBackups", func(ctx context.Context, c *Client) error { return ignore(c.ListClusterBackups(ctx, tPrj, tClu)) }},
		{"GetBackup", func(ctx context.Context, c *Client) error { return ignore(c.GetBackup(ctx, tPrj, tClu, tBackup)) }},
		{"DeleteBackup", func(ctx context.Context, c *Client) error { return c.DeleteBackup(ctx, tPrj, tClu, tBackup) }},
		{"RestoreBackup", func(ctx context.Context, c *Client) error {
			return c.RestoreBackup(ctx, tPrj, tClu, tBackup, Object{"services": []string{"data"}})
		}},
		{"ExportBackup", func(ctx context.Context, c *Client) error {
			return ignore(c.ExportBackup(ctx, tPrj, tClu, b, tBackup))
		}},
		{"GetBackupExport", func(ctx context.Context, c *Client) error {
			return ignore(c.GetBackupExport(ctx, tPrj, tClu, b, tBackup, tExport))
		}},
	}
}

func TestContractCoversInventory(t *testing.T) {
	t.Parallel()
	srv := newStubServer(t)
	c := newTestClient(t, srv)
	ctx := context.Background()

	for _, call := range contractCalls() {
		srv.reset()
		if err := call.do(ctx, c); err != nil {
			t.Errorf("%s: %v", call.name, err)
		}
		if len(srv.snapshot()) == 0 {
			t.Errorf("%s: sent no request", call.name)
		}
	}
	if t.Failed() {
		return
	}

	// Replay everything once more to collect the full hit set.
	srv.reset()
	for _, call := range contractCalls() {
		if err := call.do(ctx, c); err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
	}
	seen := map[string]bool{}
	for _, h := range srv.snapshot() {
		seen[h.Method+" "+h.Path] = true
	}
	for _, ep := range Inventory {
		key := ep.Method + " " + expand(t, ep.Path)
		if !seen[key] {
			t.Errorf("inventory row never exercised: %s %s (expected %s)", ep.Method, ep.Path, key)
		}
		delete(seen, key)
	}
	for key := range seen {
		t.Errorf("request not in Inventory: %s", key)
	}
}

func TestEndpointRequests(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := BucketID(tBkt)
	prj := "/v4/organizations/" + tOrg + "/projects/" + tPrj
	clu := prj + "/clusters/" + tClu

	t.Run("GetCluster sets ETag and decodes", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		cl, err := newTestClient(t, srv).GetCluster(ctx, tPrj, tClu)
		if err != nil {
			t.Fatal(err)
		}
		if cl.ETag != "3" || cl.CurrentState != "healthy" || cl.ID != "x1" {
			t.Errorf("cluster = %+v", cl)
		}
	})

	t.Run("GetProject returns etag", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		_, etag, err := newTestClient(t, srv).GetProject(ctx, tPrj)
		if err != nil || etag != "3" {
			t.Errorf("etag=%q err=%v", etag, err)
		}
	})

	t.Run("Create calls return the id", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		c := newTestClient(t, srv)
		for name, fn := range map[string]func() (string, error){
			"project":   func() (string, error) { return c.CreateProject(ctx, "n", "") },
			"cluster":   func() (string, error) { return c.CreateCluster(ctx, tPrj, CreateClusterRequest{Name: "n"}) },
			"bucket":    func() (string, error) { return c.CreateBucket(ctx, tPrj, tClu, CreateBucketRequest{Name: "n"}) },
			"cidr":      func() (string, error) { return c.CreateAllowedCIDR(ctx, tPrj, tClu, "10.0.0.1/32", "", "") },
			"freeClust": func() (string, error) { return c.CreateFreeCluster(ctx, tPrj, Object{}) },
			"freeBkt":   func() (string, error) { return c.CreateFreeBucket(ctx, tPrj, tClu, Object{}) },
		} {
			if id, err := fn(); err != nil || id != "new1" {
				t.Errorf("%s: id=%q err=%v", name, id, err)
			}
		}
	})

	t.Run("Create without id is an error", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
			return true
		}
		if id, err := newTestClient(t, srv).CreateProject(ctx, "n", ""); err == nil {
			t.Errorf("id=%q, want error", id)
		}
	})

	t.Run("snapshot and backup ids", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		c := newTestClient(t, srv)
		if id, err := c.CreateSnapshot(ctx, tPrj, tClu, Object{}); err != nil || id != "bk-new" {
			t.Errorf("CreateSnapshot = %q, %v", id, err)
		}
		if id, err := c.RestoreSnapshot(ctx, tPrj, tClu, tBackup, Object{}); err != nil || id != "rs-new" {
			t.Errorf("RestoreSnapshot = %q, %v", id, err)
		}
		if id, err := c.ExportBackup(ctx, tPrj, tClu, b, tBackup); err != nil || id != "ex-new" {
			t.Errorf("ExportBackup = %q, %v", id, err)
		}
	})

	t.Run("CreateCredential returns and redacts the password", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		id, pw, err := newTestClient(t, srv).CreateCredential(ctx, tPrj, tClu, CreateCredentialRequest{Name: "app"})
		if err != nil || id != "new1" || pw != tPassword {
			t.Fatalf("id=%q pw=%q err=%v", id, pw, err)
		}
		if got := redact.String("leak " + tPassword); strings.Contains(got, tPassword) {
			t.Errorf("password not registered with redact: %q", got)
		}
		var sent map[string]any
		if err := json.Unmarshal([]byte(srv.last(t).Body), &sent); err != nil {
			t.Fatal(err)
		}
		if sent["name"] != "app" {
			t.Errorf("body = %v", sent)
		}
	})

	t.Run("DeleteCluster query", func(t *testing.T) {
		t.Parallel()
		for _, retain := range []bool{true, false} {
			srv := newStubServer(t)
			if err := newTestClient(t, srv).DeleteCluster(ctx, tPrj, tClu, retain); err != nil {
				t.Fatal(err)
			}
			h := srv.last(t)
			want := "false"
			if retain {
				want = "true"
			}
			if h.Method != http.MethodDelete || h.Path != clu || h.Query.Get("retainsnapshotbackups") != want {
				t.Errorf("hit = %+v, want retainsnapshotbackups=%s", h, want)
			}
		}
	})

	t.Run("If-Match is sent on updates", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		c := newTestClient(t, srv)
		if err := c.UpdateProject(ctx, tPrj, "n", "d", "7"); err != nil {
			t.Fatal(err)
		}
		if h := srv.last(t); h.Method != http.MethodPut || h.Path != prj || h.IfMatch != "7" {
			t.Errorf("hit = %+v", h)
		}
		if err := c.UpdateCluster(ctx, tPrj, tClu, UpdateClusterRequest{Name: "n"}, ""); err != nil {
			t.Fatal(err)
		}
		if h := srv.last(t); h.IfMatch != "" {
			t.Errorf("If-Match = %q, want none", h.IfMatch)
		}
	})

	t.Run("activation and deletion protection bodies", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		c := newTestClient(t, srv)
		if err := c.TurnClusterOn(ctx, tPrj, tClu, true); err != nil {
			t.Fatal(err)
		}
		if h := srv.last(t); h.Path != clu+"/activationState" || h.Body != `{"turnOnLinkedAppService":true}` {
			t.Errorf("hit = %+v", h)
		}
		if err := c.SetDeletionProtection(ctx, tPrj, tClu, false); err != nil {
			t.Fatal(err)
		}
		if h := srv.last(t); h.Method != http.MethodPut || h.Body != `{"deletionProtection":false}` {
			t.Errorf("hit = %+v", h)
		}
		if err := c.TurnFreeClusterOn(ctx, tPrj, tClu); err != nil {
			t.Fatal(err)
		}
		if h := srv.last(t); h.Path != prj+"/clusters/freeTier/"+tClu+"/activationState" || h.Body != "" {
			t.Errorf("hit = %+v", h)
		}
	})

	t.Run("bucket IDs and escaped names", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		c := newTestClient(t, srv)
		if err := c.DeleteScope(ctx, tPrj, tClu, b, "my scope/x"); err != nil {
			t.Fatal(err)
		}
		want := clu + "/buckets/" + b + "/scopes/my%20scope%2Fx"
		if h := srv.last(t); h.Path != want {
			t.Errorf("path = %q, want %q", h.Path, want)
		}
		if err := c.CreateCollection(ctx, tPrj, tClu, b, tScope, "c", nil); err != nil {
			t.Fatal(err)
		}
		if h := srv.last(t); h.Body != `{"name":"c"}` {
			t.Errorf("body = %q", h.Body)
		}
	})

	t.Run("ListFreeBuckets returns stats", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
			_, _ = io.WriteString(w, `{"data":[{"name":"a"},{"name":"b"}],"clusterStats":{"freeMemoryInMb":5,"totalMemoryInMb":8,"maxReplicas":1}}`)
			return true
		}
		bs, st, err := newTestClient(t, srv).ListFreeBuckets(ctx, tPrj, tClu)
		if err != nil || len(bs) != 2 || st.FreeMemoryInMb != 5 || st.TotalMemoryInMb != 8 || st.MaxReplicas != 1 {
			t.Errorf("buckets=%v stats=%+v err=%v", bs, st, err)
		}
	})

	t.Run("ListScopes uses the scopes key", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
			_, _ = io.WriteString(w, `{"scopes":[{"name":"_default","collections":[{"name":"_default"}]},{"name":"s1"}]}`)
			return true
		}
		got, err := newTestClient(t, srv).ListScopes(ctx, tPrj, tClu, b)
		if err != nil || len(got) != 2 || got[0].Collections[0].Name != "_default" {
			t.Errorf("scopes=%+v err=%v", got, err)
		}
		if q := srv.last(t).Query; q.Get("perPage") != "100" {
			t.Errorf("query = %v", q)
		}
	})

	t.Run("GetSchedule 404 is ErrNotFound", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"httpStatusCode":404,"code":4000,"message":"no schedule"}`)
			return true
		}
		_, _, err := newTestClient(t, srv).GetSchedule(ctx, tPrj, tClu)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, engine.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("GetSnapshotSchedule empty is nil", func(t *testing.T) {
		t.Parallel()
		for name, respond := range map[string]func(http.ResponseWriter){
			"204":   func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
			"empty": func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) },
			"{}":    func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{}`) },
		} {
			srv := newStubServer(t)
			srv.override = func(w http.ResponseWriter, _ *http.Request) bool { respond(w); return true }
			got, err := newTestClient(t, srv).GetSnapshotSchedule(ctx, tPrj, tClu)
			if err != nil || got != nil {
				t.Errorf("%s: got=%v err=%v, want nil, nil", name, got, err)
			}
		}
		srv := newStubServer(t)
		got, err := newTestClient(t, srv).GetSnapshotSchedule(ctx, tPrj, tClu)
		if err != nil || got["interval"] != float64(24) {
			t.Errorf("schedule = %v err=%v", got, err)
		}
	})

	t.Run("no organization", func(t *testing.T) {
		t.Parallel()
		srv := newStubServer(t)
		c, err := New("test-key-0000", WithBaseURL(srv.URL))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListProjects(ctx); err == nil || !strings.Contains(err.Error(), "organization ID is not set") {
			t.Errorf("err = %v", err)
		}
		if len(srv.snapshot()) != 0 {
			t.Error("request sent without an organization")
		}
	})
}

func TestBucketID(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, want string }{
		{"travel-sample", "dHJhdmVsLXNhbXBsZQ"},
		{"a", "YQ"},
		{"a/b+c", "YS9iK2M"},
		{"héllo", "aMOpbGxv"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := BucketID(tt.name)
			if got != tt.want {
				t.Errorf("BucketID(%q) = %q, want %q", tt.name, got, tt.want)
			}
			if strings.ContainsAny(got, "+/=") {
				t.Errorf("BucketID(%q) = %q is not unpadded URL-safe", tt.name, got)
			}
		})
	}
}

func TestAsClusterGetter(t *testing.T) {
	t.Parallel()
	srv := newStubServer(t)
	g := newTestClient(t, srv).AsClusterGetter()
	cl, err := g.GetCluster(context.Background(), tPrj, tClu)
	if err != nil || cl.Name != "n1" || cl.CurrentState != "healthy" {
		t.Errorf("cluster=%+v err=%v", cl, err)
	}

	srv.mu.Lock()
	srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	srv.mu.Unlock()
	if _, err := g.GetCluster(context.Background(), tPrj, tClu); !errors.Is(err, engine.ErrNotFound) || !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
