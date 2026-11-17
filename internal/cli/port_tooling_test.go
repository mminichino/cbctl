package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/settings"
)

// failingKeyring is an OS keychain that is not there (a CI runner or a container).
type failingKeyring struct{}

var errNoKeychain = errors.New("no secret service on this machine")

func (failingKeyring) Get(string) (string, error) { return "", errNoKeychain }
func (failingKeyring) Set(string, string) error   { return errNoKeychain }
func (failingKeyring) Delete(string) error        { return errNoKeychain }

// TestConfigure_CAP045_AC5_NoKeychain: without an OS keychain, configure stops with exit 3 and
// says to use CAPELLA_API_KEY instead; it saves no profile and never prints the key. (capellactl
// saved the profile and warned; the difference is in docs/capella/migrating-from-capellactl.md.)
func TestConfigure_CAP045_AC5_NoKeychain(t *testing.T) {
	const key = "smoke-token-0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/organizations" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"org-smoke","name":"Smoke"}],`+
			`"cursor":{"pages":{"page":1,"last":1,"perPage":10,"totalItems":1}}}`)
	}))
	t.Cleanup(srv.Close)
	old := keyringFactory
	keyringFactory = func() settings.Keyring { return failingKeyring{} }
	t.Cleanup(func() { keyringFactory = old })

	home := t.TempDir()
	env := map[string]string{"CBCTL_HOME": home}
	var stdout, stderr bytes.Buffer
	d := Deps{
		Env:       func(k string) string { return env[k] },
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Stdin:     strings.NewReader(key + "\n"),
		Stdout:    &stdout,
		Stderr:    &stderr,
		IsTTY:     func(io.Reader) bool { return false },
		Cwd:       func() (string, error) { return home, nil },
		Home:      home,
		Keyring:   failingKeyring{},
	}
	code := Run(context.Background(), []string{"configure", "--api-key-stdin", "--org-id", "org-smoke",
		"--api-base-url", srv.URL, "--profile", "smoke"}, d)
	out := stdout.String() + stderr.String()
	if code != int(ExitConfigInvalid) {
		t.Fatalf("exit %d, want %d\n%s", code, ExitConfigInvalid, out)
	}
	for _, want := range []string{"no OS keychain available", "CAPELLA_API_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, key) {
		t.Fatalf("the API key was printed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a profile was saved without a keychain (stat: %v)", err)
	}
}
