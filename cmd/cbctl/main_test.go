package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// smokeVersion is stamped into the smoke binary the way GoReleaser stamps a release.
const smokeVersion = "0.0.0-smoke"

// smokeConfig is a valid capellactl-era capella.yaml (apiVersion capellactl/v1, ${VAR} org id).
const smokeConfig = `apiVersion: capellactl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: smoke
clusters:
  - name: smoke-01
    configurationType: singleNode
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.30.0.0/24 }
    availability: { type: single }
    support: { plan: basic }
    deletionProtection: true
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 1
        services: [data, index, query]
    buckets:
      - name: playground
        memoryAllocationInMb: 256
`

// smokeBad is invalid: an unknown field and a missing cloudProvider.
const smokeBad = "apiVersion: capellactl/v1\nproject: {name: p}\nclusters: [{name: c, numOfNodes: lots}]\n"

// smokeResult is one run of the built binary.
type smokeResult struct {
	code           int
	stdout, stderr string
}

func (r smokeResult) String() string {
	return "exit " + strconv.Itoa(r.code) + "\nstdout:\n" + r.stdout + "\nstderr:\n" + r.stderr
}

// TestBinarySmoke builds the real cbctl executable and runs it isolated from this machine: its own
// HOME and CBCTL_HOME, no CAPELLA_*/CBCTL_*/capellactl variables, and a working directory with no
// .env or capella.yaml. No profile exists, so the OS keychain is never read. Port of capellactl's
// CAP-045 binary smoke tests (AC2: the executable works on its own; AC5: its exit codes).
func TestBinarySmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the cbctl binary; skipped with -short")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is needed to build the binary: %v", err)
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "cbctl")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, goTool, "build", "-o", bin, "-ldflags", "-X main.version="+smokeVersion, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	work := filepath.Join(tmp, "work")
	home := filepath.Join(tmp, "home")
	cbctlHome := filepath.Join(tmp, "cbctl-home")
	for _, d := range []string{work, home, cbctlHome} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	valid := filepath.Join(work, "capella.yaml")
	bad := filepath.Join(work, "bad.yaml")
	if err := os.WriteFile(valid, []byte(smokeConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(smokeBad), 0o600); err != nil {
		t.Fatal(err)
	}
	env := isolatedEnv(home, cbctlHome)

	run := func(t *testing.T, extra []string, args ...string) smokeResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = work
		cmd.Env = append(append([]string(nil), env...), extra...)
		cmd.Stdin = strings.NewReader("")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			code = exit.ExitCode()
		case err != nil:
			t.Fatalf("run %v: %v", args, err)
		}
		return smokeResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}

	tests := []struct {
		name  string
		extra []string
		args  []string
		code  int
		want  string // in stdout+stderr
		check func(t *testing.T, r smokeResult)
	}{
		{name: "CAP045 AC2 version", args: []string{"--version"}, code: 0, check: func(t *testing.T, r smokeResult) {
			if !strings.HasPrefix(r.stdout, "cbctl version "+smokeVersion+" ") {
				t.Errorf("--version stdout %q, want the stamped version", r.stdout)
			}
			if !strings.Contains(r.stdout, "\nLicensed under Apache-2.0 · Copyright Couchbase, Inc.") {
				t.Errorf("--version stdout %q, want the licence line", r.stdout)
			}
		}},
		{name: "CAP045 AC2 help", args: []string{"--help"}, code: 0, want: "capella"},
		{name: "CAP045 AC2 guide topics bundled", args: []string{"capella", "guide", "topics"}, code: 0, want: "idempotency"},
		{name: "CAP045 AC2 schema bundled", args: []string{"capella", "guide", "config", "clusters.deletionProtection"},
			code: 0, want: "Block deletion"},
		{name: "CAP045 AC2 validate a valid capella.yaml", extra: []string{"CAPELLA_ORG_ID=org-smoke"},
			args: []string{"capella", "-c", valid, "validate"}, code: 0, want: "is valid"},
		{name: "CAP045 AC2 init presets bundled",
			args: []string{"capella", "init", "--non-interactive", "--dry-run", "--org-id", "x", "--project", "p", "--cluster", "c"},
			code: 0, want: "apiVersion: cbctl/v1"},
		{name: "CAP045 AC5 invalid capella.yaml exits 3", args: []string{"capella", "-c", bad, "validate"}, code: 3,
			want: "unknown field 'numOfNodes'"},
		{name: "CAP045 AC5 unknown command exits 3", args: []string{"no-such-command"}, code: 3, want: `unknown command "no-such-command"`},
		{name: "CAP045 AC5 no credentials exits 3", args: []string{"capella", "project", "list"}, code: 3, want: "API key not set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, tt.extra, tt.args...)
			if r.code != tt.code {
				t.Fatalf("%v: want exit %d\n%s", tt.args, tt.code, r)
			}
			if tt.want != "" && !strings.Contains(r.stdout+r.stderr, tt.want) {
				t.Errorf("%v: output lacks %q\n%s", tt.args, tt.want, r)
			}
			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}

	entries, err := os.ReadDir(cbctlHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("read-only commands wrote to CBCTL_HOME: %v", entries)
	}
}

// isolatedEnv is the process environment without anything that points cbctl at this machine's
// settings, credentials or capellactl leftovers, with home directories moved to temp dirs.
func isolatedEnv(home, cbctlHome string) []string {
	drop := []string{"CAPELLA_", "CBCTL_", "CAPELLACTL_", "BEARER_TOKEN=", "ORG_ID=", "API_BASE_URL=",
		"HOME=", "USERPROFILE=", "APPDATA=", "LOCALAPPDATA=", "XDG_CONFIG_HOME=", "CI=", "GITHUB_ACTIONS="}
	var env []string
	for _, kv := range os.Environ() {
		keep := true
		for _, p := range drop {
			if strings.HasPrefix(strings.ToUpper(kv), p) {
				keep = false
				break
			}
		}
		if keep {
			env = append(env, kv)
		}
	}
	return append(env,
		"HOME="+home, "USERPROFILE="+home, "APPDATA="+filepath.Join(home, "AppData"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"CBCTL_HOME="+cbctlHome, "NO_COLOR=1")
}
