package capellaapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
)

// TestMain keeps the retry warnings out of the test output.
func TestMain(m *testing.M) {
	logging.SetOutputs(io.Discard, io.Discard)
	os.Exit(m.Run())
}

func TestNew(t *testing.T) {
	t.Parallel()
	if _, err := New(""); err == nil {
		t.Error("New with empty key should fail")
	}
	if _, err := New("sk-valid-key-1234", WithBaseURL("http://example.com")); err == nil {
		t.Error("plain-http non-local base URL should be rejected")
	}
	c, err := New("sk-new-test-KEY-3390", WithOrgID("org-1"))
	if err != nil {
		t.Fatal(err)
	}
	if c.OrgID() != "org-1" || c.baseURL != "https://cloudapi.cloud.couchbase.com" || c.maxAttempts != 6 {
		t.Errorf("unexpected defaults: org=%q base=%q attempts=%d", c.OrgID(), c.baseURL, c.maxAttempts)
	}
	if got := redact.String("x sk-new-test-KEY-3390 y"); strings.Contains(got, "sk-new-test-KEY-3390") {
		t.Errorf("API key was not registered with redact: %q", got)
	}
}

func TestWithBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "https://cloudapi.cloud.couchbase.com", false},
		{"cloudapi.cloud.couchbase.com", "https://cloudapi.cloud.couchbase.com", false},
		{"host.example:8443", "https://host.example:8443", false},
		{"https://host.example/", "https://host.example", false},
		{"https://host.example/v4/ignored", "https://host.example", false},
		{"  HTTPS://Host.example  ", "https://Host.example", false},
		{"http://127.0.0.1:9000", "http://127.0.0.1:9000", false},
		{"http://localhost", "http://localhost", false},
		{"http://[::1]:8080", "http://[::1]:8080", false},
		{"http://api.localhost", "http://api.localhost", false},
		{"http://fake.test/", "http://fake.test", false},
		{"http://host.example", "", true},
		{"ftp://host.example", "", true},
		{"https://", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			c, err := New("sk-baseurl-test-1234", WithBaseURL(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && c.baseURL != tt.want {
				t.Errorf("baseURL = %q, want %q", c.baseURL, tt.want)
			}
		})
	}
}

func TestOrgPath(t *testing.T) {
	t.Parallel()
	c, err := New("sk-orgpath-test-1234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.OrgPath(); err == nil || err.Error() != "organization ID is not set" {
		t.Errorf("OrgPath with no org: err = %v", err)
	}
	c.SetOrgID("0b3c")
	if p, err := c.OrgPath(); err != nil || p != "/v4/organizations/0b3c" {
		t.Errorf("OrgPath = %q, %v", p, err)
	}
}

