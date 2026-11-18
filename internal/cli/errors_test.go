package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
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
		{"changes", &CLIError{Code: ExitChanges}, ExitChanges, ""},
		{"cli", Errorf(ExitConfigInvalid, "fix it", "bad config sk-secret-key-9999"), ExitConfigInvalid, "***"},
		{"api", &capellaapi.APIError{Status: 404, Message: "missing", Method: "GET", Path: "/x"}, ExitError, "404"},
		{"transport", &capellaapi.TransportError{Msg: "down"}, ExitError, "down"},
		{"ambiguous", &capellaapi.AmbiguousWriteError{Method: "POST", Path: "/x", Cause: errors.New("eof")}, ExitError, "re-run"},
		{"wait timeout", &engine.WaitTimeout{Name: "c", State: "deploying"}, ExitTimeout, "continues"},
		{"config", capellafile.NewConfigError("capella.yaml", []capellafile.Issue{{Where: "cluster c", Message: "bad", Line: 3}}), ExitConfigInvalid, "capella.yaml has 1 problem"},
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
			if tc.name == "changes" && out != "" {
				t.Fatalf("a bare exit code must print nothing, got %q", out)
			}
			if tc.substr != "" && !strings.Contains(out, tc.substr) {
				t.Fatalf("stderr=%q want substr %q", out, tc.substr)
			}
		})
	}
}

func TestHandleErrorConfigError(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	var buf bytes.Buffer
	SetErrorWriter(&buf)
	t.Cleanup(func() { SetErrorWriter(nil) })

	ce := capellafile.NewConfigError("capella.yaml", []capellafile.Issue{
		{Where: "project p › cluster c › support.plan", Message: "must be one of: basic", Line: 11, Fix: "pick one"},
		{Where: "apiVersion", Message: "missing required field 'apiVersion'", Line: 1},
	})
	code := HandleError(fmt.Errorf("loading: %w", ce))
	if code != ExitConfigInvalid {
		t.Fatalf("code = %d, want %d", code, ExitConfigInvalid)
	}
	want := "[error] - capella.yaml has 2 problems:\n" +
		"  apiVersion (line 1)\n    missing required field 'apiVersion'\n" +
		"  project p › cluster c › support.plan (line 11)\n    must be one of: basic\n    action: pick one\n" +
		"  -> " + capellafile.FixProblems + "\n"
	if buf.String() != want {
		t.Errorf("stderr =\n%q\nwant\n%q", buf.String(), want)
	}
}
