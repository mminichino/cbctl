package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mminichino/cbctl/internal/yamlio"
)

// Profile holds non-secret Capella settings for a named profile.
type Profile struct {
	OrganizationID string `json:"organizationId,omitempty"`
	// OrganizationName is informational (shown by configure list); the ID is what requests use.
	OrganizationName string `json:"organizationName,omitempty"`
	APIBaseURL       string `json:"apiBaseUrl,omitempty"`
	ConfigFile       string `json:"configFile,omitempty"`
}

type profilesFile struct {
	Profiles map[string]Profile `json:"profiles"`
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
	profiles, _, err := s.LoadWithWarnings()
	return profiles, err
}

// LoadWithWarnings is Load that also reports keys this version does not know. They are not an
// error: a file written by a newer cbctl must still load in an older one, so decoding is
// lenient and the extras are only named in one warning.
func (s ProfileStore) LoadWithWarnings() (map[string]Profile, []string, error) {
	path := s.Path
	if path == "" {
		return nil, nil, fmt.Errorf("profile store path is empty")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Profile{}, nil, nil
		}
		return nil, nil, fmt.Errorf("read profiles %s: %w", path, err)
	}
	var file profilesFile
	if err := yamlio.Unmarshal(raw, &file, yamlio.WithSource(filepath.Base(path))); err != nil {
		return nil, nil, fmt.Errorf("parse profiles %s: %w", path, err)
	}
	var warnings []string
	if extra, err := yamlio.UnknownFields(raw, &file, yamlio.WithSource(filepath.Base(path))); err == nil && len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, e := range extra {
			keys[i] = yamlio.DottedPath(e.Pointer)
		}
		warnings = append(warnings, fmt.Sprintf("%s: unknown key(s) ignored: %s", path, strings.Join(keys, ", ")))
	}
	if file.Profiles == nil {
		return map[string]Profile{}, warnings, nil
	}
	return file.Profiles, warnings, nil
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
	raw, err := yamlio.Marshal(profilesFile{Profiles: profiles})
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
