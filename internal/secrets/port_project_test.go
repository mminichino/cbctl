package secrets

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// P-TST-14: security coverage of the git guard, credential files and the password generator.

// gitRepo creates a git work tree in a temp dir and returns its path (resolved, so it compares
// equal to what git prints on macOS where /var is a symlink).
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "--template=").CombinedOutput(); err != nil {
		// Some sandboxes forbid writing .git/; that is the environment, not the code under test.
		t.Skipf("git init not possible here: %v\n%s", err, out)
	}
	return dir
}

func TestGitWarning_NotIgnoredIgnoredAndOutside(t *testing.T) {
	repo := gitRepo(t)
	creds := filepath.Join(repo, ".cbctl", "credentials")

	// The directory does not exist yet: the nearest existing ancestor is probed.
	what, action, bad := GitWarning(creds)
	if !bad || what != creds+" is not git-ignored" || !strings.Contains(action, ".gitignore") {
		t.Errorf("not ignored = %q, %q, %v", what, action, bad)
	}
	if err := CheckIgnored(creds, false); err == nil {
		t.Error("CheckIgnored passed a directory git would commit")
	} else {
		var nie *NotIgnoredError
		if !errors.As(err, &nie) || !strings.Contains(nie.Error(), "not git-ignored") || !strings.Contains(nie.Action(), "--force-overwrite-secret") {
			t.Errorf("CheckIgnored = %v", err)
		}
	}
	if err := CheckIgnored(creds, true); err != nil {
		t.Errorf("forced CheckIgnored = %v", err)
	}

	changed, err := EnsureGitignore(repo)
	if err != nil || !changed {
		t.Fatalf("EnsureGitignore = %v, %v", changed, err)
	}
	if _, _, bad := GitWarning(creds); bad {
		t.Error("still warns after .gitignore was updated")
	}
	if err := CheckIgnored(creds, false); err != nil {
		t.Errorf("CheckIgnored after .gitignore = %v", err)
	}
	if changed, err := EnsureGitignore(repo); err != nil || changed {
		t.Errorf("second EnsureGitignore = %v, %v", changed, err)
	}

	// Outside any work tree there is nothing to warn about.
	outside := t.TempDir()
	if _, _, bad := GitWarning(filepath.Join(outside, "x", "y")); bad && !insideGitTree(outside) {
		t.Error("warned outside a work tree")
	}
	if changed, err := EnsureGitignore(outside); err != nil || (changed && !insideGitTree(outside)) {
		t.Errorf("EnsureGitignore outside = %v, %v", changed, err)
	}
}

func insideGitTree(dir string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Run() == nil
}

