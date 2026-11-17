package capella

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/engine"
)

func TestClusterConfigOptions(t *testing.T) {
	cfg := NewClusterConfig().
		WithDescription("d").
		WithCloudType(CloudGCP).
		WithCloudRegion("").
		WithCIDR("10.1.0.0/23").
		WithVersion("7.6").
		WithSupportPlan(SupportEnterprise).
		WithTimeZone(TimeZoneEurope)
	req, err := cfg.Create("c")
	if err != nil {
		t.Fatal(err)
	}
	if req.Description != "d" || req.CloudProvider.Type != "gcp" || req.CloudProvider.Region != "us-east4" ||
		req.CloudProvider.CIDR != "10.1.0.0/23" || req.CouchbaseServer == nil || req.CouchbaseServer.Version != "7.6" ||
		req.Support.Plan != "enterprise" || req.Support.Timezone != "GMT" {
		t.Errorf("request = %+v", req)
	}
	if len(req.ServiceGroups) != 1 || req.ServiceGroups[0].NumOfNodes != 3 || req.ServiceGroups[0].Node.Disk.Type != "pd-ssd" {
		t.Errorf("default service group = %+v", req.ServiceGroups)
	}

	tests := []struct {
		cloud      CloudType
		region     string
		wantRegion string
		storage    int
		wantErr    bool
	}{
		{CloudAzure, "", "eastus", 256, false},
		{CloudAWS, "eu-west-1", "eu-west-1", 256, false},
		{CloudAWS, "", "us-east-2", 20000, true},
		{CloudAzure, "", "eastus", 9000, true},
		{"oci", "", "us-east-2", 100, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.cloud)+"/"+tt.wantRegion, func(t *testing.T) {
			c := NewClusterConfig().WithCloudType(tt.cloud).WithCloudRegion(tt.region).
				AddServiceGroup(NewServiceGroupConfig().WithStorage(tt.storage))
			req, err := c.Create("x")
			if tt.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if req.CloudProvider.Region != tt.wantRegion {
				t.Errorf("region = %q, want %q", req.CloudProvider.Region, tt.wantRegion)
			}
		})
	}
}

func TestDiskConfigs(t *testing.T) {
	if d := DiskConfigGCP(50); d.Type != "pd-ssd" || *d.Storage != 50 {
		t.Errorf("gcp = %+v", d)
	}
	tests := []struct {
		storage int
		want    string
	}{{64, "P6"}, {65, "P10"}, {512, "P20"}, {8192, "P60"}}
	for _, tt := range tests {
		d, err := DiskConfigAzure(tt.storage)
		if err != nil || d.Type != tt.want || d.AutoExpansion == nil || !*d.AutoExpansion {
			t.Errorf("azure(%d) = %+v, %v; want %s", tt.storage, d, err, tt.want)
		}
	}
	if _, err := DiskConfigAWS(16385); err == nil {
		t.Error("aws: want an error above 16384")
	}
	if d, err := DiskConfigForCloud(CloudGCP, 10); err != nil || d.Type != "pd-ssd" {
		t.Errorf("for gcp = %+v, %v", d, err)
	}
	if d, err := DiskConfigForCloud(CloudAzure, 10); err != nil || d.Type != "P6" {
		t.Errorf("for azure = %+v, %v", d, err)
	}
}

func TestClusterListAndFind(t *testing.T) {
	t.Run("find", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"}, &simCluster{ID: "c2", Name: "db2", State: "healthy"})
		c := NewCluster(f.project())
		got, err := c.FindByName("db2")
		if err != nil || got == nil || got.ID != "c2" {
			t.Fatalf("FindByName = %+v, %v", got, err)
		}
		if got, err := c.FindByName("nope"); err != nil || got != nil {
			t.Fatalf("FindByName(missing) = %+v, %v", got, err)
		}
	})
	t.Run("404 is empty", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.listStatus = 404
		list, err := NewCluster(f.project()).List()
		if err != nil || len(list) != 0 {
			t.Fatalf("List = %v, %v", list, err)
		}
	})
	t.Run("error", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.listStatus = 403
		_, err := NewCluster(f.project()).FindByName("db1")
		if err == nil || !strings.Contains(err.Error(), "Cluster List Error") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestClusterGetByID(t *testing.T) {
	t.Run("own data is cached", func(t *testing.T) {
		f := newFakeAPI(t)
		c := f.cluster()
		if got, err := c.GetByID("c1"); err != nil || got != c.Data {
			t.Fatalf("GetByID = %+v, %v", got, err)
		}
	})
	t.Run("project cache", func(t *testing.T) {
		f := newFakeAPI(t)
		cached := f.cluster()
		cached.Project.RegisterCluster(cached)
		other := NewCluster(cached.Project)
		if got, err := other.GetByID("c1"); err != nil || got != cached.Data {
			t.Fatalf("GetByID = %+v, %v", got, err)
		}
		if cached.Project.FindClusterByID("c1") != cached {
			t.Error("FindClusterByID did not return the registered cluster")
		}
	})
	tests := []struct {
		name    string
		status  int
		body    any
		wantErr string
	}{
		{"fetched", 200, map[string]any{"id": "c9", "name": "db9", "connectionString": "cb.x"}, ""},
		{"missing", 404, errBody(404), "Cluster ID not found"},
		{"refused", 400, errBody(400), "Cluster Get Error (HTTP 400)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.reply(http.MethodGet, fakeClustersPath+"/c9", tt.status, tt.body)
			got, err := NewCluster(f.project()).GetByID("c9")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.Name != "db9" || got.ConnectionString != "cb.x" {
				t.Fatalf("GetByID = %+v, %v", got, err)
			}
		})
	}
}

