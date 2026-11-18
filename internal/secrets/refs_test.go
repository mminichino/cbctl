package secrets

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestParseRef(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		b64    bool
		want   Ref
		wantOK bool
	}{
		{"env", "${CB_PASSWORD}", false, Ref{RefEnv, "CB_PASSWORD"}, true},
		{"env padded", "  ${X}\n", false, Ref{RefEnv, "X"}, true},
		{"file", "${file:./pw}", false, Ref{RefFile, "./pw"}, true},
		{"base64 allowed", "${base64:aGk=}", true, Ref{RefBase64, "aGk="}, true},
		{"base64 is a variable name without the switch", "${base64:aGk=}", false, Ref{RefEnv, "base64:aGk="}, true},
		{"base64 empty", "${base64:}", true, Ref{RefBase64, ""}, true},
		{"literal", "hunter2", false, Ref{}, false},
		{"embedded", "x${A}", false, Ref{}, false},
		{"two refs", "${A}${B}", false, Ref{}, false},
		{"empty", "", false, Ref{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRef(tt.in, tt.b64)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ParseRef(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRefResolve(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pw"), []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString([]byte("p@ss: w0rd #1"))
	opt := RefOptions{BaseDir: dir, Env: envOf(map[string]string{"SET": "fromenv"}), AllowBase64: true}
	tests := []struct {
		name    string
		ref     Ref
		want    string
		problem string
	}{
		{"env", Ref{RefEnv, "SET"}, "fromenv", ""},
		{"env unset", Ref{RefEnv, "NOPE"}, "", "environment variable NOPE is not set or empty"},
		{"file relative", Ref{RefFile, "pw"}, "s3cret", ""},
		{"file absolute", Ref{RefFile, filepath.Join(dir, "pw")}, "s3cret", ""},
		{"file missing", Ref{RefFile, "nope"}, "", "not found or unreadable"},
		{"file empty", Ref{RefFile, "empty"}, "", "is empty"},
		{"base64 ok", Ref{RefBase64, enc}, "p@ss: w0rd #1", ""},
		{"base64 bad padding", Ref{RefBase64, strings.TrimRight(enc, "=")}, "", "not valid standard base64"},
		{"base64 bad alphabet", Ref{RefBase64, "!!!!"}, "", "not valid standard base64"},
		{"base64 empty", Ref{RefBase64, ""}, "", "decodes to an empty value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, p := tt.ref.Resolve(opt)
			if tt.problem == "" {
				if p != nil || got != tt.want {
					t.Fatalf("got %q, %v; want %q", got, p, tt.want)
				}
				return
			}
			if p == nil || !strings.Contains(p.Message, tt.problem) || got != "" {
				t.Fatalf("got %q, %v; want problem containing %q", got, p, tt.problem)
			}
			if tt.ref.Kind == RefBase64 && strings.Contains(p.Message+p.Fix, tt.ref.Arg) && tt.ref.Arg != "" {
				t.Errorf("problem echoes the encoded value: %+v", p)
			}
		})
	}
}

func TestExpandRefs(t *testing.T) {
	opt := RefOptions{Env: envOf(map[string]string{"A": "1", "B": "2"})}
	tests := []struct {
		name     string
		in       string
		want     string
		problems int
	}{
		{"none", "plain", "plain", 0},
		{"two", "${A}-${B}", "1-2", 0},
		{"one unset", "${A}-${C}", "1-", 1},
		{"two unset", "${C}${D}", "", 2},
		{"base64 is a variable without the switch", "${base64:xx}", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := ExpandRefs(tt.in, opt)
			if got != tt.want || len(problems) != tt.problems {
				t.Fatalf("got %q, %d problems; want %q, %d", got, len(problems), tt.want, tt.problems)
			}
		})
	}
}

func TestIsWholeRef(t *testing.T) {
	for in, want := range map[string]bool{"${A}": true, " ${file:x} ": true, "${base64:aA==}": true, "a${A}": false, "": false} {
		if got := IsWholeRef(in); got != want {
			t.Errorf("IsWholeRef(%q) = %v", in, got)
		}
	}
}
