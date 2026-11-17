package redact

import "testing"

func TestMaskTail_UnicodeOnTerminal(t *testing.T) {
	prev := unicodeMask
	t.Cleanup(func() { unicodeMask = prev })
	unicodeMask = func() bool { return true }
	if got := MaskTail("sk-1234abcd"); got != "••••abcd" {
		t.Fatalf("MaskTail on a terminal = %q, want ••••abcd", got)
	}
	unicodeMask = func() bool { return false }
	if got := MaskTail("sk-1234abcd"); got != "****abcd" {
		t.Fatalf("MaskTail off a terminal = %q, want ****abcd", got)
	}
}

func TestUnicodeMask_NoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if unicodeMask() {
		t.Fatal("NO_COLOR set: want ASCII mask")
	}
}
