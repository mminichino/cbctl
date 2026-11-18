package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/secrets"
	"github.com/spf13/cobra"
)

// runCLI runs one in-process invocation with no credentials, no terminal and an isolated home.
func runCLI(t *testing.T, ctx context.Context, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	home := t.TempDir()
	var out, errBuf bytes.Buffer
	d := DefaultDeps()
	d.Env = func(k string) string {
		if k == "CBCTL_HOME" {
			return home
		}
		return ""
	}
	d.LookupEnv = func(k string) (string, bool) {
		if k == "CBCTL_HOME" {
			return home, true
		}
		return "", false
	}
	d.Stdout, d.Stderr = &out, &errBuf
	d.Stdin = strings.NewReader("")
	d.Home = home
	d.Cwd = func() (string, error) { return home, nil }
	d.IsTTY = func(io.Reader) bool { return false }
	d.Keyring = noKeyring{}
	d.Clock = engine.NewFakeClock(time.Time{}) // retries and waits never sleep
	d.ResetRedaction = true
	code = Run(ctx, args, d)
	return out.String(), errBuf.String(), code
}

// noKeyring is an empty keyring so tests never touch the OS keychain.
type noKeyring struct{}

func (noKeyring) Get(string) (string, error) { return "", errors.New("not found") }
func (noKeyring) Set(string, string) error   { return errors.New("no keyring") }
func (noKeyring) Delete(string) error        { return errors.New("no keyring") }

// groups returns the command path (without "cbctl") of every pure command group: no args prints
// help. Groups that run themselves (configure) are returned separately.
func groups(t *testing.T) (pure, self [][]string) {
	t.Helper()
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		if c.HasSubCommands() {
			p := append([]string(nil), path...)
			if c.Name() == "configure" {
				self = append(self, p)
			} else {
				pure = append(pure, p)
			}
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(sub, append(append([]string(nil), path...), sub.Name()))
		}
	}
	walk(NewRootCmd(Deps{}), nil)
	return pure, self
}

func TestUnknownSubcommand(t *testing.T) {
	pure, self := groups(t)
	if len(pure) < 10 {
		t.Fatalf("found only %d command groups; the walk is broken", len(pure))
	}
	for _, path := range pure {
		name := "cbctl " + strings.Join(path, " ")
		t.Run(name, func(t *testing.T) {
			out, errOut, code := runCLI(t, context.Background(), path...)
			if code != 0 {
				t.Fatalf("%s: exit %d, want 0 (help)\n%s", name, code, errOut)
			}
			if !strings.Contains(out, "Usage:") {
				t.Fatalf("%s: stdout is not help:\n%s", name, out)
			}
			_, errOut, code = runCLI(t, context.Background(), append(path, "bogus-verb")...)
			if code != int(ExitConfigInvalid) {
				t.Fatalf("%s bogus-verb: exit %d, want 3\n%s", name, code, errOut)
			}
			wantContains(t, errOut, `unknown command "bogus-verb"`, "->")
		})
	}
	for _, path := range self {
		name := "cbctl " + strings.Join(path, " ")
		t.Run(name, func(t *testing.T) {
			_, errOut, code := runCLI(t, context.Background(), append(path, "bogus-verb")...)
			if code != int(ExitConfigInvalid) {
				t.Fatalf("%s bogus-verb: exit %d, want 3\n%s", name, code, errOut)
			}
			wantContains(t, errOut, `unknown command "bogus-verb"`)
		})
	}
}

func TestUnknownSubcommandHints(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"capella", "projct"}, []string{`unknown command "projct"`, "did you mean project?"}},
		{[]string{"capella", "c"}, []string{`ambiguous command "c"`, "cidr", "cluster"}},
		{[]string{"bogus"}, []string{`unknown command "bogus" for "cbctl"`}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, errOut, code := runCLI(t, context.Background(), tc.args...)
			if code != int(ExitConfigInvalid) {
				t.Fatalf("exit %d, want 3\n%s", code, errOut)
			}
			wantContains(t, errOut, tc.want...)
		})
	}
}

func TestRemovedVerbHint(t *testing.T) {
	removedVerbs["capella cluster frobnicate"] = "cbctl capella apply --target cluster/NAME"
	t.Cleanup(func() { delete(removedVerbs, "capella cluster frobnicate") })
	_, errOut, code := runCLI(t, context.Background(), "capella", "cluster", "frobnicate", "c1")
	if code != int(ExitConfigInvalid) {
		t.Fatalf("exit %d, want 3\n%s", code, errOut)
	}
	wantContains(t, errOut, `unknown command "frobnicate"`, "use cbctl capella apply --target cluster/NAME")
}

