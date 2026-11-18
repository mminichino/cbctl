package redact_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
)

func TestRegisterAndString(t *testing.T) {
	// Not parallel: Register/Reset mutate the package-global registry.
	redact.Reset()
	t.Cleanup(redact.Reset)

	redact.Register("ab") // ignored
	redact.Register("secret-value")
	redact.Register("secret")

	got := redact.String("the secret-value and secret leaked")
	if strings.Contains(got, "secret-value") || strings.Contains(got, "secret ") {
		t.Fatalf("not redacted: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("expected mask: %q", got)
	}
}

func TestObjectMasksKeys(t *testing.T) {
	// Not parallel: Register/Reset mutate the package-global registry.
	redact.Reset()
	t.Cleanup(redact.Reset)

	in := map[string]any{
		"Password": "hunter2",
		"nested": map[string]any{
			"token": "abc12345",
			"ok":    "visible",
		},
		"list": []any{"plain", "abc12345"},
	}
	redact.Register("abc12345")
	out := redact.Object(in).(map[string]any)
	if out["Password"] != "***" {
		t.Fatalf("Password=%v", out["Password"])
	}
	nested := out["nested"].(map[string]any)
	if nested["token"] != "***" {
		t.Fatalf("token=%v", nested["token"])
	}
	if nested["ok"] != "visible" {
		t.Fatalf("ok=%v", nested["ok"])
	}
}
