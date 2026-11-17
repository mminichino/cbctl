package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/settings"
)

const legacyProfilesYAML = `profiles:
  default:
    organizationId: org-1
    organizationName: Example
  prod:
    organizationId: org-2
    apiBaseUrl: https://cloudapi.cloud.couchbase.com
    configFile: /work/capella.yaml
    color: blue
`

// legacyHome writes a capellactl home with two profiles.
func legacyHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte(legacyProfilesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadLegacyProfiles(t *testing.T) {
	t.Parallel()
	got, warns, err := settings.LoadLegacyProfiles(legacyHome(t))
	if err != nil {
		t.Fatal(err)
	}
	if got["default"].OrganizationID != "org-1" || got["prod"].ConfigFile != "/work/capella.yaml" ||
		got["prod"].APIBaseURL != "https://cloudapi.cloud.couchbase.com" {
		t.Errorf("profiles = %+v", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], `"color"`) {
		t.Errorf("warnings = %q, want one for the unknown key", warns)
	}

	if got, _, err := settings.LoadLegacyProfiles(t.TempDir()); err != nil || len(got) != 0 {
		t.Errorf("missing file: %v %v", got, err)
	}
	for name, body := range map[string]string{
		"malformed": "profiles: [",
		"bad url":   "profiles:\n  p:\n    apiBaseUrl: http://example.com\n",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var pe *settings.LegacyProfileError
		if _, _, err := settings.LoadLegacyProfiles(dir); !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want LegacyProfileError", name, err)
		}
	}
}

