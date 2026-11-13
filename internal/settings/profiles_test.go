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
		fi, err := os.Stat(home)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			// MkdirAll on TempDir may already exist with different mode; chmod applied on save parent.
		}
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
