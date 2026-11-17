package capellaapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

// TestEndpointsWithoutOrganization checks every endpoint refuses to build a path, and so sends
// nothing, when the client has no organization ID.
func TestEndpointsWithoutOrganization(t *testing.T) {
	t.Parallel()
	srv := newStubServer(t)
	c, err := New("test-key-0000", WithBaseURL(srv.URL), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, call := range contractCalls() {
		t.Run(call.name, func(t *testing.T) {
			err := call.do(ctx, c)
			if call.name == "ListOrganizations" {
				// The organizations list does not need an organization ID.
				return
			}
			if err == nil || !strings.Contains(err.Error(), "organization ID is not set") {
				t.Errorf("err = %v, want organization ID error", err)
			}
		})
	}
	for _, h := range srv.snapshot() {
		if h.Path != "/v4/organizations" {
			t.Errorf("request sent without an organization: %s %s", h.Method, h.Path)
		}
	}
}

// TestEndpointsPropagateHTTPErrors checks every endpoint returns the API error (not a partial
// result) when Capella answers with a non-retryable failure.
func TestEndpointsPropagateHTTPErrors(t *testing.T) {
	t.Parallel()
	srv := newStubServer(t)
	srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"httpStatusCode":400,"code":1000,"message":"bad request"}`)
		return true
	}
	c := newTestClient(t, srv)
	ctx := context.Background()
	for _, call := range contractCalls() {
		t.Run(call.name, func(t *testing.T) {
			err := call.do(ctx, c)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v (%T), want *APIError", err, err)
			}
			if apiErr.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", apiErr.Status)
			}
		})
	}
}

func TestListSnapshotRegionsBodies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name    string
		status  int
		body    string
		want    []string
		wantErr bool
	}{
		{name: "array", status: http.StatusOK, body: `["eu-west-1"]`, want: []string{"eu-west-1"}},
		{name: "envelope", status: http.StatusOK, body: `{"data":["a","b"]}`, want: []string{"a", "b"}},
		{name: "empty body", status: http.StatusOK, body: ``, want: nil},
		{name: "no content", status: http.StatusNoContent, body: ``, want: nil},
		{name: "invalid json", status: http.StatusOK, body: `not json`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newStubServer(t)
			srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
				return true
			}
			got, err := newTestClient(t, srv).ListSnapshotRegions(ctx, tPrj, tClu)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "decode GET") {
					t.Fatalf("err = %v, want decode error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("regions = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateWithUndecodableResponse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newStubServer(t)
	srv.override = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `[not json`)
		return true
	}
	c := newTestClient(t, srv)
	// Optional-ID creates (snapshot) and required-ID creates (project) both report the decode error.
	if id, err := c.CreateSnapshot(ctx, tPrj, tClu, Object{}); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("CreateSnapshot = %q, %v; want decode error", id, err)
	}
	if id, err := c.CreateProject(ctx, "n", ""); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("CreateProject = %q, %v; want decode error", id, err)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyTransportClasses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "x.invalid"}, "dns lookup failed"},
		{"refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), "connection refused"},
		{"reset", fmt.Errorf("read: %w", syscall.ECONNRESET), "connection reset"},
		{"cert verify", &tls.CertificateVerificationError{Err: errors.New("bad")}, "tls error"},
		{"unknown authority", x509.UnknownAuthorityError{}, "tls error"},
		{"hostname", x509.HostnameError{Certificate: &x509.Certificate{}, Host: "h"}, "tls error"},
		{"record header", tls.RecordHeaderError{Msg: "bad"}, "tls error"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"net timeout", timeoutErr{}, "timeout"},
		{"eof", io.EOF, "connection closed"},
		{"unexpected eof", fmt.Errorf("read: %w", io.ErrUnexpectedEOF), "connection closed"},
		{"tls text", errors.New("remote error: tls: handshake failure"), "tls error"},
		{"other", errors.New("boom"), "network error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyTransport(tt.err); got != tt.want {
				t.Errorf("classifyTransport(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
