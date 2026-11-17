package rest

import (
	"net/http"
	"strings"
	"testing"
)

func provisionOpts(ip string) ProvisionOptions {
	return ProvisionOptions{IPAddress: ip, Username: "Administrator", Password: "password"}
}

func TestValidateProvisionNode(t *testing.T) {
	tests := []struct {
		name string
		opts ProvisionOptions
		want string
	}{
		{name: "ip", opts: ProvisionOptions{Username: "u", Password: "p"}, want: "--ip-address is required"},
		{name: "username", opts: ProvisionOptions{IPAddress: "h", Password: "p"}, want: "username is required"},
		{name: "password", opts: ProvisionOptions{IPAddress: "h", Username: "u"}, want: "password is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClient().BootstrapPrimary(tt.opts); err == nil || err.Error() != tt.want {
				t.Fatalf("bootstrap err = %v", err)
			}
			if _, err := NewClient().JoinNode(tt.opts); err == nil || err.Error() != tt.want {
				t.Fatalf("join err = %v", err)
			}
		})
	}
}

func TestBootstrapPrimaryFresh(t *testing.T) {
	f := newFakeCB(t)
	opts := provisionOpts(f.addr())
	// No query service: WaitForQueryReady targets the fixed query port 8093.
	opts.Services = []string{"data", "index"}
	opts.DataPath = "/data/cb"
	opts.ClusterName = "prod"
	opts.ExternalIPAddress = "203.0.113.10:8091"
	opts.ServerGroup = "us-east-1a"

	created, err := NewClient().BootstrapPrimary(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	paths := f.requests(http.MethodPost, "/nodes/self/controller/settings")
	if len(paths) != 1 || paths[0].HasAuth || paths[0].Form.Get("path") != "/data/cb" {
		t.Fatalf("paths = %+v", paths)
	}
	inits := f.requests(http.MethodPost, "/clusterInit")
	if len(inits) != 1 {
		t.Fatalf("clusterInit = %d", len(inits))
	}
	form := inits[0].Form
	if form.Get("hostname") != "127.0.0.1" || form.Get("allowedHosts") != "*" || form.Get("clusterName") != "prod" ||
		form.Get("services") != "kv,index" || form.Get("dataPath") != "/data/cb" {
		t.Fatalf("clusterInit form = %v", form)
	}
	// 8 GiB * 0.8 / 2 services.
	if form.Get("memoryQuota") != "3276" || form.Get("indexMemoryQuota") != "3276" || form.Get("ftsMemoryQuota") != "256" {
		t.Fatalf("quotas = %v", form)
	}
	alt := f.requests(http.MethodPut, "/node/controller/setupAlternateAddresses/external")
	if len(alt) != 1 || alt[0].Form.Get("hostname") != "203.0.113.10" {
		t.Fatalf("alternate = %+v", alt)
	}
	if got := f.groupOf("ns_1@127.0.0.1"); got != "us-east-1a" {
		t.Fatalf("server group = %q", got)
	}
	// Quotas already match after init, so reconcile posts nothing.
	if n := f.count(http.MethodPost, "/pools/default"); n != 0 {
		t.Fatalf("quota posts = %d", n)
	}
}

func TestBootstrapPrimaryAlreadyInitialized(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "127.0.0.1", Services: []string{"kv"}})
	f.mu.Lock()
	f.altHost = "ext.example.com"
	f.mu.Unlock()
	opts := provisionOpts(f.addr())
	opts.Services = []string{"data"}
	opts.RAMGiB = 4
	opts.ExternalIPAddress = "EXT.example.com"
	opts.ServerGroup = "Group 1"

	created, err := NewClient().BootstrapPrimary(opts)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected created=false")
	}
	if f.count(http.MethodPost, "/clusterInit") != 0 {
		t.Fatal("must not re-init")
	}
	if f.count(http.MethodPut, "/node/controller/setupAlternateAddresses/external") != 0 {
		t.Fatal("matching alternate address must not be re-applied")
	}
	if f.count(http.MethodPost, "/pools/default") != 1 {
		t.Fatal("expected quota reconciliation")
	}
}

func TestBootstrapPrimaryErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(f *fakeCB, o *ProvisionOptions)
		wantErr string
	}{
		{
			name: "set node paths",
			setup: func(f *fakeCB, o *ProvisionOptions) {
				f.setFail("POST /nodes/self/controller/settings", 400)
				o.DataPath = "/d"
			},
			wantErr: "set node paths",
		},
		{
			name:    "memory",
			setup:   func(f *fakeCB, _ *ProvisionOptions) { f.setFail("GET /nodes/self", 500) },
			wantErr: "read node memory",
		},
		{
			name:    "cluster init",
			setup:   func(f *fakeCB, _ *ProvisionOptions) { f.setFail("POST /clusterInit", 400) },
			wantErr: "cluster init",
		},
		{
			name: "alternate address on existing node",
			setup: func(f *fakeCB, o *ProvisionOptions) {
				f.seed(fakeNode{Host: "127.0.0.1", Services: []string{"kv"}})
				f.setFail("PUT /node/controller/setupAlternateAddresses/external", 400)
				o.RAMGiB = 4
				o.ExternalIPAddress = "ext.example.com"
			},
			wantErr: "set alternate address",
		},
		{
			name: "reconcile on existing node",
			setup: func(f *fakeCB, o *ProvisionOptions) {
				f.seed(fakeNode{Host: "127.0.0.1", Services: []string{"kv"}})
				f.setFail("POST /pools/default", 400)
				o.RAMGiB = 4
			},
			wantErr: "set service quotas",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			opts := provisionOpts(f.addr())
			opts.Services = []string{"data"}
			tt.setup(f, &opts)
			_, err := NewClient().BootstrapPrimary(opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestJoinNode(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(local, rally *fakeCB, o *ProvisionOptions)
		wantJoined bool
		wantErr    string
		check      func(t *testing.T, local, rally *fakeCB)
	}{
		{
			name: "fresh join",
			setup: func(_, _ *fakeCB, o *ProvisionOptions) {
				o.Services = []string{"data", "index"}
				o.DataPath = "/data"
				o.ExternalIPAddress = "203.0.113.11"
				o.ServerGroup = "us-east-1b"
			},
			wantJoined: true,
			check: func(t *testing.T, local, rally *fakeCB) {
				adds := rally.requests(http.MethodPost, "/controller/addNode")
				if len(adds) != 1 || adds[0].Form.Get("hostname") != "127.0.0.1" || adds[0].Form.Get("services") != "kv,index" ||
					adds[0].Form.Get("user") != "Administrator" || adds[0].Form.Get("password") != "password" {
					t.Fatalf("addNode = %+v", adds)
				}
				q := rally.requests(http.MethodPost, "/pools/default")
				if len(q) != 1 || q[0].Form.Get("indexMemoryQuota") != "3276" {
					t.Fatalf("quota raise = %+v", q)
				}
				if local.count(http.MethodPost, "/nodes/self/controller/settings") != 1 {
					t.Fatal("expected data path on local node")
				}
				alt := local.requests(http.MethodPut, "/node/controller/setupAlternateAddresses/external")
				if len(alt) != 1 || alt[0].Form.Get("hostname") != "203.0.113.11" {
					t.Fatalf("alternate = %+v", alt)
				}
				if got := rally.groupOf("ns_1@127.0.0.1"); got != "us-east-1b" {
					t.Fatalf("group = %q", got)
				}
				if rally.count(http.MethodPost, "/controller/rebalance") != 0 {
					t.Fatal("join must not rebalance")
				}
			},
		},
		{
			name: "arbiter join skips quotas",
			setup: func(_, _ *fakeCB, o *ProvisionOptions) {
				o.Arbiter = true
			},
			wantJoined: true,
			check: func(t *testing.T, _, rally *fakeCB) {
				adds := rally.requests(http.MethodPost, "/controller/addNode")
				if len(adds) != 1 || adds[0].Form.Get("services") != "" {
					t.Fatalf("addNode = %+v", adds)
				}
				if rally.count(http.MethodPost, "/pools/default") != 0 {
					t.Fatal("arbiter must not raise quotas")
				}
			},
		},
		{
			name: "data path retried with auth",
			setup: func(local, _ *fakeCB, o *ProvisionOptions) {
				local.mu.Lock()
				local.pathsNeedAuth = true
				local.mu.Unlock()
				o.DataPath = "/data"
				o.RAMGiB = 4
			},
			wantJoined: true,
			check: func(t *testing.T, local, _ *fakeCB) {
				reqs := local.requests(http.MethodPost, "/nodes/self/controller/settings")
				if len(reqs) != 2 || reqs[0].HasAuth || !reqs[1].HasAuth {
					t.Fatalf("paths = %+v", reqs)
				}
			},
		},
		{
			name: "data path fails",
			setup: func(local, _ *fakeCB, o *ProvisionOptions) {
				local.setFail("POST /nodes/self/controller/settings", 500)
				o.DataPath = "/data"
			},
			wantErr: "set node paths",
		},
		{
			name: "already a member",
			setup: func(_, rally *fakeCB, _ *ProvisionOptions) {
				rally.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}}, fakeNode{Host: "127.0.0.1", Services: []string{"kv"}})
			},
			check: func(t *testing.T, _, rally *fakeCB) {
				if rally.count(http.MethodPost, "/controller/addNode") != 0 {
					t.Fatal("member must not be re-added")
				}
			},
		},
		{
			name: "addNode error but node joined",
			setup: func(_, rally *fakeCB, o *ProvisionOptions) {
				rally.mu.Lock()
				rally.addNodeStatus = http.StatusInternalServerError
				rally.mu.Unlock()
				o.RAMGiB = 4
			},
		},
		{
			name: "addNode error",
			setup: func(_, rally *fakeCB, o *ProvisionOptions) {
				rally.setFail("POST /controller/addNode", http.StatusBadRequest)
				o.RAMGiB = 4
			},
			wantErr: "add node: HTTP 400",
		},
		{
			name: "quota raise error",
			setup: func(_, rally *fakeCB, o *ProvisionOptions) {
				rally.setFail("POST /pools/default", http.StatusBadRequest)
				o.Services = []string{"index"}
				o.RAMGiB = 4
			},
			wantErr: "set service quotas",
		},
		{
			name: "rally not initialized",
			setup: func(_, rally *fakeCB, _ *ProvisionOptions) {
				rally.mu.Lock()
				rally.initialized = false
				rally.mu.Unlock()
			},
			wantErr: "is not initialized",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := newFakeCB(t)
			rally := newFakeCB(t)
			rally.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}})
			opts := provisionOpts(local.addr())
			opts.RallyIPAddress = rally.addr()
			tt.setup(local, rally, &opts)
			joined, err := NewClient().JoinNode(opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if joined != tt.wantJoined {
				t.Fatalf("joined = %v", joined)
			}
			if tt.check != nil {
				tt.check(t, local, rally)
			}
		})
	}
}

