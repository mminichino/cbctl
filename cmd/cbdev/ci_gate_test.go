package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The checked-in fixtures drive the nightly health report's verdict and exit code.
func TestReleaseGateFixtures(t *testing.T) {
	sha := strings.Repeat("a", 40)
	cases := []struct {
		file string
		code int
		want string
	}{
		{"three-green.json", 0, "PASSED"},
		{"not-green.json", 1, "FAILED"},
		{"too-few.json", 1, "FAILED"},
		{"deduped.json", 1, "ignored 2 scheduled run(s)"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			var out, errb bytes.Buffer
			f := filepath.Join("..", "..", "internal", "ci", "releasegate", "testdata", tc.file)
			code := cmdCIReleaseGate([]string{"--commit", sha, "--runs-json", f}, &out, &errb)
			if code != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("exit %d (want %d)\n%s%s", code, tc.code, out.String(), errb.String())
			}
		})
	}
}

func TestReleaseGateUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := cmdCIReleaseGate([]string{"--commit", "x", "--nights", "0"}, &out, &errb); code != 2 {
		t.Fatalf("--nights 0: exit %d, want 2", code)
	}
	if code := cmdCIReleaseGate([]string{"--commit", "x", "--runs-json", filepath.Join(t.TempDir(), "absent.json")}, &out, &errb); code != 1 {
		t.Fatalf("unreadable fixture: exit %d, want 1", code)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdCIReleaseGate([]string{"--commit", "x", "--runs-json", bad}, &out, &errb); code != 1 {
		t.Fatalf("unparsable runs: exit %d, want 1", code)
	}
	out.Reset()
	// The nightly is a health signal: with --report-only missing evidence never blocks.
	if code := cmdCIReleaseGate([]string{"--commit", "x", "--runs-json", bad, "--report-only"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "UNKNOWN") {
		t.Fatalf("unparsable runs --report-only: exit %d, want 0 with UNKNOWN\n%s", code, out.String())
	}
}

func TestDefaultRepo(t *testing.T) {
	prev := gitRemote
	t.Cleanup(func() { gitRemote = prev })

	t.Setenv("GITHUB_REPOSITORY", "env/repo")
	if got := defaultRepo(context.Background()); got != "env/repo" {
		t.Fatalf("env: %q", got)
	}
	t.Setenv("GITHUB_REPOSITORY", "")
	gitRemote = func(context.Context) (string, error) { return "git@github.com:someone/fork.git\n", nil }
	if got := defaultRepo(context.Background()); got != "someone/fork" {
		t.Fatalf("remote: %q", got)
	}
	gitRemote = func(context.Context) (string, error) { return "", errors.New("not a git repository") }
	if got := defaultRepo(context.Background()); got != fallbackRepo {
		t.Fatalf("no remote: %q, want %q", got, fallbackRepo)
	}
	gitRemote = func(context.Context) (string, error) { return "https://gitlab.com/x/y.git", nil }
	if got := defaultRepo(context.Background()); got != fallbackRepo {
		t.Fatalf("non-GitHub remote: %q, want %q", got, fallbackRepo)
	}
}

func TestCoverGateTiers(t *testing.T) {
	dir := t.TempDir()
	tiers := filepath.Join(dir, "tiers.json")
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("tiers.json", `{"root":"m/internal","tiers":[{"name":"core","min":80,"packages":["plan"]}]}`)
	pass := write("pass.out", "mode: set\nm/internal/plan/a.go:1.1,2.1 9 1\nm/internal/plan/a.go:3.1,4.1 1 0\n")
	fail := write("fail.out", "mode: set\nm/internal/plan/a.go:1.1,2.1 9 0\nm/internal/plan/a.go:3.1,4.1 1 1\n")
	stale := write("stale.out", "mode: set\nm/internal/plan/a.go:1.1,2.1 4 1\n")
	other := write("other.out", "mode: set\nm/internal/cli/a.go:1.1,2.1 1 1\n")

	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"pass", []string{"--tiers", tiers, "--profile", pass}, 0, "90.0%"},
		{"below", []string{"--tiers", tiers, "--profile", fail}, 1, "BELOW"},
		{"merged profiles", []string{"--tiers", tiers, "--profile", fail, "--profile", pass}, 0, "100.0%"},
		{"stale profile", []string{"--tiers", tiers, "--profile", pass, "--profile", stale}, 2, ""},
		{"missing package", []string{"--tiers", tiers, "--profile", other}, 1, "MISSING"},
		{"no profile", []string{"--tiers", tiers}, 2, ""},
		{"tiers with min", []string{"--tiers", tiers, "--min", "80", "--profile", pass}, 2, ""},
		{"profile without tiers", []string{"--profile", pass}, 2, ""},
		{"bad tiers", []string{"--tiers", filepath.Join(dir, "absent.json"), "--profile", pass}, 2, ""},
		{"bad profile", []string{"--tiers", tiers, "--profile", filepath.Join(dir, "absent.out")}, 2, ""},
		{"no min", []string{"./internal/plan"}, 2, ""},
		{"no pkg", []string{"--min", "80"}, 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := cmdCICoverGate(tc.args, &out, &errb)
			if code != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("exit %d (want %d)\nstdout:\n%s\nstderr:\n%s", code, tc.code, out.String(), errb.String())
			}
		})
	}
}
