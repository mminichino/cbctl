package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestForServerAndForCapella(t *testing.T) {
	tests := []struct {
		name      string
		got       Endpoint
		want      Endpoint
		wantAdmin string
		wantQuery string
	}{
		{
			name:      "server plain default ports",
			got:       ForServer("10.0.0.1", false),
			want:      Endpoint{Host: "10.0.0.1", AdminPort: 8091, QueryPort: 8093},
			wantAdmin: "http://10.0.0.1:8091/pools",
			wantQuery: "http://10.0.0.1:8093/query/service",
		},
		{
			name:      "server tls explicit admin port",
			got:       ForServer("db.example.com:9000", true),
			want:      Endpoint{Host: "db.example.com", AdminPort: 9000, QueryPort: 18093, UseSSL: true},
			wantAdmin: "https://db.example.com:9000/pools",
			wantQuery: "https://db.example.com:18093/query/service",
		},
		{
			name:      "server tls default ports",
			got:       ForServer("db.example.com", true),
			want:      Endpoint{Host: "db.example.com", AdminPort: 18091, QueryPort: 18093, UseSSL: true},
			wantAdmin: "https://db.example.com:18091/pools",
			wantQuery: "https://db.example.com:18093/query/service",
		},
		{
			name:      "capella ignores given port",
			got:       ForCapella("cb.abc.cloud.couchbase.com:1234"),
			want:      Endpoint{Host: "cb.abc.cloud.couchbase.com", AdminPort: 18091, QueryPort: 18093, UseSSL: true},
			wantAdmin: "https://cb.abc.cloud.couchbase.com:18091/pools",
			wantQuery: "https://cb.abc.cloud.couchbase.com:18093/query/service",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("endpoint = %+v, want %+v", tt.got, tt.want)
			}
			if got := tt.got.adminURL("/pools"); got != tt.wantAdmin {
				t.Fatalf("adminURL = %q, want %q", got, tt.wantAdmin)
			}
			if got := tt.got.queryURL("/query/service"); got != tt.wantQuery {
				t.Fatalf("queryURL = %q, want %q", got, tt.wantQuery)
			}
		})
	}
}

func TestNewClientDefaults(t *testing.T) {
	c := NewClient()
	if c.HTTP == nil || c.HTTP.Timeout == 0 || c.Timeout == 0 {
		t.Fatalf("client missing timeouts: %+v", c)
	}
}

func TestDoFormRequests(t *testing.T) {
	tests := []struct {
		name     string
		call     func(c *Client, ep Endpoint) error
		method   string
		wantAuth bool
		wantUser string
		wantPass string
	}{
		{
			name: "post form with credentials",
			call: func(c *Client, ep Endpoint) error {
				return c.PostForm(ep, Ptr("Administrator"), Ptr("secret"), "/settings/x", map[string]string{"a": "1", "b": "two words"})
			},
			method: http.MethodPost, wantAuth: true, wantUser: "Administrator", wantPass: "secret",
		},
		{
			name: "put form without credentials",
			call: func(c *Client, ep Endpoint) error {
				return c.PutForm(ep, nil, nil, "/settings/x", map[string]string{"a": "1", "b": "two words"})
			},
			method: http.MethodPut,
		},
		{
			name: "do form user without password",
			call: func(c *Client, ep Endpoint) error {
				_, status, err := c.DoForm(http.MethodPost, ep, Ptr("ro"), nil, "/settings/x", map[string]string{"a": "1", "b": "two words"})
				if status != http.StatusOK {
					t.Errorf("status = %d", status)
				}
				return err
			},
			method: http.MethodPost, wantAuth: true, wantUser: "ro",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got recordedRequest
			ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse form: %v", err)
				}
				got = recordedRequest{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Form: r.PostForm}
				got.User, got.Pass, got.HasAuth = r.BasicAuth()
			})
			if err := tt.call(NewClient(), ep); err != nil {
				t.Fatal(err)
			}
			if got.Method != tt.method || got.Path != "/settings/x" {
				t.Fatalf("request = %s %s", got.Method, got.Path)
			}
			if got.ContentType != "application/x-www-form-urlencoded" {
				t.Fatalf("content type = %q", got.ContentType)
			}
			if got.Form.Get("a") != "1" || got.Form.Get("b") != "two words" {
				t.Fatalf("form = %v", got.Form)
			}
			if got.HasAuth != tt.wantAuth || got.User != tt.wantUser || got.Pass != tt.wantPass {
				t.Fatalf("auth = %v %q %q", got.HasAuth, got.User, got.Pass)
			}
		})
	}
}