func TestResolveFallsBackToCapellactl(t *testing.T) {
	t.Parallel()
	home, legacy := t.TempDir(), legacyHome(t)
	lkr := settings.NewMemKeyring()
	if err := lkr.Set("prod", "legacy-key"); err != nil {
		t.Fatal(err)
	}
	s, err := settings.Resolve(settings.Inputs{
		Profile: "prod", Env: func(string) string { return "" }, Home: home,
		Keyring: settings.NewMemKeyring(), LegacyHome: legacy, LegacyKeyring: lkr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.APIKey.Value != "legacy-key" || s.OrgID.Value != "org-2" || s.ConfigFile.Value != "/work/capella.yaml" {
		t.Errorf("settings = %+v", s)
	}
	joined := strings.Join(s.Warnings, "\n")
	for _, want := range []string{"read from capellactl", "capellactl's keychain", "cbctl configure migrate"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q lack %q", joined, want)
		}
	}

	// A cbctl profile of the same name wins and nothing is read from capellactl.
	if err := (settings.ProfileStore{Path: settings.ProfilesPath(home)}).Save(map[string]settings.Profile{"prod": {OrganizationID: "org-new"}}); err != nil {
		t.Fatal(err)
	}
	kr := settings.NewMemKeyring()
	if err := kr.Set("prod", "new-key"); err != nil {
		t.Fatal(err)
	}
	s, err = settings.Resolve(settings.Inputs{
		Profile: "prod", Env: func(string) string { return "" }, Home: home,
		Keyring: kr, LegacyHome: legacy, LegacyKeyring: lkr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.APIKey.Value != "new-key" || s.OrgID.Value != "org-new" || len(s.Warnings) != 0 {
		t.Errorf("cbctl profile must win: %+v", s)
	}
}

// brokenKeyring fails every read, like a locked keychain.
type brokenKeyring struct{}

func (brokenKeyring) Get(string) (string, error) { return "", errors.New("keychain locked") }
func (brokenKeyring) Set(string, string) error   { return errors.New("keychain locked") }
func (brokenKeyring) Delete(string) error        { return errors.New("keychain locked") }

func TestMigrateLegacy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newRun := func(t *testing.T) (settings.MigrateOptions, *settings.MemKeyring) {
		lkr := settings.NewMemKeyring()
		for _, p := range []string{"default", "prod"} {
			if err := lkr.Set(p, "key-"+p); err != nil {
				t.Fatal(err)
			}
		}
		kr := settings.NewMemKeyring()
		return settings.MigrateOptions{FromDir: legacyHome(t), ToDir: t.TempDir(), Keyring: kr, LegacyKeyring: lkr}, kr
	}
	load := func(t *testing.T, dir string) map[string]settings.Profile {
		p, err := settings.ProfileStore{Path: settings.ProfilesPath(dir)}.Load()
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("imports, then skips", func(t *testing.T) {
		t.Parallel()
		opts, kr := newRun(t)
		rep, err := settings.MigrateLegacy(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(rep.Imported, ",") != "default,prod" || len(rep.KeyMissing) != 0 {
			t.Fatalf("report = %+v", rep)
		}
		if got := load(t, opts.ToDir); got["prod"].OrganizationID != "org-2" {
			t.Errorf("profiles = %+v", got)
		}
		if k, _ := kr.Get("prod"); k != "key-prod" {
			t.Errorf("key = %q", k)
		}
		again, err := settings.MigrateLegacy(ctx, opts)
		if err != nil || strings.Join(again.Skipped, ",") != "default,prod" || len(again.Imported) != 0 {
			t.Errorf("second run = %+v %v", again, err)
		}
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		t.Parallel()
		opts, kr := newRun(t)
		opts.DryRun = true
		rep, err := settings.MigrateLegacy(ctx, opts)
		if err != nil || len(rep.Imported) != 2 {
			t.Fatalf("report = %+v %v", rep, err)
		}
		if _, err := os.Stat(settings.ProfilesPath(opts.ToDir)); !os.IsNotExist(err) {
			t.Error("profiles.yaml written by a dry run")
		}
		if _, err := kr.Get("prod"); !errors.Is(err, settings.ErrNotFound) {
			t.Error("key written by a dry run")
		}
	})

	t.Run("conflict, then force", func(t *testing.T) {
		t.Parallel()
		opts, kr := newRun(t)
		if err := (settings.ProfileStore{Path: settings.ProfilesPath(opts.ToDir)}).Save(map[string]settings.Profile{"prod": {OrganizationID: "other"}}); err != nil {
			t.Fatal(err)
		}
		rep, err := settings.MigrateLegacy(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rep.Conflicts["prod"]; !ok || strings.Join(rep.Imported, ",") != "default" {
			t.Fatalf("report = %+v", rep)
		}
		if load(t, opts.ToDir)["prod"].OrganizationID != "other" {
			t.Error("conflicting profile overwritten without --force")
		}
		opts.Force = true
		rep, err = settings.MigrateLegacy(ctx, opts)
		if err != nil || len(rep.Conflicts) != 0 {
			t.Fatalf("forced = %+v %v", rep, err)
		}
		if load(t, opts.ToDir)["prod"].OrganizationID != "org-2" {
			t.Error("--force did not overwrite")
		}
		if k, _ := kr.Get("prod"); k != "key-prod" {
			t.Errorf("key = %q", k)
		}
	})

	t.Run("locked legacy keychain", func(t *testing.T) {
		t.Parallel()
		opts, _ := newRun(t)
		opts.LegacyKeyring = brokenKeyring{}
		opts.RemoveLegacy = true
		rep, err := settings.MigrateLegacy(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(rep.KeyMissing, ",") != "default,prod" || len(rep.Removed) != 0 {
			t.Errorf("report = %+v", rep)
		}
	})

	t.Run("remove legacy", func(t *testing.T) {
		t.Parallel()
		opts, _ := newRun(t)
		opts.RemoveLegacy = true
		opts.Profiles = []string{"prod"}
		lkr := opts.LegacyKeyring
		rep, err := settings.MigrateLegacy(ctx, opts)
		if err != nil || strings.Join(rep.Removed, ",") != "prod" {
			t.Fatalf("report = %+v %v", rep, err)
		}
		left, _, err := settings.LoadLegacyProfiles(opts.FromDir)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := left["prod"]; ok || left["default"].OrganizationID != "org-1" {
			t.Errorf("capellactl profiles after removal = %+v", left)
		}
		if _, err := lkr.Get("prod"); !errors.Is(err, settings.ErrNotFound) {
			t.Error("legacy key not removed")
		}
	})

	t.Run("unknown profile and nothing to migrate", func(t *testing.T) {
		t.Parallel()
		opts, _ := newRun(t)
		opts.Profiles = []string{"nope"}
		if _, err := settings.MigrateLegacy(ctx, opts); !errors.Is(err, settings.ErrUnknownLegacyProfile) {
			t.Errorf("err = %v", err)
		}
		rep, err := settings.MigrateLegacy(ctx, settings.MigrateOptions{FromDir: t.TempDir(), ToDir: t.TempDir()})
		if err != nil || !rep.Nothing() {
			t.Errorf("empty home = %+v %v", rep, err)
		}
	})
}

func TestMigrateLegacySharedHome(t *testing.T) {
	t.Parallel()
	home := legacyHome(t)
	before, err := os.ReadFile(settings.ProfilesPath(home))
	if err != nil {
		t.Fatal(err)
	}
	lkr, kr := settings.NewMemKeyring(), settings.NewMemKeyring()
	if err := lkr.Set("prod", "key-prod"); err != nil {
		t.Fatal(err)
	}
	rep, err := settings.MigrateLegacy(context.Background(), settings.MigrateOptions{
		FromDir: home, ToDir: home, Keyring: kr, LegacyKeyring: lkr, RemoveLegacy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Imported, ",") != "prod" || strings.Join(rep.Skipped, ",") != "default" {
		t.Errorf("report = %+v", rep)
	}
	if k, _ := kr.Get("prod"); k != "key-prod" {
		t.Errorf("key = %q", k)
	}
	after, err := os.ReadFile(settings.ProfilesPath(home))
	if err != nil || string(after) != string(before) {
		t.Errorf("shared profiles.yaml changed:\n%s", after)
	}
}
