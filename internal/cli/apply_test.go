package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
)

const applyServerYAML = `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: demo
spec:
  credentials:
    password: ${APPLY_TEST_PASSWORD}
  nodes:
    - ipAddress: 127.0.0.1
`

const applyCapellaYAML = `apiVersion: cbctl/v1
kind: CouchbaseCapella
metadata:
  name: demo-cap
spec:
  project: demo
  provider: aws
  region: us-east-1
  size: dev
`

// applyRun is the result of one `cbctl apply` invocation.
type applyRun struct {
	stdout, stderr string
	code           int
}

// runApplyCmd runs cbctl with args in dir, with stdin and the extra environment variables set.
func runApplyCmd(t *testing.T, dir, stdin string, env map[string]string, args ...string) applyRun {
	t.Helper()
	var out, errBuf bytes.Buffer
	d := cli.DefaultDeps()
	d.Env = func(k string) string { return env[k] }
	d.LookupEnv = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	d.Stdout, d.Stderr = &out, &errBuf
	d.Stdin = strings.NewReader(stdin)
	d.IsTTY = func(io.Reader) bool { return false }
	d.Cwd = func() (string, error) { return dir, nil }
	d.Home = t.TempDir()
	d.ResetRedaction = true
	code := cli.Run(context.Background(), args, d)
	return applyRun{out.String(), errBuf.String(), code}
}

func writeFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplySkeletonExitCodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "server.yaml", applyServerYAML)
	writeFile(t, dir, "capella.yml", applyCapellaYAML)
	writeFile(t, dir, "bad.yaml", "apiVersion: cbctl/v1\nkind: Nope\nmetadata:\n  name: x\n")
	writeFile(t, dir, "capella-style.yaml", "apiVersion: cbctl/v1\norganizationId: ${CAPELLA_ORG_ID}\nprojects: []\n")
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"APPLY_TEST_PASSWORD": "s3cret-pw"}

	tests := []struct {
		name     string
		stdin    string
		env      map[string]string
		args     []string
		code     int
		stderrIn string
	}{
		{"no file", "", env, []string{"apply"}, 3, "-f is required"},
		{"missing file", "", env, []string{"apply", "-f", "nope.yaml"}, 3, "cannot read"},
		{"invalid kind", "", env, []string{"apply", "-f", "bad.yaml"}, 3, "problem"},
		{"capella.yaml style", "", env, []string{"apply", "-f", "capella-style.yaml"}, 3, "cbctl capella apply -c"},
		{"unset variable", "", nil, []string{"apply", "-f", "server.yaml"}, 3, "APPLY_TEST_PASSWORD"},
		{"empty dir", "", env, []string{"apply", "-f", "empty"}, 3, "holds no"},
		{"bad output", "", env, []string{"apply", "-f", "server.yaml", "-o", "yaml"}, 3, "invalid output format"},
		{"stdin twice", "", env, []string{"apply", "-f", "-", "-f", "-"}, 3, "twice"},
		{"capella stub", applyCapellaYAML, env, []string{"apply", "-f", "-"}, 3, "not implemented"},
		{"duplicate across files", "", env, []string{"apply", "-f", "server.yaml", "-f", "server.yaml"}, 3, "already declares"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := runApplyCmd(t, dir, tt.stdin, tt.env, tt.args...)
			if r.code != tt.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, tt.code, r.stdout, r.stderr)
			}
			if !strings.Contains(r.stderr, tt.stderrIn) {
				t.Fatalf("stderr lacks %q:\n%s", tt.stderrIn, r.stderr)
			}
		})
	}
}

func TestApplyDirectoryReadsYAMLFilesInNameOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "b.yaml", applyCapellaYAML)
	writeFile(t, dir, "a.yml", applyServerYAML)
	writeFile(t, dir, "notes.txt", "not a manifest")
	r := runApplyCmd(t, dir, "", map[string]string{"APPLY_TEST_PASSWORD": "pw-value"}, "apply", "-f", ".", "--dry-run")
	// a.yml (Server) sorts before b.yaml (Capella), so the Server stub fails first.
	if r.code != 3 || !strings.Contains(r.stderr, "CouchbaseServer") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
}