func TestJoinNodeRequiresRally(t *testing.T) {
	_, err := NewClient().JoinNode(provisionOpts("10.0.0.2"))
	if err == nil || err.Error() != "--rally-ip-address is required" {
		t.Fatalf("err = %v", err)
	}
}

func TestRebalanceCluster(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(f *fakeCB, o *ProvisionOptions)
		wantErr string
	}{
		{name: "rally address", setup: func(f *fakeCB, o *ProvisionOptions) { o.RallyIPAddress = f.addr(); o.IPAddress = "" }},
		{name: "falls back to ip address", setup: func(_ *fakeCB, _ *ProvisionOptions) {}},
		{
			name:    "missing address",
			setup:   func(_ *fakeCB, o *ProvisionOptions) { o.IPAddress = " " },
			wantErr: "--rally-ip-address is required",
		},
		{
			name: "not initialized",
			setup: func(f *fakeCB, _ *ProvisionOptions) {
				f.mu.Lock()
				f.initialized = false
				f.mu.Unlock()
			},
			wantErr: "is not initialized",
		},
		{
			name:    "rebalance rejected",
			setup:   func(f *fakeCB, _ *ProvisionOptions) { f.setFail("POST /controller/rebalance", 400) },
			wantErr: "rebalance: HTTP 400",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			f.seed(fakeNode{Host: "10.0.0.1"}, fakeNode{Host: "10.0.0.2"})
			opts := provisionOpts(f.addr())
			tt.setup(f, &opts)
			err := NewClient().RebalanceCluster(opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reqs := f.requests(http.MethodPost, "/controller/rebalance")
			if len(reqs) != 1 || reqs[0].Form.Get("knownNodes") != "ns_1@10.0.0.1,ns_1@10.0.0.2" {
				t.Fatalf("rebalance = %+v", reqs)
			}
			if f.count(http.MethodGet, "/pools/default/rebalanceProgress") == 0 {
				t.Fatal("expected rebalance progress poll")
			}
		})
	}
}

