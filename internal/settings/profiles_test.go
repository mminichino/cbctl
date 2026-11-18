package settings_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/settings"
)

func TestProfileStoreModesAndNoKeyOnDisk(t *testing.T) {
	home := t.TempDir()
	store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
	kr := settings.NewMemKeyring()
	if err := kr.Set("default", "super-secret-api-key-zzzz"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(map[string]settings.Profile{
		"default": {OrganizationID: "org-1", APIBaseURL: "https://example.com"},
	}); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		// The home dir mode isn't asserted: t.TempDir may already exist with
		// another mode. The profiles file itself must be private.
		pfi, err := os.Stat(store.Path)
		if err != nil {
			t.Fatal(err)
		}
		if pfi.Mode().Perm() != 0o600 {
			t.Fatalf("profiles.yaml mode=%o", pfi.Mode().Perm())
		}
	}

	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "super-secret-api-key-zzzz") {
			t.Errorf("API key found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["default"].OrganizationID != "org-1" {
		t.Fatalf("%+v", loaded)
	}
}

func TestHomeDir(t *testing.T) {
	t.Parallel()
	env := func(k string) string {
		switch k {
		case "CBCTL_HOME":
			return "/tmp/cbctl-home"
		default:
			return ""
		}
	}
	if got := settings.HomeDir(env); got != "/tmp/cbctl-home" {
		t.Fatalf("got %q", got)
	}
}

func TestCorruptProfilesError(t *testing.T) {
	home := t.TempDir()
	path := settings.ProfilesPath(home)
	_ = os.MkdirAll(home, 0o700)
	_ = os.WriteFile(path, []byte(":\n  - bad\n  [yaml"), 0o600)
	_, err := settings.ProfileStore{Path: path}.Load()
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err=%v", err)
	}
}

func TestProfileStoreLoadWithWarnings(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantOrg  string
		wantWarn []string
		wantErr  string
	}{
		{"known keys only", "profiles:\n  dev:\n    organizationId: org-1\n", "org-1", nil, ""},
		{"unknown keys are one warning", "version: 2\nprofiles:\n  dev:\n    organizationId: org-1\n    colour: red\n", "org-1", []string{"version", "profiles.dev.colour"}, ""},
		{"empty file", "", "", nil, ""},
		{"bad type names the line", "profiles:\n  dev:\n    organizationId: [a]\n", "", nil, "profiles.yaml:3:"},
		{"syntax error", "profiles: [\n", "", nil, "profiles.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profiles.yaml")
			if err := os.WriteFile(path, []byte(tt.text), 0o600); err != nil {
				t.Fatal(err)
			}
			got, warns, err := settings.ProfileStore{Path: path}.LoadWithWarnings()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got["dev"].OrganizationID != tt.wantOrg {
				t.Errorf("organizationId = %q, want %q", got["dev"].OrganizationID, tt.wantOrg)
			}
			if len(tt.wantWarn) == 0 && len(warns) != 0 {
				t.Errorf("warnings = %v, want none", warns)
			}
			if len(tt.wantWarn) > 0 {
				if len(warns) != 1 {
					t.Fatalf("warnings = %v, want exactly one", warns)
				}
				for _, k := range tt.wantWarn {
					if !strings.Contains(warns[0], k) {
						t.Errorf("warning %q does not name %q", warns[0], k)
					}
				}
			}
		})
	}
}
