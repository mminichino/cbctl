package secrets

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSpecialHasNoForbidden(t *testing.T) {
	t.Parallel()
	if strings.ContainsAny(Special, Forbidden) {
		t.Fatalf("Special %q contains a forbidden character", Special)
	}
}

func TestMeetsRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pw   string
		want bool
	}{
		{"valid", "Abcdef1!", true},
		{"valid long", "Passw0rd-with-Everything[ok]", true},
		{"too short", "Abc1!xy", false},
		{"no upper", "abcdef1!", false},
		{"no lower", "ABCDEF1!", false},
		{"no digit", "Abcdefg!", false},
		{"no special", "Abcdefg1", false},
		{"empty", "", false},
		{"forbidden <", "Abcdef1!<", false},
		{"forbidden >", "Abcdef1!>", false},
		{"forbidden ;", "Abcdef1!;", false},
		{"forbidden dot", "Abcdef1!.", false},
		{"forbidden *", "Abcdef1!*", false},
		{"forbidden &", "Abcdef1!&", false},
		{"forbidden |", "Abcdef1!|", false},
		{"forbidden pound", "Abcdef1!£", false},
		{"forbidden equals", "Abcdef1!=", false},
		{"length counts runes", "Aa1!é", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MeetsRules(tc.pw); got != tc.want {
				t.Errorf("MeetsRules(%q) = %v, want %v", tc.pw, got, tc.want)
			}
		})
	}
}

func TestGeneratePasswordPolicy(t *testing.T) {
	t.Parallel()
	for i := 0; i < 500; i++ {
		pw := MustGenerate(32)
		if !MeetsRules(pw) {
			t.Fatalf("generated password %q does not meet the rules", pw)
		}
		if strings.ContainsAny(pw, Forbidden) {
			t.Fatalf("generated password %q contains a forbidden character", pw)
		}
		if strings.Contains(pw, "=") {
			t.Fatalf("generated password %q contains Capella-rejected '='", pw)
		}
	}
}

func TestGeneratePasswordLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"default", 32, 32},
		{"min", MinLength, MinLength},
		{"long", 128, 128},
		{"below min is raised", 4, MinLength},
		{"zero is raised", 0, MinLength},
		{"negative is raised", -3, MinLength},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for i := 0; i < 50; i++ {
				pw := MustGenerate(tc.in)
				if got := utf8.RuneCountInString(pw); got != tc.want {
					t.Fatalf("len = %d, want %d (%q)", got, tc.want, pw)
				}
				if !MeetsRules(pw) {
					t.Fatalf("password %q does not meet the rules", pw)
				}
			}
		})
	}
}

func TestGeneratePasswordUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		pw := MustGenerate(32)
		if seen[pw] {
			t.Fatalf("duplicate password generated: %q", pw)
		}
		seen[pw] = true
	}
}

func TestGeneratePasswordUsesAllClasses(t *testing.T) {
	t.Parallel()
	// Even at the minimum length, every class is represented.
	for i := 0; i < 200; i++ {
		pw := MustGenerate(MinLength)
		for name, class := range map[string]string{"upper": upper, "lower": lower, "digit": digit, "special": Special} {
			if !strings.ContainsAny(pw, class) {
				t.Fatalf("password %q has no %s character", pw, name)
			}
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestGenerateRandFailure(t *testing.T) {
	prev := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = prev })
	pw, err := Generate(32)
	if err == nil || pw != "" {
		t.Fatalf("Generate = %q, %v; want an error and no password", pw, err)
	}
	if !strings.Contains(err.Error(), "generating password") {
		t.Fatalf("err = %v, want a 'generating password' context", err)
	}
}