func TestClusterResolve(t *testing.T) {
	t.Run("by id", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		p := f.project()
		p.Org.Client.DatabaseID = "c1"
		c := NewCluster(p)
		if err := c.Resolve(); err != nil {
			t.Fatal(err)
		}
		if c.Data.Name != "db1" || c.Buckets == nil || c.Credentials == nil || c.AllowedCIDR == nil || c.Certificate == nil {
			t.Errorf("resolved cluster = %+v", c)
		}
	})
	t.Run("by name", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		p := f.project()
		p.Org.Client.DatabaseName = "db1"
		c := NewCluster(p)
		if err := c.Resolve(); err != nil || c.Data.ID != "c1" {
			t.Fatalf("Resolve = %v, data %+v", err, c.Data)
		}
	})
	t.Run("nothing configured", func(t *testing.T) {
		f := newFakeAPI(t)
		err := NewCluster(f.project()).Resolve()
		if !IsNotFound(err) {
			t.Fatalf("err = %v, want not found", err)
		}
	})
	t.Run("missing name", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f)
		p := f.project()
		p.Org.Client.DatabaseName = "db1"
		c := NewCluster(p)
		if err := c.Resolve(); !IsNotFound(err) || c.Buckets != nil {
			t.Fatalf("Resolve = %v, buckets %v", err, c.Buckets)
		}
	})
}

func TestClusterCreate(t *testing.T) {
	single := func() *ClusterConfig { return NewClusterConfig().SingleNode(nil) }

	t.Run("existing healthy cluster is reused", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy", Conn: "cb.c1"})
		c := NewCluster(f.project())
		if err := c.Create("db1", single()); err != nil {
			t.Fatal(err)
		}
		if created, _ := s.count(); created != 0 {
			t.Errorf("POSTs = %d, want 0", created)
		}
		if c.Data.ID != "c1" || c.Certificate.CertificatePEM != "PEM-c1" || c.Project.FindClusterByName("db1") != c {
			t.Errorf("cluster = %+v, cert %q", c.Data, c.Certificate.CertificatePEM)
		}
	})
	t.Run("existing failed cluster is an error", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "deploymentFailed"})
		err := NewCluster(f.project()).Create("db1", single())
		var wf *engine.WaitFailed
		if !errors.As(err, &wf) || !strings.Contains(err.Error(), "waiting for existing cluster db1") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("existing lookup fails after the wait", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		s.getHook = func(_ string, n int) (int, any, bool) {
			if n >= 2 {
				return 404, errBody(404), true
			}
			return 0, nil, false
		}
		err := NewCluster(f.project()).Create("db1", single())
		if err == nil || !strings.Contains(err.Error(), "cluster lookup failed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("new cluster", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		c := NewCluster(f.project())
		if err := c.Create("fresh", single()); err != nil {
			t.Fatal(err)
		}
		if created, deleted := s.count(); created != 1 || deleted != 0 {
			t.Errorf("created/deleted = %d/%d, want 1/0", created, deleted)
		}
		body := decodeBody(t, f.callsTo(http.MethodPost, fakeClustersPath)[0])
		if body["name"] != "fresh" {
			t.Errorf("POST name = %v", body["name"])
		}
		if c.Data.ID != "n1" || c.Data.Name != "fresh" || c.Certificate.CertificatePEM != "PEM-n1" {
			t.Errorf("cluster = %+v", c.Data)
		}
		got, err := c.ConnectString()
		if err != nil || got != "couchbases://cb.n1.cloud.couchbase.com" {
			t.Errorf("ConnectString = %q, %v", got, err)
		}
	})
	t.Run("failed deployment is removed and retried", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.createStates = []string{"deploymentFailed", "healthy"}
		c := NewCluster(f.project())
		if err := c.Create("fresh", single()); err != nil {
			t.Fatal(err)
		}
		if created, deleted := s.count(); created != 2 || deleted != 1 {
			t.Errorf("created/deleted = %d/%d, want 2/1", created, deleted)
		}
		if f.countCalls(http.MethodDelete, fakeClustersPath+"/n1") != 1 || c.Data.ID != "n2" {
			t.Errorf("deleted n1 = %d, data %+v", f.countCalls(http.MethodDelete, fakeClustersPath+"/n1"), c.Data)
		}
	})
	t.Run("gives up after three failed deployments", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.createStates = []string{"deploymentFailed", "deploymentFailed", "deploymentFailed", "healthy"}
		err := NewCluster(f.project()).Create("fresh", single())
		var wf *engine.WaitFailed
		if !errors.As(err, &wf) || !strings.Contains(err.Error(), "after 3 attempts") {
			t.Fatalf("err = %v", err)
		}
		if created, deleted := s.count(); created != 3 || deleted != 3 {
			t.Errorf("created/deleted = %d/%d, want 3/3", created, deleted)
		}
	})
	t.Run("failed removal stops the retry", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.createStates = []string{"deploymentFailed"}
		s.deleteStatus = 409
		err := NewCluster(f.project()).Create("fresh", single())
		if err == nil || !strings.Contains(err.Error(), "removing failed cluster n1") {
			t.Fatalf("err = %v", err)
		}
		if created, _ := s.count(); created != 1 {
			t.Errorf("created = %d, want 1", created)
		}
	})
	t.Run("wait timeout is not retried", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.createStates = []string{"deploying"}
		err := NewCluster(f.project()).Create("fresh", single())
		var wt *engine.WaitTimeout
		if !errors.As(err, &wt) {
			t.Fatalf("err = %v, want *engine.WaitTimeout", err)
		}
		if created, deleted := s.count(); created != 1 || deleted != 0 {
			t.Errorf("created/deleted = %d/%d, want 1/0", created, deleted)
		}
	})
	t.Run("post refused", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.postStatus = 422
		err := NewCluster(f.project()).Create("fresh", single())
		if err == nil || !strings.Contains(err.Error(), "Cluster Create Error (HTTP 422)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("read back fails", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.getHook = func(_ string, n int) (int, any, bool) {
			if n >= 2 {
				return 400, errBody(400), true
			}
			return 0, nil, false
		}
		err := NewCluster(f.project()).Create("fresh", single())
		if err == nil || !strings.Contains(err.Error(), "cluster creation failed: Cluster Get Error") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("list fails", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		s.listStatus = 401
		if err := NewCluster(f.project()).Create("fresh", single()); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("invalid config", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f)
		err := NewCluster(f.project()).Create("fresh", NewClusterConfig().WithCloudType("oci"))
		if err == nil || !strings.Contains(err.Error(), "unsupported cloud type") {
			t.Fatalf("err = %v", err)
		}
		if created, _ := s.count(); created != 0 {
			t.Errorf("POSTs = %d, want 0", created)
		}
	})
}

