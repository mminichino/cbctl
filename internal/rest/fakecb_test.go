package rest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// recordedRequest is one request seen by a fake Couchbase Server.
type recordedRequest struct {
	Method      string
	Path        string
	RawQuery    string
	ContentType string
	Body        string
	Form        url.Values
	User        string
	Pass        string
	HasAuth     bool
}

// fakeNode is a cluster member in the simulated /pools/default payload.
type fakeNode struct {
	Host     string
	Services []string // REST names (kv, n1ql, index, fts, ...)
}

// fakeCB emulates the subset of the Couchbase Server management REST API the
// rest package uses. All state is guarded by mu so tests pass under -race.
type fakeCB struct {
	t   *testing.T
	srv *httptest.Server

	mu              sync.Mutex
	reqs            []recordedRequest
	initialized     bool
	memoryTotal     int64
	nodes           []fakeNode
	quotas          map[string]int // REST quota field -> MiB
	groups          []ServerGroup
	rev             int
	altHost         string
	rebalanceStatus string
	pathsNeedAuth   bool           // /nodes/self/controller/settings returns 401 without auth
	addNodeStatus   int            // when non-zero: add the node, then answer with this status
	fail            map[string]int // "METHOD /path" -> forced status
}

func newFakeCB(t *testing.T) *fakeCB {
	t.Helper()
	f := &fakeCB{
		t:               t,
		memoryTotal:     8 << 30,
		quotas:          map[string]int{},
		rev:             1,
		rebalanceStatus: "none",
		fail:            map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// seed marks the fake as an initialized cluster with the given members, all in "Group 1".
func (f *fakeCB) seed(nodes ...fakeNode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initialized = true
	f.nodes = append([]fakeNode(nil), nodes...)
	g := ServerGroup{Name: "Group 1", URI: "/pools/default/serverGroups/0"}
	for _, n := range nodes {
		g.Nodes = append(g.Nodes, ServerGroupNode{OTPNode: "ns_1@" + n.Host})
	}
	f.groups = []ServerGroup{g}
}

func (f *fakeCB) setFail(key string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[key] = status
}

// addr is "host:port" of the fake.
func (f *fakeCB) addr() string { return f.srv.Listener.Addr().String() }

// endpoint returns an Endpoint whose admin and query ports both hit the fake.
func (f *fakeCB) endpoint() Endpoint {
	f.t.Helper()
	host, portText, err := net.SplitHostPort(f.addr())
	if err != nil {
		f.t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		f.t.Fatal(err)
	}
	return Endpoint{Host: host, AdminPort: port, QueryPort: port}
}

// requests returns the recorded requests matching method and path.
func (f *fakeCB) requests(method, path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, r := range f.reqs {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeCB) count(method, path string) int { return len(f.requests(method, path)) }

func (f *fakeCB) groupOf(otp string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.groups {
		for _, n := range g.Nodes {
			if n.OTPNode == otp {
				return g.Name
			}
		}
	}
	return ""
}

func (f *fakeCB) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		f.t.Errorf("encode response: %v", err)
	}
}

func (f *fakeCB) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read body: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	rec := recordedRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		ContentType: r.Header.Get("Content-Type"),
		Body:        string(body),
	}
	rec.User, rec.Pass, rec.HasAuth = r.BasicAuth()
	if strings.HasPrefix(rec.ContentType, "application/x-www-form-urlencoded") {
		form, err := url.ParseQuery(string(body))
		if err != nil {
			f.t.Errorf("parse form: %v", err)
		}
		rec.Form = form
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, rec)

	key := r.Method + " " + r.URL.Path
	if status := f.fail[key]; status != 0 {
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, "forced failure") // test response; write errors surface client-side
		return
	}

	switch key {
	case "GET /pools":
		if f.initialized && !rec.HasAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		pools := []any{}
		if f.initialized {
			pools = append(pools, map[string]any{"name": "default"})
		}
		f.writeJSON(w, map[string]any{"pools": pools})
	case "GET /pools/default":
		if !f.initialized {
			http.Error(w, `"unknown pool"`, http.StatusNotFound)
			return
		}
		nodes := make([]any, 0, len(f.nodes))
		for _, n := range f.nodes {
			nodes = append(nodes, map[string]any{
				"otpNode":            "ns_1@" + n.Host,
				"configuredHostname": n.Host + ":8091",
				"hostname":           n.Host + ":8091",
				"services":           n.Services,
				"version":            "7.6.0",
				"os":                 "x86_64-linux",
			})
		}
		payload := map[string]any{"nodes": nodes, "rebalanceStatus": f.rebalanceStatus}
		for field, v := range f.quotas {
			payload[field] = v
		}
		f.writeJSON(w, payload)
	case "POST /pools/default":
		for field := range rec.Form {
			n, err := strconv.Atoi(rec.Form.Get(field))
			if err != nil {
				http.Error(w, "bad quota", http.StatusBadRequest)
				return
			}
			f.quotas[field] = n
		}
		w.WriteHeader(http.StatusOK)
	case "GET /nodes/self":
		if f.initialized && !rec.HasAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		payload := map[string]any{"memoryTotal": f.memoryTotal}
		if f.altHost != "" {
			payload["alternateAddresses"] = map[string]any{"external": map[string]any{"hostname": f.altHost}}
		}
		f.writeJSON(w, payload)
	case "POST /nodes/self/controller/settings":
		if f.pathsNeedAuth && !rec.HasAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	case "POST /clusterInit":
		host := rec.Form.Get("hostname")
		f.initialized = true
		f.nodes = []fakeNode{{Host: host, Services: strings.Split(rec.Form.Get("services"), ",")}}
		f.groups = []ServerGroup{{
			Name:  "Group 1",
			URI:   "/pools/default/serverGroups/0",
			Nodes: []ServerGroupNode{{OTPNode: "ns_1@" + host}},
		}}
		for _, svc := range quotaServiceList() {
			if v := rec.Form.Get(svc.Field); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil {
					http.Error(w, "bad quota", http.StatusBadRequest)
					return
				}
				f.quotas[svc.Field] = n
			}
		}
		w.WriteHeader(http.StatusOK)
	case "PUT /node/controller/setupAlternateAddresses/external":
		f.altHost = rec.Form.Get("hostname")
		w.WriteHeader(http.StatusOK)
	case "GET /pools/default/serverGroups":
		f.writeJSON(w, map[string]any{
			"groups": f.groups,
			"uri":    fmt.Sprintf("/pools/default/serverGroups?rev=%d", f.rev),
		})
	case "POST /pools/default/serverGroups":
		name := rec.Form.Get("name")
		for _, g := range f.groups {
			if g.Name == name {
				http.Error(w, `{"name":"already exists"}`, http.StatusBadRequest)
				return
			}
		}
		f.groups = append(f.groups, ServerGroup{Name: name, URI: "/pools/default/serverGroups/" + name})
		f.rev++
		w.WriteHeader(http.StatusOK)
	case "PUT /pools/default/serverGroups":
		if r.URL.Query().Get("rev") != strconv.Itoa(f.rev) {
			http.Error(w, "revision mismatch", http.StatusConflict)
			return
		}
		var payload struct {
			Groups []ServerGroup `json:"groups"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		f.groups = payload.Groups
		f.rev++
		w.WriteHeader(http.StatusOK)
	case "POST /controller/addNode":
		host := rec.Form.Get("hostname")
		var services []string
		if s := rec.Form.Get("services"); s != "" {
			services = strings.Split(s, ",")
		}
		f.nodes = append(f.nodes, fakeNode{Host: host, Services: services})
		if len(f.groups) > 0 {
			f.groups[0].Nodes = append(f.groups[0].Nodes, ServerGroupNode{OTPNode: "ns_1@" + host})
		}
		if f.addNodeStatus != 0 {
			w.WriteHeader(f.addNodeStatus)
			return
		}
		f.writeJSON(w, map[string]any{"otpNode": "ns_1@" + host})
	case "POST /controller/rebalance":
		w.WriteHeader(http.StatusOK)
	case "GET /pools/default/rebalanceProgress":
		f.writeJSON(w, map[string]any{"status": f.rebalanceStatus})
	case "GET /pools/default/tasks":
		f.writeJSON(w, []any{})
	case "POST /query/service":
		f.writeJSON(w, map[string]any{"status": "success"})
	default:
		http.NotFound(w, r)
	}
}

// closedEndpoint returns an endpoint on a port nothing listens on any more.
func closedEndpoint(t *testing.T) Endpoint {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	ep := (&fakeCB{t: t, srv: srv}).endpoint()
	srv.Close()
	return ep
}

// handlerEndpoint starts a one-off server for handler and returns its endpoint.
func handlerEndpoint(t *testing.T, handler http.HandlerFunc) Endpoint {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return (&fakeCB{t: t, srv: srv}).endpoint()
}
