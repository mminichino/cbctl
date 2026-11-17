package rest

import (
	"net/http"
	"strings"
	"testing"
)

func TestReadQuotaOverride(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{name: "unset", value: "", want: 500},
		{name: "number", value: " 1024 ", want: 1024},
		{name: "not a number", value: "lots", want: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readQuotaOverride(map[string]string{"k": tt.value}, "k", 500)
			if got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestAnyInt64(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		want   int64
		wantOK bool
	}{
		{name: "float", in: float64(42), want: 42, wantOK: true},
		{name: "int", in: 7, want: 7, wantOK: true},
		{name: "int zero", in: 0, want: 0, wantOK: false},
		{name: "int64", in: int64(9), want: 9, wantOK: true},
		{name: "string", in: " 12 ", want: 12, wantOK: true},
		{name: "bad string", in: "x", want: 0, wantOK: false},
		{name: "bool", in: true, want: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := anyInt64(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("anyInt64(%v) = %d,%v want %d,%v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestChangedQuotas(t *testing.T) {
	if got := changedQuotas(map[string]int{"data": 1}, nil); got != nil {
		t.Fatalf("empty updates = %v", got)
	}
	got := changedQuotas(map[string]int{"data": 512, "index": 256}, map[string]int{"data": 512, "index": 300})
	if len(got) != 1 || got["index"] != 300 {
		t.Fatalf("changed = %v", got)
	}
}

func TestPoolsHelpers(t *testing.T) {
	payload := map[string]any{
		"memoryQuota":      float64(1000),
		"indexMemoryQuota": "300",
		"ftsMemoryQuota":   true,
		"nodes": []any{
			"not a map",
			map[string]any{"services": []any{"kv", 5, "n1ql"}},
			map[string]any{"services": []any{"cbas"}},
		},
	}
	quotas := serviceQuotasFromPools(payload)
	if len(quotas) != 2 || quotas["data"] != 1000 || quotas["index"] != 300 {
		t.Fatalf("quotas = %v", quotas)
	}
	services := servicesFromPools(payload)
	if len(services) != 3 || !services["data"] || !services["query"] || !services["analytics"] {
		t.Fatalf("services = %v", services)
	}
}

func TestClusterInitFields(t *testing.T) {
	tests := []struct {
		name string
		req  ClusterInitRequest
		want map[string]string
		none []string
	}{
		{
			name: "full provisioner request",
			req: ClusterInitRequest{
				Hostname: "localhost", Username: "Administrator", Password: "password",
				Services: []string{"data", "query", "search"}, Quotas: map[string]int{"data": 1024, "fts": 512},
				ClusterName: " prod ", DataPath: "/data/cb", AllowedHosts: "*",
			},
			want: map[string]string{
				"hostname": "127.0.0.1", "username": "Administrator", "password": "password", "port": "SAME",
				"services": "kv,n1ql,fts", "allowedHosts": "*", "indexerStorageMode": "plasma",
				"clusterName": "prod", "dataPath": "/data/cb", "indexPath": "/data/cb",
				"analyticsPath": "/data/cb", "eventingPath": "/data/cb",
				"memoryQuota": "1024", "ftsMemoryQuota": "512",
			},
			none: []string{"indexMemoryQuota"},
		},
		{
			name: "allowed hosts defaults to hostname",
			req:  ClusterInitRequest{Hostname: "cb1", Username: "u", Password: "p", Services: []string{"data"}},
			want: map[string]string{"hostname": "cb1.local", "allowedHosts": "cb1.local", "services": "kv"},
			none: []string{"clusterName", "dataPath", "memoryQuota"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			if err := NewClient().ClusterInit(f.endpoint(), tt.req); err != nil {
				t.Fatal(err)
			}
			reqs := f.requests(http.MethodPost, "/clusterInit")
			if len(reqs) != 1 {
				t.Fatalf("clusterInit requests = %d", len(reqs))
			}
			if reqs[0].HasAuth {
				t.Fatal("clusterInit must be unauthenticated")
			}
			for k, v := range tt.want {
				if got := reqs[0].Form.Get(k); got != v {
					t.Errorf("%s = %q want %q", k, got, v)
				}
			}
			for _, k := range tt.none {
				if _, ok := reqs[0].Form[k]; ok {
					t.Errorf("unexpected field %s", k)
				}
			}
		})
	}
}

func TestInitializeSingleNode(t *testing.T) {
	f := newFakeCB(t)
	err := NewClient().InitializeSingleNode(f.endpoint(), "Administrator", "password", []string{"data", "index"}, map[string]int{"data": 2048, "index": 512}, "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	r := f.requests(http.MethodPost, "/clusterInit")
	if len(r) != 1 || r[0].Form.Get("hostname") != "10.0.0.5" || r[0].Form.Get("allowedHosts") != "10.0.0.5" ||
		r[0].Form.Get("indexMemoryQuota") != "512" || r[0].Form.Get("services") != "kv,index" {
		t.Fatalf("requests = %+v", r)
	}
	f.setFail("POST /clusterInit", http.StatusBadRequest)
	if err := NewClient().InitializeSingleNode(f.endpoint(), "u", "p", nil, nil, "h"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeMemoryTotal(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(f *fakeCB)
		username    string
		want        int64
		wantErr     string
		wantAuthGet bool
	}{
		{name: "uninitialized node without auth", username: "Administrator", want: 8 << 30},
		{
			name: "initialized node retries with auth", username: "Administrator", want: 8 << 30, wantAuthGet: true,
			setup: func(f *fakeCB) { f.seed(fakeNode{Host: "10.0.0.1"}) },
		},
		{
			name: "initialized node without username", username: " ", wantErr: "HTTP 401",
			setup: func(f *fakeCB) { f.seed(fakeNode{Host: "10.0.0.1"}) },
		},
		{
			name: "missing memoryTotal", username: "Administrator", wantErr: "did not report memoryTotal",
			setup: func(f *fakeCB) {
				f.mu.Lock()
				f.memoryTotal = 0
				f.mu.Unlock()
			},
		},
		{
			name: "server error", username: "Administrator", wantErr: "HTTP 500",
			setup: func(f *fakeCB) { f.setFail("GET /nodes/self", http.StatusInternalServerError) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			got, err := NewClient().NodeMemoryTotal(f.endpoint(), tt.username, "password")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("memory = %d", got)
			}
			reqs := f.requests(http.MethodGet, "/nodes/self")
			last := reqs[len(reqs)-1]
			if last.HasAuth != tt.wantAuthGet {
				t.Fatalf("last /nodes/self auth = %v", last.HasAuth)
			}
		})
	}
}

func TestNodeMemoryTotalAuthRetryFails(t *testing.T) {
	ep := handlerEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := NewClient().NodeMemoryTotal(ep, "Administrator", "wrong"); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}

func TestReconcileServiceQuotasRequests(t *testing.T) {
	tests := []struct {
		name       string
		services   []string
		ramGiB     int
		options    map[string]string
		quotas     map[string]int
		setup      func(f *fakeCB)
		wantPosted map[string]string // nil means no POST expected
		wantErr    string
	}{
		{
			name: "sizes node services and pins absent ones", services: []string{"data", "index"}, ramGiB: 10,
			// 10 GiB * 0.8 = 8192 MiB / 2 services = 4096 each.
			quotas:     map[string]int{"memoryQuota": 256, "indexMemoryQuota": 4096, "ftsMemoryQuota": 512, "cbasMemoryQuota": 1024, "eventingMemoryQuota": 256},
			wantPosted: map[string]string{"memoryQuota": "4096", "ftsMemoryQuota": "256"},
		},
		{
			name: "option override", services: []string{"data"}, ramGiB: 4,
			options:    map[string]string{"couchbase.server.data.quota": "1000"},
			quotas:     map[string]int{"memoryQuota": 256, "indexMemoryQuota": 256, "ftsMemoryQuota": 256, "cbasMemoryQuota": 1024, "eventingMemoryQuota": 256},
			wantPosted: map[string]string{"memoryQuota": "1000"},
		},
		{
			name: "already reconciled", services: []string{"data"}, ramGiB: 4,
			// 4 GiB * 0.8 = 3276 MiB.
			quotas: map[string]int{"memoryQuota": 3276, "indexMemoryQuota": 256, "ftsMemoryQuota": 256, "cbasMemoryQuota": 1024, "eventingMemoryQuota": 256},
		},
		{
			name: "memory read failure", services: []string{"data"}, wantErr: "read node memory",
			setup: func(f *fakeCB) { f.setFail("GET /nodes/self", http.StatusInternalServerError) },
		},
		{
			name: "pools read failure", services: []string{"data"}, ramGiB: 4, wantErr: "HTTP 503",
			setup: func(f *fakeCB) { f.setFail("GET /pools/default", http.StatusServiceUnavailable) },
		},
		{
			name: "set failure", services: []string{"data"}, ramGiB: 4, wantErr: "set service quotas",
			setup: func(f *fakeCB) { f.setFail("POST /pools/default", http.StatusBadRequest) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			f.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv", "index"}})
			f.mu.Lock()
			for k, v := range tt.quotas {
				f.quotas[k] = v
			}
			f.mu.Unlock()
			if tt.setup != nil {
				tt.setup(f)
			}
			err := NewClient().ReconcileServiceQuotas(f.endpoint(), "Administrator", "password", tt.services, tt.options, tt.ramGiB)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			posts := f.requests(http.MethodPost, "/pools/default")
			if tt.wantPosted == nil {
				if len(posts) != 0 {
					t.Fatalf("unexpected posts %+v", posts)
				}
				return
			}
			if len(posts) != 1 {
				t.Fatalf("posts = %d", len(posts))
			}
			if len(posts[0].Form) != len(tt.wantPosted) {
				t.Fatalf("form = %v want %v", posts[0].Form, tt.wantPosted)
			}
			for k, v := range tt.wantPosted {
				if got := posts[0].Form.Get(k); got != v {
					t.Errorf("%s = %q want %q", k, got, v)
				}
			}
			if posts[0].User != "Administrator" || posts[0].Pass != "password" {
				t.Fatalf("auth = %q/%q", posts[0].User, posts[0].Pass)
			}
		})
	}
}

func TestRaiseQuotasForNewServices(t *testing.T) {
	t.Run("raises quota for a service new to the cluster", func(t *testing.T) {
		rally := newFakeCB(t)
		rally.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}})
		node := newFakeCB(t)
		node.mu.Lock()
		node.memoryTotal = 10 << 30
		node.mu.Unlock()
		err := NewClient().RaiseQuotasForNewServices(rally.endpoint(), node.endpoint(), "Administrator", "password", []string{"data", "index"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if node.count(http.MethodGet, "/nodes/self") != 1 || rally.count(http.MethodGet, "/nodes/self") != 0 {
			t.Fatal("memory must be read from the joining node")
		}
		posts := rally.requests(http.MethodPost, "/pools/default")
		if len(posts) != 1 || len(posts[0].Form) != 1 || posts[0].Form.Get("indexMemoryQuota") != "4096" {
			t.Fatalf("posts = %+v", posts)
		}
	})
	t.Run("no new services", func(t *testing.T) {
		rally := newFakeCB(t)
		rally.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv", "index"}})
		if err := NewClient().RaiseQuotasForNewServices(rally.endpoint(), closedEndpoint(t), "u", "p", []string{"data"}, 4); err != nil {
			t.Fatal(err)
		}
		if rally.count(http.MethodPost, "/pools/default") != 0 {
			t.Fatal("unexpected quota post")
		}
	})
	t.Run("memory failure", func(t *testing.T) {
		err := NewClient().RaiseQuotasForNewServices(closedEndpoint(t), closedEndpoint(t), "u", "p", []string{"data"}, 0)
		if err == nil || !strings.Contains(err.Error(), "read node memory") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("pools failure", func(t *testing.T) {
		if err := NewClient().RaiseQuotasForNewServices(closedEndpoint(t), closedEndpoint(t), "u", "p", []string{"data"}, 4); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("set failure", func(t *testing.T) {
		rally := newFakeCB(t)
		rally.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}})
		rally.setFail("POST /pools/default", http.StatusBadRequest)
		err := NewClient().RaiseQuotasForNewServices(rally.endpoint(), closedEndpoint(t), "u", "p", []string{"index"}, 4)
		if err == nil || !strings.Contains(err.Error(), "set service quotas") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSetServiceQuotasEmpty(t *testing.T) {
	// Unknown services produce no fields, so nothing is posted (closed endpoint would fail).
	if err := NewClient().SetServiceQuotas(closedEndpoint(t), "u", "p", map[string]int{"query": 512}); err != nil {
		t.Fatal(err)
	}
}

func TestSetNodePaths(t *testing.T) {
	tests := []struct {
		name     string
		username string
		path     string
		wantReq  bool
		wantAuth bool
	}{
		{name: "blank path is no-op", username: "u", path: "  "},
		{name: "unauthenticated", username: "", path: " /data ", wantReq: true},
		{name: "authenticated", username: "Administrator", path: "/data", wantReq: true, wantAuth: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			if err := NewClient().SetNodePaths(f.endpoint(), tt.username, "password", tt.path); err != nil {
				t.Fatal(err)
			}
			reqs := f.requests(http.MethodPost, "/nodes/self/controller/settings")
			if !tt.wantReq {
				if len(reqs) != 0 {
					t.Fatalf("unexpected requests %+v", reqs)
				}
				return
			}
			if len(reqs) != 1 {
				t.Fatalf("requests = %d", len(reqs))
			}
			for _, k := range []string{"path", "index_path", "cbas_path", "eventing_path"} {
				if reqs[0].Form.Get(k) != "/data" {
					t.Errorf("%s = %q", k, reqs[0].Form.Get(k))
				}
			}
			if reqs[0].HasAuth != tt.wantAuth {
				t.Fatalf("auth = %v", reqs[0].HasAuth)
			}
		})
	}
}

func TestRebalanceKnownNodes(t *testing.T) {
	f := newFakeCB(t)
	err := NewClient().Rebalance(f.endpoint(), "Administrator", "password", []string{"10.0.0.1:8091", " ", "ns_1@10.0.0.2", " cb3.local "})
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.requests(http.MethodPost, "/controller/rebalance")
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if got := reqs[0].Form.Get("knownNodes"); got != "ns_1@10.0.0.1,ns_1@10.0.0.2,ns_1@cb3.local" {
		t.Fatalf("knownNodes = %q", got)
	}
	f.setFail("POST /controller/rebalance", http.StatusBadRequest)
	if err := NewClient().Rebalance(f.endpoint(), "u", "p", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestSetupAlternateAddress(t *testing.T) {
	f := newFakeCB(t)
	err := NewClient().SetupAlternateAddress(f.endpoint(), "Administrator", "password", "ext.example.com", map[string]int{"kv": 9000, "n1ql": 9050})
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.requests(http.MethodPut, "/node/controller/setupAlternateAddresses/external")
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	form := reqs[0].Form
	if form.Get("hostname") != "ext.example.com" || form.Get("kv") != "9000" || form.Get("n1ql") != "9050" || !reqs[0].HasAuth {
		t.Fatalf("request = %+v", reqs[0])
	}
}
