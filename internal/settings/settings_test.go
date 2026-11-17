package settings_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/settings"
)

func TestSanitizeAPIKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"quotes", `"abc123"`, "abc123"},
		{"single quotes", `'abc123'`, "abc123"},
		{"BOM", "\ufeffabc123", "abc123"},
		{"Bearer", "Bearer abc123", "abc123"},
		{"bearer lower", "bearer abc123", "abc123"},
		{"paste markers", "\x1b[200~abc123\x1b[201~", "abc123"},
		{"bracket paste", "[200~abc123[201~", "abc123"},
		{"internal spaces", "ab cd", "ab cd"},
		{"trim", "  abc123\n", "abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := settings.SanitizeAPIKey(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "https://cloudapi.cloud.couchbase.com", false},
		{"cloudapi.cloud.couchbase.com", "https://cloudapi.cloud.couchbase.com", false},
		{"https://cloudapi.cloud.couchbase.com/", "https://cloudapi.cloud.couchbase.com", false},
		{"host:443", "https://host:443", false},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", false},
		{"http://localhost", "http://localhost", false},
		{"http://evil.example.com", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := settings.NormalizeBaseURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)

	home := t.TempDir()
	kr := settings.NewMemKeyring()
	_ = kr.Set("default", "keychain-key-xxxx")
	store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
	if err := store.Save(map[string]settings.Profile{
		"default": {OrganizationID: "org-from-profile", APIBaseURL: "https://profile.example.com"},
	}); err != nil {
		t.Fatal(err)
	}

	envMap := map[string]string{}
	env := func(k string) string { return envMap[k] }

	t.Run("flag token wins", func(t *testing.T) {
		envMap = map[string]string{"CAPELLA_API_KEY": "env-key-xxxx"}
		s, err := settings.Resolve(settings.Inputs{
			Token: "flag-key-xxxx", Home: home, Env: env, Keyring: kr,
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.APIKey.Value != "flag-key-xxxx" || s.APIKey.Source != settings.SourceFlag {
			t.Fatalf("%+v", s.APIKey)
		}
	})

	t.Run("env CAPELLA_API_KEY", func(t *testing.T) {
		envMap = map[string]string{"CAPELLA_API_KEY": "env-key-xxxx"}
		s, err := settings.Resolve(settings.Inputs{Home: home, Env: env, Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		if s.APIKey.Value != "env-key-xxxx" || s.APIKey.Detail != "CAPELLA_API_KEY" {
			t.Fatalf("%+v", s.APIKey)
		}
	})

	t.Run("env CAPELLA_TOKEN alias", func(t *testing.T) {
		envMap = map[string]string{"CAPELLA_TOKEN": "alias-key-xxxx"}
		s, err := settings.Resolve(settings.Inputs{Home: home, Env: env, Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		if s.APIKey.Value != "alias-key-xxxx" || s.APIKey.Detail != "CAPELLA_TOKEN" {
			t.Fatalf("%+v", s.APIKey)
		}
	})

	t.Run("keychain when profile exists", func(t *testing.T) {
		envMap = map[string]string{}
		s, err := settings.Resolve(settings.Inputs{Home: home, Env: env, Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		if s.APIKey.Value != "keychain-key-xxxx" || s.APIKey.Source != settings.SourceKeychain {
			t.Fatalf("%+v", s.APIKey)
		}
	})

	t.Run("explicit api-host beats env", func(t *testing.T) {
		envMap = map[string]string{"CAPELLA_API_BASE_URL": "https://env.example.com"}
		s, err := settings.Resolve(settings.Inputs{
			APIHost: "https://flag.example.com", Home: home, Env: env, Keyring: kr, Token: "x",
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.BaseURL.Value != "https://flag.example.com" || s.BaseURL.Source != settings.SourceFlag {
			t.Fatalf("%+v", s.BaseURL)
		}
	})

	t.Run("default api-host does not beat env", func(t *testing.T) {
		envMap = map[string]string{"CAPELLA_API_BASE_URL": "https://env.example.com"}
		s, err := settings.Resolve(settings.Inputs{
			APIHost: "", // unchanged cobra default → not passed
			Home:    home, Env: env, Keyring: kr, Token: "x",
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.BaseURL.Value != "https://env.example.com" || s.BaseURL.Detail != "CAPELLA_API_BASE_URL" {
			t.Fatalf("%+v", s.BaseURL)
		}
	})

	t.Run("org from profile", func(t *testing.T) {
		envMap = map[string]string{}
		s, err := settings.Resolve(settings.Inputs{Home: home, Env: env, Keyring: kr, Token: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if s.OrgID.Value != "org-from-profile" || s.OrgID.Source != settings.SourceProfile {
			t.Fatalf("%+v", s.OrgID)
		}
	})

	t.Run("missing profile warns", func(t *testing.T) {
		envMap = map[string]string{}
		s, err := settings.Resolve(settings.Inputs{
			Profile: "missing", Home: home, Env: env, Keyring: kr, Token: "tok-xxxx",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Warnings) == 0 || !strings.Contains(s.Warnings[0], "not found") {
			t.Fatalf("warnings=%v", s.Warnings)
		}
	})
}
