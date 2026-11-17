package rest

import (
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

func serverConfig(host string) *config.Config {
	return &config.Config{Hostname: host, Username: "Administrator", Password: "password"}
}

func TestClusterExists(t *testing.T) {
	tests := []struct {
		name string
		seed bool
		want bool
	}{
		{name: "initialized", seed: true, want: true},
		{name: "uninitialized", seed: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			if tt.seed {
				f.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}})
			}
			if got := NewClient().ClusterExists(serverConfig(f.addr()), nil); got != tt.want {
				t.Fatalf("exists = %v", got)
			}
		})
	}
}

func TestIsClusterInitializedViaPools(t *testing.T) {
	// /pools/default fails but unauthenticated /pools lists a pool.
	ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pools" {
			_, _ = w.Write([]byte(`{"pools":[{"name":"default"}]}`)) // test response
			return
		}
		http.NotFound(w, r)
	})
	if !NewClient().IsClusterInitialized(ep, "u", "p") {
		t.Fatal("expected initialized via /pools")
	}
}

func TestClusterMap(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv", "n1ql"}})
	text, err := NewClient().ClusterMap(serverConfig(f.addr()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Cluster Host List:") || !strings.Contains(text, "[01] int: 10.0.0.1:8091") ||
		!strings.Contains(text, "[Services] kv,n1ql [version] 7.6.0") {
		t.Fatalf("map = %q", text)
	}
	if _, err := NewClient().ClusterMap(serverConfig(closedEndpointAddr(t))); err == nil {
		t.Fatal("expected error")
	}
}

func closedEndpointAddr(t *testing.T) string {
	ep := closedEndpoint(t)
	return net.JoinHostPort(ep.Host, strconv.Itoa(ep.AdminPort))
}

func TestCreateClusterAlreadyInitialized(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "127.0.0.1", Services: []string{"kv"}})
	cfg := serverConfig(f.addr())
	created, err := NewClient().CreateCluster(cfg, map[string]string{"couchbase.server.0.ip": f.addr(), "couchbase.server.0.ram": "4", "couchbase.server.0.services": "data"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected created=false")
	}
	if f.count(http.MethodPost, "/clusterInit") != 0 {
		t.Fatal("must not init")
	}
	posts := f.requests(http.MethodPost, "/pools/default")
	if len(posts) != 1 || posts[0].Form.Get("memoryQuota") != "3276" {
		t.Fatalf("quota posts = %+v", posts)
	}
}

func TestCreateClusterErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(f *fakeCB)
		wantErr string
	}{
		{name: "memory", setup: func(f *fakeCB) { f.setFail("GET /nodes/self", 500) }, wantErr: "failed to create Couchbase Server cluster: HTTP 500"},
		{name: "init", setup: func(f *fakeCB) { f.setFail("POST /clusterInit", 400) }, wantErr: "failed to create Couchbase Server cluster: HTTP 400"},
		{
			name: "reconcile on existing",
			setup: func(f *fakeCB) {
				f.seed(fakeNode{Host: "127.0.0.1", Services: []string{"kv"}})
				f.setFail("POST /pools/default", 400)
			},
			wantErr: "set service quotas",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			tt.setup(f)
			_, err := NewClient().CreateCluster(serverConfig(f.addr()), nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCreateClusterMultiNodeRebalanceFailure(t *testing.T) {
	primary := newFakeCB(t)
	second := newFakeCB(t)
	primary.setFail("POST /controller/rebalance", http.StatusBadRequest)
	opts := map[string]string{
		"couchbase.server.0.ip":       primary.addr(),
		"couchbase.server.0.services": "data",
		"couchbase.server.1.ip":       second.addr(),
		"couchbase.server.1.services": "data,eventing",
		// RAM set so the second node's memory is not read over its default port.
		"couchbase.server.1.ram": "4",
	}
	_, err := NewClient().CreateCluster(serverConfig(primary.addr()), opts)
	if err == nil || !strings.Contains(err.Error(), "failed to create Couchbase Server cluster: HTTP 400") {
		t.Fatalf("err = %v", err)
	}
	if second.count(http.MethodGet, "/pools") != 1 {
		t.Fatal("expected API wait on second node")
	}
	adds := primary.requests(http.MethodPost, "/controller/addNode")
	if len(adds) != 1 || adds[0].Form.Get("hostname") != "127.0.0.1" || adds[0].Form.Get("services") != "kv,eventing" {
		t.Fatalf("addNode = %+v", adds)
	}
	quota := primary.requests(http.MethodPost, "/pools/default")
	if len(quota) != 1 || quota[0].Form.Get("eventingMemoryQuota") != "1638" {
		t.Fatalf("quota raise = %+v", quota)
	}
	reb := primary.requests(http.MethodPost, "/controller/rebalance")
	if len(reb) != 1 || reb[0].Form.Get("knownNodes") != "ns_1@127.0.0.1,ns_1@127.0.0.1" {
		t.Fatalf("rebalance = %+v", reb)
	}
}

func TestApplyAlternateAddresses(t *testing.T) {
	t.Run("skips nodes without alternate", func(t *testing.T) {
		nodes := []models.ClusterNodeConfig{{IP: "10.0.0.1"}}
		if err := NewClient().ApplyAlternateAddresses(nodes, "u", "p", false, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("setup error is returned", func(t *testing.T) {
		// An invalid host fails request construction without touching the network.
		nodes := []models.ClusterNodeConfig{{IP: "bad host%zz", AlternateAddress: "ext.example.com:9000"}}
		if err := NewClient().ApplyAlternateAddresses(nodes, "u", "p", false, true); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestParseHostOnly(t *testing.T) {
	tests := map[string]string{"10.0.0.1:9000": "10.0.0.1", "cb.local": "cb.local", "": config.DefaultHostname}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			if got := ParseHostOnly(in); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}

func TestWaitHappyPaths(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv", "n1ql", "index"}})
	ep := f.endpoint()
	c := NewClient()
	if err := c.WaitForNodesAPI([]Endpoint{ep, ep}, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForCluster(ep, "u", "p", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForRebalanceComplete(ep, "u", "p", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForClusterServices(ep, "u", "p", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForQueryReady(ep, "u", "p", 1); err != nil {
		t.Fatal(err)
	}
	if n := f.count(http.MethodPost, "/query/service"); n != 2 {
		t.Fatalf("query posts = %d", n)
	}
}

func TestWaitZeroAttempts(t *testing.T) {
	ep := closedEndpoint(t)
	c := NewClient()
	checks := map[string]error{
		"cluster not ready":               c.WaitForCluster(ep, "u", "p", 0),
		"rebalance did not complete":      c.WaitForRebalanceComplete(ep, "u", "p", 0),
		"cluster services not ready":      c.WaitForClusterServices(ep, "u", "p", 0),
		"query service not ready":         c.WaitForQueryReady(ep, "u", "p", 0),
		"node API not ready on 127.0.0.1": c.WaitForNodesAPI([]Endpoint{ep}, 1),
	}
	for want, err := range checks {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}

func TestIsRebalanceInProgress(t *testing.T) {
	tests := []struct {
		name   string
		routes map[string]string
		want   bool
	}{
		{name: "progress running", routes: map[string]string{"/pools/default/rebalanceProgress": `{"status":"running"}`}, want: true},
		{name: "progress none", routes: map[string]string{"/pools/default/rebalanceProgress": `{"status":"none"}`, "/pools/default": `{"rebalanceStatus":"running"}`}, want: false},
		{name: "pools status", routes: map[string]string{"/pools/default": `{"rebalanceStatus":"running"}`}, want: true},
		{name: "running task", routes: map[string]string{"/pools/default": `{"rebalanceStatus":"none"}`, "/pools/default/tasks": `["x",{"type":"rebalance","status":"running"}]`}, want: true},
		{name: "idle", routes: map[string]string{"/pools/default/tasks": `[{"type":"rebalance","status":"notRunning"}]`}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
				body, ok := tt.routes[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(body)) // test response
			})
			if got := NewClient().isRebalanceInProgress(ep, "u", "p"); got != tt.want {
				t.Fatalf("in progress = %v", got)
			}
		})
	}
}

func TestParseCapellaNodes(t *testing.T) {
	got := ParseCapellaNodes(map[string]string{
		"capella.cluster.node.1.cpu":      "8",
		"capella.cluster.node.1.ram":      "32",
		"capella.cluster.node.1.services": "kv, n1ql,fts,cbas,eventing,custom",
		"capella.cluster.node.0.cpu":      "bad",
		"capella.cluster.node.0.ram":      "bad",
		"capella.cluster.node.0.services": "",
		"capella.cluster.node.0.other":    "ignored",
		"unrelated":                       "x",
	})
	want := []models.CapellaNodeConfig{
		{CPU: 4, RAM: 16, Services: []string{"data", "query", "index", "search"}},
		{CPU: 8, RAM: 32, Services: []string{"data", "query", "search", "analytics", "eventing", "custom"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildServerOptionsRoundTrip(t *testing.T) {
	specs := []models.NodeSpec{
		{Host: "10.0.0.1", Services: []string{"data", "query"}, RAMGiB: 8, AlternateAddress: "ext1", AlternatePorts: map[string]int{"kv": 9000}},
		{Host: "10.0.0.2", Services: []string{"index"}},
	}
	opts := BuildServerOptions(specs, true)
	if opts["couchbase.server.0.ram"] != "8" || opts["couchbase.server.0.alternatePorts"] != "kv:9000" ||
		opts["couchbase.server.1.services"] != "index" || opts[config.PropServerExtAPI] != "true" {
		t.Fatalf("opts = %v", opts)
	}
	if _, ok := opts["couchbase.server.1.ram"]; ok {
		t.Fatal("unset ram must be omitted")
	}
	if !ParseUseExtAPI(opts) {
		t.Fatal("ext api should be on")
	}
	nodes := ParseServerNodes(opts)
	if len(nodes) != 2 || nodes[0].RAMGiB != 8 || nodes[0].AlternateAddress != "ext1" || nodes[0].AlternatePorts["kv"] != 9000 ||
		!reflect.DeepEqual(nodes[1].Services, []string{"index"}) {
		t.Fatalf("nodes = %+v", nodes)
	}
	if got := APIHostForNode(nodes[0], true); got != "ext1" {
		t.Fatalf("ext api host = %q", got)
	}
	if got := APIHostForNode(nodes[0], false); got != "10.0.0.1" {
		t.Fatalf("api host = %q", got)
	}
	if ep := NodeEndpoint(nodes[1], true, true); ep.Host != "10.0.0.2" || ep.AdminPort != 18091 {
		t.Fatalf("endpoint = %+v", ep)
	}
	if BuildServerOptions(nil, false)[config.PropServerExtAPI] != "" {
		t.Fatal("ext api should be unset")
	}
}

func TestParseUseExtAPI(t *testing.T) {
	for in, want := range map[string]bool{"1": true, "TRUE": true, " yes ": true, "on": true, "no": false, "": false} {
		t.Run(in, func(t *testing.T) {
			if got := ParseUseExtAPI(map[string]string{config.PropServerExtAPI: in}); got != want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestParseServerNodesFields(t *testing.T) {
	nodes := ParseServerNodes(map[string]string{
		"couchbase.server.2.ip":                  "10.0.0.3",
		"couchbase.server.0.external":            " ext0 ",
		"couchbase.server.0.alternatePort.query": "9050",
		"couchbase.server.0.alternatePort.data":  "x",
		"couchbase.server.0.alternatePort. ":     "1",
		"couchbase.server.0.alternatePorts":      "bad",
		"couchbase.server.0.ram":                 "-1",
	})
	if len(nodes) != 2 || nodes[0].IP != config.DefaultHostname || nodes[1].IP != "10.0.0.3" {
		t.Fatalf("nodes = %+v", nodes)
	}
	if nodes[0].AlternateAddress != "ext0" || !reflect.DeepEqual(nodes[0].AlternatePorts, map[string]int{"n1ql": 9050}) || nodes[0].RAMGiB != 0 {
		t.Fatalf("node0 = %+v", nodes[0])
	}
}

func TestServiceNames(t *testing.T) {
	tests := []struct {
		in, server, capella, rest string
		query                     bool
	}{
		{"kv", "data", "data", "kv", false},
		{"N1QL", "query", "query", "n1ql", true},
		{"index", "index", "index", "index", false},
		{"search", "fts", "search", "fts", false},
		{"eventing", "eventing", "eventing", "eventing", false},
		{"cbas", "analytics", "analytics", "cbas", false},
		{" Custom ", "custom", "custom", " Custom ", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeServerService(tt.in); got != tt.server {
				t.Errorf("server = %q", got)
			}
			if got := NormalizeCapellaService(tt.in); got != tt.capella {
				t.Errorf("capella = %q", got)
			}
			if got := ToRESTService(tt.in); got != tt.rest {
				t.Errorf("rest = %q", got)
			}
			if got := IsQueryService(tt.in); got != tt.query {
				t.Errorf("query = %v", got)
			}
		})
	}
}

func TestParseCapellaServices(t *testing.T) {
	if got := ParseCapellaServices(" "); !reflect.DeepEqual(got, DefaultCapellaServices) {
		t.Fatalf("defaults = %v", got)
	}
	if got := ParseCapellaServices("kv,,fts"); !reflect.DeepEqual(got, []string{"data", "search"}) {
		t.Fatalf("got %v", got)
	}
}

func TestParseAlternatePortsErrors(t *testing.T) {
	tests := []struct{ in, want string }{
		{"kv", "invalid alternate port kv; expected service:port pairs"},
		{"kv:abc", "invalid alternate port abc; expected service:port pairs"},
		{"kv:", "invalid alternate port ; expected service:port pairs"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := ParseAlternatePorts(tt.in)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v", err)
			}
		})
	}
	got, err := ParseAlternatePorts("kv:9000, ,query:9050")
	if err != nil || !reflect.DeepEqual(got, map[string]int{"kv": 9000, "n1ql": 9050}) {
		t.Fatalf("got %v err %v", got, err)
	}
}
