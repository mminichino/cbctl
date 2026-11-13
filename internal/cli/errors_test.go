package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

func TestHandleError(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	redact.Register("sk-secret-key-9999")

	cases := []struct {
		name   string
		err    error
		code   ExitCode
		substr string
	}{
		{"nil", nil, ExitOK, ""},
		{"canceled", context.Canceled, ExitInterrupted, ""},
		{"cli", Errorf(ExitConfigInvalid, "fix it", "bad config sk-secret-key-9999"), ExitConfigInvalid, "***"},
		{"api", &capellaapi.APIError{Status: 404, Message: "missing", Method: "GET", Path: "/x"}, ExitError, "404"},
		{"transport", &capellaapi.TransportError{Msg: "down"}, ExitError, "down"},
		{"ambiguous", &capellaapi.AmbiguousWriteError{Method: "POST", Path: "/x", Cause: errors.New("eof")}, ExitError, "re-run"},
		{"wait timeout", &engine.WaitTimeout{Name: "c", State: "deploying"}, ExitTimeout, "continues"},
		{"wait failed", &engine.WaitFailed{Name: "c", State: "deploymentFailed"}, ExitError, "activity log"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			SetErrorWriter(&buf)
			t.Cleanup(func() { SetErrorWriter(nil) })
			code := HandleError(tc.err)
			if code != tc.code {
				t.Fatalf("code=%d want %d stderr=%q", code, tc.code, buf.String())
			}
			out := buf.String()
			if strings.Contains(out, "sk-secret-key-9999") {
				t.Fatalf("secret leaked: %q", out)
			}
			if tc.substr != "" && !strings.Contains(out, tc.substr) {
				t.Fatalf("stderr=%q want substr %q", out, tc.substr)
			}
		})
	}
}