func TestUsageErrorsExit3(t *testing.T) {
	cases := [][]string{
		{"capella", "--bogus-flag"},
		{"capella", "bucket", "list", "--nope"},
		{"capella", "cluster", "get"},               // missing positional
		{"bucket", "create"},                        // Server command arity
		{"scope", "create", "s1"},                   // required --bucket
		{"capella", "cluster", "list", "--project"}, // flag needs a value
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, errOut, code := runCLI(t, context.Background(), args...)
			if code != int(ExitConfigInvalid) {
				t.Fatalf("exit %d, want 3\n%s", code, errOut)
			}
		})
	}
}

func TestPositionalThatLooksLikeACommand(t *testing.T) {
	// "schedule" is an argument to the leaf "cluster get", never an unknown subcommand.
	_, errOut, _ := runCLI(t, context.Background(), "capella", "cluster", "get", "schedule")
	if strings.Contains(errOut, "unknown command") {
		t.Fatalf("leaf positional treated as a subcommand:\n%s", errOut)
	}
}

func TestPrefixMatchingStillWorks(t *testing.T) {
	out, errOut, code := runCLI(t, context.Background(), "capella", "gui", "topics")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("guide topics printed nothing")
	}
}

func TestCancelledContextExits130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Server commands connect through cmd.Context(); an already-cancelled context must surface
	// as an interrupt, not a generic failure.
	cases := [][]string{
		{"bucket", "exists", "b1", "--host", "127.0.0.1"},
		{"keyspace", "create", "b.s.c", "--host", "127.0.0.1"},
		{"import", "docs.jsonl", "b.s.c", "--host", "127.0.0.1"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, errOut, code := runCLI(t, ctx, args...)
			if code != int(ExitInterrupted) {
				t.Fatalf("exit %d, want 130\n%s", code, errOut)
			}
		})
	}
}

func TestFailureKeepsExitCodes(t *testing.T) {
	cases := []struct {
		err  error
		want ExitCode
	}{
		{errors.New("boom"), ExitError},
		{&engine.WaitTimeout{Name: "c1", State: "deploying"}, ExitTimeout},
		{fmt.Errorf("waiting: %w", context.DeadlineExceeded), ExitTimeout},
		{context.Canceled, ExitInterrupted},
		{Errorf(ExitBlocked, "", "blocked"), ExitBlocked},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		SetErrorWriter(&buf)
		got := HandleError(failure("Failed to create cluster", tc.err))
		SetErrorWriter(io.Discard)
		if got != tc.want {
			t.Errorf("failure(%v) -> exit %d, want %d (%s)", tc.err, got, tc.want, buf.String())
		}
	}
}

func TestHandleErrorRestoreError(t *testing.T) {
	var buf bytes.Buffer
	SetErrorWriter(&buf)
	defer SetErrorWriter(io.Discard)
	err := &secrets.RestoreError{
		Primary: errors.New("update credential: HTTP 500"),
		Restore: errors.New("restoring /x/c1.app.json: permission denied"),
		Path:    "/x/c1.app.json",
		Backup:  "/x/c1.app.json.bak",
	}
	if code := HandleError(err); code != ExitError {
		t.Fatalf("exit %d, want 1", code)
	}
	wantContains(t, buf.String(), "update credential: HTTP 500", "could not restore previous password file",
		"copy /x/c1.app.json.bak to /x/c1.app.json")
}

// TestRunRedactionUnderConcurrency runs credential-bearing invocations concurrently with
// ResetRedaction: one run's reset must never unregister another in-flight run's secret.
func TestRunRedactionUnderConcurrency(t *testing.T) {
	const secret = "tok-UNIQUE-9f3c1e"
	var wg sync.WaitGroup
	leaks := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errOut, _ := runCLI(t, context.Background(), "capella", "project", "list",
				"--token", secret, "--api-host", "http://127.0.0.1:1", "--org-id", "org-1")
			if strings.Contains(errOut, secret) {
				leaks <- errOut
			}
		}()
	}
	wg.Wait()
	close(leaks)
	for l := range leaks {
		t.Errorf("secret leaked to stderr:\n%s", l)
	}
}
