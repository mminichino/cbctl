package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/settings"
)

// legacyFixture is one machine with capellactl leftovers: a cbctl home, a capellactl home, both
// keychains and a working directory, all isolated from the developer's own.
type legacyFixture struct {
	t                 *testing.T
	env               map[string]string
	home, legacy, cwd string
	kr, lkr           *settings.MemKeyring
}

func newLegacyFixture(t *testing.T, profilesYAML string) *legacyFixture {
	t.Helper()
	lf := &legacyFixture{t: t, env: map[string]string{}, home: t.TempDir(), legacy: t.TempDir(), cwd: t.TempDir(),
		kr: settings.NewMemKeyring(), lkr: settings.NewMemKeyring()}
	if profilesYAML != "" {
		lf.write(filepath.Join(lf.legacy, "profiles.yaml"), profilesYAML)
	}
	return lf
}

func (lf *legacyFixture) write(path, text string) {
	lf.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		lf.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		lf.t.Fatal(err)
	}
}

// fake points the fixture at a fake Capella (base URL only: key and org come from profiles).
func (lf *legacyFixture) fake() *capellafake.Fake {
	f := capellafake.New(lf.t)
	lf.env["CAPELLA_API_BASE_URL"] = f.URL
	return f
}

func (lf *legacyFixture) run(args ...string) (stdout, stderr string, code int) {
	lf.t.Helper()
	var out, errBuf strings.Builder
	d := Deps{
		Env: func(k string) string { return lf.env[k] },
		LookupEnv: func(k string) (string, bool) {
			v, ok := lf.env[k]
			return v, ok
		},
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errBuf,
		IsTTY:         func(io.Reader) bool { return false },
		Cwd:           func() (string, error) { return lf.cwd, nil },
		Home:          lf.home,
		LegacyHome:    lf.legacy,
		Keyring:       lf.kr,
		LegacyKeyring: lf.lkr,
		Clock:         engine.NewFakeClock(time.Time{}),
	}
	d.ResetRedaction = true
	code = Run(context.Background(), args, d)
	return out.String(), errBuf.String(), code
}

func TestCapellactlConfigEnvVar(t *testing.T) {
	lf := newLegacyFixture(t, "")
	f := lf.fake()
	lf.env["CAPELLA_API_KEY"], lf.env["CAPELLA_ORG_ID"] = f.APIKey, f.OrgID
	path := filepath.Join(lf.cwd, "elsewhere", "x.yaml")
	lf.write(path, envDoc)
	lf.env["CAPELLACTL_CONFIG"] = path

	stdout, stderr, code := lf.run("capella", "plan")
	if code != 0 {
		t.Fatalf("plan exit %d\n%s\n%s", code, stdout, stderr)
	}
	wantContains(t, stdout, "envclu")
	if n := strings.Count(stderr, "CAPELLACTL_CONFIG is deprecated"); n != 1 {
		t.Errorf("deprecation warnings = %d, want 1:\n%s", n, stderr)
	}
}

func TestLegacyEnvAuth(t *testing.T) {
	lf := newLegacyFixture(t, "")
	f := lf.fake()
	lf.env["BEARER_TOKEN"], lf.env["ORG_ID"] = f.APIKey, f.OrgID
	f.SeedProject("legacy-env-proj")
	stdout, stderr, code := lf.run("capella", "project", "list")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	wantContains(t, stdout, "legacy-env-proj")
	wantContains(t, stderr, "BEARER_TOKEN is deprecated; use CAPELLA_API_KEY", "ORG_ID is deprecated; use CAPELLA_ORG_ID")
	if strings.Contains(stderr, f.APIKey) {
		t.Error("API key printed")
	}

	// The cbctl variable wins over a different legacy one, with a warning naming both.
	lf.env["CAPELLA_API_KEY"], lf.env["BEARER_TOKEN"] = f.APIKey, "stale-key"
	_, stderr, code = lf.run("capella", "project", "list")
	if code != 0 || !strings.Contains(stderr, "CAPELLA_API_KEY is also set and wins") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
}

func TestLegacyProfileAndKeychainFallback(t *testing.T) {
	lf := newLegacyFixture(t, "")
	f := lf.fake()
	lf.write(filepath.Join(lf.legacy, "profiles.yaml"), "profiles:\n  default:\n    organizationId: "+f.OrgID+"\n")
	if err := lf.lkr.Set("default", f.APIKey); err != nil {
		t.Fatal(err)
	}
	f.SeedProject("from-legacy")
	stdout, stderr, code := lf.run("capella", "project", "list")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	wantContains(t, stdout, "from-legacy")
	for _, w := range []string{"capellactl's keychain entry", "profiles.yaml; run"} {
		if n := strings.Count(stderr, w); n != 1 {
			t.Errorf("%q printed %d times, want once:\n%s", w, n, stderr)
		}
	}
}

