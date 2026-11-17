package secrets_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/secrets"
)

func TestWriteGeneratedCredential(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	dir := t.TempDir()
	path, err := secrets.WriteGeneratedCredential(dir, "c1", "user", "id-1", "SecretPass1!", false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode=%o", fi.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "SecretPass1!") {
		t.Fatal("password missing from file")
	}
	masked := redact.String("leak SecretPass1! here")
	if strings.Contains(masked, "SecretPass1!") {
		t.Fatal("password not registered with redact")
	}
	_, err = secrets.WriteGeneratedCredential(dir, "c1", "user", "id-1", "OtherPass1!", false)
	if err == nil {
		t.Fatal("expected refuse overwrite")
	}
	if _, ok := err.(*secrets.FileExistsError); !ok {
		t.Fatalf("want FileExistsError got %T %v", err, err)
	}
	msg := secrets.SavedMessage(path)
	if !strings.Contains(msg, path) || strings.Contains(msg, "SecretPass1!") {
		t.Fatalf("msg=%q", msg)
	}
	_ = filepath.Base(path)
}

func TestRotateRestore(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	dir := t.TempDir()
	_, err := secrets.WriteGeneratedCredential(dir, "c", "u", "1", "FirstPass1!", true)
	if err != nil {
		t.Fatal(err)
	}
	path, bak, err := secrets.WriteRotatedCredential(dir, "c", "u", "1", "SecondPass2!")
	if err != nil {
		t.Fatal(err)
	}
	if bak == "" {
		t.Fatal("expected backup")
	}
	if err := secrets.RestoreRotatedCredential(path, bak); err != nil {
		t.Fatalf("RestoreRotatedCredential: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "FirstPass1!") {
		t.Fatalf("restore failed: %s", raw)
	}
}

func TestRestoreRotatedCredentialNoBackupRemovesFile(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	dir := t.TempDir()
	path, bak, err := secrets.WriteRotatedCredential(dir, "c", "u", "1", "OnlyPass1!")
	if err != nil {
		t.Fatal(err)
	}
	if bak != "" {
		t.Fatalf("backup = %q, want none for a first write", bak)
	}
	if err := secrets.RestoreRotatedCredential(path, bak); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present after restore without backup: %v", err)
	}
}

func TestRestoreRotatedCredentialError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	redact.Reset()
	t.Cleanup(redact.Reset)
	dir := t.TempDir()
	if _, err := secrets.WriteGeneratedCredential(dir, "c", "u", "1", "FirstPass1!", true); err != nil {
		t.Fatal(err)
	}
	path, bak, err := secrets.WriteRotatedCredential(dir, "c", "u", "1", "SecondPass2!")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	err = secrets.RestoreRotatedCredential(path, bak)
	if err == nil {
		t.Fatal("want an error restoring over a read-only file")
	}
	re := &secrets.RestoreError{Primary: errors.New("HTTP 500"), Restore: err, Path: path, Backup: bak}
	if !strings.Contains(re.Error(), "HTTP 500") || !strings.Contains(re.Action(), bak) || !strings.Contains(re.Action(), path) {
		t.Fatalf("RestoreError = %q / %q; want both paths and the primary error", re.Error(), re.Action())
	}
	if !errors.Is(re, re.Primary) {
		t.Fatal("RestoreError must unwrap to the primary error")
	}
}

func TestGitWarningUsesDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A directory outside any repository never warns, whatever the process cwd is.
	dir := t.TempDir()
	if _, _, ok := secrets.GitWarning(filepath.Join(dir, ".cbctl", "secrets")); ok {
		t.Fatal("warned for a directory outside a git work tree")
	}
}

// gitRepo makes a fresh git work tree; it skips when git is not installed.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	return dir
}

func TestCheckIgnoredAndEnsureGitignore(t *testing.T) {
	repo := gitRepo(t)
	creds := filepath.Join(repo, ".cbctl", "credentials") // not created yet
	var ni *secrets.NotIgnoredError
	if err := secrets.CheckIgnored(creds, false); !errors.As(err, &ni) {
		t.Fatalf("unignored dir in a repo: err = %v, want NotIgnoredError", err)
	}
	if err := secrets.CheckIgnored(creds, true); err != nil {
		t.Fatalf("force: %v", err)
	}
	added, err := secrets.EnsureGitignore(repo)
	if err != nil || !added {
		t.Fatalf("EnsureGitignore = %v, %v", added, err)
	}
	if err := secrets.CheckIgnored(creds, false); err != nil {
		t.Fatalf("after .gitignore: %v", err)
	}
	if added, err := secrets.EnsureGitignore(repo); err != nil || added {
		t.Fatalf("second EnsureGitignore = %v, %v; want no change", added, err)
	}
	raw, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil || strings.Count(string(raw), ".cbctl/") != 1 {
		t.Fatalf(".gitignore = %q %v", raw, err)
	}

	outside := t.TempDir()
	if added, err := secrets.EnsureGitignore(outside); err != nil || added {
		t.Fatalf("outside a repo = %v, %v", added, err)
	}
	if err := secrets.CheckIgnored(filepath.Join(outside, ".cbctl", "credentials"), false); err != nil {
		t.Fatalf("outside a repo: %v", err)
	}
}

func TestResolveCredentialsDir(t *testing.T) {
	t.Parallel()
	mk := func(t *testing.T, base string, rel ...string) {
		for _, r := range rel {
			if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(r)), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name, configured string
		dirs             []string
		want             string
		legacy, note     bool
	}{
		{"default", "", nil, secrets.DefaultDirName, false, false},
		{"only legacy", "", []string{secrets.LegacyDirName}, secrets.LegacyDirName, true, true},
		{"both", "", []string{secrets.LegacyDirName, secrets.DefaultDirName}, secrets.DefaultDirName, false, true},
		{"configured wins", "out", []string{secrets.LegacyDirName}, "out", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			mk(t, base, c.dirs...)
			dir, legacy, note := secrets.ResolveCredentialsDir(c.configured, base)
			if dir != filepath.Join(base, filepath.FromSlash(c.want)) || legacy != c.legacy || (note != "") != c.note {
				t.Errorf("= (%q, %v, %q)", dir, legacy, note)
			}
		})
	}
	// An absolute path needs a drive letter on Windows; "/abs/out" is rooted but not absolute there.
	abs, base := "/abs/out", "/base"
	if runtime.GOOS == "windows" {
		abs, base = `C:\abs\out`, `C:\base`
	}
	if dir, _, _ := secrets.ResolveCredentialsDir(abs, base); dir != filepath.Clean(abs) {
		t.Errorf("absolute = %q", dir)
	}
}
