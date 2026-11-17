package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCBDev(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanSecrets(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, secrets, "a.json", `{"password":"Generated-Pass-0001"}`)
	writeFile(t, secrets, "b.json", `{"password":"Generated-Pass-0002"}`)
	clean := writeFile(t, dir, "clean.log", "all good\n")
	leaky := writeFile(t, dir, "leaky.log", "ok\npassword is Generated-Pass-0002\n")
	t.Setenv("CBDEV_TEST_KEY", "an-api-key-value-123")

	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"clean", []string{clean, "--secrets-dir", secrets, "--env", "CBDEV_TEST_KEY"}, 0, "3 secret value(s) checked"},
		{"hit", []string{leaky, "--secrets-dir", secrets}, 1, "leaky.log:2"},
		{"missing file", []string{clean, filepath.Join(dir, "nope.log"), "--secrets-dir", secrets}, 2, "no such file"},
		{"allow missing", []string{clean, filepath.Join(dir, "nope.log"), "--secrets-dir", secrets, "--allow-missing"}, 0, "skipping missing"},
		{"allow missing needs one file", []string{filepath.Join(dir, "nope.log"), "--secrets-dir", secrets, "--allow-missing"}, 2, "no such file"},
		{"too few secrets", []string{clean, "--secrets-dir", secrets, "--require-secrets", "3"}, 2, "--require-secrets 3"},
		{"enough secrets", []string{clean, "--secrets-dir", secrets, "--require-secrets", "2"}, 0, "2 secret value(s)"},
		{"bad count", []string{clean, "--require-secrets", "x"}, 2, "needs a number"},
		{"no secrets", []string{clean, "--secrets-dir", filepath.Join(dir, "empty")}, 2, "no secret values"},
		{"unset env", []string{clean, "--env", "CBDEV_TEST_UNSET"}, 2, "is not set"},
		{"unknown flag", []string{clean, "--bogus"}, 2, "unknown flag"},
		{"no files", []string{"--secrets-dir", secrets}, 2, "missing FILE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runCBDev(t, append([]string{"ci", "scan-secrets"}, tt.args...)...)
			if code != tt.code || !strings.Contains(out+errOut, tt.want) {
				t.Fatalf("code=%d want %d; output:\n%s%s\nwant %q", code, tt.code, out, errOut, tt.want)
			}
		})
	}
}

func TestDedupe(t *testing.T) {
	dir := t.TempDir()
	runs := writeFile(t, dir, "runs.json", `{"workflow_runs":[
		{"id":2,"run_attempt":1,"head_sha":"abc","event":"workflow_dispatch","status":"in_progress","created_at":"2026-11-16T08:00:00Z"},
		{"id":1,"run_attempt":1,"head_sha":"abc","event":"schedule","status":"completed","conclusion":"success","created_at":"2026-11-16T07:00:00Z"}]}`)
	tests := []struct {
		name string
		args []string
		code int
		skip string
		want string
	}{
		{"duplicate dispatch", []string{"--run-id", "2", "--runs-json", runs}, 0, "skip=true", "already passed"},
		{"the scheduled run itself", []string{"--run-id", "1", "--runs-json", runs}, 0, "skip=false", "no duplicate"},
		{"unreadable list fails open", []string{"--run-id", "2", "--runs-json", filepath.Join(dir, "nope.json")}, 0, "skip=false", "::warning::"},
		{"not listed fails open", []string{"--run-id", "3", "--runs-json", runs}, 0, "skip=false", "::warning::"},
		{"no run id", []string{"--run-id", "", "--runs-json", runs}, 2, "", "--run-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out")
			t.Setenv("GITHUB_OUTPUT", output)
			code, out, errOut := runCBDev(t, append([]string{"ci", "dedupe"}, tt.args...)...)
			if code != tt.code || !strings.Contains(out+errOut, tt.want) {
				t.Fatalf("code=%d want %d; output:\n%s%s\nwant %q", code, tt.code, out, errOut, tt.want)
			}
			if tt.skip == "" {
				return
			}
			got, err := os.ReadFile(output)
			if err != nil || strings.TrimSpace(string(got)) != tt.skip {
				t.Fatalf("GITHUB_OUTPUT = %q, %v; want %s", got, err, tt.skip)
			}
		})
	}
}
