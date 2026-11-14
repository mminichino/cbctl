// Package capellafake is a stateful, in-memory stand-in for the Capella Management API v4, served
// by an httptest.Server. It implements the endpoints the cbctl Capella commands use so that CLI
// and client tests run offline.
//
// Behaviours modelled after the real API: bearer authentication, 201/202/204 success codes, an
// "ETag: Version: N" header with 412 on a stale If-Match, 404 for unknown resources, 409 for
// duplicate names and for writes against a cluster that is mid-operation, page/perPage
// pagination with cursor.pages.next, base64url bucket IDs, a one-per-organization free tier,
// one-time generated credential passwords, and clusters whose state advances (deploying to
// healthy, destroying to gone, and so on) after a configurable number of polls.
//
// The package is test support: failures to set up are reported through the testing.TB handed to
// New.
package capellafake

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
)

// DefaultAPIKey is the bearer token every Fake accepts.
const DefaultAPIKey = "capella-fake-api-key-0123456789"

// DefaultSettlePolls is how many polls a cluster stays in a transitional state by default.
const DefaultSettlePolls = 2

// Obj is a JSON object.
type Obj = map[string]any

// Request is one request the Fake served.
type Request struct {
	Method   string
	Path     string // unescaped
	RawQuery string
	IfMatch  string
	Body     string
	Status   int // status the Fake answered with
}

// Fake is the in-memory Capella server. Create one with New; it is shut down when the test ends.
type Fake struct {
	// URL is the base URL of the server (http://127.0.0.1:port), for CAPELLA_API_BASE_URL.
	URL string
	// OrgID is the organization the API key can use.
	OrgID string
	// APIKey is the bearer token the server accepts.
	APIKey string
	// Server is the underlying test server.
	Server *httptest.Server

	t  testing.TB
	mu sync.Mutex

	orgs     []Obj
	projects []*project
	routes   []route

	settle   int // polls a transitional state lasts
	pageSize int // when > 0, caps perPage so tests can force multiple pages
	failures []*failure
	log      []Request
}

// res is a versioned resource; ver is the number in its ETag.
type res struct {
	obj Obj
	ver int
}

type project struct {
	res
	clusters []*cluster
}

type cluster struct {
	res
	projectID string
	free      bool
	target    string // state the cluster moves to once polled settle times; "" = stays put
	polls     int

	buckets []*bucket
	creds   []*res
	cidrs   []*res

	schedule       Obj
	scheduleVer    int
	schedulePaused bool

	snapSchedule Obj
	snapshots    []*snapshot
	restores     []*snapshot
	backups      []*backup
	exports      []*export
}

type bucket struct {
	res
	scopes      []*scope
	backupSched Obj
}

type scope struct {
	name  string
	colls []*res
}

// New starts a Fake and registers its shutdown with t.Cleanup.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		t:      t,
		OrgID:  uuid.NewString(),
		APIKey: DefaultAPIKey,
		settle: DefaultSettlePolls,
	}
	f.orgs = []Obj{{"id": f.OrgID, "name": "cbctl-fake-org", "description": "fake organization",
		"preferences": Obj{"sessionDuration": 7200}, "audit": audit(1)}}
	f.routeAll()
	f.Server = httptest.NewServer(http.HandlerFunc(f.dispatch))
	f.URL = f.Server.URL
	t.Cleanup(f.Server.Close)
	return f
}

// Client returns a capellaapi.Client wired to the Fake (base URL, API key, organization). Retries
// run on a fake clock so they never sleep. Options are applied last and can override those
// defaults.
func (f *Fake) Client(opts ...capellaapi.Option) *capellaapi.Client {
	f.t.Helper()
	base := []capellaapi.Option{
		capellaapi.WithBaseURL(f.URL),
		capellaapi.WithOrgID(f.OrgID),
		capellaapi.WithClock(engine.NewFakeClock(time.Time{})),
	}
	c, err := capellaapi.New(f.APIKey, append(base, opts...)...)
	if err != nil {
		f.t.Fatalf("capellafake: build client: %v", err)
	}
	return c
}

// SettlePolls sets how many polls (single-cluster GETs) a cluster reports its transitional state
// before moving to its final one. The default is DefaultSettlePolls; 0 settles on the first poll.
func (f *Fake) SettlePolls(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settle = n
}

// SetPageSize caps the page size the server honours (0 removes the cap), to exercise pagination
// with few items.
func (f *Fake) SetPageSize(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSize = n
}

// FailNext makes the next times requests whose method equals method ("" or "*" matches any) and
// whose unescaped path matches the regular expression pathRegex answer with status and an error
// body instead of being handled. Failures are consumed in the order registered.
func (f *Fake) FailNext(method, pathRegex string, status, times int) {
	re, err := regexp.Compile(pathRegex)
	if err != nil {
		f.t.Fatalf("capellafake: FailNext: bad path regexp %q: %v", pathRegex, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, &failure{method: strings.ToUpper(method), re: re, status: status, left: times})
}

// RequestLog returns a copy of every request served so far, in order.
func (f *Fake) RequestLog() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.log...)
}