func TestConfigureMigrateCommand(t *testing.T) {
	lf := newLegacyFixture(t, "")
	f := lf.fake()
	lf.write(filepath.Join(lf.legacy, "profiles.yaml"),
		"profiles:\n  default:\n    organizationId: org-d\n  prod:\n    organizationId: "+f.OrgID+"\n")
	for p, k := range map[string]string{"default": "key-default", "prod": f.APIKey} {
		if err := lf.lkr.Set(p, k); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, code := lf.run("configure", "migrate", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "would import profile prod") {
		t.Fatalf("dry run: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if _, err := lf.kr.Get("prod"); err == nil {
		t.Fatal("dry run stored a key")
	}

	stdout, stderr, code = lf.run("configure", "migrate")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	wantContains(t, stdout, "imported profile default", "imported profile prod")
	if strings.Contains(stdout+stderr, f.APIKey) || strings.Contains(stdout+stderr, "key-default") {
		t.Error("an API key was printed")
	}

	stdout, _, code = lf.run("configure", "import-capellactl", "-o", "json")
	var rep settings.MigrationReport
	if code != 0 || json.Unmarshal([]byte(stdout), &rep) != nil || strings.Join(rep.Skipped, ",") != "default,prod" {
		t.Fatalf("second run (json): exit %d %q", code, stdout)
	}

	// After the migration the key comes from cbctl's keychain, with no capellactl warnings.
	lf.lkr = settings.NewMemKeyring()
	f.SeedProject("after-migrate")
	stdout, stderr, code = lf.run("capella", "project", "list", "--profile", "prod")
	if code != 0 || !strings.Contains(stdout, "after-migrate") || strings.Contains(stderr, "capellactl") {
		t.Fatalf("after migrate: exit %d\n%s\n%s", code, stdout, stderr)
	}

	// A cbctl profile that differs is a conflict (exit 4) and is left alone.
	lf.write(filepath.Join(lf.legacy, "profiles.yaml"), "profiles:\n  prod:\n    organizationId: changed\n")
	stdout, stderr, code = lf.run("configure", "migrate")
	if code != int(ExitBlocked) || !strings.Contains(stdout, "conflict: profile prod") {
		t.Fatalf("conflict: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if p, _ := settings.LoadProfile("prod", func(string) string { return "" }, lf.home); p.OrganizationID != f.OrgID {
		t.Errorf("conflicting profile overwritten: %+v", p)
	}
	if _, _, code = lf.run("configure", "migrate", "--force"); code != 0 {
		t.Fatalf("--force exit %d", code)
	}

	lf.write(filepath.Join(lf.legacy, "profiles.yaml"), "profiles: [")
	if _, _, code = lf.run("configure", "migrate"); code != int(ExitConfigInvalid) {
		t.Errorf("malformed legacy profiles: exit %d, want 3", code)
	}
	empty := newLegacyFixture(t, "")
	if stdout, _, code = empty.run("configure", "migrate"); code != 0 || !strings.Contains(stdout, "nothing to migrate") {
		t.Errorf("empty: exit %d %q", code, stdout)
	}
}

func TestCapellaDoctor(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		lf := newLegacyFixture(t, "")
		stdout, stderr, code := lf.run("capella", "doctor", "--detailed-exitcode")
		if code != 0 || !strings.Contains(stdout, "No capellactl leftovers") {
			t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
		}
	})
	t.Run("fully legacy", func(t *testing.T) {
		lf := newLegacyFixture(t, "profiles:\n  default:\n    organizationId: org-1\n")
		if err := lf.lkr.Set("default", "SECRETKEY123"); err != nil {
			t.Fatal(err)
		}
		lf.env["BEARER_TOKEN"] = "SECRETKEY123"
		lf.write(filepath.Join(lf.cwd, "capella.yaml"), strings.Replace(envDoc, "cbctl/v1", "capellactl/v1", 1))
		lf.write(filepath.Join(lf.cwd, ".capellactl", "credentials", "envclu.app.json"), "{}")
		kinds := []string{doctorLegacyEnv, doctorLegacyProfile, doctorLegacyKeychain, doctorLegacyCredsDir, doctorAPIVersion}
		if _, err := exec.LookPath("git"); err == nil {
			if err := exec.Command("git", "init", "-q", lf.cwd).Run(); err == nil {
				kinds = append(kinds, doctorCredsNotIgnored)
			}
		}

		stdout, stderr, code := lf.run("capella", "doctor", "--detailed-exitcode", "-o", "json")
		if code != int(ExitBlocked) {
			t.Fatalf("exit %d, want 4\n%s\n%s", code, stdout, stderr)
		}
		var rep DoctorReport
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
		}
		seen := map[string]bool{}
		for _, it := range rep.Items {
			seen[it.Kind] = true
			if it.Action == "" {
				t.Errorf("item %+v has no action", it)
			}
		}
		for _, k := range kinds {
			if !seen[k] {
				t.Errorf("kind %s missing from %+v", k, rep.Items)
			}
		}
		if strings.Contains(stdout+stderr, "SECRETKEY123") {
			t.Errorf("a key was printed:\n%s\n%s", stdout, stderr)
		}
		if _, _, code := lf.run("capella", "doctor"); code != 0 {
			t.Errorf("without --detailed-exitcode: exit %d", code)
		}
	})
}

