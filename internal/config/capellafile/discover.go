package capellafile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/settings"
)

// Where a discovered path came from.
const (
	SourceFlag    = "--config"
	SourceEnv     = "CBCTL_CONFIG"
	SourceProfile = "profile configFile"
	SourceYAML    = "./capella.yaml"
	SourceYML     = "./capella.yml"
	SourceDefault = "default"
)

// EnvVar is the environment variable that names the config file.
const EnvVar = "CBCTL_CONFIG"

// Discovery is the result of looking for the config file.
type Discovery struct {
	// Path is the file's path (absolute when cwd is absolute); "capella.yaml" under cwd when
	// nothing was found.
	Path string
	// Source says where the choice came from: one of the Source constants.
	Source string
	// Exists reports whether Path is an existing file.
	Exists bool
}

// Explicit reports whether the path was named by --config, CBCTL_CONFIG or the profile, as
// opposed to being found in the working directory.
func (d Discovery) Explicit() bool {
	return d.Source == SourceFlag || d.Source == SourceEnv || d.Source == SourceProfile
}

// Managed reports whether file-managed mode is active: the file exists or was named explicitly.
func (d Discovery) Managed() bool {
	return d.Exists || d.Explicit()
}

// Discover returns the config file path and where the choice came from. The order is: the
// --config flag, CBCTL_CONFIG, the profile's configFile, ./capella.yaml, ./capella.yml. Relative
// paths from the first three are resolved against cwd. Having both ./capella.yaml and
// ./capella.yml is an error (a *ConfigError). When nothing exists the path is capella.yaml in
// cwd and Exists is false; the caller decides whether that is an error.
func Discover(flag string, env func(string) string, profile settings.Profile, cwd string) (Discovery, error) {
	resolve := func(p string) string {
		p = strings.TrimSpace(p)
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(cwd, p)
	}
	explicit := func(p, source string) Discovery {
		path := resolve(p)
		return Discovery{Path: path, Source: source, Exists: isFile(path)}
	}

	if strings.TrimSpace(flag) != "" {
		return explicit(flag, SourceFlag), nil
	}
	if env != nil {
		if v := env(EnvVar); strings.TrimSpace(v) != "" {
			return explicit(v, SourceEnv), nil
		}
	}
	if strings.TrimSpace(profile.ConfigFile) != "" {
		return explicit(profile.ConfigFile, SourceProfile), nil
	}

	yamlPath := filepath.Join(cwd, "capella.yaml")
	ymlPath := filepath.Join(cwd, "capella.yml")
	hasYAML, hasYML := isFile(yamlPath), isFile(ymlPath)
	switch {
	case hasYAML && hasYML:
		return Discovery{}, NewConfigError("capella.yaml", []Issue{{
			Path:    "(root)",
			Where:   cwd,
			Message: "both capella.yaml and capella.yml found; pass --config",
			Fix:     "delete one of them, or pass --config PATH to choose",
		}})
	case hasYAML:
		return Discovery{Path: yamlPath, Source: SourceYAML, Exists: true}, nil
	case hasYML:
		return Discovery{Path: ymlPath, Source: SourceYML, Exists: true}, nil
	}
	return Discovery{Path: yamlPath, Source: SourceDefault}, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
