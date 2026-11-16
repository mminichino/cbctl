package port

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPort_RefusesDirtySourceUnlessForce(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	// --template= avoids copying hook templates, which read-only or sandboxed
	// git installs can refuse. Some sandboxes forbid creating .git at all; that
	// is an environment limit, not a product failure, so skip in that case.
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "--template=").CombinedOutput(); err != nil {
		if strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "Permission denied") {
			t.Skipf("environment forbids creating a git repo: %s", out)
		}
		t.Fatalf("git init: %v: %s", err, out)
	}
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "test")
	docs := filepath.Join(dir, "docs", "release-1.5.0")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	story := filepath.Join(docs, "CAP-099-example.md")
	if err := os.WriteFile(story, []byte("# CAP-099 — Example\n\n```\ncapellactl sync\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-qm", "init")

	// Dirty the tree.
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	_, err := Port(Options{Src: filepath.Join(dir, "docs"), Dst: dst, Release: []string{"1.5.0"}})
	if err == nil {
		t.Fatal("expected dirty-tree error")
	}
	if _, err := Port(Options{Src: filepath.Join(dir, "docs"), Dst: dst, Release: []string{"1.5.0"}, Force: true}); err != nil {
		t.Fatalf("force should allow dirty tree: %v", err)
	}
}
