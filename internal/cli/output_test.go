package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/redact"
)

const (
	ansiErr  = "\x1b[1;31m[error]\x1b[0m"
	ansiWarn = "\x1b[1;33m[warning]\x1b[0m"
	ansiOK   = "\x1b[1;32m[ok]\x1b[0m"
)

// outcase is one invocation of the in-process CLI with a chosen environment and fake terminals.
type outcase struct {
	env            map[string]string
	outTTY, errTTY bool
	args           []string
}

// run executes the case and returns stdout, stderr and the exit code.
func (c outcase) run(t *testing.T) (stdout, stderr string, code int) {
	t.Helper()
	home := t.TempDir()
	var out, errBuf bytes.Buffer
	d := DefaultDeps()
	d.Env = func(k string) string {
		if k == "CBCTL_HOME" {
			return home
		}
		return c.env[k]
	}
	d.LookupEnv = func(k string) (string, bool) {
		if k == "CBCTL_HOME" {
			return home, true
		}
		v, ok := c.env[k]
		return v, ok
	}
	d.Stdout, d.Stderr = &out, &errBuf
	d.Stdin = strings.NewReader("")
	d.Home = home
	d.Cwd = func() (string, error) { return home, nil }
	d.IsTTY = func(io.Reader) bool { return false }
	d.IsTerminal = func(w any) bool {
		return (c.outTTY && w == any(&out)) || (c.errTTY && w == any(&errBuf))
	}
	d.Keyring = noKeyring{}
	d.Clock = engine.NewFakeClock(time.Time{})
	d.ResetRedaction = true
	code = Run(context.Background(), c.args, d)
	return out.String(), errBuf.String(), code
}

func TestColorFlagAndErrorLines(t *testing.T) {
	// "bucket create" with no name is a usage error: exit 3, one [error] line on stderr.
	bad := []string{"bucket", "create"}
	tests := []struct {
		name        string
		c           outcase
		wantColored bool
		wantExit    int
		wantPrefix  string
	}{
		{"auto, stderr piped", outcase{args: bad}, false, 3, ""},
		{"auto, stderr terminal", outcase{args: bad, errTTY: true}, true, 3, ""},
		{"always, piped", outcase{args: append([]string{"--color=always"}, bad...)}, true, 3, ""},
		{"always, space form", outcase{args: append([]string{"--color", "always"}, bad...)}, true, 3, ""},
		{"never, terminal", outcase{args: append([]string{"--color=never"}, bad...), errTTY: true}, false, 3, ""},
		{"never beats CLICOLOR_FORCE", outcase{args: append([]string{"--color=never"}, bad...), env: map[string]string{"CLICOLOR_FORCE": "1"}}, false, 3, ""},
		{"NO_COLOR, terminal", outcase{args: bad, errTTY: true, env: map[string]string{"NO_COLOR": "1"}}, false, 3, ""},
		{"always beats NO_COLOR", outcase{args: append([]string{"--color=always"}, bad...), env: map[string]string{"NO_COLOR": "1"}}, true, 3, ""},
		{"FORCE_COLOR, piped", outcase{args: bad, env: map[string]string{"FORCE_COLOR": "1"}}, true, 3, ""},
		{"stdout terminal only", outcase{args: bad, outTTY: true}, false, 3, ""},
		{"flag after subcommand", outcase{args: append(append([]string{}, bad...), "--color=always")}, true, 3, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := tc.c.run(t)
			if code != tc.wantExit {
				t.Fatalf("exit %d, want %d\nstderr: %s", code, tc.wantExit, errOut)
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty", out)
			}
			if got := strings.Contains(errOut, "\x1b["); got != tc.wantColored {
				t.Errorf("colored = %v, want %v\nstderr: %q", got, tc.wantColored, errOut)
			}
			label := "[error]"
			if tc.wantColored {
				label = ansiErr
			}
			if !strings.HasPrefix(errOut, label+" - ") {
				t.Errorf("stderr should start with %q: %q", label+" - ", errOut)
			}
		})
	}
}

func TestColorFlagInvalid(t *testing.T) {
	for _, args := range [][]string{
		{"--color=sometimes", "cluster", "exists"},
		{"--color=sometimes", "capella", "project", "list"},
		{"capella", "project", "list", "--color=sometimes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, errOut, code := outcase{args: args}.run(t)
			if code != 3 {
				t.Fatalf("exit %d, want 3\nstderr: %s", code, errOut)
			}
			if out != "" || !strings.Contains(errOut, "--color must be auto, always or never") {
				t.Errorf("stdout=%q stderr=%q", out, errOut)
			}
		})
	}
}

