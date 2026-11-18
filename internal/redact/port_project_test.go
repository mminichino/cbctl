package redact_test

import (
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

func TestMaskTail_Masks(t *testing.T) {
	tests := []struct {
		name, in, mask, want string
	}{
		{"short ascii", "abc", "****", "****abc"},
		{"long ascii", "sk-1234abcd", "****", "****abcd"},
		{"long unicode", "sk-1234abcd", "••••", "••••abcd"},
		{"empty", "", "****", "(not set)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redact.MaskTail(tc.in, tc.mask)
			if got != tc.want || strings.Contains(got, "1234") {
				t.Errorf("MaskTail(%q, %q) = %q, want %q", tc.in, tc.mask, got, tc.want)
			}
		})
	}
}

func TestHasPrintableSecret(t *testing.T) {
	for in, want := range map[string]bool{"": false, " \t\n": false, "\x00\x01": false, " a ": true, "•": true} {
		if got := redact.HasPrintableSecret(in); got != want {
			t.Errorf("HasPrintableSecret(%q) = %v, want %v", in, got, want)
		}
	}
}
