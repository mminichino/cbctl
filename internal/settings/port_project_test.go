package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/settings"
)

const pastedKey = "AbC123xyz.0987654321-key"

// TestConfigure_CAP037_AC7_CleansPastedKey: copy/paste damage is undone and each fix is reported,
// without the key in the report.
func TestConfigure_CAP037_AC7_CleansPastedKey(t *testing.T) {
	cases := []struct {
		name, raw string
		fixes     []string
	}{
		{"clean", pastedKey, nil},
		{"surrounding whitespace", "  " + pastedKey + "\n", nil},
		{"quotes", `"` + pastedKey + `"`, []string{"removed surrounding quotes"}},
		{"bearer", "Bearer " + pastedKey, []string{"removed a leading 'Bearer '"}},
		{"line break inside", pastedKey[:10] + "\n" + pastedKey[10:], []string{"removed 1 line break/space/control character(s)"}},
		{"paste markers", "\x1b[200~" + pastedKey + "\x1b[201~", []string{"removed terminal paste markers"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, fixes := settings.CleanAPIKey(tc.raw)
			if key != pastedKey {
				t.Errorf("key = %q, want %q", key, pastedKey)
			}
			if !reflect.DeepEqual(fixes, tc.fixes) {
				t.Errorf("fixes = %q, want %q", fixes, tc.fixes)
			}
			for _, f := range fixes {
				if f == pastedKey || len(f) > 0 && contains(f, pastedKey) {
					t.Errorf("fix note %q contains the key", f)
				}
			}
		})
	}
}

