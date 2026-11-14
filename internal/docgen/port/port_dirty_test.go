package port

import (
	"os"
	"os/exec"
	"path/filepath"
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
	run("git", "init", "-q")
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