func TestSyncKeepsCapellactlAPIVersion(t *testing.T) {
	lf := newLegacyFixture(t, "")
	f := lf.fake()
	lf.env["CAPELLA_API_KEY"], lf.env["CAPELLA_ORG_ID"] = f.APIKey, f.OrgID
	f.SeedProject("envproj")
	path := filepath.Join(lf.cwd, "capella.yaml")
	lf.write(path, "apiVersion: capellactl/v1\nprojects:\n  - name: envproj\n    clusters: []\n")
	if stdout, stderr, code := lf.run("capella", "sync"); code != 0 {
		t.Fatalf("sync exit %d\n%s\n%s", code, stdout, stderr)
	}
	if got := rawString(path, "apiVersion"); got != "capellactl/v1" {
		t.Errorf("apiVersion after sync = %q, want capellactl/v1", got)
	}
	if _, _, code := lf.run("capella", "sync", "--api-version", "cbctl/v1"); code != 0 {
		t.Fatalf("sync --api-version exit %d", code)
	}
	if got := rawString(path, "apiVersion"); got != "cbctl/v1" {
		t.Errorf("apiVersion after --api-version = %q", got)
	}
	if _, _, code := lf.run("capella", "sync", "--api-version", "v9"); code != int(ExitConfigInvalid) {
		t.Errorf("bad --api-version: exit %d, want 3", code)
	}
}

// TestCapellactlExamplesPlan plans every capellactl example, unchanged, against an empty Capella.
func TestCapellactlExamplesPlan(t *testing.T) {
	src := filepath.Join("..", "config", "capellafile", "testdata", "capellactl")
	files, err := filepath.Glob(filepath.Join(src, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no snapshots: %v", err)
	}
	secret, err := os.ReadFile(filepath.Join(src, "secrets", "reporting_ro.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			lf := newLegacyFixture(t, "")
			fk := lf.fake()
			lf.env["CAPELLA_API_KEY"], lf.env["CAPELLA_ORG_ID"] = fk.APIKey, fk.OrgID
			lf.env["ORDERS_APP_PASSWORD"] = "Passw0rd!Passw0rd!"
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			lf.write(filepath.Join(lf.cwd, "capella.yaml"), string(raw))
			lf.write(filepath.Join(lf.cwd, "secrets", "reporting_ro.txt"), string(secret))
			if stdout, stderr, code := lf.run("capella", "plan"); code != 0 {
				t.Fatalf("plan exit %d\n%s\n%s", code, stdout, stderr)
			}
		})
	}
}

func TestSyncIgnoresCredentialsInGitRepo(t *testing.T) {
	lf := newLegacyFixture(t, "")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := exec.Command("git", "init", "-q", lf.cwd).Run(); err != nil {
		t.Skipf("git init: %v", err)
	}
	f := lf.fake()
	lf.env["CAPELLA_API_KEY"], lf.env["CAPELLA_ORG_ID"] = f.APIKey, f.OrgID
	f.SeedProject("envproj")
	stdout, stderr, code := lf.run("capella", "sync")
	if code != 0 {
		t.Fatalf("sync exit %d\n%s\n%s", code, stdout, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(lf.cwd, ".gitignore"))
	if err != nil || !strings.Contains(string(raw), ".cbctl/") {
		t.Fatalf(".gitignore = %q %v", raw, err)
	}
	wantContains(t, stderr, "added .cbctl/ to")
}