// TestWindowsDocs_CAP055_AC4_APIKeyStdinCRLFBOM: a key piped from PowerShell (CRLF, UTF-8 BOM) is
// stored clean; the BOM is reported, the CRLF is not.
func TestWindowsDocs_CAP055_AC4_APIKeyStdinCRLFBOM(t *testing.T) {
	cases := []struct {
		name, raw string
		fixes     []string
	}{
		{"crlf", pastedKey + "\r\n", nil},
		{"bom and crlf", "\ufeff" + pastedKey + "\r\n", []string{"removed a byte-order mark"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, fixes := settings.CleanAPIKey(tc.raw)
			if key != pastedKey || !reflect.DeepEqual(fixes, tc.fixes) {
				t.Errorf("CleanAPIKey(%q) = %q, %q; want %q, %q", tc.raw, key, fixes, pastedKey, tc.fixes)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestConfigure_CAP037_AC3_PrecedenceMatrix: the organization comes from CAPELLA_ORG_ID, then the
// selected profile, then the config file; --profile selects that profile's organization and key;
// CAPELLA_API_KEY beats every keychain entry.
func TestConfigure_CAP037_AC3_PrecedenceMatrix(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	cases := []struct {
		name, envOrg, profile, fileOrg string
		wantOrg, wantSource            string
		wantKey                        string
	}{
		{"default profile", "", "", "", "org-A", settings.SourceProfile, "key-default-1234"},
		{"env beats default profile", "org-B", "", "", "org-B", settings.SourceEnv, "key-default-1234"},
		{"profile prod", "", "prod", "", "org-P", settings.SourceProfile, "key-prod-5678"},
		{"env beats --profile", "org-B", "prod", "", "org-B", settings.SourceEnv, "key-prod-5678"},
		{"missing profile falls back to the file", "", "missing", "org-F", "org-F", settings.SourceFile, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
			if err := store.Save(map[string]settings.Profile{
				"default": {OrganizationID: "org-A"}, "prod": {OrganizationID: "org-P"},
			}); err != nil {
				t.Fatal(err)
			}
			kr := settings.NewMemKeyring()
			_ = kr.Set("default", "key-default-1234")
			_ = kr.Set("prod", "key-prod-5678")
			envMap := map[string]string{}
			if tc.envOrg != "" {
				envMap["CAPELLA_ORG_ID"] = tc.envOrg
			}
			env := func(k string) string { return envMap[k] }
			in := settings.Inputs{Profile: tc.profile, FileOrgID: tc.fileOrg, FileOrgSource: "capella.yaml",
				Env: env, Home: home, Keyring: kr}
			s, err := settings.Resolve(in)
			if err != nil {
				t.Fatal(err)
			}
			if s.OrgID.Value != tc.wantOrg || s.OrgID.Source != tc.wantSource {
				t.Errorf("org = %q (%s), want %q (%s)", s.OrgID.Value, s.OrgID.Source, tc.wantOrg, tc.wantSource)
			}
			if s.APIKey.Value != tc.wantKey {
				t.Errorf("key = %q, want %q", s.APIKey.Value, tc.wantKey)
			}
			envMap["CAPELLA_API_KEY"] = "key-env-0000"
			s, err = settings.Resolve(in)
			if err != nil {
				t.Fatal(err)
			}
			if s.APIKey.Value != "key-env-0000" || s.APIKey.Detail != "CAPELLA_API_KEY" {
				t.Errorf("with CAPELLA_API_KEY: key = %+v", s.APIKey)
			}
		})
	}
}

// --- P-TST-14: security coverage of keychain, profile and resolution paths ---

func TestOSKeyring_GetSetDelete(t *testing.T) {
	keyring.MockInit() // in-memory provider: never the real OS keychain
	for _, kr := range []settings.OSKeyring{{}, {Service: settings.LegacyKeyringService}} {
		if _, err := kr.Get("p"); !errors.Is(err, settings.ErrNotFound) {
			t.Fatalf("%q: Get of a missing entry = %v, want ErrNotFound", kr.Service, err)
		}
		if err := kr.Set("p", "secret-key-1234"); err != nil {
			t.Fatal(err)
		}
		if v, err := kr.Get("p"); err != nil || v != "secret-key-1234" {
			t.Fatalf("%q: Get = %q, %v", kr.Service, v, err)
		}
		if err := kr.Delete("p"); err != nil {
			t.Fatal(err)
		}
		if err := kr.Delete("p"); err != nil {
			t.Errorf("%q: Delete of a missing entry = %v, want nil", kr.Service, err)
		}
	}
	// The two services are separate keychains.
	if err := (settings.OSKeyring{}).Set("shared", "cbctl-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := (settings.OSKeyring{Service: settings.LegacyKeyringService}).Get("shared"); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("legacy service sees the cbctl entry: %v", err)
	}
	// An empty stored value is treated as absent.
	if err := (settings.OSKeyring{}).Set("empty", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := (settings.OSKeyring{}).Get("empty"); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("empty entry = %v, want ErrNotFound", err)
	}
}

func TestOSKeyring_Unavailable(t *testing.T) {
	boom := errors.New("no secret service")
	keyring.MockInitWithError(boom)
	t.Cleanup(keyring.MockInit)
	kr := settings.OSKeyring{}
	if _, err := kr.Get("p"); !errors.Is(err, boom) {
		t.Errorf("Get = %v", err)
	}
	if err := kr.Set("p", "k"); !errors.Is(err, boom) {
		t.Errorf("Set = %v", err)
	}
	if err := kr.Delete("p"); !errors.Is(err, boom) {
		t.Errorf("Delete = %v", err)
	}
	// Resolution reports an unreadable keychain as a warning, never as the key.
	home := t.TempDir()
	if err := (settings.ProfileStore{Path: settings.ProfilesPath(home)}).Save(map[string]settings.Profile{"default": {OrganizationID: "org-1"}}); err != nil {
		t.Fatal(err)
	}
	s, err := settings.Resolve(settings.Inputs{Env: mapEnv(nil), Home: home, Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if s.APIKey.Value != "" || s.APIKey.Source != settings.SourceNone {
		t.Errorf("APIKey = %+v", s.APIKey)
	}
	if !strings.Contains(strings.Join(s.Warnings, "\n"), "keychain unavailable") {
		t.Errorf("warnings = %v", s.Warnings)
	}
}

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestMemKeyring_ZeroValue(t *testing.T) {
	var kr settings.MemKeyring
	if err := kr.Set("p", "k"); err != nil {
		t.Fatal(err)
	}
	if v, err := kr.Get("p"); err != nil || v != "k" {
		t.Errorf("Get = %q, %v", v, err)
	}
}

func TestResolve_APIKeySources(t *testing.T) {
	home := t.TempDir()
	if err := (settings.ProfileStore{Path: settings.ProfilesPath(home)}).Save(map[string]settings.Profile{
		"default": {OrganizationID: "org-1", ConfigFile: "/p/capella.yaml"},
	}); err != nil {
		t.Fatal(err)
	}
	kr := settings.NewMemKeyring()
	if err := kr.Set("default", "chain-key-1234"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		in       settings.Inputs
		env      map[string]string
		key, src string
		cfg      string
	}{
		{"flag", settings.Inputs{Token: "flag-key-1234"}, nil, "flag-key-1234", settings.SourceFlag, "/p/capella.yaml"},
		{"env", settings.Inputs{}, map[string]string{"CAPELLA_API_KEY": "env-key-1234"}, "env-key-1234", settings.SourceEnv, "/p/capella.yaml"},
		{"alias", settings.Inputs{}, map[string]string{"CAPELLA_TOKEN": "alias-key-1234"}, "alias-key-1234", settings.SourceEnv, "/p/capella.yaml"},
		{"keychain", settings.Inputs{}, nil, "chain-key-1234", settings.SourceKeychain, "/p/capella.yaml"},
		{"config flag", settings.Inputs{ConfigFile: "/flag.yaml"}, nil, "chain-key-1234", settings.SourceKeychain, "/flag.yaml"},
		{"config env", settings.Inputs{}, map[string]string{"CBCTL_CONFIG": "/env.yaml"}, "chain-key-1234", settings.SourceKeychain, "/env.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Env, in.Home, in.Keyring = mapEnv(tc.env), home, kr
			s, err := settings.Resolve(in)
			if err != nil {
				t.Fatal(err)
			}
			if s.APIKey.Value != tc.key || s.APIKey.Source != tc.src {
				t.Errorf("APIKey = %+v, want %s from %s", s.APIKey, tc.key, tc.src)
			}
			if s.ConfigFile.Value != tc.cfg {
				t.Errorf("ConfigFile = %+v, want %s", s.ConfigFile, tc.cfg)
			}
		})
	}
	// A profile without a keychain entry has no key; an empty entry is no key.
	if err := kr.Set("default", ""); err != nil {
		t.Fatal(err)
	}
	s, err := settings.Resolve(settings.Inputs{Env: mapEnv(nil), Home: home, Keyring: kr})
	if err != nil || s.APIKey.Source != settings.SourceNone {
		t.Errorf("empty keychain entry: %+v, %v", s.APIKey, err)
	}
}

func TestLoadProfile_Fallbacks(t *testing.T) {
	home, legacy := t.TempDir(), t.TempDir()
	if err := (settings.ProfileStore{Path: settings.ProfilesPath(home)}).Save(map[string]settings.Profile{"dev": {OrganizationID: "org-dev"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings.ProfilesPath(legacy), []byte("profiles:\n  old:\n    organizationId: org-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := mapEnv(map[string]string{"CBCTL_PROFILE": "dev"})
	if p, err := settings.LoadProfile("", env, home); err != nil || p.OrganizationID != "org-dev" {
		t.Errorf("CBCTL_PROFILE dev = %+v, %v", p, err)
	}
	if p, err := settings.LoadProfileWithLegacy("old", env, home, legacy); err != nil || p.OrganizationID != "org-old" {
		t.Errorf("legacy fallback = %+v, %v", p, err)
	}
	if p, err := settings.LoadProfileWithLegacy("old", env, home, home); err != nil || p.OrganizationID != "" {
		t.Errorf("same home has no fallback = %+v, %v", p, err)
	}
	if p, err := settings.LoadProfile("", mapEnv(map[string]string{"CBCTL_HOME": home, "CBCTL_PROFILE": "dev"}), ""); err != nil || p.OrganizationID != "org-dev" {
		t.Errorf("home from CBCTL_HOME = %+v, %v", p, err)
	}
	if err := os.WriteFile(settings.ProfilesPath(legacy), []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var lpe *settings.LegacyProfileError
	if _, err := settings.LoadProfileWithLegacy("old", env, home, legacy); !errors.As(err, &lpe) || errors.Unwrap(err) == nil || !strings.Contains(err.Error(), "capellactl profiles") {
		t.Errorf("broken legacy file = %v", err)
	}
	if err := os.WriteFile(settings.ProfilesPath(home), []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.LoadProfile("dev", env, home); err == nil {
		t.Error("broken profiles.yaml loaded")
	}
}

func TestProfileStore_Errors(t *testing.T) {
	if _, err := (settings.ProfileStore{}).Load(); err == nil {
		t.Error("Load with no path succeeded")
	}
	if err := (settings.ProfileStore{}).Save(nil); err == nil {
		t.Error("Save with no path succeeded")
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (settings.ProfileStore{Path: filepath.Join(blocker, "sub", "profiles.yaml")}).Save(nil); err == nil {
		t.Error("Save under a file succeeded")
	}
	if _, err := (settings.ProfileStore{Path: dir}).Load(); err == nil {
		t.Error("Load of a directory succeeded")
	}
	empty := filepath.Join(dir, "empty.yaml")
	if err := (settings.ProfileStore{Path: empty}).Save(nil); err != nil {
		t.Fatal(err)
	}
	if got, err := (settings.ProfileStore{Path: empty}).Load(); err != nil || len(got) != 0 {
		t.Errorf("empty store = %v, %v", got, err)
	}
}

func TestHomeDirs(t *testing.T) {
	if got := settings.HomeDir(mapEnv(map[string]string{"CBCTL_HOME": "/h"})); got != "/h" {
		t.Errorf("CBCTL_HOME = %s", got)
	}
	if got := settings.HomeDir(mapEnv(map[string]string{"XDG_CONFIG_HOME": "/x", "APPDATA": "/a"})); got != filepath.Join("/x", "cbctl") && got != filepath.Join("/a", "cbctl") {
		t.Errorf("XDG = %s", got)
	}
	if got := settings.HomeDir(mapEnv(nil)); !strings.HasSuffix(got, filepath.Join(".config", "cbctl")) && !strings.HasSuffix(got, "cbctl") {
		t.Errorf("default = %s", got)
	}
	t.Setenv("CBCTL_HOME", "/proc-home")
	if got := settings.HomeDir(nil); got != "/proc-home" {
		t.Errorf("process env = %s", got)
	}
	t.Setenv("CAPELLACTL_HOME", "/legacy-home")
	if got := settings.LegacyHome(nil); got != "/legacy-home" {
		t.Errorf("LegacyHome = %s", got)
	}
}

func TestLegacyEnv_ShimAndInUse(t *testing.T) {
	env := mapEnv(map[string]string{"BEARER_TOKEN": "legacy-key-1234", "ORG_ID": "o1", "CAPELLA_ORG_ID": "o2"})
	if got := settings.LegacyEnvInUse(env); strings.Join(got, ",") != "BEARER_TOKEN,ORG_ID" {
		t.Errorf("LegacyEnvInUse = %v", got)
	}
	var warned []string
	shim := settings.NewLegacyShim(env, func(s string) { warned = append(warned, s) })
	// Asked twice: the notice is printed once.
	for range 2 {
		if shim.Env("CAPELLA_API_KEY") != "legacy-key-1234" {
			t.Error("legacy key not used")
		}
	}
	if shim.Env("CAPELLA_ORG_ID") != "o2" {
		t.Error("cbctl variable does not win")
	}
	if shim.Env("HOME_UNRELATED") != "" {
		t.Error("unrelated variable resolved")
	}
	if len(warned) != 2 || !strings.Contains(warned[0], "BEARER_TOKEN is deprecated; use CAPELLA_API_KEY") ||
		!strings.Contains(warned[1], "CAPELLA_ORG_ID is also set and wins") {
		t.Errorf("warnings = %q", warned)
	}
	for _, w := range warned {
		if strings.Contains(w, "legacy-key-1234") {
			t.Errorf("warning leaks the key: %s", w)
		}
	}
	t.Setenv("BEARER_TOKEN", "proc-key-1234")
	t.Setenv("CAPELLA_API_KEY", "")
	if settings.NewLegacyShim(nil, nil).Env("CAPELLA_API_KEY") != "proc-key-1234" {
		t.Error("process env shim")
	}
}

func TestBaseURLHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "https://cloudapi.cloud.couchbase.com",
		"cloudapi.example.com":     "https://cloudapi.example.com",
		"https://h.example.com/x/": "https://h.example.com",
		"http://localhost:8080":    "http://localhost:8080",
		"http://127.0.0.1:1":       "http://127.0.0.1:1",
		"http://[::1]:1":           "http://[::1]:1",
		"http://api.localhost":     "http://api.localhost",
		"http://fake.test":         "http://fake.test",
		"http://127.0.0.2":         "http://127.0.0.2",
	} {
		got, err := settings.NormalizeBaseURL(in)
		if in == "" {
			if err != nil || got == "" {
				t.Errorf("default = %q, %v", got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"http://example.com", "ftp://x.example.com", "https://", "https://%zz"} {
		if _, err := settings.NormalizeBaseURL(bad); err == nil {
			t.Errorf("NormalizeBaseURL(%q) accepted", bad)
		}
	}
	if got := settings.HostFromBaseURL(" https://h.example.com/ "); got != "h.example.com" {
		t.Errorf("HostFromBaseURL = %q", got)
	}
	if got := settings.HostFromBaseURL("http://localhost:1"); got != "localhost:1" {
		t.Errorf("HostFromBaseURL = %q", got)
	}
}

func TestProcessEnvHelpers(t *testing.T) {
	t.Setenv("CBCTL_PORT_PROBE", "v")
	if settings.ProcessEnv("CBCTL_PORT_PROBE") != "v" {
		t.Error("ProcessEnv")
	}
	if v, ok := settings.ProcessLookupEnv("CBCTL_PORT_PROBE"); !ok || v != "v" {
		t.Error("ProcessLookupEnv")
	}
	if wd, err := settings.ProcessGetwd(); err != nil || wd == "" {
		t.Errorf("ProcessGetwd = %q, %v", wd, err)
	}
	t.Setenv("CBCTL_PORT_APPLY", "")
	if err := settings.ApplyEnv(map[string]string{"CBCTL_PORT_APPLY": "x"}); err != nil || os.Getenv("CBCTL_PORT_APPLY") != "x" {
		t.Errorf("ApplyEnv: %v", err)
	}
	if err := settings.ApplyEnv(map[string]string{"": "x"}); err == nil {
		t.Error("ApplyEnv accepted an empty name")
	}
}

func TestLoadDotEnv_Paths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// No .env up to the project root.
	if p, _, _, err := settings.LoadDotEnv(sub, nil); err != nil || p != "" {
		t.Errorf("no .env = %q, %v", p, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CBCTL_PORT_DOT=1\nCAPELLA_API_KEY=dot-key-1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lookup := func(k string) (string, bool) { return "", k == "CAPELLA_API_KEY" }
	p, vals, names, err := settings.LoadDotEnv(sub, lookup)
	if err != nil || p != filepath.Join(root, ".env") || vals["CBCTL_PORT_DOT"] != "1" || strings.Join(names, ",") != "CBCTL_PORT_DOT" {
		t.Errorf("LoadDotEnv = %q %v %v %v", p, vals, names, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A='unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := settings.LoadDotEnv(sub, lookup); err == nil {
		t.Error("broken .env parsed")
	}
	t.Chdir(sub)
	if _, _, _, err := settings.LoadDotEnv("", lookup); err == nil {
		t.Error("cwd lookup did not find the broken .env")
	}
}

// failKeyring fails the operations named in fail; others use the embedded MemKeyring.
type failKeyring struct {
	*settings.MemKeyring
	fail map[string]bool
}

var errKeychain = errors.New("keychain locked")

func (k failKeyring) Get(p string) (string, error) {
	if k.fail["get"] {
		return "", errKeychain
	}
	return k.MemKeyring.Get(p)
}

func (k failKeyring) Set(p, v string) error {
	if k.fail["set"] {
		return errKeychain
	}
	return k.MemKeyring.Set(p, v)
}

func (k failKeyring) Delete(p string) error {
	if k.fail["delete"] {
		return errKeychain
	}
	return k.MemKeyring.Delete(p)
}

func legacyHomeWith(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(settings.ProfilesPath(dir), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const legacyTwo = "extra: kept\nprofiles:\n  dev:\n    organizationId: org-dev\n  ops:\n    organizationId: org-ops\n"

func TestMigrateLegacy_KeychainFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("cbctl keychain cannot store", func(t *testing.T) {
		from, to := legacyHomeWith(t, legacyTwo), t.TempDir()
		lk := settings.NewMemKeyring()
		_ = lk.Set("dev", "legacy-key-dev1")
		rep, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: to, Profiles: []string{"dev"},
			Keyring: failKeyring{settings.NewMemKeyring(), map[string]bool{"set": true}}, LegacyKeyring: lk, RemoveLegacy: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(rep.KeyMissing, ",") != "dev" || len(rep.Removed) != 0 {
			t.Errorf("report = %+v", rep)
		}
		if w := strings.Join(rep.Warnings, "\n"); !strings.Contains(w, "cannot store the API key") || strings.Contains(w, "legacy-key-dev1") {
			t.Errorf("warnings = %q", rep.Warnings)
		}
	})
	t.Run("capellactl keychain unreadable", func(t *testing.T) {
		from, to := legacyHomeWith(t, legacyTwo), t.TempDir()
		rep, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: to,
			Keyring: settings.NewMemKeyring(), LegacyKeyring: failKeyring{settings.NewMemKeyring(), map[string]bool{"get": true}}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(rep.KeyMissing, ",") != "dev,ops" {
			t.Errorf("KeyMissing = %v", rep.KeyMissing)
		}
	})
	t.Run("no cbctl keychain", func(t *testing.T) {
		from, to := legacyHomeWith(t, legacyTwo), t.TempDir()
		lk := settings.NewMemKeyring()
		_ = lk.Set("dev", "legacy-key-dev1")
		rep, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: to, Profiles: []string{"dev"}, LegacyKeyring: lk})
		if err != nil || strings.Join(rep.KeyMissing, ",") != "dev" {
			t.Errorf("report = %+v, %v", rep, err)
		}
	})
	t.Run("capellactl entry cannot be deleted", func(t *testing.T) {
		from, to := legacyHomeWith(t, legacyTwo), t.TempDir()
		lk := failKeyring{settings.NewMemKeyring(), map[string]bool{"delete": true}}
		_ = lk.Set("dev", "legacy-key-dev1")
		rep, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: to, Profiles: []string{"dev"},
			Keyring: settings.NewMemKeyring(), LegacyKeyring: lk, RemoveLegacy: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Removed) != 0 || !strings.Contains(strings.Join(rep.Warnings, "\n"), "kept in capellactl") {
			t.Errorf("report = %+v", rep)
		}
	})
}

func TestMigrateLegacy_RemoveLegacy(t *testing.T) {
	ctx := context.Background()
	from, to := legacyHomeWith(t, legacyTwo), t.TempDir()
	lk, kr := settings.NewMemKeyring(), settings.NewMemKeyring()
	_ = lk.Set("dev", "legacy-key-dev1")
	rep, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: to, Profiles: []string{"dev"},
		Keyring: kr, LegacyKeyring: lk, RemoveLegacy: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Removed, ",") != "dev" {
		t.Fatalf("report = %+v", rep)
	}
	raw, err := os.ReadFile(settings.ProfilesPath(from))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "org-dev") || !strings.Contains(string(raw), "org-ops") || !strings.Contains(string(raw), "extra: kept") {
		t.Errorf("capellactl profiles.yaml after removal:\n%s", raw)
	}
	if v, _ := kr.Get("dev"); v != "legacy-key-dev1" {
		t.Errorf("cbctl key = %q", v)
	}
	if _, err := lk.Get("dev"); !errors.Is(err, settings.ErrNotFound) {
		t.Error("capellactl keychain entry kept")
	}

	// One shared home (CAPELLACTL_HOME = CBCTL_HOME): only keychain entries move, the file stays.
	shared := legacyHomeWith(t, legacyTwo)
	_ = lk.Set("ops", "legacy-key-ops1")
	rep, err = settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: shared, ToDir: shared, Profiles: []string{"ops"},
		Keyring: settings.NewMemKeyring(), LegacyKeyring: lk, RemoveLegacy: true})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(settings.ProfilesPath(shared)); string(raw) != legacyTwo {
		t.Errorf("shared profiles.yaml rewritten:\n%s", raw)
	}
	if strings.Join(rep.Removed, ",") != "ops" && strings.Join(rep.Skipped, ",") != "ops" {
		t.Errorf("shared report = %+v", rep)
	}
}

func TestMigrateLegacy_Refusals(t *testing.T) {
	ctx := context.Background()
	if _, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{}); err == nil {
		t.Error("no homes accepted")
	}
	from := legacyHomeWith(t, legacyTwo)
	if _, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: t.TempDir(), Profiles: []string{"nope"}}); !errors.Is(err, settings.ErrUnknownLegacyProfile) {
		t.Errorf("unknown profile = %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := settings.MigrateLegacy(cctx, settings.MigrateOptions{FromDir: from, ToDir: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled = %v", err)
	}
	if _, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: legacyHomeWith(t, "profiles: [\n"), ToDir: t.TempDir()}); err == nil {
		t.Error("broken capellactl file accepted")
	}
	to := t.TempDir()
	if err := os.WriteFile(settings.ProfilesPath(to), []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: from, ToDir: to}); err == nil {
		t.Error("broken cbctl file accepted")
	}
}

func TestSanitizeAPIKey_Variants(t *testing.T) {
	for in, want := range map[string]string{
		"\ufeff  key-abcd1234 \r\n":      "key-abcd1234",
		`"key-abcd1234"`:                 "key-abcd1234",
		"'Bearer key-abcd1234'":          "key-abcd1234",
		"\x1b[200~key-abcd1234\x1b[201~": "key-abcd1234",
		"\"\ufeffkey-abcd1234\"":         "key-abcd1234",
	} {
		if got := settings.SanitizeAPIKey(in); got != want {
			t.Errorf("SanitizeAPIKey(%q) = %q", in, got)
		}
	}
}

func TestMigrateLegacy_RemoveLegacyKeepsComments(t *testing.T) {
	const withComments = "# capellactl profiles - hand edited\nextra: kept # keep me\nprofiles:\n  # the dev org\n  dev:\n    organizationId: org-dev\n  # the ops org\n  ops:\n    organizationId: org-ops # ops id\n"
	from, to := legacyHomeWith(t, withComments), t.TempDir()
	lk := settings.NewMemKeyring()
	_ = lk.Set("dev", "legacy-key-dev1")
	rep, err := settings.MigrateLegacy(context.Background(), settings.MigrateOptions{FromDir: from, ToDir: to,
		Profiles: []string{"dev"}, Keyring: settings.NewMemKeyring(), LegacyKeyring: lk, RemoveLegacy: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Removed, ",") != "dev" {
		t.Fatalf("report = %+v", rep)
	}
	raw, err := os.ReadFile(settings.ProfilesPath(from))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"# capellactl profiles - hand edited", "extra: kept # keep me", "# the ops org", "organizationId: org-ops # ops id"} {
		if !strings.Contains(got, want) {
			t.Errorf("comment %q lost:\n%s", want, got)
		}
	}
	if strings.Contains(got, "org-dev") {
		t.Errorf("dev profile not removed:\n%s", got)
	}
}
