package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
)

var updateOutputGoldens = flag.Bool("update-output", false, "rewrite the golden files in testdata/output")

// outputEnvs are the terminal situations every command output golden is recorded in.
var outputEnvs = []struct {
	name   string
	env    map[string]string
	flags  []string
	tty    bool
	colors bool // whether stdout and stderr labels carry escape codes
}{
	{name: "tty-color", tty: true, colors: true},
	{name: "tty-nocolor", tty: true, env: map[string]string{"NO_COLOR": "1"}},
	{name: "pipe"},
	{name: "pipe-always", flags: []string{"--color=always"}, colors: true},
}

// renderGolden is the text recorded for one invocation: both streams and the exit code, with
// the escape character spelled out so the file is reviewable.
func renderGolden(stdout, stderr string, code int) string {
	vis := strings.NewReplacer("\x1b", "<ESC>")
	return "stdout:\n" + vis.Replace(stdout) + "stderr:\n" + vis.Replace(stderr) + "exit: " + strconv.Itoa(code) + "\n"
}

// TestCommandOutputGolden records what each kind of command prints in text mode on a color
// terminal, a terminal with NO_COLOR, a pipe and a pipe forced to color (P6, AC "per-command
// output in color and no-color, TTY and non-TTY"). Regenerate with -update-output and review the diff.
func TestCommandOutputGolden(t *testing.T) {
	up := newFakeServerREST(t, true)
	down := newFakeServerREST(t, false)
	auth := []string{"-u", "Administrator", "-p", "password", "--no-ssl"}
	cmds := []struct {
		name string
		args []string
	}{
		{"cluster-rebalance-ok", append([]string{"cluster", "rebalance", "--rally-ip-address", up.hostPort()}, auth...)},
		{"configure-remove-nothing", []string{"configure", "remove", "--profile", "ci", "-y"}},
		{"capella-configure-remove-nothing-json", []string{"capella", "configure", "remove", "--profile", "ci", "-y", "-o", "json"}},
		{"cluster-create-already-configured", append([]string{"cluster", "create", "--host", up.hostPort(), "--ram", "4"}, auth...)},
		{"node-join-already-configured", append([]string{"cluster", "join", "--rally-ip-address", up.hostPort(), "--ip-address", up.hostPort(), "--ram", "4"}, auth...)},
		{"cluster-exists-true", append([]string{"cluster", "exists", "--host", up.hostPort()}, auth...)},
		{"cluster-exists-false", append([]string{"cluster", "exists", "--host", down.hostPort()}, auth...)},
		{"cluster-map-failure", append([]string{"cluster", "map", "--host", down.hostPort()}, auth...)},
		{"usage-error", []string{"bucket", "create"}},
		{"unknown-command", []string{"bogus-verb"}},
		{"configure-list", []string{"configure", "list"}},
	}
	for _, c := range cmds {
		for _, e := range outputEnvs {
			t.Run(c.name+"/"+e.name, func(t *testing.T) {
				oc := outcase{env: e.env, outTTY: e.tty, errTTY: e.tty, args: append(append([]string{}, e.flags...), c.args...)}
				stdout, stderr, code := oc.run(t)
				got := renderGolden(stdout, stderr, code)
				path := filepath.Join("testdata", "output", c.name+"."+e.name+".golden")
				if *updateOutputGoldens {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read golden (run with -update-output to create it): %v", err)
				}
				if got != string(want) {
					t.Errorf("output differs from %s\ngot:\n%s\nwant:\n%s", path, got, want)
				}
				// Whatever the golden holds, escape codes appear only where color was decided on.
				if !e.colors && strings.Contains(stdout+stderr, "\x1b[") {
					t.Errorf("escape code without color:\n%q", stdout+stderr)
				}
				// Data on stdout is never colored, in any situation.
				if c.name == "cluster-exists-true" || c.name == "cluster-exists-false" || c.name == "configure-list" {
					if strings.Contains(stdout, "\x1b[") {
						t.Errorf("escape code in stdout data: %q", stdout)
					}
				}
			})
		}
	}
}

// TestSuccessLinesAreOK pins the 0.7.0 rename: a completed change prints "[ok]", a no-op prints
// "[info]", on stdout in text mode.
func TestSuccessLinesAreOK(t *testing.T) {
	up := newFakeServerREST(t, true)
	auth := []string{"-u", "Administrator", "-p", "password", "--no-ssl"}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"rebalance", []string{"cluster", "rebalance", "--rally-ip-address", up.hostPort()}, "[ok] - Cluster rebalanced\n"},
		{"already configured", []string{"cluster", "create", "--host", up.hostPort(), "--ram", "4"}, "[info] - Cluster already configured\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := outcase{args: append(tc.args, auth...)}.run(t)
			if code != 0 || out != tc.want || errOut != "" {
				t.Errorf("exit %d stdout=%q stderr=%q, want stdout %q", code, out, errOut, tc.want)
			}
		})
	}
}

// TestSecretOnceColorVariants: the generated password is printed once and unstyled on stdout,
// and the "shown only once" warning carries a colored label, on a color terminal.
func TestSecretOnceColorVariants(t *testing.T) {
	const pw = "Gen3rated-Pa55word!"
	for _, tty := range []bool{false, true} {
		name := "pipe"
		if tty {
			name = "terminal"
		}
		t.Run(name, func(t *testing.T) {
			d, out, errBuf := fakeTermDeps(tty, tty)
			defer logging.SetPrinter(logging.SetPrinter(newPrinter(d)))
			redact.Register(pw)
			t.Cleanup(redact.Reset)
			printSecretOnce(&runtime{Out: newRenderer(out, errBuf, false, false)}, nil, "password", pw)
			if got, want := out.String(), "password: "+pw+"\n"; got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
			if strings.Count(out.String()+errBuf.String(), pw) != 1 {
				t.Errorf("password must appear exactly once:\n%s%s", out, errBuf)
			}
			wantWarn := "[warning] - this password is shown only once\n"
			if tty {
				wantWarn = ansiWarn + " - this password is shown only once\n"
			}
			if errBuf.String() != wantWarn {
				t.Errorf("stderr = %q, want %q", errBuf.String(), wantWarn)
			}
		})
	}
}
