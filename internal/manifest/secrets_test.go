package manifest

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
)

func loadWithEnv(t *testing.T, password string, env map[string]string) (*Set, error) {
	t.Helper()
	text := srv("credentials:\n  password: " + password + "\nnodes:\n  - ipAddress: 10.0.0.1\n")
	return Load(context.Background(), Source{
		Path:  "-",
		Stdin: strings.NewReader(text),
		Cwd:   "testdata/valid",
		Env:   func(k string) string { return env[k] },
	})
}

func TestSecretResolve(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte("p@ss:w0rd!"))
	tests := []struct {
		name     string
		password string
		env      map[string]string
		want     string
		problem  string
	}{
		{"env", "${CB_PW}", map[string]string{"CB_PW": "from-env-77"}, "from-env-77", ""},
		{"env unset", "${CB_PW}", nil, "", "CB_PW"},
		{"env empty", "${CB_PW}", map[string]string{"CB_PW": ""}, "", "CB_PW"},
		{"file", "${file:data/pw.txt}", nil, "file-secret-9f3", ""},
		{"file missing", "${file:data/nope.txt}", nil, "", "nope.txt"},
		{"base64", "${base64:" + enc + "}", nil, "p@ss:w0rd!", ""},
		{"base64 invalid", "${base64:!!!}", nil, "", "base64"},
		{"base64 unpadded", "${base64:" + strings.TrimRight(enc, "=") + "}", nil, "", "base64"},
		{"base64 empty", "${base64:}", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redact.Reset()
			t.Cleanup(redact.Reset)
			set, err := loadWithEnv(t, tt.password, tt.env)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			err = set.Resolve()
			if tt.want == "" {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("Resolve error = %v, want *ValidationError", err)
				}
				if !strings.Contains(err.Error(), tt.problem) {
					t.Errorf("error %q lacks %q", err, tt.problem)
				}
				if set.Docs[0].Server.Credentials.Password.Resolved() {
					t.Error("password marked resolved after a failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			pw := set.Docs[0].Server.Credentials.Password
			if got := pw.Value(); got != tt.want {
				t.Errorf("Value = %q, want %q", got, tt.want)
			}
			if got := redact.String("x " + tt.want + " y"); strings.Contains(got, tt.want) {
				t.Errorf("resolved value not redacted: %q", got)
			}
			if strings.HasPrefix(tt.password, "${base64:") {
				if got := redact.String("x " + enc + " y"); strings.Contains(got, enc) {
					t.Errorf("encoded base64 value not redacted: %q", got)
				}
			}
			if pw.String() != tt.password {
				t.Errorf("String = %q, want the reference %q", pw.String(), tt.password)
			}
		})
	}
}

func TestSecretNeverInErrors(t *testing.T) {
	t.Cleanup(redact.Reset)
	// A literal password is rejected, and the message never echoes it.
	_, err := loadWithEnv(t, "hunter2-literal", nil)
	if err == nil {
		t.Fatal("literal password accepted")
	}
	if strings.Contains(err.Error(), "hunter2-literal") {
		t.Errorf("error echoes the literal: %v", err)
	}
}

func TestSecretValueBeforeResolvePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Value before Resolve did not panic")
		}
	}()
	s := ParseSecretRef("${X}")
	_ = s.Value()
}

func TestSecretMarshalIsReference(t *testing.T) {
	s := ParseSecretRef("${X}")
	b, err := s.MarshalJSON()
	if err != nil || string(b) != `"${X}"` {
		t.Fatalf("MarshalJSON = %s, %v", b, err)
	}
}

func TestSecretResolveAllDocsBeforeApply(t *testing.T) {
	t.Cleanup(redact.Reset)
	text := srv("credentials:\n  password: ${GOOD}\nnodes:\n  - ipAddress: 10.0.0.1\n") +
		"---\n" + strings.Replace(srv("credentials:\n  password: ${BAD}\nnodes:\n  - ipAddress: 10.0.0.2\n"), "name: test", "name: second", 1)
	set, err := Load(context.Background(), Source{Path: "-", Stdin: strings.NewReader(text), Cwd: "testdata/valid",
		Env: func(k string) string {
			if k == "GOOD" {
				return "ok-value-1"
			}
			return ""
		}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = set.Resolve()
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Issues) != 1 || ve.Issues[0].Doc != 2 {
		t.Fatalf("Resolve error = %v, want one issue in document 2", err)
	}
}

func FuzzParseSecretRef(f *testing.F) {
	for _, s := range []string{"", "${X}", "${file:a}", "${base64:AAAA}", "${base64:}", "${", "plain", "${X}${Y}", "$${X}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r := ParseSecretRef(s)
		if r.Problem() == "" && !strings.Contains(s, "${") {
			t.Fatalf("%q accepted as a reference", s)
		}
		if r.Raw != s {
			t.Fatalf("Raw = %q, want %q", r.Raw, s)
		}
	})
}
