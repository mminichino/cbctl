package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/mminichino/cbctl/internal/yamlio"
)

// LegacyEnv maps each capellactl environment variable to the cbctl variable that replaces it.
var LegacyEnv = map[string]string{
	"CAPELLACTL_CONFIG":  "CBCTL_CONFIG",
	"CAPELLACTL_PROFILE": "CBCTL_PROFILE",
	"CAPELLACTL_QUIET":   "CBCTL_QUIET",
	"CAPELLACTL_HOME":    "CBCTL_HOME",
	"BEARER_TOKEN":       "CAPELLA_API_KEY",
	"ORG_ID":             "CAPELLA_ORG_ID",
	"API_BASE_URL":       "CAPELLA_API_BASE_URL",
}

// legacyFor is the reverse of LegacyEnv: cbctl name → capellactl name.
var legacyFor = func() map[string]string {
	m := map[string]string{}
	for legacy, name := range LegacyEnv {
		m[name] = legacy
	}
	return m
}()

// LegacyKeyringService is the OS keychain service capellactl stored API keys under.
const LegacyKeyringService = "capellactl"

// Notice is one use of a capellactl setting that cbctl honoured.
type Notice struct {
	Legacy, Replacement, Detail string
}

// String is the one-line warning for the notice.
func (n Notice) String() string {
	if n.Detail != "" {
		return fmt.Sprintf("%s is deprecated; use %s (%s)", n.Legacy, n.Replacement, n.Detail)
	}
	return fmt.Sprintf("%s is deprecated; use %s", n.Legacy, n.Replacement)
}

// lookupWithLegacy reads name from env, falling back to its capellactl variable. source is the
// variable the value came from. A notice is returned when the legacy variable was used, or when
// both are set to different values (the cbctl one wins).
func lookupWithLegacy(env func(string) string, name string) (val, source string, notice *Notice) {
	v := env(name)
	legacy, ok := legacyFor[name]
	if !ok {
		return v, name, nil
	}
	lv := env(legacy)
	switch {
	case v != "" && lv != "" && v != lv:
		return v, name, &Notice{Legacy: legacy, Replacement: name, Detail: name + " is also set and wins"}
	case v != "":
		return v, name, nil
	case lv != "":
		return lv, legacy, &Notice{Legacy: legacy, Replacement: name}
	}
	return "", name, nil
}

// LegacyShim wraps env so every cbctl variable falls back to its capellactl name. Each legacy
// variable is reported once, through warn, the first time it is used.
type LegacyShim struct {
	env  func(string) string
	warn func(string)
	mu   sync.Mutex
	seen map[string]bool
}

// NewLegacyShim returns a shim over env; warn receives each notice once (nil: dropped).
func NewLegacyShim(env func(string) string, warn func(string)) *LegacyShim {
	if env == nil {
		env = ProcessEnv
	}
	return &LegacyShim{env: env, warn: warn, seen: map[string]bool{}}
}

// Env is the wrapped lookup.
func (s *LegacyShim) Env(name string) string {
	v, _, n := lookupWithLegacy(s.env, name)
	if n != nil && s.warn != nil {
		s.mu.Lock()
		first := !s.seen[n.Legacy]
		s.seen[n.Legacy] = true
		s.mu.Unlock()
		if first {
			s.warn(n.String())
		}
	}
	return v
}

// LegacyEnvInUse lists the capellactl variables set in env, sorted.
func LegacyEnvInUse(env func(string) string) []string {
	var out []string
	for legacy := range LegacyEnv {
		if env(legacy) != "" {
			out = append(out, legacy)
		}
	}
	sort.Strings(out)
	return out
}

// LegacyHome returns capellactl's config directory: CAPELLACTL_HOME, then %APPDATA%\capellactl on
// Windows, then $XDG_CONFIG_HOME/capellactl, then ~/.config/capellactl (capellactl
// config/settings.py home_dir).
func LegacyHome(env func(string) string) string {
	return legacyHomeFor(env, runtime.GOOS)
}

func legacyHomeFor(env func(string) string, goos string) string {
	if env == nil {
		env = ProcessEnv
	}
	if v := env("CAPELLACTL_HOME"); v != "" {
		return v
	}
	if goos == "windows" {
		if app := env("APPDATA"); app != "" {
			return filepath.Join(app, "capellactl")
		}
	}
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "capellactl")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "capellactl")
}

// LegacyProfileError is a capellactl profiles.yaml that cannot be read.
type LegacyProfileError struct {
	Path string
	Err  error
}

func (e *LegacyProfileError) Error() string {
	return fmt.Sprintf("capellactl profiles %s: %v", e.Path, e.Err)
}

func (e *LegacyProfileError) Unwrap() error { return e.Err }

// LoadLegacyProfiles reads capellactl's profiles.yaml in dir. A missing file is no profiles.
// apiBaseUrl is validated like a cbctl base URL; unknown keys are returned as warnings.
func LoadLegacyProfiles(dir string) (map[string]Profile, []string, error) {
	path := ProfilesPath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Profile{}, nil, nil
		}
		return nil, nil, &LegacyProfileError{Path: path, Err: err}
	}
	var doc struct {
		Profiles map[string]map[string]any `json:"profiles"`
	}
	if err := yamlio.Unmarshal(raw, &doc, yamlio.WithSource(filepath.Base(path)), yamlio.WithUseNumber()); err != nil {
		return nil, nil, &LegacyProfileError{Path: path, Err: err}
	}
	out := map[string]Profile{}
	var warnings []string
	names := make([]string, 0, len(doc.Profiles))
	for n := range doc.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		var p Profile
		keys := make([]string, 0, len(doc.Profiles[name]))
		for k := range doc.Profiles[name] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := fmt.Sprint(doc.Profiles[name][k])
			switch k {
			case "organizationId":
				p.OrganizationID = v
			case "apiBaseUrl":
				if _, err := NormalizeBaseURL(v); err != nil {
					return nil, nil, &LegacyProfileError{Path: path, Err: fmt.Errorf("profile %s: %w", name, err)}
				}
				p.APIBaseURL = v
			case "configFile":
				p.ConfigFile = v
			case "organizationName":
				p.OrganizationName = v
			default:
				warnings = append(warnings, fmt.Sprintf("capellactl profile %s: unknown key %q ignored", name, k))
			}
		}
		out[name] = p
	}
	return out, warnings, nil
}

// LegacyCredentialsDir is capellactl's default credentialsOutput.
const LegacyCredentialsDir = "./.capellactl/credentials"