func TestColorFlagInHelp(t *testing.T) {
	out, _, code := outcase{args: []string{"--help"}}.run(t)
	if code != 0 || !strings.Contains(out, "--color string") || !strings.Contains(out, "auto|always|never") {
		t.Errorf("exit %d; --help does not list --color:\n%s", code, out)
	}
}

// TestStatusLinesStayOnStdout (D1): [info] on stdout in text mode, colored per stdout only.
func TestStatusLinesStayOnStdout(t *testing.T) {
	srv := newFakeServerREST(t, true)
	hp := srv.hostPort()
	base := []string{"cluster", "create", "--host", hp, "--ram", "4", "-u", "Administrator", "-p", "password", "--no-ssl"}
	tests := []struct {
		name string
		c    outcase
		want string
	}{
		{"piped", outcase{args: base}, "[info] - Cluster already configured\n"},
		{"terminals", outcase{args: base, outTTY: true, errTTY: true}, "[info] - Cluster already configured\n"},
		{"always", outcase{args: append([]string{"--color=always"}, base...)}, "[info] - Cluster already configured\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := tc.c.run(t)
			if code != 0 || out != tc.want || errOut != "" {
				t.Errorf("exit %d stdout=%q stderr=%q", code, out, errOut)
			}
		})
	}
}

// TestContractStdoutIsPlain: exists prints true/false and cluster map prints its host list, with
// no escape code under any flag or environment, even on a color terminal.
func TestContractStdoutIsPlain(t *testing.T) {
	up := newFakeServerREST(t, true)
	down := newFakeServerREST(t, false)
	auth := []string{"-u", "Administrator", "-p", "password", "--no-ssl"}
	envs := []map[string]string{nil, {"CLICOLOR_FORCE": "1"}, {"FORCE_COLOR": "1", "TERM": "xterm"}}
	for _, env := range envs {
		for _, flag := range []string{"--color=auto", "--color=always", "--color=never"} {
			for _, tty := range []bool{false, true} {
				c := func(args ...string) outcase {
					return outcase{env: env, outTTY: tty, errTTY: tty, args: append(append([]string{flag}, args...), auth...)}
				}
				name := flag + "/" + strings.Join(mapKeys(env), ",")
				t.Run(name, func(t *testing.T) {
					if out, _, code := c("cluster", "exists", "--host", up.hostPort()).run(t); code != 0 || out != "true\n" {
						t.Errorf("exists (up): exit %d stdout %q", code, out)
					}
					if out, _, code := c("cluster", "exists", "--host", down.hostPort()).run(t); code != 0 || out != "false\n" {
						t.Errorf("exists (down): exit %d stdout %q", code, out)
					}
					out, _, code := c("cluster", "map", "--host", up.hostPort()).run(t)
					if code != 0 || !strings.HasPrefix(out, "Cluster Host List:") || strings.Contains(out, "\x1b[") {
						t.Errorf("map: exit %d stdout %q", code, out)
					}
				})
			}
		}
	}
}

func mapKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestContractJSONHasNoANSI: -o json writes one plain document to stdout under every flag, env
// and terminal combination.
func TestContractJSONHasNoANSI(t *testing.T) {
	envs := []map[string]string{nil, {"CLICOLOR_FORCE": "1"}, {"FORCE_COLOR": "1"}}
	for _, env := range envs {
		for _, flag := range []string{"--color=auto", "--color=always"} {
			for _, tty := range []bool{false, true} {
				name := flag + "/" + strings.Join(mapKeys(env), ",")
				t.Run(name, func(t *testing.T) {
					c := outcase{env: env, outTTY: tty, errTTY: tty,
						args: []string{flag, "capella", "configure", "list", "-o", "json"}}
					out, errOut, code := c.run(t)
					if code != 0 {
						t.Fatalf("exit %d\nstderr: %s", code, errOut)
					}
					if strings.Contains(out, "\x1b") {
						t.Errorf("escape code in JSON stdout: %q", out)
					}
					var v any
					if err := json.Unmarshal([]byte(out), &v); err != nil {
						t.Errorf("stdout is not one JSON document: %v\n%q", err, out)
					}
				})
			}
		}
	}
}