func TestClusterNodeHosts(t *testing.T) {
	ep := handlerEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[
			"junk",
			{"otpNode":"ns_1@10.0.0.1","hostname":"other:8091"},
			{"otpNode":"ns_1@10.0.0.1"},
			{"configuredHostname":"10.0.0.2:8091"},
			{"hostname":"cb3.local:8091"},
			{"otpNode":"","hostname":"  "}
		]}`)) // test response
	})
	hosts, err := NewClient().ClusterNodeHosts(ep, "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(hosts, ",") != "10.0.0.1,10.0.0.2,cb3.local" {
		t.Fatalf("hosts = %v", hosts)
	}
	if _, err := NewClient().ClusterNodeHosts(closedEndpoint(t), "u", "p"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsNodeInClusterAndWait(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "10.0.0.1"})
	c := NewClient()
	ok, err := c.IsNodeInCluster(f.endpoint(), "u", "p", "10.0.0.1:8091")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	ok, err = c.IsNodeInCluster(f.endpoint(), "u", "p", "10.0.0.9")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if err := c.WaitForNodeInCluster(f.endpoint(), "u", "p", "10.0.0.1", 3); err != nil {
		t.Fatal(err)
	}
	// Zero attempts returns at once without polling.
	if err := c.WaitForNodeInCluster(f.endpoint(), "u", "p", "10.0.0.9", 0); err == nil || !strings.Contains(err.Error(), "waiting for node 10.0.0.9") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.IsNodeInCluster(closedEndpoint(t), "u", "p", "h"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExternalHostname(t *testing.T) {
	f := newFakeCB(t)
	c := NewClient()
	host, err := c.externalHostname(f.endpoint(), "u", "p")
	if err != nil || host != "" {
		t.Fatalf("host=%q err=%v", host, err)
	}
	f.mu.Lock()
	f.altHost = " ext.example.com:9000 "
	f.mu.Unlock()
	host, err = c.externalHostname(f.endpoint(), "u", "p")
	if err != nil || host != "ext.example.com" {
		t.Fatalf("host=%q err=%v", host, err)
	}
	if _, err := c.externalHostname(closedEndpoint(t), "u", "p"); err == nil {
		t.Fatal("expected error")
	}
}

func TestApplyExternalAddressNoop(t *testing.T) {
	if err := NewClient().applyExternalAddress(closedEndpoint(t), ProvisionOptions{ExternalIPAddress: " "}); err != nil {
		t.Fatal(err)
	}
}