func TestClusterDelete(t *testing.T) {
	t.Run("nothing resolved", func(t *testing.T) {
		f := newFakeAPI(t)
		if err := NewCluster(f.project()).Delete(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("deletes and waits", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		c := f.cluster()
		if err := c.Delete(); err != nil {
			t.Fatal(err)
		}
		if c.Data != nil || f.countCalls(http.MethodDelete, fakeClusterPath) != 1 {
			t.Errorf("data %+v, DELETEs %d", c.Data, f.countCalls(http.MethodDelete, fakeClusterPath))
		}
	})
	t.Run("already gone", func(t *testing.T) {
		f := newFakeAPI(t)
		newClusterSim(f)
		if err := f.cluster().Delete(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("refused", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		s.deleteStatus = 409
		err := f.cluster().Delete()
		if err == nil || !strings.Contains(err.Error(), "Cluster Delete Error (HTTP 409)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("destroy failed", func(t *testing.T) {
		f := newFakeAPI(t)
		s := newClusterSim(f, &simCluster{ID: "c1", Name: "db1", State: "healthy"})
		s.getHook = func(string, int) (int, any, bool) {
			return 200, map[string]any{"id": "c1", "name": "db1", "currentState": "destroyFailed"}, true
		}
		c := f.cluster()
		err := c.Delete()
		var wf *engine.WaitFailed
		if !errors.As(err, &wf) || !strings.Contains(err.Error(), "cluster deletion failed") || c.Data == nil {
			t.Fatalf("err = %v, data %+v", err, c.Data)
		}
	})
}

func TestClusterConnectString(t *testing.T) {
	f := newFakeAPI(t)
	if got, err := NewCluster(f.project()).ConnectString(); got != "" || err != nil {
		t.Errorf("unresolved = %q, %v", got, err)
	}
	c := f.cluster()
	if got, err := c.ConnectString(); got != "" || err != nil {
		t.Errorf("no connection string = %q, %v", got, err)
	}
	c.Data.ConnectionString = "cb.example.com"
	if got, err := c.ConnectString(); got != "couchbases://cb.example.com" || err != nil {
		t.Errorf("ConnectString = %q, %v", got, err)
	}
}
