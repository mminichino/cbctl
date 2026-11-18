// Package settings resolves Capella auth and connection settings.
package settings

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/redact"
)

// Source says where a resolved value came from; it is shown by `configure show`.
const (
	SourceFlag     = "flag"
	SourceEnv      = "env"
	SourceProfile  = "profile"
	SourceFile     = "config file"
	SourceKeychain = "keychain"
	SourceDefault  = "default"
	SourceNone     = "not set"
)

const (
	defaultProfile  = "default"
	defaultBaseURL  = "https://" + config.DefaultCapellaAPIHost
	envAPIKey       = "CAPELLA_API_KEY"
	envTokenAlias   = "CAPELLA_TOKEN"
	envOrgID        = "CAPELLA_ORG_ID"
	envAPIBaseURL   = "CAPELLA_API_BASE_URL"
	envProfile      = "CBCTL_PROFILE"
	envConfig       = "CBCTL_CONFIG"
	errAPIKeyNotSet = "Capella API key not set; use --token, set CAPELLA_API_KEY (or put it in .env), or run 'cbctl configure'"
)

// ErrAPIKeyNotSet is the canonical missing-key message.
var ErrAPIKeyNotSet = fmt.Errorf("%s", errAPIKeyNotSet)

// Value is a resolved setting with provenance.
type Value struct {
	Value  string
	Source string
	Detail string
}

// IsSet reports whether Value is non-empty.
func (v Value) IsSet() bool { return v.Value != "" }

// Settings holds resolved Capella connection settings.
type Settings struct {
	Profile string
	APIKey  Value
	OrgID   Value
	BaseURL Value
	// ConfigFile is the capella.yaml path from --config, CBCTL_CONFIG or the profile. It is empty
	// when none names one; file discovery then falls back to ./capella.yaml.
	ConfigFile Value
	Warnings   []string
}

// Inputs are already-parsed command-line values plus injectable dependencies.
type Inputs struct {
	Profile string
	Token   string
	OrgID   string
	APIHost string // set only when --api-host was explicitly changed
	// ConfigFile is the --config flag value.
	ConfigFile string
	// FileOrgID is the organizationId declared in the config file; it is the last resort for the
	// organization (flag, environment and profile win). FileOrgSource names the file.
	FileOrgID     string
	FileOrgSource string
	Env           func(string) string
	Home          string
	Keyring       Keyring
	// LegacyHome is capellactl's config directory; a profile cbctl does not have is read from
	// there (with a warning). Empty: no legacy profiles.
	LegacyHome string
	// LegacyKeyring holds capellactl's API keys; it is read when Keyring has no key for the
	// profile (with a warning). Nil: no legacy keychain.
	LegacyKeyring Keyring
}

// Resolve applies precedence and sanitizes API key / base URL.
func Resolve(in Inputs) (*Settings, error) {
	env := in.Env
	if env == nil {
		env = ProcessEnv
	}
	home := in.Home
	if home == "" {
		home = HomeDir(env)
	}
	kr := in.Keyring
	if kr == nil {
		kr = OSKeyring{}
	}

	var warnings []string
	profileName := firstNonEmpty(in.Profile, env(envProfile), defaultProfile)

	store := ProfileStore{Path: ProfilesPath(home)}
	profiles, storeWarnings, err := store.LoadWithWarnings()
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, storeWarnings...)
	profile, profileOK := profiles[profileName]
	legacyProfile := false
	if !profileOK && in.LegacyHome != "" && filepath.Clean(in.LegacyHome) != filepath.Clean(home) {
		legacy, lw, err := LoadLegacyProfiles(in.LegacyHome)
		if err != nil {
			return nil, err
		}
		if p, ok := legacy[profileName]; ok {
			profile, profileOK, legacyProfile = p, true, true
			warnings = append(warnings, lw...)
			warnings = append(warnings, fmt.Sprintf("profile %q is read from capellactl's %s; run 'cbctl configure migrate' to copy it",
				profileName, ProfilesPath(in.LegacyHome)))
		}
	}
	if in.Profile != "" && !profileOK {
		warnings = append(warnings, fmt.Sprintf(`profile %q not found; run 'cbctl configure --profile %s' to create it`, profileName, profileName))
	}

	apiKey, warn := resolveAPIKey(in.Token, env, kr, profileName, profileOK && !legacyProfile)
	warnings = append(warnings, warn...)
	if !apiKey.IsSet() && profileOK && in.LegacyKeyring != nil {
		if key, err := in.LegacyKeyring.Get(profileName); err == nil && key != "" {
			apiKey = Value{Value: key, Source: SourceKeychain, Detail: profileName + " (capellactl)"}
			warnings = append(warnings, fmt.Sprintf("the API key of profile %q is read from capellactl's keychain entry; run 'cbctl configure migrate' to copy it",
				profileName))
		}
	}
	if apiKey.IsSet() {
		apiKey.Value = SanitizeAPIKey(apiKey.Value)
		redact.Register(apiKey.Value)
	}

	org := resolveOrgID(in.OrgID, env, profile, profileName, profileOK)
	if !org.IsSet() && in.FileOrgID != "" {
		org = Value{Value: in.FileOrgID, Source: SourceFile, Detail: in.FileOrgSource}
	}
	configFile := resolveConfigFile(in.ConfigFile, env, profile, profileName, profileOK)

	baseRaw, baseSrc, baseDetail := resolveBaseURLRaw(in.APIHost, env, profile, profileName, profileOK)
	baseNorm, err := NormalizeBaseURL(baseRaw)
	if err != nil {
		return nil, err
	}
	base := Value{Value: baseNorm, Source: baseSrc, Detail: baseDetail}

	return &Settings{
		Profile:    profileName,
		APIKey:     apiKey,
		OrgID:      org,
		BaseURL:    base,
		ConfigFile: configFile,
		Warnings:   warnings,
	}, nil
}

