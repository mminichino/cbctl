package capellaapi

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
)

// P-TST-14: secrets never leave the transport layer in debug logs, API errors or transport
// errors.

const transportKey = "sk-transport-KEY-7781"

func captureDebug(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevErr := logging.Outputs()
	logging.SetOutputs(&buf, &buf)
	logging.SetLevel(logging.LevelDebug)
	t.Cleanup(func() {
		logging.SetOutputs(prevOut, prevErr)
		logging.SetLevel(logging.LevelInfo)
	})
	return &buf
}

func TestTransportRedaction_DebugLogAndAPIError(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	const password = "Hunter2-transport-pw"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A server that echoes secrets back must not leak them through the client.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":4001,"message":"bad password ` + password + ` for key ` + transportKey + `","hint":"key ` + transportKey + `"}`))
	}))
	t.Cleanup(srv.Close)
	logs := captureDebug(t)

	c, err := New(transportKey, WithBaseURL(srv.URL), WithOrgID("org-1"), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.CreateCredential(context.Background(), "p1", "c1", CreateCredentialRequest{Name: "app", Password: password})
	if err == nil {
		t.Fatal("expected an API error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("err = %v", err)
	}
	for _, where := range []string{err.Error(), apiErr.Message, apiErr.Hint, apiErr.Body, logs.String()} {
		if strings.Contains(where, password) || strings.Contains(where, transportKey) {
			t.Errorf("secret leaked: %s", where)
		}
	}
	if !strings.Contains(logs.String(), `"password":"***"`) {
		t.Errorf("debug log has no masked body: %s", logs.String())
	}
}

func TestTransportRedaction_TransportError(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	// A listener that is closed at once: every attempt fails to connect.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	logs := captureDebug(t)
	redact.Register("127.0.0.1")
	c, err := New(transportKey, WithBaseURL("http://"+addr), WithOrgID("org-1"), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListProjects(context.Background())
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v", err)
	}
	if te.Unwrap() == nil {
		t.Error("transport error lost its cause")
	}
	for _, where := range []string{err.Error(), logs.String()} {
		if strings.Contains(where, transportKey) || strings.Contains(where, "127.0.0.1") {
			t.Errorf("secret leaked: %s", where)
		}
	}
	// The masked cause keeps the chain for errors.Is / errors.As.
	cause := redactErr(errors.New("dial 127.0.0.1: refused"))
	var me *maskedError
	if !errors.As(cause, &me) || strings.Contains(cause.Error(), "127.0.0.1") || errors.Unwrap(cause) == nil {
		t.Errorf("redactErr = %v", cause)
	}
	plain := errors.New("nothing secret")
	if redactErr(plain) != plain {
		t.Error("redactErr wrapped an error with nothing to mask")
	}
}
