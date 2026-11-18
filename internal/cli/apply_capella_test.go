package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/manifest/capellaapply"
	"github.com/mminichino/cbctl/internal/server"
)

const capellaManifest = `apiVersion: cbctl/v1
kind: CouchbaseCapella
metadata:
  name: demo-cap
spec:
  project: demo
  provider: aws
  region: us-east-1
  size: small
  buckets:
    - name: orders
      scopes:
        - name: billing
          collections:
            - name: invoices
      import:
        - scope: billing
          collection: invoices
          credential: app
          keyField: id
          data:
            - { id: a }
            - { id: b }
  credentials:
    - name: app
      password: ${CAP_APP_PW}
      access:
        privileges: read+write
        bucket: orders
    - name: gen
      access:
        privileges: read
        allBuckets: true
`

// sdkStub is the import connection of the CLI tests.
type sdkStub struct {
	mu    sync.Mutex
	empty bool
	n     int
}

func (s *sdkStub) CollectionIsEmpty(string, string, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.empty, nil
}

func (s *sdkStub) ImportDocuments(_ context.Context, src server.DocSource, _, _, _, _ string) (int, error) {
	n := 0
	for {
		if _, _, err := src.Next(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return n, err
		}
		n++
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n += n
	return n, nil
}

func (s *sdkStub) connector() capellaapply.Connector {
	return func(context.Context, string, string, string) (capellaapply.DataModel, func(), error) {
		return s, func() {}, nil
	}
}

// capellaRun runs cbctl with the extra environment and no terminal.
func capellaRun(t *testing.T, dir, stdin string, env map[string]string, args ...string) applyRun {
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
	d.Clock = engine.NewFakeClock(time.Time{})
	d.ResetRedaction = true
	code := cli.Run(context.Background(), args, d)
	return applyRun{out.String(), errBuf.String(), code}
}

func capellaFixture(t *testing.T) (*capellafake.Fake, *sdkStub, map[string]string, string) {
	t.Helper()
	f := capellafake.New(t)
	sdk := &sdkStub{empty: true}
	env := cli.UseCapellaApplyFake(t, f, sdk.connector())
	env["CAP_APP_PW"] = "Cli-App-Passw0rd!1"
	dir := t.TempDir()
	writeFile(t, dir, "cap.yaml", capellaManifest)
	return f, sdk, env, dir
}

func TestApplyCapellaTextRun(t *testing.T) {
	f, sdk, env, dir := capellaFixture(t)
	r := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	if n := strings.Count(r.stdout, "credential gen password: "); n != 1 {
		t.Fatalf("generated password lines = %d, want 1:\n%s", n, r.stdout)
	}
	if !strings.Contains(r.stdout, "[ok]") || !strings.Contains(r.stdout, "CouchbaseCapella/demo-cap applied") {
		t.Fatalf("stdout lacks the ok lines:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout+r.stderr, env["CAP_APP_PW"]) {
		t.Fatal("output leaks the named password")
	}
	if sdk.n != 2 {
		t.Fatalf("imported %d documents, want 2", sdk.n)
	}

	// Applying again changes nothing and prints no new password.
	f.ResetLog()
	sdk.empty = false
	r = capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml")
	if r.code != 0 {
		t.Fatalf("second run: exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	if len(f.Writes()) != 0 {
		t.Fatalf("second run wrote: %v", f.Writes())
	}
	if strings.Contains(r.stdout, "password: ") {
		t.Fatalf("second run printed a password:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "credential app exists; password not changed") {
		t.Fatalf("missing D3 line:\n%s", r.stdout)
	}
	if sdk.n != 2 {
		t.Fatalf("second run imported again (%d)", sdk.n)
	}
}

func TestApplyCapellaJSON(t *testing.T) {
	f, _, env, dir := capellaFixture(t)
	tests := []struct {
		name       string
		args       []string
		code       int
		wantStatus string
		writes     bool
	}{
		{"dry run", []string{"apply", "-f", "cap.yaml", "-o", "json", "--dry-run"}, 0, `"status":"planned"`, false},
		{"applied", []string{"apply", "-f", "cap.yaml", "-o", "json"}, 0, `"status":"applied"`, true},
		{"unchanged", []string{"apply", "-f", "cap.yaml", "-o", "json"}, 0, `"status":"unchanged"`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f.ResetLog()
			r := capellaRun(t, dir, "", env, tc.args...)
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, tc.code, r.stdout, r.stderr)
			}
			clitest.AssertJSONSchema(t, "apply.manifest", []byte(r.stdout))
			if !strings.Contains(strings.ReplaceAll(r.stdout, " ", ""), tc.wantStatus) {
				t.Errorf("stdout lacks %s:\n%s", tc.wantStatus, r.stdout)
			}
			if got := len(f.Writes()) > 0; got != tc.writes {
				t.Errorf("writes = %v, want %v", got, tc.writes)
			}
			if strings.Contains(r.stdout+r.stderr, env["CAP_APP_PW"]) {
				t.Error("output leaks the named password")
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
				t.Fatalf("stdout is not one JSON document: %v", err)
			}
		})
	}
}

func TestApplyCapellaJSONCarriesGeneratedPasswordOnce(t *testing.T) {
	_, _, env, dir := capellaFixture(t)
	r := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml", "-o", "json")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	var doc struct {
		Documents []struct {
			ConnectionString string `json:"connectionString"`
			Passwords        []struct {
				Credential string `json:"credential"`
				Password   string `json:"password"`
			} `json:"credentialPasswords"`
		} `json:"documents"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Documents) != 1 || len(doc.Documents[0].Passwords) != 1 || doc.Documents[0].Passwords[0].Credential != "gen" {
		t.Fatalf("documents = %+v", doc.Documents)
	}
	if doc.Documents[0].ConnectionString == "" {
		t.Fatal("no connection string")
	}
	if strings.Contains(r.stderr, doc.Documents[0].Passwords[0].Password) {
		t.Fatal("the generated password is also on stderr")
	}
}

func TestApplyCapellaExitCodes(t *testing.T) {
	groups := func(n string) string {
		return strings.Replace(capellaManifest, "  size: small\n", "  serviceGroups:\n    - services: [data, index, query]\n      nodes: "+n+"\n      compute: { cpu: 4, ramGb: 16 }\n", 1)
	}
	tests := []struct {
		name     string
		first    string
		second   string
		args     []string
		code     int
		stderrIn string
	}{
		{"region change", capellaManifest, strings.Replace(capellaManifest, "us-east-1", "us-west-2", 1), nil, 3, "region"},
		{"scale in without -y", groups("4"), groups("3"), nil, 4, "--yes"},
		{"scale in with -y", groups("4"), groups("3"), []string{"-y"}, 0, ""},
		{"scale out without -y", groups("3"), groups("4"), nil, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, _, env, dir := capellaFixture(t)
			writeFile(t, dir, "cap.yaml", tc.first)
			if r := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml"); r.code != 0 {
				t.Fatalf("setup: exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			writeFile(t, dir, "cap.yaml", tc.second)
			f.ResetLog()
			r := capellaRun(t, dir, "", env, append([]string{"apply", "-f", "cap.yaml"}, tc.args...)...)
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, tc.code, r.stdout, r.stderr)
			}
			if !strings.Contains(r.stderr, tc.stderrIn) {
				t.Fatalf("stderr lacks %q:\n%s", tc.stderrIn, r.stderr)
			}
			if tc.code != 0 && len(f.Writes()) != 0 {
				t.Fatalf("a refused apply wrote: %v", f.Writes())
			}
		})
	}
}

func TestApplyCapellaNeedsAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cap.yaml", capellaManifest)
	r := capellaRun(t, dir, "", map[string]string{"CAP_APP_PW": "x-Passw0rd!1234"}, "apply", "-f", "cap.yaml")
	if r.code != 3 || !strings.Contains(r.stderr, "API key") {
		t.Fatalf("exit %d\nstderr: %s", r.code, r.stderr)
	}
}

func TestApplyCapellaLeavesCapellaYAMLAlone(t *testing.T) {
	_, _, env, dir := capellaFixture(t)
	path := filepath.Join(dir, "capella.yaml")
	body := "apiVersion: cbctl/v1\nprojects:\n  - name: other\n"
	writeFile(t, dir, "capella.yaml", body)
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if r := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml"); r.code != 0 {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != body {
		t.Fatalf("capella.yaml changed: %q %v", got, err)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(old) {
		t.Fatalf("capella.yaml mtime changed: %v", st.ModTime())
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.bak"))
	if len(matches) != 0 {
		t.Fatalf("backup files created: %v", matches)
	}
}

var (
	passwordLine = regexp.MustCompile(`(?m)^(credential \S+ password: ).*$`)
	hostLine     = regexp.MustCompile(`Connection string: \S+`)
	pathLine     = regexp.MustCompile(`password saved to \S+`)
	durations    = regexp.MustCompile(`\(\d+s\)|in \d+s`)
)

// normalizeCapella hides what changes from run to run: generated passwords, hosts, paths, timings.
func normalizeCapella(s string) string {
	s = passwordLine.ReplaceAllString(s, "${1}<password>")
	s = hostLine.ReplaceAllString(s, "Connection string: <host>")
	s = pathLine.ReplaceAllString(s, "password saved to <path>")
	return durations.ReplaceAllString(s, "<t>")
}

// TestApplyCapellaGoldenText compares the text output of a first and a second run with the
// goldens in testdata (rewrite them with -update).
func TestApplyCapellaGoldenText(t *testing.T) {
	f, sdk, env, dir := capellaFixture(t)
	first := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml")
	f.ResetLog()
	sdk.empty = false
	second := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml")
	dry := capellaRun(t, dir, "", env, "apply", "-f", "cap.yaml", "--dry-run")
	for name, r := range map[string]applyRun{"first": first, "second": second, "dry-run": dry} {
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s\n%s", name, r.code, r.stdout, r.stderr)
		}
		got := "# stdout\n" + normalizeCapella(r.stdout) + "# stderr\n" + normalizeCapella(r.stderr)
		golden := filepath.Join("testdata", "output", "apply-capella-"+name+".golden")
		if f := flag.Lookup("update-output"); f != nil && f.Value.String() == "true" {
			if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("missing golden (run go test ./internal/cli -run GoldenText -update-output): %v", err)
		}
		if string(want) != got {
			t.Errorf("%s differs from %s\n--- want\n%s\n--- got\n%s", name, golden, want, got)
		}
	}
}
