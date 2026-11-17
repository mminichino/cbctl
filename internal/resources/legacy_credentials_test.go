package resources

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/secrets"
)

// generatedCredYAML is credYAML with no password, so Capella-side creation generates one.
var generatedCredYAML = strings.Replace(credYAML, "        password: ${APP_PW}\n", "", 1)

func TestCredentialRotationKeepsCapellactlDir(t *testing.T) {
	e := newEnv(t, generatedCredYAML)
	legacyDir := filepath.Join(e.dir, filepath.FromSlash(secrets.LegacyDirName))
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	e.load(generatedCredYAML) // the directory choice is made on load, like a fresh invocation
	if got := e.cfg.CredentialsOutput(); got != legacyDir {
		t.Fatalf("CredentialsOutput = %q, want capellactl's %q", got, legacyDir)
	}
	if !strings.Contains(e.cfg.CredentialsOutputNote(), "capellactl") {
		t.Errorf("note = %q", e.cfg.CredentialsOutputNote())
	}
	e.apply(e.plan())
	path := secrets.SecretPath(legacyDir, "one", "app")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("secret not written to the capellactl dir: %v", err)
	}
	e.rc.RotatePasswords = true
	e.apply(e.plan())
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("rotation backup not in the capellactl dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".cbctl")); !os.IsNotExist(err) {
		t.Error(".cbctl/ was created although the capellactl dir is in use")
	}
}

func TestCredentialGenerationRefusesUnignoredRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t, generatedCredYAML)
	if out, err := exec.Command("git", "init", "-q", e.dir).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out) // sandboxed runners may forbid it
	}
	pid := e.f.SeedProject("sandbox")
	e.f.SeedCluster(pid, "one", "healthy")
	res := planExecute(e, e.plan())
	var ni *secrets.NotIgnoredError
	if res.OK() || !errors.As(res.Err, &ni) {
		t.Fatalf("apply in an unignored repo: ok=%v err=%v, want NotIgnoredError", res.OK(), res.Err)
	}
	if requests(e.f, "POST", `/users$`) != 0 {
		t.Fatal("credential created although its password could not be saved safely")
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".cbctl")); !os.IsNotExist(err) {
		t.Error("secret directory written")
	}

	added, err := secrets.EnsureGitignore(e.dir)
	if err != nil || !added {
		t.Fatalf("EnsureGitignore = %v %v", added, err)
	}
	if res := planExecute(e, e.plan()); !res.OK() {
		t.Fatalf("apply after .gitignore: %v", res.Err)
	}
}
