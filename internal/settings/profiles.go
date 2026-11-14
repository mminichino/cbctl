package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile holds non-secret Capella settings for a named profile.
type Profile struct {
	OrganizationID string `yaml:"organizationId,omitempty"`
	APIBaseURL     string `yaml:"apiBaseUrl,omitempty"`
	ConfigFile     string `yaml:"configFile,omitempty"`
}

type profilesFile struct {
	Profiles map[string]Profile `yaml:"profiles"`
}

// ProfileStore reads and writes profiles.yaml under HomeDir.
type ProfileStore struct {
	Path string
}

// HomeDir returns the cbctl config directory.
// Order: CBCTL_HOME → (windows) %APPDATA%\cbctl → $XDG_CONFIG_HOME/cbctl → ~/.config/cbctl.
func HomeDir(env func(string) string) string {
	if env == nil {
		env = ProcessEnv
	}
	if v := env("CBCTL_HOME"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if app := env("APPDATA"); app != "" {
			return filepath.Join(app, "cbctl")
		}
	}
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "cbctl")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "cbctl")
}

// Load returns profiles from disk. A missing file yields an empty map.
func (s ProfileStore) Load() (map[string]Profile, error) {
	path := s.Path
	if path == "" {
		return nil, fmt.Errorf("profile store path is empty")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Profile{}, nil
		}
		return nil, fmt.Errorf("read profiles %s: %w", path, err)
	}
	var file profilesFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse profiles %s: %w", path, err)
	}
	if file.Profiles == nil {
		return map[string]Profile{}, nil
	}
	return file.Profiles, nil
}

// Save writes profiles atomically with directory mode 0700 and file mode 0600.
func (s ProfileStore) Save(profiles map[string]Profile) error {
	path := s.Path
	if path == "" {
		return fmt.Errorf("profile store path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create profile dir %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700)
	if profiles == nil {
		profiles = map[string]Profile{}
	}
	raw, err := yaml.Marshal(profilesFile{Profiles: profiles})
	if err != nil {
		return fmt.Errorf("marshal profiles: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".profiles-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp profiles: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp profiles: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp profiles: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp profiles: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename profiles %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

// ProfilesPath returns <home>/profiles.yaml.
func ProfilesPath(home string) string {
	return filepath.Join(strings.TrimRight(home, string(filepath.Separator)), "profiles.yaml")
}
