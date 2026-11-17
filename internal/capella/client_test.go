package capella

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config"
)

func TestNewClientDefaults(t *testing.T) {
	c := NewClient("tok", "")
	if c.APIHost != config.DefaultCapellaAPIHost {
		t.Errorf("APIHost = %q, want %q", c.APIHost, config.DefaultCapellaAPIHost)
	}
	if c.ProjectName != config.DefaultCapellaProjectName {
		t.Errorf("ProjectName = %q, want %q", c.ProjectName, config.DefaultCapellaProjectName)
	}
	if got := NewClient("tok", "api.example.com").APIHost; got != "api.example.com" {
		t.Errorf("APIHost = %q, want api.example.com", got)
	}
}

func TestNewClientFromConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		if _, err := NewClientFromConfig(nil); err == nil {
			t.Fatal("want an error for a nil config")
		}
	})
	t.Run("missing token", func(t *testing.T) {
		_, err := NewClientFromConfig(config.New())
		if err == nil || !strings.Contains(err.Error(), "API key not set") {
			t.Fatalf("err = %v, want API key not set", err)
		}
	})
	t.Run("all properties", func(t *testing.T) {
		cfg := config.New()
		props := map[string]string{
			config.PropCapellaToken:      "tok",
			config.PropCapellaAPIHost:    "api.example.com",
			config.PropCapellaOrgName:    "acme",
			config.PropCapellaOrgID:      "o1",
			config.PropCapellaProject:    "proj",
			config.PropCapellaProjectID:  "p1",
			config.PropCapellaDatabase:   "db",
			config.PropCapellaDatabaseID: "d1",
			config.PropCapellaUserEmail:  "a@b.c",
			config.PropCapellaUserID:     "u1",
		}
		for k, v := range props {
			cfg.SetProp(k, v)
		}
		c, err := NewClientFromConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"tok", "api.example.com", "acme", "o1", "proj", "p1", "db", "d1", "a@b.c", "u1"}
		got := []string{c.Token, c.APIHost, c.OrganizationName, c.OrganizationID, c.ProjectName, c.ProjectID,
			c.DatabaseName, c.DatabaseID, c.AccountEmail, c.AccountID}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("client fields = %v\nwant           %v", got, want)
		}
		if !c.hasOrganizationName() {
			t.Error("hasOrganizationName = false, want true")
		}
	})
}

func TestClientContext(t *testing.T) {
	var nilClient *Client
	if nilClient.context() == nil {
		t.Fatal("nil client context is nil")
	}
	nilClient.SetContext(context.Background()) // must not panic
	c := NewClient("tok", "")
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "v")
	c.SetContext(nil) //nolint:staticcheck // nil is ignored, the case under test
	if c.context().Value(key{}) != nil {
		t.Fatal("unexpected context value before SetContext")
	}
	c.SetContext(ctx)
	if c.context().Value(key{}) != "v" {
		t.Fatal("SetContext did not take effect")
	}
	if nilClient.hasOrganizationName() {
		t.Error("nil client hasOrganizationName = true")
	}
}

func TestClientEmptyTokenFails(t *testing.T) {
	f := newFakeAPI(t)
	c := f.client()
	c.Token = ""
	if err := c.GetJSON("/v4/x", nil); err == nil {
		t.Fatal("want an error for an empty API key")
	}
	if _, err := c.GetPaged("/v4/x", 0); err == nil {
		t.Fatal("GetPaged: want an error for an empty API key")
	}
}

func TestClientJSONMethods(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	tests := []struct {
		method string
		call   func(c *Client, body, dest any) error
	}{
		{http.MethodPost, func(c *Client, b, d any) error { return c.PostJSON("/v4/thing", b, d) }},
		{http.MethodPut, func(c *Client, b, d any) error { return c.PutJSON("/v4/thing", b, d) }},
		{http.MethodPatch, func(c *Client, b, d any) error { return c.PatchJSON("/v4/thing", b, d) }},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			f := newFakeAPI(t)
			f.handle(tt.method, "/v4/thing", func(_ *http.Request, body []byte) (int, any) {
				var in payload
				if err := json.Unmarshal(body, &in); err != nil {
					t.Errorf("decode body: %v", err)
				}
				return http.StatusOK, map[string]any{"id": "echo-" + in.Name}
			})
			var out IDResponse
			if err := tt.call(f.client(), payload{Name: "x"}, &out); err != nil {
				t.Fatalf("%s: %v", tt.method, err)
			}
			if out.ID != "echo-x" {
				t.Errorf("decoded id = %q, want echo-x", out.ID)
			}
			calls := f.callsTo(tt.method, "/v4/thing")
			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}
			if got := calls[0].Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q", got)
			}
			if got := calls[0].Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
		})
	}
}

func TestClientDoJSONErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     any
		wantCode int
		wantMsg  string
		notFound bool
	}{
		{"not found", http.StatusNotFound, errBody(404), 404, "not found", true},
		{"bad request", http.StatusBadRequest, errBody(400), 400, "Capella API error", false},
		{"forbidden", http.StatusForbidden, errBody(403), 403, "Capella API error", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.reply(http.MethodDelete, "/v4/thing", tt.status, tt.body)
			c := f.client()
			err := c.Delete("/v4/thing")
			var api *CapellaAPIError
			if !AsCapellaAPI(err, &api) {
				t.Fatalf("err = %v, want *CapellaAPIError", err)
			}
			if api.Code != tt.wantCode || api.Message != tt.wantMsg {
				t.Errorf("api error = %d %q, want %d %q", api.Code, api.Message, tt.wantCode, tt.wantMsg)
			}
			if IsNotFound(err) != tt.notFound {
				t.Errorf("IsNotFound = %v, want %v", IsNotFound(err), tt.notFound)
			}
			if c.lastStatus != tt.status || !strings.Contains(c.lastBody, http.StatusText(tt.status)) {
				t.Errorf("last = %d %q", c.lastStatus, c.lastBody)
			}
			var inner *capellaapi.APIError
			if !errors.As(err, &inner) {
				t.Error("cause *capellaapi.APIError is not unwrappable")
			}
		})
	}
	t.Run("bad JSON is a decode error, not an API error", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, "/v4/thing", http.StatusOK, "{not json")
		var out IDResponse
		err := f.client().GetJSON("/v4/thing", &out)
		var api *CapellaAPIError
		if err == nil || AsCapellaAPI(err, &api) {
			t.Fatalf("err = %v, want a plain decode error", err)
		}
	})
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("x"), false},
		{"capellaapi sentinel", fmt.Errorf("wrap: %w", capellaapi.ErrNotFound), true},
		{"api 404", apiError(404, "", "m", nil), true},
		{"api 500", apiError(500, "", "m", nil), false},
		{"not found type", fmt.Errorf("wrap: %w", notFound("gone")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
	if AsCapellaAPI(errors.New("x"), nil) || AsCapellaNotFound(errors.New("x"), nil) {
		t.Error("nil target must report false")
	}
}

func TestGetPaged(t *testing.T) {
	t.Run("single page", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, "/v4/list", 200, listPage(map[string]any{"id": "a"}, map[string]any{"id": "b"}))
		items, err := f.client().GetPaged("/v4/list", 0)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodePaged[IDResponse](items)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			t.Errorf("items = %+v", got)
		}
	})
	t.Run("404 is nil without error", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, "/v4/list", 404, errBody(404))
		items, err := f.client().GetPaged("/v4/list", 10)
		if err != nil || items != nil {
			t.Fatalf("GetPaged = %v, %v; want nil, nil", items, err)
		}
	})
	t.Run("API error is returned without the legacy fallback", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, "/v4/list", 400, errBody(400))
		if _, err := f.client().GetPaged("/v4/list", 10); err == nil {
			t.Fatal("want an error for HTTP 400")
		}
		if n := f.countCalls(http.MethodGet, "/v4/list"); n != 1 {
			t.Errorf("GETs = %d, want 1", n)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, "/v4/list", 200, listPage())
		c := f.client()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.SetContext(ctx)
		if _, err := c.GetPaged("/v4/list", 10); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// legacyEnvelope is a list page whose cursor.pages.next is a string: capellaapi.Paginate cannot
// decode it, so GetPaged falls back to the page/last loop.
func legacyEnvelope(pageNo, last, total int, ids ...string) map[string]any {
	data := []any{}
	for _, id := range ids {
		data = append(data, map[string]any{"id": id})
	}
	return map[string]any{"data": data, "cursor": map[string]any{"pages": map[string]any{
		"page": pageNo, "last": last, "totalItems": total, "next": "opaque",
	}}}
}

