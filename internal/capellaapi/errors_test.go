package capellaapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
)

func TestAPIErrorIsSentinels(t *testing.T) {
	t.Parallel()
	sentinels := []error{ErrUnauthorized, ErrForbidden, ErrNotFound, ErrConflict,
		ErrPreconditionFailed, ErrValidation, ErrRateLimited, ErrServer}
	tests := []struct {
		status int
		want   error
	}{
		{400, ErrValidation},
		{401, ErrUnauthorized},
		{403, ErrForbidden},
		{404, ErrNotFound},
		{409, ErrConflict},
		{412, ErrPreconditionFailed},
		{422, ErrValidation},
		{429, ErrRateLimited},
		{500, ErrServer},
		{503, ErrServer},
		{418, nil},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			t.Parallel()
			var err error = newAPIError("GET", "/v4/x", tt.status, []byte(`{"message":"m"}`))
			for _, s := range sentinels {
				if got, want := errors.Is(err, s), s == tt.want; got != want {
					t.Errorf("errors.Is(%d, %v) = %v, want %v", tt.status, s, got, want)
				}
			}
			wrapped := fmt.Errorf("outer: %w", err)
			var apiErr *APIError
			if !errors.As(wrapped, &apiErr) || apiErr.Status != tt.status {
				t.Errorf("errors.As failed for wrapped %d", tt.status)
			}
		})
	}
}

func TestAPIErrorParsing(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 300)
	tests := []struct {
		name        string
		status      int
		body        string
		wantMsg     string
		wantHint    string
		wantCode    int
		wantErrText string
	}{
		{
			name: "capella envelope", status: 409,
			body:    `{"httpStatusCode":409,"code":4002,"message":"name taken","hint":"pick another"}`,
			wantMsg: "name taken", wantHint: "pick another", wantCode: 4002,
			wantErrText: "POST /v4/p: 409 name taken (hint: pick another)",
		},
		{
			name: "no hint", status: 404, body: `{"code":4025,"message":"gone"}`,
			wantMsg: "gone", wantCode: 4025, wantErrText: "POST /v4/p: 404 gone",
		},
		{
			name: "html proxy body", status: 502, body: "<html>\nBad\r\nGateway</html>",
			wantMsg:     "HTTP 502: <html> Bad Gateway</html>",
			wantErrText: "POST /v4/p: 502 HTTP 502: <html> Bad Gateway</html>",
		},
		{
			name: "json without message", status: 500, body: `{"code":1}`,
			wantMsg: `HTTP 500: {"code":1}`, wantCode: 1,
			wantErrText: `POST /v4/p: 500 HTTP 500: {"code":1}`,
		},
		{
			name: "empty body", status: 503, body: "",
			wantMsg: "HTTP 503", wantErrText: "POST /v4/p: 503 HTTP 503",
		},
		{
			name: "fallback truncated to 200", status: 500, body: long,
			wantMsg:     "HTTP 500: " + long[:200],
			wantErrText: "POST /v4/p: 500 HTTP 500: " + long[:200],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newAPIError("POST", "/v4/p", tt.status, []byte(tt.body))
			if e.Message != tt.wantMsg || e.Hint != tt.wantHint || e.Code != tt.wantCode {
				t.Errorf("got message=%q hint=%q code=%d", e.Message, e.Hint, e.Code)
			}
			if e.Error() != tt.wantErrText {
				t.Errorf("Error() = %q, want %q", e.Error(), tt.wantErrText)
			}
			if e.Method != "POST" || e.Path != "/v4/p" || e.Status != tt.status {
				t.Errorf("unexpected metadata %+v", e)
			}
		})
	}
}

func TestAPIErrorBodyTruncated(t *testing.T) {
	t.Parallel()
	// Multi-byte runes straddling the 2 KiB boundary must not be split.
	body := strings.Repeat("é", 2000)
	e := newAPIError("GET", "/v4/x", 500, []byte(body))
	if len(e.Body) > 2048 || !strings.HasPrefix(body, e.Body) {
		t.Fatalf("Body len=%d, want a rune-aligned prefix of at most 2048 bytes", len(e.Body))
	}
	if len(e.Body) < 2046 {
		t.Errorf("Body unexpectedly short: %d", len(e.Body))
	}
}