// ResetLog clears the request log.
func (f *Fake) ResetLog() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = nil
}

// Count returns how many logged requests have the given method ("" or "*" for any) and a path
// matching pathRegex.
func (f *Fake) Count(method, pathRegex string) int {
	re, err := regexp.Compile(pathRegex)
	if err != nil {
		f.t.Fatalf("capellafake: Count: bad path regexp %q: %v", pathRegex, err)
	}
	method = strings.ToUpper(method)
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.log {
		if (method == "" || method == "*" || r.Method == method) && re.MatchString(r.Path) {
			n++
		}
	}
	return n
}

// AddOrganization adds an organization the key can list but not use, to test the several-
// organizations path, and returns its ID.
func (f *Fake) AddOrganization(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.NewString()
	f.orgs = append(f.orgs, Obj{"id": id, "name": name, "audit": audit(1)})
	return id
}

// SeedProject creates a project directly and returns its ID.
func (f *Fake) SeedProject(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newProject(name, "").obj["id"].(string)
}

// SeedCluster creates a cluster in the given project directly, already in state (for example
// "healthy"), and returns its ID. It has one data/index/query node of 4 vCPU / 16 GB.
func (f *Fake) SeedCluster(projectID, name, state string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.findProject(projectID)
	if p == nil {
		f.t.Fatalf("capellafake: SeedCluster: unknown project %q", projectID)
	}
	c := f.newCluster(p, Obj{
		"name":          name,
		"cloudProvider": Obj{"type": "aws", "region": "us-east-1", "cidr": "10.0.0.0/20"},
		"serviceGroups": []any{Obj{"numOfNodes": 3, "services": []any{"data", "index", "query"},
			"node": Obj{"compute": Obj{"cpu": 4, "ram": 16}, "disk": Obj{"type": "gp3", "storage": 50, "iops": 3000}}}},
		"availability": Obj{"type": "multi"},
		"support":      Obj{"plan": "basic", "timezone": "ET"},
	}, false)
	c.obj["currentState"] = state
	c.target = ""
	return c.obj["id"].(string)
}

// SetClusterState forces a cluster into state and stops it progressing, so a test can keep a
// cluster "deploying" forever or start one "turnedOff". It reports whether the cluster exists.
func (f *Fake) SetClusterState(clusterID, state string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, c := f.findClusterAnywhere(clusterID)
	if c == nil {
		return false
	}
	c.obj["currentState"] = state
	c.target = ""
	return true
}

// ClusterState returns a cluster's current state ("" when it does not exist).
func (f *Fake) ClusterState(clusterID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, c := f.findClusterAnywhere(clusterID)
	if c == nil {
		return ""
	}
	return str(c.obj, "currentState")
}

// SchedulePaused reports whether a cluster's on/off schedule has been paused.
func (f *Fake) SchedulePaused(clusterID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, c := f.findClusterAnywhere(clusterID)
	return c != nil && c.schedulePaused
}

// ---- routing -------------------------------------------------------------------------------

type params map[string]string

type handler func(c *rctx)

type route struct {
	method  string
	pattern string // original {param} template, for inventory parity
	re      *regexp.Regexp
	names   []string
	h       handler
	anyOrg  bool // does not require the {org} segment to be f.OrgID
}

type failure struct {
	method string
	re     *regexp.Regexp
	status int
	left   int
}

// rctx is the per-request context handed to handlers; the Fake's mutex is held while it lives.
type rctx struct {
	f      *Fake
	w      http.ResponseWriter
	r      *http.Request
	p      params
	body   Obj
	parsed bool
}

var segment = regexp.MustCompile(`\{([a-zA-Z]+)\}`)

// handle registers a route. pattern uses {name} for one path segment. Routes are tried in
// registration order, so literal segments such as "freeTier" must be registered before the
// wildcard routes they would otherwise be captured by.
func (f *Fake) handle(method, pattern string, h handler) { f.add(method, pattern, h, false) }

// handleAnyOrg is handle for a route that is allowed to name an organization other than OrgID.
func (f *Fake) handleAnyOrg(method, pattern string, h handler) { f.add(method, pattern, h, true) }

func (f *Fake) add(method, pattern string, h handler, anyOrg bool) {
	var names []string
	expr := "^" + segment.ReplaceAllStringFunc(pattern, func(m string) string {
		names = append(names, segment.FindStringSubmatch(m)[1])
		return `([^/]+)`
	}) + "$"
	f.routes = append(f.routes, route{method: method, pattern: pattern, re: regexp.MustCompile(expr), names: names, h: h, anyOrg: anyOrg})
}

