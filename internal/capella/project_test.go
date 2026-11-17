package capella

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/engine"
)

func TestProjectResolveOrCreate(t *testing.T) {
	newProject := func(f *fakeAPI, name string) *Project {
		o := f.organization()
		o.Client.AccountID = "u1"
		f.reply(http.MethodGet, fakeUsersPath+"/u1", 200, userBody("p1"))
		f.reply(http.MethodGet, fakeProjectPath, 200, map[string]any{"id": "p1", "name": "existing"})
		return NewProject(o, name, nil)
	}
	t.Run("finds the user's project by name", func(t *testing.T) {
		f := newFakeAPI(t)
		p := newProject(f, "existing")
		if err := p.Resolve(); err != nil {
			t.Fatal(err)
		}
		if p.ID() != "p1" || f.countCalls(http.MethodPost, fakeProjectsPath) != 0 {
			t.Errorf("ID = %q, POSTs = %d", p.ID(), f.countCalls(http.MethodPost, fakeProjectsPath))
		}
	})
	t.Run("creates a missing project and takes ownership", func(t *testing.T) {
		f := newFakeAPI(t)
		p := newProject(f, "fresh")
		f.reply(http.MethodPost, fakeProjectsPath, 201, map[string]any{"id": "p2"})
		f.reply(http.MethodPatch, fakeUsersPath+"/u1", 200, nil)
		f.reply(http.MethodGet, fakeProjectsPath+"/p2", 200, map[string]any{"id": "p2", "name": "fresh"})
		if err := p.Resolve(); err != nil {
			t.Fatal(err)
		}
		if p.ID() != "p2" || p.Org.FindProjectByID("p2") != p {
			t.Errorf("ID = %q, registered = %v", p.ID(), p.Org.FindProjectByID("p2"))
		}
		body := decodeBody(t, f.callsTo(http.MethodPost, fakeProjectsPath)[0])
		if body["name"] != "fresh" || body["description"] != "Automatically Created Project" {
			t.Errorf("POST body = %v", body)
		}
		if n := f.countCalls(http.MethodPatch, fakeUsersPath+"/u1"); n != 1 {
			t.Errorf("PATCHes = %d, want 1", n)
		}
	})
	t.Run("create refused", func(t *testing.T) {
		f := newFakeAPI(t)
		p := newProject(f, "fresh")
		f.reply(http.MethodPost, fakeProjectsPath, 403, errBody(403))
		if err := p.Resolve(); err == nil || !strings.Contains(err.Error(), "Project Create Error (HTTP 403)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ownership refused", func(t *testing.T) {
		f := newFakeAPI(t)
		p := newProject(f, "fresh")
		f.reply(http.MethodPost, fakeProjectsPath, 201, map[string]any{"id": "p2"})
		f.reply(http.MethodPatch, fakeUsersPath+"/u1", 403, errBody(403))
		if err := p.Resolve(); err == nil || !strings.Contains(err.Error(), "User Set Error") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("user project fetch fails", func(t *testing.T) {
		f := newFakeAPI(t)
		p := newProject(f, "existing")
		f.reply(http.MethodGet, fakeProjectPath, 500, errBody(500))
		if err := p.Resolve(); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("user not configured", func(t *testing.T) {
		f := newFakeAPI(t)
		p := NewProject(f.organization(), "", nil)
		if p.Name() != config.DefaultCapellaProjectName {
			t.Errorf("default name = %q", p.Name())
		}
		err := p.Resolve()
		if err == nil || !strings.Contains(err.Error(), "Capella user not configured") {
			t.Fatalf("err = %v", err)
		}
		if _, err := p.CreateProject(); err == nil {
			t.Fatal("CreateProject without a user: want an error")
		}
	})
	t.Run("configured project id", func(t *testing.T) {
		f := newFakeAPI(t)
		p := newProject(f, "ignored")
		p.Org.Client.ProjectID = "p1"
		if err := p.Resolve(); err != nil || p.ID() != "p1" {
			t.Fatalf("Resolve = %v, ID %q", err, p.ID())
		}
	})
}

func TestProjectListAndClusters(t *testing.T) {
	t.Run("list projects", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeProjectsPath, 200, listPage(map[string]any{"id": "p1", "name": "a"}, map[string]any{"id": "p2", "name": "b"}))
		got, err := f.project().ListProjects()
		if err != nil || len(got) != 2 || got[1].Name != "b" {
			t.Fatalf("ListProjects = %+v, %v", got, err)
		}
	})
	t.Run("list projects refused", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeProjectsPath, 403, errBody(403))
		if _, err := f.project().ListProjects(); err == nil || !strings.Contains(err.Error(), "Project List Error") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("default cluster", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		p := f.project()
		p.Org.Client.DatabaseName = "db1"
		c, err := p.GetDefaultCluster()
		if err != nil || c.Data == nil || p.FindClusterByID("c1") != c {
			t.Fatalf("GetDefaultCluster = %+v, %v", c, err)
		}
	})
	t.Run("default cluster missing is unresolved", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f)
		p := f.project()
		p.Org.Client.DatabaseName = "db1"
		c, err := p.GetDefaultCluster()
		if err != nil || c == nil || c.Data != nil {
			t.Fatalf("GetDefaultCluster = %+v, %v", c, err)
		}
	})
	t.Run("default cluster error names it", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeClustersPath+"/c7", 400, errBody(400))
		p := f.project()
		p.Org.Client.DatabaseID = "c7"
		if _, err := p.GetDefaultCluster(); err == nil || !strings.Contains(err.Error(), "cannot find cluster c7") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("create cluster needs a config", func(t *testing.T) {
		f := newFakeAPI(t)
		if _, err := f.project().CreateCluster("x", nil); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("create cluster uses the database name, then the cache", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		p := f.project()
		p.Org.Client.DatabaseName = "db1"
		c, err := p.CreateCluster("", NewClusterConfig().SingleNode(nil))
		if err != nil || c.Data.Name != "db1" {
			t.Fatalf("CreateCluster = %+v, %v", c, err)
		}
		again, err := p.CreateCluster("db1", NewClusterConfig().SingleNode(nil))
		if err != nil || again != c {
			t.Fatalf("second CreateCluster = %+v, %v", again, err)
		}
		if created, _ := s.count(); created != 1 {
			t.Errorf("created = %d, want 1", created)
		}
		if got, err := p.AddCluster("db1"); err != nil || got != c {
			t.Errorf("AddCluster cached = %+v, %v", got, err)
		}
	})
	t.Run("create cluster random name and failure", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.postStatus = 400
		_, err := f.project().CreateCluster("", NewClusterConfig().SingleNode(nil))
		if err == nil || !strings.Contains(err.Error(), "cannot create cluster ") {
			t.Fatalf("err = %v", err)
		}
		name := RandomName()
		if left, right, ok := strings.Cut(name, "-"); !ok || left == "" || right == "" {
			t.Errorf("RandomName = %q", name)
		}
	})
}

// backendConfig points a legacy Backend config at the fake.
func backendConfig(f *fakeAPI) *config.Config {
	f.reply(http.MethodGet, fakeOrgPath, 200, map[string]any{"id": "org-1", "name": "org"})
	f.reply(http.MethodGet, fakeUsersPath+"/u1", 200, userBody("p1"))
	f.reply(http.MethodGet, fakeProjectPath, 200, map[string]any{"id": "p1", "name": "default"})
	cfg := config.New()
	cfg.SetProp(config.PropCapellaToken, "test-token")
	cfg.SetProp(config.PropCapellaAPIHost, f.srv.URL)
	cfg.SetProp(config.PropCapellaOrgID, "org-1")
	cfg.SetProp(config.PropCapellaProjectID, "p1")
	cfg.SetProp(config.PropCapellaUserID, "u1")
	cfg.SetProp(config.PropCapellaDatabase, "db1")
	cfg.Username, cfg.Password = "", "" // no database user unless a test sets one
	return cfg
}

func testBackend() *Backend {
	b := NewBackend()
	b.Clock = engine.NewFakeClock(time.Time{})
	b.WaitTimeout = time.Minute
	return b
}

func TestBackendCreateCluster(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		if _, err := testBackend().CreateCluster(context.Background(), nil); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("validation", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		delete(cfg.Properties, config.PropCapellaUserID)
		if _, err := testBackend().CreateCluster(context.Background(), cfg); err == nil {
			t.Fatal("want a validation error without a user")
		}
	})
	t.Run("new cluster with an allowed CIDR, no user: reachability skipped", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		cfg.SetProp(config.PropCapellaAllowCIDR, "1.2.3.4/32")
		s := newClusterSim(f)
		f.reply(http.MethodGet, fakeClustersPath+"/n1/allowedcidrs", 200, listPage())
		f.reply(http.MethodPost, fakeClustersPath+"/n1/allowedcidrs", 201, map[string]any{"id": "a1"})
		f.reply(http.MethodGet, fakeClustersPath+"/n1/allowedcidrs/a1", 200, map[string]any{"id": "a1", "cidr": "1.2.3.4/32"})
		b := testBackend()
		created, err := b.CreateCluster(context.Background(), cfg)
		if err != nil || !created {
			t.Fatalf("CreateCluster = %v, %v; want true, nil", created, err)
		}
		if n, _ := s.count(); n != 1 || b.Cluster == nil || b.Cluster.Data.ID != "n1" {
			t.Errorf("created = %d, cluster %+v", n, b.Cluster)
		}
		if f.countCalls(http.MethodPost, fakeClustersPath+"/n1/allowedcidrs") != 1 {
			t.Error("allowed CIDR not created")
		}
	})
	t.Run("existing cluster with a user but no CIDR is reused", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		cfg.Username, cfg.Password = "app", "pw"
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		f.reply(http.MethodGet, fakeCredsPath, 200, listPage(map[string]any{"id": "u5", "name": "app"}))
		created, err := testBackend().CreateCluster(context.Background(), cfg)
		if err != nil || created {
			t.Fatalf("CreateCluster = %v, %v; want false, nil", created, err)
		}
		if f.countCalls(http.MethodPost, fakeCredsPath) != 0 {
			t.Error("existing credential re-created")
		}
	})
	t.Run("credential failure", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		cfg.Username, cfg.Password = "app", "pw"
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		f.reply(http.MethodGet, fakeCredsPath, 403, errBody(403))
		if _, err := testBackend().CreateCluster(context.Background(), cfg); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("CIDR failure", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		cfg.SetProp(config.PropCapellaAllowCIDR, "1.2.3.4/32")
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		f.reply(http.MethodGet, fakeCIDRsPath, 403, errBody(403))
		if _, err := testBackend().CreateCluster(context.Background(), cfg); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("lookup failure", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		s := newClusterSim(f)
		s.listStatus = 401
		if _, err := testBackend().CreateCluster(context.Background(), cfg); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("create failure", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		s := newClusterSim(f)
		s.postStatus = 422
		_, err := testBackend().CreateCluster(context.Background(), cfg)
		if err == nil || !strings.Contains(err.Error(), "failed to create Capella cluster") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBackendBuckets(t *testing.T) {
	t.Run("not connected", func(t *testing.T) {
		b := testBackend()
		if err := b.CreateBucket("x", 0, 0); err == nil {
			t.Error("CreateBucket: want an error")
		}
		if err := b.DropBucket("x"); err == nil {
			t.Error("DropBucket: want an error")
		}
	})
	t.Run("create sends quota and replicas", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
		f.reply(http.MethodPost, fakeBucketsPath, 201, map[string]any{"id": "b1"})
		f.reply(http.MethodGet, fakeBucketsPath+"/b1", 200, map[string]any{"id": "b1", "name": "app"})
		b := testBackend()
		b.Cluster = f.cluster()
		if err := b.CreateBucket("app", 256, 0); err != nil {
			t.Fatal(err)
		}
		body := decodeBody(t, f.callsTo(http.MethodPost, fakeBucketsPath)[0])
		if body["memoryAllocationInMb"] != 256.0 || body["replicas"] != 0.0 {
			t.Errorf("body = %v", body)
		}
	})
	t.Run("create failure is wrapped", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 500, errBody(500))
		b := testBackend()
		b.Cluster = f.cluster()
		if err := b.CreateBucket("app", 0, -1); err == nil || !strings.Contains(err.Error(), "bucketCreate: Capella API error") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("drop", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{map[string]any{"id": "b1", "name": "app"}}})
		f.reply(http.MethodDelete, fakeBucketsPath+"/b1", 204, nil)
		b := testBackend()
		b.Cluster = f.cluster()
		if err := b.DropBucket("app"); err != nil {
			t.Fatal(err)
		}
		if f.countCalls(http.MethodDelete, fakeBucketsPath+"/b1") != 1 {
			t.Error("bucket not deleted")
		}
		if err := b.DropBucket("missing"); err != nil {
			t.Fatalf("missing bucket: %v", err)
		}
	})
	t.Run("drop lookup failure", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 403, errBody(403))
		b := testBackend()
		b.Cluster = f.cluster()
		if err := b.DropBucket("app"); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestBackendDestroyResolved(t *testing.T) {
	t.Run("nil config without a cluster is a no-op", func(t *testing.T) {
		if err := testBackend().DestroyCluster(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("already resolved cluster", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		b := testBackend()
		b.Cluster = f.cluster()
		b.StreamHost = "h"
		if err := b.DestroyCluster(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if b.Cluster != nil || b.StreamHost != "" || f.countCalls(http.MethodDelete, fakeClusterPath) != 1 {
			t.Errorf("cluster %+v, host %q", b.Cluster, b.StreamHost)
		}
	})
	t.Run("missing cluster is a no-op", func(t *testing.T) {
		f := newFakeAPI(t)
		cfg := backendConfig(f)
		newClusterSim(f)
		if err := testBackend().DestroyCluster(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCertTempfiles(t *testing.T) {
	t.Cleanup(cleanupTempCerts)
	tests := []struct {
		name, in, want string
	}{
		{"adds newline", "PEM", "PEM\n"},
		{"keeps newline", "PEM\n", "PEM\n"},
	}
	var paths []string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := writeCertTempfile(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, path)
			got, err := os.ReadFile(path)
			if err != nil || string(got) != tt.want {
				t.Errorf("file = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	if len(tempCertFiles) != len(paths) {
		t.Fatalf("tracked = %d, want %d", len(tempCertFiles), len(paths))
	}
	cleanupTempCerts()
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists (%v)", p, err)
		}
	}
	if endsWithNewline("") || !endsWithNewline("x\n") {
		t.Error("endsWithNewline")
	}
	if maxInt(1, 2) != 2 || maxInt(3, 2) != 3 {
		t.Error("maxInt")
	}
	if _, _, err := ConnectCluster("", "u", "p", "", 0, 0, 0); err == nil {
		t.Error("ConnectCluster with no connection string: want an error")
	}
}
