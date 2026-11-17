package capella

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
)

// Paths of the fixed fake Capella object graph: organization org-1, project p1, cluster c1.
const (
	fakeOrgPath      = "/v4/organizations/org-1"
	fakeProjectsPath = fakeOrgPath + "/projects"
	fakeProjectPath  = fakeProjectsPath + "/p1"
	fakeClustersPath = fakeProjectPath + "/clusters"
	fakeClusterPath  = fakeClustersPath + "/c1"
	fakeUsersPath    = fakeOrgPath + "/users"
)

// recordedCall is one request seen by fakeAPI.
type recordedCall struct {
	Method string
	Path   string
	Query  string
	Body   []byte
	Header http.Header
}

// fakeHandler answers one route. A string or []byte reply is written as is; anything else is
// JSON encoded. A nil reply writes no body.
type fakeHandler func(r *http.Request, body []byte) (int, any)

// fakeAPI is a route-table stand-in for the Capella v4 REST API. Unrouted requests fail the test.
type fakeAPI struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]fakeHandler
	// fallback answers requests no exact route matches (dynamic ids); nil fails the test.
	fallback fakeHandler
	calls    []recordedCall
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, routes: map[string]fakeHandler{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// handle registers h for method and exact path (query excluded).
func (f *fakeAPI) handle(method, path string, h fakeHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = h
}

// reply registers a fixed status and body for method and path.
func (f *fakeAPI) reply(method, path string, status int, body any) {
	f.handle(method, path, func(*http.Request, []byte) (int, any) { return status, body })
}

// setFallback installs the handler for requests that match no exact route.
func (f *fakeAPI) setFallback(h fakeHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = h
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read body: %v", err)
	}
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body, Header: r.Header.Clone()})
	h := f.routes[r.Method+" "+r.URL.Path]
	if h == nil {
		h = f.fallback
	}
	f.mu.Unlock()
	if h == nil {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		writeFake(w, http.StatusNotFound, map[string]any{"code": 404, "message": "no route"})
		return
	}
	status, out := h(r, body)
	writeFake(w, status, out)
}

func writeFake(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	switch b := body.(type) {
	case nil:
	case string:
		_, _ = io.WriteString(w, b)
	case []byte:
		_, _ = w.Write(b)
	default:
		_ = json.NewEncoder(w).Encode(b)
	}
}

// callsTo returns the recorded requests for method and path.
func (f *fakeAPI) callsTo(method, path string) []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedCall
	for _, c := range f.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// countCalls returns how many requests were seen for method and path.
func (f *fakeAPI) countCalls(method, path string) int { return len(f.callsTo(method, path)) }

// decodeBody unmarshals the body of a recorded call into a generic value.
func decodeBody(t *testing.T, c recordedCall) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.Body, &m); err != nil {
		t.Fatalf("decode request body %q: %v", c.Body, err)
	}
	return m
}

// client returns a legacy client aimed at the fake, with an instant clock and a short wait bound.
func (f *fakeAPI) client() *Client {
	c := NewClient("test-token", "")
	c.BaseURL = f.srv.URL
	c.OrganizationID = "org-1"
	c.Clock = engine.NewFakeClock(time.Time{})
	c.WaitTimeout = time.Minute
	return c
}

// organization returns an already resolved organization org-1 (no request is made).
func (f *fakeAPI) organization() *Organization {
	return &Organization{
		Client:       f.client(),
		Organization: &OrganizationData{ID: "org-1", Name: "org"},
		byID:         map[string]*Project{},
		byName:       map[string]*Project{},
	}
}

// project returns an already resolved project p1.
func (f *fakeAPI) project() *Project {
	return NewProject(f.organization(), "", &ProjectData{ID: "p1", Name: "proj"})
}

// cluster returns an already resolved cluster c1 (db1) with its sub-resource helpers attached.
func (f *fakeAPI) cluster() *Cluster {
	c := NewCluster(f.project())
	c.Data = &ClusterData{ID: "c1", Name: "db1"}
	c.attachServices()
	return c
}

// listPage wraps items in a single-page Capella list envelope.
func listPage(items ...any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"data": items, "cursor": map[string]any{"pages": map[string]any{"page": 1, "last": 1}}}
}

// errBody is a Capella error body.
func errBody(code int) map[string]any {
	return map[string]any{"code": code, "message": http.StatusText(code)}
}