func TestDoFormErrors(t *testing.T) {
	t.Run("non-2xx returns body and status", func(t *testing.T) {
		ep := handlerEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "  boom  ", http.StatusBadRequest)
		})
		body, status, err := NewClient().DoForm(http.MethodPost, ep, nil, nil, "/x", nil)
		if err == nil || !strings.Contains(err.Error(), "HTTP 400: boom") {
			t.Fatalf("err = %v", err)
		}
		if status != http.StatusBadRequest || !strings.Contains(string(body), "boom") {
			t.Fatalf("status=%d body=%q", status, body)
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		_, status, err := NewClient().DoForm(http.MethodPost, closedEndpoint(t), nil, nil, "/x", nil)
		if err == nil || status != 0 {
			t.Fatalf("status=%d err=%v", status, err)
		}
	})
	t.Run("invalid url", func(t *testing.T) {
		if err := NewClient().PostForm(Endpoint{Host: "bad host%zz", AdminPort: 1}, nil, nil, "/x", nil); err == nil {
			t.Fatal("expected URL error")
		}
	})
}

func TestDoJSON(t *testing.T) {
	tests := []struct {
		name        string
		payload     any
		user, pass  *string
		status      int
		wantBody    string
		wantCT      string
		wantAuth    bool
		wantErrText string
	}{
		{name: "nil payload no content type", status: http.StatusOK, user: Ptr("u")},
		{
			name: "json payload", payload: map[string]any{"groups": []string{"a"}}, user: Ptr("u"), pass: Ptr("p"),
			status: http.StatusOK, wantBody: `{"groups":["a"]}`, wantCT: "application/json", wantAuth: true,
		},
		{
			name: "server error", payload: map[string]int{"n": 1}, status: http.StatusConflict,
			wantBody: `{"n":1}`, wantCT: "application/json", wantErrText: "HTTP 409",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got recordedRequest
			ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
				buf := new(bytes.Buffer)
				if _, err := buf.ReadFrom(r.Body); err != nil {
					t.Errorf("read: %v", err)
				}
				got = recordedRequest{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, ContentType: r.Header.Get("Content-Type"), Body: buf.String()}
				got.User, got.Pass, got.HasAuth = r.BasicAuth()
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"ok":true}`)) // response write errors would surface as client errors
			})
			body, status, err := NewClient().DoJSON(http.MethodPut, ep, tt.user, tt.pass, "/pools/default/serverGroups?rev=7", tt.payload)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("err = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if status != tt.status || string(body) != `{"ok":true}` {
				t.Fatalf("status=%d body=%q", status, body)
			}
			if got.Method != http.MethodPut || got.Path != "/pools/default/serverGroups" || got.RawQuery != "rev=7" {
				t.Fatalf("request = %s %s?%s", got.Method, got.Path, got.RawQuery)
			}
			if got.ContentType != tt.wantCT || got.Body != tt.wantBody {
				t.Fatalf("content-type=%q body=%q", got.ContentType, got.Body)
			}
			if tt.user != nil && (!got.HasAuth || got.User != *tt.user) {
				t.Fatalf("auth = %v %q", got.HasAuth, got.User)
			}
			if tt.user == nil && got.HasAuth {
				t.Fatal("unexpected auth")
			}
			if tt.pass != nil && got.Pass != *tt.pass {
				t.Fatalf("pass = %q", got.Pass)
			}
		})
	}
}

func TestDoJSONErrors(t *testing.T) {
	c := NewClient()
	if _, _, err := c.DoJSON(http.MethodPut, closedEndpoint(t), nil, nil, "/x", map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("expected marshal error")
	}
	if _, _, err := c.DoJSON(http.MethodPut, Endpoint{Host: "bad host%zz"}, nil, nil, "/x", nil); err == nil {
		t.Fatal("expected URL error")
	}
	if err := c.PutJSON(closedEndpoint(t), nil, nil, "/x", map[string]int{"a": 1}); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestPutJSONSuccess(t *testing.T) {
	var decoded map[string]int
	ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("decode: %v", err)
		}
	})
	if err := NewClient().PutJSON(ep, Ptr("u"), Ptr("p"), "/x", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if decoded["a"] != 1 {
		t.Fatalf("decoded = %v", decoded)
	}
}

func TestGetJSON(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		dest     bool
		user     *string
		wantErr  string
		wantAuth bool
	}{
		{name: "decode", status: http.StatusOK, body: `{"a":"b"}`, dest: true, user: Ptr("u"), wantAuth: true},
		{name: "nil dest skips decode", status: http.StatusOK, body: `not json`},
		{name: "bad json", status: http.StatusOK, body: `{`, dest: true, wantErr: "unexpected end"},
		{name: "unauthorized", status: http.StatusUnauthorized, body: "denied", dest: true, wantErr: "HTTP 401: denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hasAuth bool
			var pass string
			ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
				_, pass, hasAuth = r.BasicAuth()
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body)) // response write errors would surface as client errors
			})
			var dest map[string]string
			var target any
			if tt.dest {
				target = &dest
			}
			status, err := NewClient().GetJSON(ep, tt.user, nil, "/x", target)
			if status != tt.status {
				t.Fatalf("status = %d", status)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if hasAuth != tt.wantAuth || pass != "" {
				t.Fatalf("auth = %v pass=%q", hasAuth, pass)
			}
			if tt.dest && dest["a"] != "b" {
				t.Fatalf("dest = %v", dest)
			}
		})
	}
	t.Run("connection refused", func(t *testing.T) {
		if _, err := NewClient().GetJSON(closedEndpoint(t), nil, nil, "/x", nil); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid url", func(t *testing.T) {
		if _, err := NewClient().GetJSON(Endpoint{Host: "bad host%zz"}, nil, nil, "/x", nil); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestPostQueryForm(t *testing.T) {
	t.Run("posts statement to query port", func(t *testing.T) {
		f := newFakeCB(t)
		ep := f.endpoint()
		ep.AdminPort = 1 // must not be used
		if err := NewClient().PostQueryForm(ep, Ptr("Administrator"), Ptr("password"), "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		reqs := f.requests(http.MethodPost, "/query/service")
		if len(reqs) != 1 {
			t.Fatalf("query requests = %d", len(reqs))
		}
		if reqs[0].Form.Get("statement") != "SELECT 1" || reqs[0].User != "Administrator" || reqs[0].Pass != "password" {
			t.Fatalf("request = %+v", reqs[0])
		}
	})
	t.Run("user without password", func(t *testing.T) {
		f := newFakeCB(t)
		if err := NewClient().PostQueryForm(f.endpoint(), Ptr("u"), nil, "SELECT 2"); err != nil {
			t.Fatal(err)
		}
		if r := f.requests(http.MethodPost, "/query/service"); len(r) != 1 || !r[0].HasAuth || r[0].Pass != "" {
			t.Fatalf("requests = %+v", r)
		}
	})
	t.Run("non-2xx", func(t *testing.T) {
		f := newFakeCB(t)
		f.setFail("POST /query/service", http.StatusServiceUnavailable)
		err := NewClient().PostQueryForm(f.endpoint(), nil, nil, "SELECT 1")
		if err == nil || !strings.Contains(err.Error(), "HTTP 503: forced failure") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		if err := NewClient().PostQueryForm(closedEndpoint(t), nil, nil, "SELECT 1"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid url", func(t *testing.T) {
		if err := NewClient().PostQueryForm(Endpoint{Host: "bad host%zz"}, nil, nil, "SELECT 1"); err == nil {
			t.Fatal("expected error")
		}
	})
}