func resolveConfigFile(flag string, env func(string) string, profile Profile, profileName string, profileOK bool) Value {
	if flag != "" {
		return Value{Value: flag, Source: SourceFlag}
	}
	if v := env(envConfig); v != "" {
		return Value{Value: v, Source: SourceEnv, Detail: envConfig}
	}
	if profileOK && profile.ConfigFile != "" {
		return Value{Value: profile.ConfigFile, Source: SourceProfile, Detail: profileName}
	}
	return Value{Source: SourceNone}
}

// LoadProfile returns the profile named by flag, CBCTL_PROFILE or "default" (empty when it does
// not exist), so callers can use its settings before the full resolution runs.
func LoadProfile(flag string, env func(string) string, home string) (Profile, error) {
	return LoadProfileWithLegacy(flag, env, home, "")
}

// LoadProfileWithLegacy is LoadProfile that falls back to capellactl's profiles in legacyHome
// (empty: none) when cbctl has no profile of that name.
func LoadProfileWithLegacy(flag string, env func(string) string, home, legacyHome string) (Profile, error) {
	if env == nil {
		env = ProcessEnv
	}
	if home == "" {
		home = HomeDir(env)
	}
	profiles, err := ProfileStore{Path: ProfilesPath(home)}.Load()
	if err != nil {
		return Profile{}, err
	}
	name := firstNonEmpty(flag, env(envProfile), defaultProfile)
	if p, ok := profiles[name]; ok || legacyHome == "" || filepath.Clean(legacyHome) == filepath.Clean(home) {
		return p, nil
	}
	legacy, _, err := LoadLegacyProfiles(legacyHome)
	if err != nil {
		return Profile{}, err
	}
	return legacy[name], nil
}

func resolveAPIKey(token string, env func(string) string, kr Keyring, profile string, profileOK bool) (Value, []string) {
	var warnings []string
	if token != "" {
		return Value{Value: token, Source: SourceFlag}, nil
	}
	if v := env(envAPIKey); v != "" {
		return Value{Value: v, Source: SourceEnv, Detail: envAPIKey}, nil
	}
	if v := env(envTokenAlias); v != "" {
		return Value{Value: v, Source: SourceEnv, Detail: envTokenAlias}, nil
	}
	if !profileOK {
		return Value{Source: SourceNone}, nil
	}
	key, err := kr.Get(profile)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Value{Source: SourceNone}, nil
		}
		warnings = append(warnings, fmt.Sprintf("keychain unavailable: %v", err))
		return Value{Source: SourceNone}, warnings
	}
	if key == "" {
		return Value{Source: SourceNone}, nil
	}
	return Value{Value: key, Source: SourceKeychain, Detail: profile}, warnings
}

func resolveOrgID(flag string, env func(string) string, profile Profile, profileName string, profileOK bool) Value {
	if flag != "" {
		return Value{Value: flag, Source: SourceFlag}
	}
	if v := env(envOrgID); v != "" {
		return Value{Value: v, Source: SourceEnv, Detail: envOrgID}
	}
	if profileOK && profile.OrganizationID != "" {
		return Value{Value: profile.OrganizationID, Source: SourceProfile, Detail: profileName}
	}
	return Value{Source: SourceNone}
}

func resolveBaseURLRaw(apiHost string, env func(string) string, profile Profile, profileName string, profileOK bool) (raw, source, detail string) {
	if apiHost != "" {
		return apiHost, SourceFlag, ""
	}
	if v := env(envAPIBaseURL); v != "" {
		return v, SourceEnv, envAPIBaseURL
	}
	if profileOK && profile.APIBaseURL != "" {
		return profile.APIBaseURL, SourceProfile, profileName
	}
	return defaultBaseURL, SourceDefault, ""
}

// NormalizeBaseURL turns "host", "host:port", "https://host/" into "https://host" (no trailing slash, no path).
func NormalizeBaseURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultBaseURL, nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid API base URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("invalid API base URL: missing host")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" {
		if scheme == "http" && isLocalHost(host) {
			// allowed for tests
		} else {
			return "", fmt.Errorf("API base URL must use https (got %s)", scheme)
		}
	}
	out := scheme + "://" + u.Host
	return strings.TrimRight(out, "/"), nil
}

func isLocalHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" {
		return true
	}
	if strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".test") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// SanitizeAPIKey trims whitespace/CR/LF, a UTF-8 BOM, surrounding quotes, a leading
// "Bearer " prefix, and terminal bracketed-paste markers.
func SanitizeAPIKey(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\r\n")
	for _, m := range []string{"\x1b[200~", "\x1b[201~", "[200~", "[201~"} {
		s = strings.ReplaceAll(s, m, "")
	}
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
		}
	}
	if len(s) >= 7 && strings.EqualFold(s[:7], "Bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	// Drop any remaining BOM after quote strip.
	if strings.HasPrefix(s, "\ufeff") {
		_, size := utf8.DecodeRuneInString(s)
		s = s[size:]
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// HostFromBaseURL strips the scheme from a normalized base URL for legacy PropCapellaAPIHost.
func HostFromBaseURL(base string) string {
	base = strings.TrimSpace(base)
	base = strings.TrimPrefix(base, "https://")
	base = strings.TrimPrefix(base, "http://")
	return strings.TrimRight(base, "/")
}
