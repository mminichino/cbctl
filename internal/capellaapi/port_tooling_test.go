package capellaapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
)

// decodeBody decodes a recorded JSON request body.
func decodeBody(t *testing.T, h hit) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(h.Body), &out); err != nil {
		t.Fatalf("%s %s body %q: %v", h.Method, h.Path, h.Body, err)
	}
	return out
}

// TestScopeCollection_CAP025_AC1_EndpointContract: the scope and collection endpoints send the
// right method, path and body, and update-collection sends If-Match from the collection's ETag.
func TestScopeCollection_CAP025_AC1_EndpointContract(t *testing.T) {
	ctx := context.Background()
	srv := newStubServer(t)
	srv.override = func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/scopes"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"scopes":[{"name":"_default","collections":[]}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/scopes/inventory"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"name":"inventory","collections":[]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/collections"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"name":"items","maxTTL":0}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/collections/items"):
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"Version: 3"`)
			_, _ = io.WriteString(w, `{"name":"items","maxTTL":0}`)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut || r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			return false
		}
		return true
	}
	c := newTestClient(t, srv)
	b := BucketID("orders")

	scopes, err := c.ListScopes(ctx, "p-1", "c-1", b)
	if err != nil || len(scopes) != 1 || scopes[0].Name != "_default" {
		t.Fatalf("ListScopes = %+v, %v", scopes, err)
	}
	if err := c.CreateScope(ctx, "p-1", "c-1", b, "inventory"); err != nil {
		t.Fatal(err)
	}
	if s, _, err := c.GetScope(ctx, "p-1", "c-1", b, "inventory"); err != nil || s.Name != "inventory" {
		t.Fatalf("GetScope = %+v, %v", s, err)
	}
	cols, err := c.ListCollections(ctx, "p-1", "c-1", b, "inventory")
	if err != nil || !reflect.DeepEqual(cols, []Collection{{Name: "items"}}) {
		t.Fatalf("ListCollections = %+v, %v", cols, err)
	}
	ttl := -1
	if err := c.CreateCollection(ctx, "p-1", "c-1", b, "inventory", "items", &ttl); err != nil {
		t.Fatal(err)
	}
	_, etag, err := c.GetCollection(ctx, "p-1", "c-1", b, "inventory", "items")
	if err != nil || etag != "3" {
		t.Fatalf("GetCollection etag = %q, %v", etag, err)
	}
	if err := c.UpdateCollection(ctx, "p-1", "c-1", b, "inventory", "items", -1, etag); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCollection(ctx, "p-1", "c-1", b, "inventory", "items"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteScope(ctx, "p-1", "c-1", b, "inventory"); err != nil {
		t.Fatal(err)
	}

	base := "/v4/organizations/" + tOrg + "/projects/p-1/clusters/c-1/buckets/" + b
	type sent struct{ Method, Path string }
	var got []sent
	hits := srv.snapshot()
	for _, h := range hits {
		got = append(got, sent{h.Method, strings.TrimPrefix(h.Path, base)})
	}
	want := []sent{
		{"GET", "/scopes"}, {"POST", "/scopes"}, {"GET", "/scopes/inventory"},
		{"GET", "/scopes/inventory/collections"}, {"POST", "/scopes/inventory/collections"},
		{"GET", "/scopes/inventory/collections/items"}, {"PUT", "/scopes/inventory/collections/items"},
		{"DELETE", "/scopes/inventory/collections/items"}, {"DELETE", "/scopes/inventory"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests:\n got %v\nwant %v", got, want)
	}
	if body := decodeBody(t, hits[1]); !reflect.DeepEqual(body, map[string]any{"name": "inventory"}) {
		t.Errorf("create scope body = %v", body)
	}
	if body := decodeBody(t, hits[4]); !reflect.DeepEqual(body, map[string]any{"name": "items", "maxTTL": float64(-1)}) {
		t.Errorf("create collection body = %v", body)
	}
	if body := decodeBody(t, hits[6]); !reflect.DeepEqual(body, map[string]any{"maxTTL": float64(-1)}) {
		t.Errorf("update collection body = %v", body)
	}
	if hits[6].IfMatch != "3" {
		t.Errorf("update collection If-Match = %q, want 3", hits[6].IfMatch)
	}
}

// TestCredential_CAP027_AC1_EndpointContract: create sends name, credentialType basic, access and
// the supplied password and returns the id and password; list and get read the users; get never
// carries a password and returns the ETag; update sends only {access} with If-Match.
func TestCredential_CAP027_AC1_EndpointContract(t *testing.T) {
	ctx := context.Background()
	const pw = "S3cret!Orders-pw"
	access := []AccessEntry{{Privileges: []string{"data_reader"}, Resources: Resources{Buckets: []ResourceBucket{{Name: "orders"}}}}}
	accessJSON := `[{"privileges":["data_reader"],"resources":{"buckets":[{"name":"orders"}]}}]`
	srv := newStubServer(t)
	srv.override = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"u-1","password":"`+pw+`"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/users"):
			_, _ = io.WriteString(w, `{"data":[{"id":"u-1","name":"orders_app","access":`+accessJSON+`}],`+
				`"cursor":{"pages":{"page":1,"last":1,"perPage":10}}}`)
		case r.Method == http.MethodGet:
			w.Header().Set("ETag", `"Version: 3"`)
			_, _ = io.WriteString(w, `{"id":"u-1","name":"orders_app","access":`+accessJSON+`}`)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			return false
		}
		return true
	}
	c := newTestClient(t, srv)

	id, got, err := c.CreateCredential(ctx, "p-1", "c-1", CreateCredentialRequest{Name: "orders_app", Password: pw, Access: access})
	if err != nil || id != "u-1" || got != pw {
		t.Fatalf("CreateCredential = %q, %q, %v", id, got, err)
	}
	list, err := c.ListCredentials(ctx, "p-1", "c-1")
	if err != nil || len(list) != 1 || list[0].Name != "orders_app" {
		t.Fatalf("ListCredentials = %+v, %v", list, err)
	}
	cred, etag, err := c.GetCredential(ctx, "p-1", "c-1", "u-1")
	if err != nil || etag != "3" || !reflect.DeepEqual(cred.Access, access) {
		t.Fatalf("GetCredential = %+v, %q, %v", cred, etag, err)
	}
	if err := c.UpdateCredential(ctx, "p-1", "c-1", "u-1", UpdateCredentialRequest{Access: access}, etag); err != nil {
		t.Fatal(err)
	}

	hits := srv.snapshot()
	var methods []string
	for _, h := range hits {
		methods = append(methods, h.Method)
	}
	if !reflect.DeepEqual(methods, []string{"POST", "GET", "GET", "PUT"}) {
		t.Fatalf("methods = %v", methods)
	}
	users := "/v4/organizations/" + tOrg + "/projects/p-1/clusters/c-1/users"
	for i, want := range []string{users, users, users + "/u-1", users + "/u-1"} {
		if hits[i].Path != want {
			t.Errorf("request %d path = %s, want %s", i, hits[i].Path, want)
		}
	}
	var wantAccess []any
	if err := json.Unmarshal([]byte(accessJSON), &wantAccess); err != nil {
		t.Fatal(err)
	}
	wantCreate := map[string]any{"name": "orders_app", "credentialType": "basic", "access": wantAccess, "password": pw}
	if body := decodeBody(t, hits[0]); !reflect.DeepEqual(body, wantCreate) {
		t.Errorf("create body = %v, want %v", body, wantCreate)
	}
	if body := decodeBody(t, hits[3]); !reflect.DeepEqual(body, map[string]any{"access": wantAccess}) {
		t.Errorf("update body = %v, want only access", body)
	}
	if hits[3].IfMatch != "3" {
		t.Errorf("update If-Match = %q, want 3", hits[3].IfMatch)
	}
}

// TestClient_CAP003_AC3_RetryLogged: each retry is logged as a warning naming the method, the
// path, the attempt and the reason.
func TestClient_CAP003_AC3_RetryLogged(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"x"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)
	c := testClient(t, srv.URL, engine.NewFakeClock(time.Time{}))

	const path = "/v4/organizations/org-1/projects"
	var stderr bytes.Buffer
	var err error
	logging.WithOutputs(nil, &stderr, func() {
		_, err = c.Get(context.Background(), path, nil, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "retrying GET " + path + " (attempt 2/6) after 429"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("log = %q, want %q", stderr.String(), want)
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("the retry is not logged as a warning: %q", stderr.String())
	}
}