func TestAPIErrorHints(t *testing.T) {
	t.Parallel()
	const keyHint = "use the API key token (not the key ID) and check the key's IP allow-list"
	tests := []struct {
		name, method, path string
		status             int
		body               string
		wantHint           string
	}{
		{"401 key hint", "GET", "/v4/organizations", 401, `{"message":"bad key"}`, keyHint},
		{"403 known role", "POST", "/v4/organizations/o/projects", 403, `{"message":"no"}`,
			"requires: Organization Owner or Project Creator"},
		{"403 cluster role", "GET", "/v4/organizations/o/projects/p/clusters/c", 403, `{"message":"no"}`,
			"requires: Organization Owner, Project Owner or Project Manager"},
		{"403 capella hint wins", "POST", "/v4/organizations/o/projects", 403, `{"message":"no","hint":"ask admin"}`, "ask admin"},
		{"403 unknown endpoint", "GET", "/v4/other", 403, `{"message":"no"}`, ""},
		{"404 no hint", "GET", "/v4/organizations", 404, `{"message":"no"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newAPIError(tt.method, tt.path, tt.status, []byte(tt.body))
			if e.Hint != tt.wantHint {
				t.Errorf("Hint = %q, want %q", e.Hint, tt.wantHint)
			}
			if tt.wantHint != "" && !strings.Contains(e.Error(), "(hint: "+tt.wantHint+")") {
				t.Errorf("Error() %q lacks hint", e.Error())
			}
		})
	}
}

func TestAPIErrorMasksSecrets(t *testing.T) {
	const secret = "sk-errors-test-SECRET-7731"
	redact.Register(secret)

	e := newAPIError("GET", "/v4/x", 400, []byte(`{"message":"bad token `+secret+`","hint":"try `+secret+`"}`))
	for name, s := range map[string]string{"Message": e.Message, "Hint": e.Hint, "Body": e.Body, "Error": e.Error()} {
		if strings.Contains(s, secret) {
			t.Errorf("%s leaks the secret: %q", name, s)
		}
	}

	// A secret registered after the error was built is still masked by Error().
	const late = "late-registered-secret-9921"
	e2 := &APIError{Status: 500, Method: "GET", Path: "/v4/x", Message: "boom " + late}
	redact.Register(late)
	if strings.Contains(e2.Error(), late) {
		t.Errorf("Error() leaks late secret: %q", e2.Error())
	}
	if (&TransportError{Msg: "x " + secret}).Error() == "x "+secret {
		t.Errorf("TransportError leaks the secret")
	}
	if strings.Contains((&AmbiguousWriteError{Method: "POST", Path: "/p", Cause: errors.New(secret)}).Error(), secret) {
		t.Errorf("AmbiguousWriteError leaks the secret")
	}
}

func TestAPIErrorFromClientMasksKey(t *testing.T) {
	const key = "sk-client-test-KEY-5512"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A misbehaving upstream echoing the credential back.
		w.WriteHeader(401)
		_, _ = io.WriteString(w, r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	c, err := New(key, WithBaseURL(srv.URL), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "/v4/organizations", nil, nil)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("not an *APIError")
	}
	if strings.Contains(apiErr.Error(), key) || strings.Contains(apiErr.Body, key) {
		t.Errorf("key leaked: %q / %q", apiErr.Error(), apiErr.Body)
	}
}

func TestTransportAndAmbiguousUnwrap(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")
	te := &TransportError{Msg: "GET /v4/x: could not reach Capella (timeout)", Cause: cause}
	if te.Error() != te.Msg || !errors.Is(te, cause) || errors.Unwrap(te) != cause {
		t.Errorf("TransportError: Error=%q unwrap=%v", te.Error(), errors.Unwrap(te))
	}
	ae := &AmbiguousWriteError{Method: "POST", Path: "/v4/x", Cause: cause}
	if !errors.Is(ae, cause) || errors.Unwrap(ae) != cause {
		t.Errorf("AmbiguousWriteError does not unwrap to its cause")
	}
	if !strings.Contains(ae.Error(), "POST /v4/x") || !strings.Contains(ae.Error(), "boom") {
		t.Errorf("AmbiguousWriteError text = %q", ae.Error())
	}
}

func TestRequiredRoles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method, path, want string
	}{
		{"POST", "/v4/organizations/o/projects", "Organization Owner or Project Creator"},
		{"PUT", "/v4/organizations/o/projects/p", "Organization Owner or Project Owner"},
		{"DELETE", "/v4/organizations/o/projects/p", "Organization Owner or Project Owner"},
		{"GET", "/v4/organizations", "any organization role (Organization Member or above)"},
		{"get", "/v4/organizations/o/projects", "any organization role (Organization Member or above)"},
		{"GET", "/v4/organizations/o/projects/p/clusters/c/buckets/b/backup/schedules",
			"Organization Owner, Project Owner or Project Manager"},
		{"GET", "/v4/organizations/o/projects/p/clusters/c/cloudsnapshotbackupschedule/options",
			"Organization Owner, Project Owner or Project Manager"},
		{"POST", "/v4/organizations/o/projects/p/clusters/c/cloudsnapshotbackups", "Organization Owner or Project Owner"},
		{"GET", "/v4/organizations/o/projects/p/clusters/c/backups/x", "Organization Owner or Project Owner"},
		{"GET", "/v4/organizations/o/projects/p/clusters/c/buckets", "Organization Owner, Project Owner or Project Manager"},
		{"GET", "/v4/organizations/o/projects/p", ""},
		{"GET", "/healthz", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()
			if got := RequiredRoles(tt.method, tt.path); got != tt.want {
				t.Errorf("RequiredRoles = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInventoryWellFormed(t *testing.T) {
	t.Parallel()
	seen := map[Endpoint]bool{}
	for _, e := range Inventory {
		if e.Method == "" || !strings.HasPrefix(e.Path, "/v4/") {
			t.Errorf("malformed endpoint %+v", e)
		}
		if seen[e] {
			t.Errorf("duplicate endpoint %+v", e)
		}
		seen[e] = true
	}
	if len(Inventory) == 0 {
		t.Fatal("empty inventory")
	}
}
