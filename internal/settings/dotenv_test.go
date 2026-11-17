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

	lookup := func(k string) (string, bool) {
		if k == "CAPELLA_API_KEY" {
			return "already-set", true
		}
		return "", false
	}

	path, values, names, err := settings.LoadDotEnv(sub, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if path != envFile {
		t.Fatalf("path=%q", path)
	}
	if _, ok := values["CAPELLA_API_KEY"]; ok {
		t.Fatal("must not override existing env")
	}
	if values["CAPELLA_ORG_ID"] != "org-1" {
		t.Fatalf("org=%q", values["CAPELLA_ORG_ID"])
	}
	if values["QUOTED"] != "hello" {
		t.Fatalf("quoted=%q", values["QUOTED"])
	}
	if len(names) == 0 {
		t.Fatal("expected names")
	}
	if err := settings.ApplyEnv(values); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CAPELLA_ORG_ID") != "org-1" {
		t.Fatalf("ApplyEnv org=%q", os.Getenv("CAPELLA_ORG_ID"))
	}

	// Search stops at go.mod: .env outside root is ignored when start is under root without .env deeper.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, ".env"), []byte("X=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(outside, "proj")
	_ = os.MkdirAll(proj, 0o755)
	_ = os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module p\n"), 0o644)
	path, values, _, err = settings.LoadDotEnv(proj, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || len(values) != 0 {
		t.Fatalf("should stop at go.mod without loading parent .env, got %q %#v", path, values)
	}
}

func TestLoadDotEnvMissingSilent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	path, values, names, err := settings.LoadDotEnv(dir, nil)
	if err != nil || path != "" || values != nil || names != nil {
		t.Fatalf("path=%q values=%v names=%v err=%v", path, values, names, err)
	}
}

func TestLoadDotEnvMalformed(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("BAD LINE WITHOUT EQUALS AND \x00\n"), 0o600)
	_, _, _, err := settings.LoadDotEnv(dir, nil)
	_ = err
}