func TestRequestHeadersQueryAndBody(t *testing.T) {
	type seen struct {
		method, path, query string
		header              http.Header
		body                string
	}
	var (
		mu  sync.Mutex
		got seen
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = seen{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Clone(), string(b)}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := testClient(t, srv.URL, engine.NewFakeClock(time.Time{}), WithUserAgent("1.2.3"))
	last := func() seen {
		mu.Lock()
		defer mu.Unlock()
		return got
	}

	t.Run("body and if-match", func(t *testing.T) {
		_, err := c.Put(context.Background(), "/v4/organizations/o/projects/p%20q", map[string]string{"name": "n"}, "12", nil)
		if err != nil {
			t.Fatal(err)
		}
		got := last()
		if got.method != "PUT" || got.path != "/v4/organizations/o/projects/p%20q" || got.body != `{"name":"n"}` {
			t.Errorf("got %+v", got)
		}
		wantHeaders := map[string]string{
			"Authorization": "Bearer " + testKey,
			"Accept":        "application/json",
			"Content-Type":  "application/json",
			"If-Match":      "12",
		}
		for k, v := range wantHeaders {
			if got.header.Get(k) != v {
				t.Errorf("header %s = %q, want %q", k, got.header.Get(k), v)
			}
		}
		if ua := got.header.Get("User-Agent"); !strings.HasPrefix(ua, "cbctl/1.2.3 (go/go") || !strings.HasSuffix(ua, ")") {
			t.Errorf("User-Agent = %q", ua)
		}
	})

	t.Run("no body means no content type and no if-match", func(t *testing.T) {
		q := url.Values{"retainsnapshotbackups": {"true"}}
		if _, err := c.Delete(context.Background(), "/v4/x", q); err != nil {
			t.Fatal(err)
		}
		got := last()
		if got.method != "DELETE" || got.query != "retainsnapshotbackups=true" || got.body != "" {
			t.Errorf("got %+v", got)
		}
		if got.header.Get("Content-Type") != "" || got.header.Get("If-Match") != "" {
			t.Errorf("unexpected Content-Type/If-Match: %v", got.header)
		}
	})
}

func TestResponseHandling(t *testing.T) {
	srv, _ := scripted(t,
		func(w http.ResponseWriter, _ *http.Request) { // 204
			w.WriteHeader(http.StatusNoContent)
		},
		func(w http.ResponseWriter, _ *http.Request) { // 200 empty
			w.WriteHeader(http.StatusOK)
		},
		func(w http.ResponseWriter, _ *http.Request) { // 200 not JSON
			_, _ = io.WriteString(w, "<html>oops</html>")
		},
		func(w http.ResponseWriter, _ *http.Request) { // ETag + JSON
			w.Header().Set("ETag", `"Version: 4"`)
			_, _ = io.WriteString(w, `{"id":"c1","name":"n"}`)
		},
	)
	c := testClient(t, srv.URL, engine.NewFakeClock(time.Time{}))
	ctx := context.Background()

	out := &Project{ID: "keep"}
	if _, err := c.Get(ctx, "/v4/x", nil, out); err != nil || out.ID != "keep" {
		t.Errorf("204: err=%v out=%+v, want untouched", err, out)
	}
	if _, err := c.Get(ctx, "/v4/x", nil, out); err != nil || out.ID != "keep" {
		t.Errorf("empty 200: err=%v out=%+v, want untouched", err, out)
	}
	_, err := c.Get(ctx, "/v4/x", nil, out)
	if err == nil || !strings.HasPrefix(err.Error(), "decode GET /v4/x: ") {
		t.Errorf("non-JSON 2xx: err = %v", err)
	}
	resp, err := c.Get(ctx, "/v4/x", nil, out)
	if err != nil || out.ID != "c1" || resp.ETag() != "4" || resp.Status != 200 {
		t.Errorf("success: err=%v out=%+v etag=%q", err, out, resp.ETag())
	}
}

func TestWritesAccepted(t *testing.T) {
	srv, _ := scripted(t, status(201), okJSON(`{}`), status(500), status(204))
	c := testClient(t, srv.URL, engine.NewFakeClock(time.Time{}), WithMaxAttempts(1))
	ctx := context.Background()

	_, _ = c.Post(ctx, "/v4/a", nil, nil) // 201: counts
	_, _ = c.Get(ctx, "/v4/a", nil, nil)  // 200 GET: does not count
	_, _ = c.Post(ctx, "/v4/a", nil, nil) // 500: does not count
	_, _ = c.Delete(ctx, "/v4/a", nil)    // 204: counts
	if got := c.WritesAccepted(); got != 2 {
		t.Errorf("WritesAccepted = %d, want 2", got)
	}
}

func TestDebugBodyIsRedacted(t *testing.T) {
	t.Parallel()
	got := redactedBody([]byte(`{"name":"u","password":"hunter2hunter2","nested":{"token":"abcd1234"}}`))
	if strings.Contains(got, "hunter2") || strings.Contains(got, "abcd1234") {
		t.Errorf("redactedBody leaks: %s", got)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got, " body=")), &v); err != nil || v["name"] != "u" {
		t.Errorf("redactedBody not valid JSON with name preserved: %s (%v)", got, err)
	}
	if redactedBody(nil) != "" || redactedBody([]byte("not json")) != "" {
		t.Error("redactedBody should be empty for no or invalid JSON")
	}
}
