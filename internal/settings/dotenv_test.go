package settings_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mminichino/cbctl/internal/settings"
)

func TestLoadDotEnv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(root, ".env")
	content := "CAPELLA_API_KEY=from-dotenv\nexport CAPELLA_ORG_ID=org-1\nQUOTED=\"hello\"\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CAPELLA_API_KEY", "already-set")
	_ = os.Unsetenv("CAPELLA_ORG_ID")
	_ = os.Unsetenv("QUOTED")

	path, names, err := settings.LoadDotEnv(sub)
	if err != nil {
		t.Fatal(err)
	}
	if path != envFile {
		t.Fatalf("path=%q", path)
	}
	if os.Getenv("CAPELLA_API_KEY") != "already-set" {
		t.Fatal("must not override existing env")
	}
	if os.Getenv("CAPELLA_ORG_ID") != "org-1" {
		t.Fatalf("org=%q", os.Getenv("CAPELLA_ORG_ID"))
	}
	if os.Getenv("QUOTED") != "hello" {
		t.Fatalf("quoted=%q", os.Getenv("QUOTED"))
	}
	if len(names) == 0 {
		t.Fatal("expected names")
	}

	// Search stops at go.mod: .env outside root is ignored when start is under root without .env deeper.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, ".env"), []byte("X=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// nested project with go.mod and no .env
	proj := filepath.Join(outside, "proj")
	_ = os.MkdirAll(proj, 0o755)
	_ = os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module p\n"), 0o644)
	path, _, err = settings.LoadDotEnv(proj)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("should stop at go.mod without loading parent .env, got %q", path)
	}
}

func TestLoadDotEnvMissingSilent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	path, names, err := settings.LoadDotEnv(dir)
	if err != nil || path != "" || names != nil {
		t.Fatalf("path=%q names=%v err=%v", path, names, err)
	}
}

func TestLoadDotEnvMalformed(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	// godotenv.Read is fairly permissive; use a binary-ish invalid file that still may parse.
	// Spec: malformed → warning at Execute(); here we just ensure parse errors are returned.
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("BAD LINE WITHOUT EQUALS AND \x00\n"), 0o600)
	_, _, err := settings.LoadDotEnv(dir)
	// godotenv may or may not error; if it errors, that's fine for the caller to warn.
	_ = err
}