func fakeTermDeps(outTTY, errTTY bool) (Deps, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	d := DefaultDeps()
	d.Env = func(string) string { return "" }
	d.Stdout, d.Stderr = &out, &errBuf
	d.IsTerminal = func(w any) bool { return (outTTY && w == any(&out)) || (errTTY && w == any(&errBuf)) }
	return d, &out, &errBuf
}

// TestPrintHelpersFollowTheStream: printError, printAction, printNotice and the wizard's error
// lines are colored on a terminal and plain on a pipe, and redact before styling.
func TestPrintHelpersFollowTheStream(t *testing.T) {
	const secret = "sekret-helper-test-4711"
	redact.Register(secret)
	t.Cleanup(redact.Reset)

	tests := []struct {
		name      string
		tty       bool
		call      func(w io.Writer)
		wantTTY   string
		wantPlain string
	}{
		{"printError", false, func(w io.Writer) { writeLabeled(w, output.KindError, "failed with %s", secret) },
			ansiErr + " - failed with " + redact.String(secret) + "\n", "[error] - failed with " + redact.String(secret) + "\n"},
		{"printAction", false, func(w io.Writer) { writeAction(w, "Try again.") },
			"  → Try again.\n", "  → Try again.\n"}, // Unicode until P6 (glyphPolicy)
		{"printNotice", false, func(w io.Writer) { printNotice(w, "no key", "Run configure.") },
			ansiWarn + " - no key\n  → Run configure.\n", "[warning] - no key\n  → Run configure.\n"},
		{"printNotice without action", false, func(w io.Writer) { printNotice(w, "no key", "") },
			ansiWarn + " - no key\n", "[warning] - no key\n"},
	}
	for _, tc := range tests {
		for _, tty := range []bool{true, false} {
			name := tc.name + "/pipe"
			want := tc.wantPlain
			if tty {
				name, want = tc.name+"/terminal", tc.wantTTY
			}
			t.Run(name, func(t *testing.T) {
				d, _, errBuf := fakeTermDeps(false, tty)
				defer logging.SetPrinter(logging.SetPrinter(newPrinter(d)))
				tc.call(errBuf)
				if got := errBuf.String(); got != want {
					t.Errorf("got %q, want %q", got, want)
				}
				if strings.Contains(errBuf.String(), secret) {
					t.Error("secret leaked")
				}
			})
		}
	}
}

func TestWizardLinesFollowTheStream(t *testing.T) {
	run := func(t *testing.T, tty bool) string {
		d, out, _ := fakeTermDeps(tty, false)
		defer logging.SetPrinter(logging.SetPrinter(newPrinter(d)))
		w := &initIO{in: bufio.NewReader(strings.NewReader("nope\ny\n")), out: out}
		got, err := w.ask("Provider", "", []string{"aws", "gcp"})
		if err == nil {
			t.Fatalf("ask returned %q; the input has no valid answer and should end", got)
		}
		return out.String()
	}
	if got := run(t, true); !strings.Contains(got, ansiErr+" - nope is not one of aws, gcp\n") {
		t.Errorf("terminal wizard line not colored: %q", got)
	}
	got := run(t, false)
	if !strings.Contains(got, "[error] - nope is not one of aws, gcp\n") || strings.Contains(got, "\x1b[") {
		t.Errorf("piped wizard line should be plain: %q", got)
	}
}

// TestSecretsPrintedOnceUnredacted: a generated password goes to stdout as issued, plain, even
// when the invocation colors output.
func TestSecretsPrintedOnceUnredacted(t *testing.T) {
	const pw = "Zx9-generated-once"
	for _, jsonMode := range []bool{false, true} {
		d, out, errBuf := fakeTermDeps(true, true)
		d.Env = func(k string) string {
			if k == "CLICOLOR_FORCE" {
				return "1"
			}
			return ""
		}
		defer logging.SetPrinter(logging.SetPrinter(newPrinter(d)))
		redact.Register(pw)
		r := newRenderer(out, errBuf, jsonMode, false)
		r.Secret(map[string]string{"name": "app"}, "password", pw)
		redact.Reset()
		if strings.Count(out.String(), pw) != 1 || strings.Contains(out.String()+errBuf.String(), "\x1b[") {
			t.Errorf("json=%v: stdout=%q stderr=%q", jsonMode, out.String(), errBuf.String())
		}
	}
}
