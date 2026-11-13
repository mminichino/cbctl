package secrets_test

import (
	"os"
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
	secrets.RestoreRotatedCredential(path, bak)
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "FirstPass1!") {
		t.Fatalf("restore failed: %s", raw)
	}
}