func TestGetPagedLegacyFallback(t *testing.T) {
	pageOf := func(r *http.Request) string { return r.URL.Query().Get("page") }
	t.Run("walks page/last", func(t *testing.T) {
		f := newFakeAPI(t)
		f.handle(http.MethodGet, "/v4/list", func(r *http.Request, _ []byte) (int, any) {
			if pageOf(r) == "2" {
				return 200, legacyEnvelope(2, 2, 0, "c")
			}
			return 200, legacyEnvelope(1, 2, 0, "a", "b")
		})
		items, err := f.client().GetPaged("/v4/list", 2)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := DecodePaged[IDResponse](items)
		if len(got) != 3 || got[2].ID != "c" {
			t.Errorf("items = %+v, want a b c", got)
		}
		var legacyPerPage bool
		for _, c := range f.callsTo(http.MethodGet, "/v4/list") {
			q, _ := url.ParseQuery(c.Query)
			if q.Get("perPage") == "2" {
				legacyPerPage = true
			}
		}
		if !legacyPerPage {
			t.Error("legacy loop did not send perPage=2")
		}
	})
	t.Run("derives last from totalItems", func(t *testing.T) {
		f := newFakeAPI(t)
		f.handle(http.MethodGet, "/v4/list", func(r *http.Request, _ []byte) (int, any) {
			if pageOf(r) == "2" {
				return 200, legacyEnvelope(2, 0, 3, "c")
			}
			return 200, legacyEnvelope(1, 0, 3, "a", "b")
		})
		items, err := f.client().GetPaged("/v4/list", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 3 {
			t.Errorf("items = %d, want 3", len(items))
		}
	})
	t.Run("no totals stops after the first page", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, "/v4/list", 200, legacyEnvelope(1, 0, 0, "a"))
		items, err := f.client().GetPaged("/v4/list", 2)
		if err != nil || len(items) != 1 {
			t.Fatalf("GetPaged = %d items, %v; want 1, nil", len(items), err)
		}
	})
	t.Run("legacy 404 is nil", func(t *testing.T) {
		f := newFakeAPI(t)
		f.handle(http.MethodGet, "/v4/list", func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("perPage") == "7" {
				return 404, errBody(404)
			}
			return 200, legacyEnvelope(1, 1, 0, "a")
		})
		items, err := f.client().GetPaged("/v4/list", 7)
		if err != nil || items != nil {
			t.Fatalf("GetPaged = %v, %v; want nil, nil", items, err)
		}
	})
	t.Run("legacy error is returned", func(t *testing.T) {
		f := newFakeAPI(t)
		f.handle(http.MethodGet, "/v4/list", func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("perPage") == "7" {
				return 400, errBody(400)
			}
			return 200, legacyEnvelope(1, 1, 0, "a")
		})
		if _, err := f.client().GetPaged("/v4/list", 7); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("bad path", func(t *testing.T) {
		f := newFakeAPI(t)
		if _, err := f.client().getPagedLegacy("%zz", 1); err == nil {
			t.Fatal("want a parse error")
		}
	})
}

func TestDecodePagedBadItem(t *testing.T) {
	if _, err := DecodePaged[IDResponse]([]json.RawMessage{json.RawMessage(`"str"`)}); err == nil {
		t.Fatal("want a decode error")
	}
}

func TestErrorStrings(t *testing.T) {
	var nilAPI *CapellaAPIError
	var nilNF *CapellaNotFoundError
	var nilUNC *UserNotConfiguredError
	cause := errors.New("cause")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"api nil", nilAPI, "capella api error"},
		{"api body", &CapellaAPIError{Code: 400, Body: "b", Message: "m"}, "m (HTTP 400): b"},
		{"api code", &CapellaAPIError{Code: 500, Message: "m"}, "m (HTTP 500)"},
		{"api message", &CapellaAPIError{Message: "m"}, "m"},
		{"not found nil", nilNF, "capella resource not found"},
		{"not found empty", &CapellaNotFoundError{}, "capella resource not found"},
		{"not found msg", notFound("gone"), "gone"},
		{"unc nil", nilUNC, "Capella user not configured"},
		{"unc msg", &UserNotConfiguredError{Message: "x"}, "x"},
		{"invalid storage", fmtInvalidStorage(9), "invalid storage value (HTTP 0): 9"},
		{"unsupported cloud", fmtUnsupportedCloud("oci"), "unsupported cloud type (HTTP 0): oci"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
	if nilAPI.Unwrap() != nil {
		t.Error("nil Unwrap != nil")
	}
	if !errors.Is(apiError(1, "", "m", cause), cause) {
		t.Error("apiError does not unwrap to its cause")
	}
}
