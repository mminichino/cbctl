package cli_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// panicTransport panics on the first request with a value that carries the request's API key
// and the canary password, as a careless bug might.
type panicTransport struct{ canary string }

func (p panicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	panic(fmt.Sprintf("boom: auth=%q canary=%s", req.Header.Get("Authorization"), p.canary))
}

// TestRedaction_CAP008_AC5_UncaughtExceptionRedacted (P-TST-13): a panic inside a command is
// recovered by cli.Run, printed as an internal error with an action, redacted (no API key, no
// registered secret, also in the --log-level debug stack) and exits 1.
func TestRedaction_CAP008_AC5_UncaughtExceptionRedacted(t *testing.T) {
	for _, args := range [][]string{
		{"capella", "plan"},
		{"capella", "--log-level", "debug", "plan"},
		{"capella", "project", "list"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newScenario(t)
			s.Env["APP_PW"] = canaryPassword
			t.Setenv("APP_PW", canaryPassword)
			writeCapellaYAML(t, s, credentialDoc)
			s.HTTPClient = &http.Client{Transport: panicTransport{canary: canaryPassword}}
			r := s.Run(args...)
			wantExit(t, r, cli.ExitError)
			wantContains(t, r.Stderr, "[error] - internal error: boom: auth=",
				"  → Run the command again with --log-level debug to see more")
			secrets := []string{s.Fake.APIKey, canaryPassword}
			if args[1] == "project" {
				secrets = secrets[:1] // project list never reads capella.yaml, so APP_PW is not a known secret
			}
			clitest.AssertNoSecret(t, r, secrets...)
			if args[1] == "--log-level" {
				wantContains(t, r.Stderr, "goroutine ")
			}
		})
	}
}

// TestRedaction_CAP008_AC1_DebugAndRetryRedacted: declarative commands at --log-level debug,
// including a failing GET that is retried, never print the API key or the canary password.
func TestRedaction_CAP008_AC1_DebugAndRetryRedacted(t *testing.T) {
	s := newScenario(t)
	s.Env["APP_PW"] = canaryPassword
	t.Setenv("APP_PW", canaryPassword)
	writeCapellaYAML(t, s, credentialDoc)
	mustScenario(t, s.Run("capella", "--log-level", "debug", "apply", "--yes"))
	s.Fake.FailNext("GET", `/clusters$`, 503, 1)
	for _, args := range [][]string{
		{"capella", "--log-level", "debug", "plan"},
		{"capella", "--log-level", "debug", "validate"},
		{"capella", "--log-level", "debug", "-o", "json", "plan"},
		{"capella", "--log-level", "debug", "drift"},
	} {
		r := mustScenario(t, s.Run(args...))
		clitest.AssertNoSecret(t, r, s.Fake.APIKey, canaryPassword)
	}
	if n := s.Fake.Count("GET", `/clusters$`); n < 2 {
		t.Fatalf("injected 503 was not retried (%d cluster list GETs)", n)
	}
}