// InventoryRoutes returns every registered method+path template (Phase 4 inventory parity).
func (f *Fake) InventoryRoutes() []capellaapi.Endpoint {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capellaapi.Endpoint, 0, len(f.routes))
	for _, rt := range f.routes {
		out = append(out, capellaapi.Endpoint{Method: rt.method, Path: rt.pattern})
	}
	return out
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (f *Fake) dispatch(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() {
		f.log = append(f.log, Request{
			Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
			IfMatch: r.Header.Get("If-Match"), Body: string(body), Status: rec.status,
		})
	}()

	c := &rctx{f: f, w: rec, r: r}
	if r.Header.Get("Authorization") != "Bearer "+f.APIKey {
		c.err(http.StatusUnauthorized, "invalid or missing API key")
		return
	}
	for _, fl := range f.failures {
		if fl.left > 0 && (fl.method == "" || fl.method == "*" || fl.method == r.Method) && fl.re.MatchString(r.URL.Path) {
			fl.left--
			c.err(fl.status, "injected failure")
			return
		}
	}

	esc := r.URL.EscapedPath()
	for i := range f.routes {
		rt := &f.routes[i]
		if rt.method != r.Method {
			continue
		}
		m := rt.re.FindStringSubmatch(esc)
		if m == nil {
			continue
		}
		c.p = make(params, len(rt.names))
		for j, name := range rt.names {
			v, err := url.PathUnescape(m[j+1])
			if err != nil {
				v = m[j+1]
			}
			c.p[name] = v
		}
		if org, ok := c.p["org"]; ok && !rt.anyOrg && org != f.OrgID {
			c.err(http.StatusForbidden, "the API key has no access to this organization")
			return
		}
		rt.h(c)
		return
	}
	c.err(http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
}

// ---- response helpers ----------------------------------------------------------------------

func (c *rctx) writeJSON(status int, v any) {
	c.w.Header().Set("Content-Type", "application/json")
	c.w.WriteHeader(status)
	if err := json.NewEncoder(c.w).Encode(v); err != nil {
		c.f.t.Logf("capellafake: encode response: %v", err)
	}
}

func (c *rctx) noContent() { c.w.WriteHeader(http.StatusNoContent) }

func (c *rctx) etag(ver int) { c.w.Header().Set("ETag", fmt.Sprintf(`"Version: %d"`, ver)) }

// err writes a Capella-style error body.
func (c *rctx) err(status int, msg string) {
	c.writeJSON(status, Obj{"httpStatusCode": status, "code": 1000 + status, "message": msg, "hint": ""})
}

func (c *rctx) notFound(what string) { c.err(http.StatusNotFound, what+" not found") }

// json parses the request body once. An empty body is an empty object; a malformed one is
// answered with 400 and false is returned.
func (c *rctx) json() (Obj, bool) {
	if c.parsed {
		return c.body, true
	}
	raw, _ := io.ReadAll(c.r.Body)
	c.body = Obj{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &c.body); err != nil {
			c.err(http.StatusBadRequest, "request body is not a JSON object")
			return nil, false
		}
	}
	c.parsed = true
	return c.body, true
}

// matches checks If-Match against ver, answering 412 on a mismatch.
func (c *rctx) matches(ver int) bool {
	im := strings.TrimSpace(c.r.Header.Get("If-Match"))
	if im == "" {
		return true
	}
	im = strings.Trim(strings.TrimPrefix(im, "W/"), `"`)
	if len(im) >= 8 && strings.EqualFold(im[:8], "version:") {
		im = im[8:]
	}
	if strings.TrimSpace(im) == strconv.Itoa(ver) {
		return true
	}
	c.err(http.StatusPreconditionFailed, "the resource was modified; fetch it again and retry")
	return false
}

// page writes one page of items under key with a Capella cursor.
func (c *rctx) page(key string, items []Obj) {
	q := c.r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	per, _ := strconv.Atoi(q.Get("perPage"))
	if per < 1 {
		per = 10
	}
	if limit := c.f.pageSize; limit > 0 && per > limit {
		per = limit
	}
	total := len(items)
	start := (page - 1) * per
	if start > total {
		start = total
	}
	end := start + per
	if end > total {
		end = total
	}
	last := (total + per - 1) / per
	if last < 1 {
		last = 1
	}
	pages := Obj{"page": page, "previous": max(page-1, 0), "last": last, "perPage": per, "totalItems": total}
	if end < total {
		pages["next"] = page + 1
	}
	out := make([]Obj, 0, end-start)
	for _, it := range items[start:end] {
		out = append(out, clone(it))
	}
	c.writeJSON(http.StatusOK, Obj{"cursor": Obj{"pages": pages, "hrefs": Obj{}}, key: out})
}

// ---- small utilities -----------------------------------------------------------------------

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func audit(ver int) Obj {
	return Obj{"createdBy": "capellafake", "createdAt": now(), "modifiedBy": "capellafake", "modifiedAt": now(), "version": ver}
}

// clone deep-copies a JSON object.
func clone(o Obj) Obj {
	if o == nil {
		return nil
	}
	raw, _ := json.Marshal(o)
	out := Obj{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func str(o Obj, k string) string {
	s, _ := o[k].(string)
	return s
}

func sub(o Obj, k string) Obj {
	m, _ := o[k].(map[string]any)
	return m
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func list(o Obj, k string) []any {
	l, _ := o[k].([]any)
	return l
}

func same(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// touch bumps r's version and audit after a modification.
func (r *res) touch() {
	r.ver++
	r.obj["audit"] = audit(r.ver)
}

func objs(rs []*res) []Obj {
	out := make([]Obj, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.obj)
	}
	return out
}
