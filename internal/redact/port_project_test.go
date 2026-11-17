package redact_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
)

// P-TST-14: secret-key masking covers non-string values; MaskTail and HasPrintableSecret.

func TestObject_MasksNonStringSecretValues(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	in := map[string]any{
		"apiKey":    map[string]any{"value": "x"},
		"secretKey": []any{"a"},
		"token":     42.0,
		"password":  "",
		"secret":    nil,
		"name":      "app",
		"count":     3.0,
		"flag":      true,
	}
	out := redact.Object(in).(map[string]any)
	for _, k := range []string{"apiKey", "secretKey", "token"} {
		if out[k] != "***" {
			t.Errorf("%s = %v, want ***", k, out[k])
		}
	}
	if out["password"] != "" || out["secret"] != nil {
		t.Errorf("empty secret values changed: password=%v secret=%v", out["password"], out["secret"])
	}
	if out["name"] != "app" || out["count"] != 3.0 || out["flag"] != true {
		t.Errorf("non-secret values changed: %v", out)
	}
	if got := redact.Object(nil); got != nil {
		t.Errorf("Object(nil) = %v", got)
	}
	// The input is not modified.
	if in["token"] != 42.0 {
		t.Error("Object mutated its input")
	}
}

func TestMaskTail_ShortAndNoTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	// Test binaries do not write to a terminal, so the ASCII mask is used.
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdout is a terminal")
	}
	if got := redact.MaskTail("abc"); got != "****abc" {
		t.Errorf("MaskTail(abc) = %q", got)
	}
	if got := redact.MaskTail("sk-1234abcd"); got != "****abcd" || strings.Contains(got, "1234") {
		t.Errorf("MaskTail = %q", got)
	}
}

func TestMaskTail_ClosedStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	_ = w.Close()
	old := os.Stdout
	os.Stdout = w // a closed file: Stat fails, which counts as "not a terminal"
	t.Cleanup(func() { os.Stdout = old })
	t.Setenv("NO_COLOR", "")
	if got := redact.MaskTail("sk-1234abcd"); got != "****abcd" {
		t.Errorf("MaskTail = %q", got)
	}
}

func TestHasPrintableSecret(t *testing.T) {
	for in, want := range map[string]bool{"": false, " \t\n": false, "\x00\x01": false, " a ": true, "•": true} {
		if got := redact.HasPrintableSecret(in); got != want {
			t.Errorf("HasPrintableSecret(%q) = %v, want %v", in, got, want)
		}
	}
}