func TestEnsureGitignore_ExistingFile(t *testing.T) {
	t.Run("appends after a line without newline", func(t *testing.T) {
		repo := gitRepo(t)
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("bin"), 0o600); err != nil {
			t.Fatal(err)
		}
		if changed, err := EnsureGitignore(repo); err != nil || !changed {
			t.Fatalf("EnsureGitignore = %v, %v", changed, err)
		}
		raw, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
		if string(raw) != "bin\n.cbctl/\n" {
			t.Errorf(".gitignore = %q", raw)
		}
	})
	t.Run("an overridden rule is left alone", func(t *testing.T) {
		repo := gitRepo(t)
		text := ".cbctl/\n!.cbctl/credentials/\n"
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if changed, err := EnsureGitignore(repo); err != nil || changed {
			t.Errorf("EnsureGitignore = %v, %v", changed, err)
		}
		raw, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
		if string(raw) != text {
			t.Errorf(".gitignore = %q", raw)
		}
	})
	t.Run("unreadable .gitignore", func(t *testing.T) {
		repo := gitRepo(t)
		if err := os.Mkdir(filepath.Join(repo, ".gitignore"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := EnsureGitignore(repo); err == nil {
			t.Error("a .gitignore directory was accepted")
		}
	})
}

func TestCredentialFiles_Errors(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteGeneratedCredential(dir, "c1", "app", "id-1", "Secret-pw-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", fi.Mode().Perm(), err)
	}
	_, err = WriteGeneratedCredential(dir, "c1", "app", "id-1", "Secret-pw-5678", false)
	var fee *FileExistsError
	if !errors.As(err, &fee) || !strings.Contains(fee.Error(), "--force-overwrite-secret") {
		t.Errorf("overwrite = %v", err)
	}

	// The secrets directory cannot be created under a file.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteGeneratedCredential(filepath.Join(blocker, "sub"), "c1", "app", "id", "Secret-pw-1234", false); err == nil {
		t.Error("wrote under a file")
	}
	if _, err := Preflight(filepath.Join(blocker, "sub"), "c1", "app", false); err == nil {
		t.Error("Preflight under a file passed")
	}
	// The target path is a directory: the rename fails and no temp file is left.
	target := SecretPath(dir, "c2", "app")
	if err := os.MkdirAll(filepath.Join(target, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteGeneratedCredential(dir, "c2", "app", "id", "Secret-pw-1234", true); err == nil {
		t.Error("renamed over a directory")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".cred-*.tmp")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestChmodOrWarn_WarnsOncePerDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	chmodOrWarn(dir, filepath.Join(dir, "a"), 0o600)
	chmodOrWarn(dir, filepath.Join(dir, "b"), 0o600)
	if _, seen := chmodWarned.Load(dir); !seen {
		t.Error("the failed chmod was not recorded")
	}
}

func TestRotatedCredential_RestorePaths(t *testing.T) {
	dir := t.TempDir()
	if err := RestoreRotatedCredential("", ""); err != nil {
		t.Errorf("nothing to restore = %v", err)
	}
	// No previous file: restoring removes the new one.
	path, backup, err := WriteRotatedCredential(dir, "c1", "app", "id", "Secret-pw-1111")
	if err != nil || backup != "" {
		t.Fatalf("first rotation = %q, %q, %v", path, backup, err)
	}
	if err := RestoreRotatedCredential(path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("new file kept")
	}
	if err := RestoreRotatedCredential(path, ""); err != nil {
		t.Errorf("restore of an absent file = %v", err)
	}
	// With a previous file: restoring puts it back.
	if _, err := WriteGeneratedCredential(dir, "c1", "app", "id", "Secret-pw-2222", false); err != nil {
		t.Fatal(err)
	}
	path, backup, err = WriteRotatedCredential(dir, "c1", "app", "id", "Secret-pw-3333")
	if err != nil || backup == "" {
		t.Fatalf("rotation = %q, %q, %v", path, backup, err)
	}
	if err := RestoreRotatedCredential(path, backup); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "Secret-pw-2222") {
		t.Errorf("restored file = %s", raw)
	}
	if err := RestoreRotatedCredential(path, filepath.Join(dir, "missing.bak")); err == nil {
		t.Error("restore from a missing backup passed")
	}
	if err := RestoreRotatedCredential(filepath.Join(dir, "nodir", "x.json"), backup); err == nil {
		t.Error("restore into a missing dir passed")
	}
	nonEmpty := filepath.Join(dir, "full")
	if err := os.MkdirAll(filepath.Join(nonEmpty, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RestoreRotatedCredential(nonEmpty, ""); err == nil {
		t.Error("removing a non-empty directory passed")
	}
	// The backup cannot be written.
	bakDir := SecretPath(dir, "c3", "app") + ".bak"
	if err := os.WriteFile(SecretPath(dir, "c3", "app"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bakDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteRotatedCredential(dir, "c3", "app", "id", "Secret-pw-4444"); err == nil {
		t.Error("rotation over an unwritable backup passed")
	}
}

func TestRestoreError_Action(t *testing.T) {
	primary := errors.New("PUT failed")
	e := &RestoreError{Primary: primary, Restore: errors.New("disk full"), Path: "/s/c.app.json"}
	if !errors.Is(e, primary) || !strings.Contains(e.Error(), "disk full") {
		t.Errorf("error = %v", e)
	}
	if !strings.HasPrefix(e.Action(), "delete /s/c.app.json") {
		t.Errorf("Action = %s", e.Action())
	}
	e.Backup = "/s/c.app.json.bak"
	if e.Action() != "copy /s/c.app.json.bak to /s/c.app.json" {
		t.Errorf("Action = %s", e.Action())
	}
}

func TestMustGenerate_PanicsWithoutEntropy(t *testing.T) {
	old := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = old })
	defer func() {
		if recover() == nil {
			t.Error("MustGenerate did not panic")
		}
	}()
	MustGenerate(16)
}
