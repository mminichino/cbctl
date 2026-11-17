package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/settings"
)

func TestConfigureSaveAndShow(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)

	home := t.TempDir()
	t.Setenv("CBCTL_HOME", home)
	kr := settings.NewMemKeyring()
	oldKR := keyringFactory
	keyringFactory = func() settings.Keyring { return kr }
	t.Cleanup(func() { keyringFactory = oldKR })

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer test-key-aaaa" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"nope"}`)
			return
		}
		if r.URL.Path == "/v4/organizations" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"org-1","name":"One"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPELLA_API_BASE_URL", srv.URL)

	rootCmd.SetArgs([]string{"configure", "--api-key-stdin", "--profile", "ci"})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		_, _ = w.WriteString("test-key-aaaa\n")
		_ = w.Close()
	}()
	err := rootCmd.Execute()
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if hits == 0 {
		t.Fatal("expected API call")
	}
	got, err := kr.Get("ci")
	if err != nil || got != "test-key-aaaa" {
		t.Fatalf("keyring=%q err=%v", got, err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "profiles.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "test-key-aaaa") {
		t.Fatal("API key written to profiles.yaml")
	}

	// 401 saves nothing
	kr2 := settings.NewMemKeyring()
	keyringFactory = func() settings.Keyring { return kr2 }
	home2 := t.TempDir()
	t.Setenv("CBCTL_HOME", home2)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"bad"}`)
	}))
	t.Cleanup(bad.Close)
	t.Setenv("CAPELLA_API_BASE_URL", bad.URL)
	rootCmd.SetArgs([]string{"configure", "--api-key-stdin", "--profile", "bad"})
	r2, w2, _ := os.Pipe()
	os.Stdin = r2
	go func() {
		_, _ = w2.WriteString("bad-key-bbbb\n")
		_ = w2.Close()
	}()
	err = rootCmd.Execute()
	os.Stdin = oldStdin
	if err == nil {
		t.Fatal("expected 401 error")
	}
	if _, err := kr2.Get("bad"); err == nil {
		t.Fatal("401 must not save key")
	}
}

func TestConfigureShowJSONRedacts(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	t.Setenv("CAPELLA_API_KEY", "env-secret-zzzz")
	t.Setenv("CBCTL_HOME", t.TempDir())
	keyringFactory = func() settings.Keyring { return settings.NewMemKeyring() }
	t.Cleanup(func() { keyringFactory = func() settings.Keyring { return settings.OSKeyring{} } })

	var out strings.Builder
	rootCmd.SetArgs([]string{"configure", "show", "--profile", "default", "-o", "json"})
	rootCmd.SetOut(&out)
	rootCmd.SetErr(io.Discard)
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "env-secret-zzzz") {
		t.Fatalf("secret in output: %s", got)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(got), &rows); err != nil {
		t.Fatalf("json: %v body=%q", err, got)
	}
}

func TestConfigureShowConfigFileSource(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	home := t.TempDir()
	t.Setenv("CBCTL_HOME", home)
	t.Setenv("CAPELLA_API_KEY", "env-secret-zzzz")
	t.Setenv("CBCTL_CONFIG", "")
	keyringFactory = func() settings.Keyring { return settings.NewMemKeyring() }
	t.Cleanup(func() { keyringFactory = func() settings.Keyring { return settings.OSKeyring{} } })
	store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
	if err := store.Save(map[string]settings.Profile{"default": {ConfigFile: "/work/prod.yaml"}}); err != nil {
		t.Fatal(err)
	}

	show := func(args ...string) string {
		var out strings.Builder
		rootCmd.SetArgs(append([]string{"configure", "show", "--profile", "default"}, args...))
		rootCmd.SetOut(&out)
		rootCmd.SetErr(io.Discard)
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	got := show()
	if !strings.Contains(got, "/work/prod.yaml") || !strings.Contains(got, "config file") {
		t.Errorf("show must list the profile's config file:\n%s", got)
	}
	t.Setenv("CBCTL_CONFIG", "/tmp/env.yaml")
	if got := show(); !strings.Contains(got, "/tmp/env.yaml") {
		t.Errorf("CBCTL_CONFIG must override the profile:\n%s", got)
	}
}

func TestConfigureAcceptsAPIBaseURL(t *testing.T) {
	root := NewRootCmd(DefaultDeps())
	cmd, _, err := root.Find([]string{"configure"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--api-base-url", "https://cloudapi.example.com"}); err != nil {
		t.Fatalf("capellactl's --api-base-url must parse: %v", err)
	}
	if got, _ := cmd.Flags().GetString("api-host"); got != "https://cloudapi.example.com" {
		t.Errorf("--api-base-url set api-host to %q", got)
	}
	if fl := cmd.Flags().Lookup("api-base-url"); fl == nil {
		t.Error("lookup of the old name must resolve to api-host")
	}
}
