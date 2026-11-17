// Package clitest provides hermetic scenario helpers for Capella CLI tests.
package clitest

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/settings"
)

// Scenario is one isolated Capella CLI test environment: a fake API, temp home/cwd,
// scripted prompts, and hermetic Deps for cli.Run.
type Scenario struct {
	T       testing.TB
	Fake    *capellafake.Fake
	Clock   *engine.FakeClock
	Env     map[string]string // CAPELLA_API_BASE_URL, CAPELLA_API_KEY, CAPELLA_ORG_ID, CBCTL_HOME preset
	Dir     string            // t.TempDir(): cwd for the CLI and home of capella.yaml
	Keyring *settings.MemKeyring
	Prompts *ScriptedPrompter

	tty bool
}

// ScenarioOption customizes NewScenario.
type ScenarioOption func(*Scenario)

// NewScenario wires a fake Capella, temp dir, env, and registers cleanup.
func NewScenario(t testing.TB, opts ...ScenarioOption) *Scenario {
	t.Helper()
	dir := t.TempDir()
	clk := engine.NewFakeClock(time.Time{})
	fake := capellafake.New(t, capellafake.WithClock(clk))
	s := &Scenario{
		T:       t,
		Fake:    fake,
		Clock:   clk,
		Dir:     dir,
		Keyring: settings.NewMemKeyring(),
		Prompts: NewScriptedPrompter(t),
		Env: map[string]string{
			"CAPELLA_API_BASE_URL": fake.URL,
			"CAPELLA_API_KEY":      fake.APIKey,
			"CAPELLA_ORG_ID":       fake.OrgID,
			"CBCTL_HOME":           dir,
			// Clear aliases / mode flags that would leak from the developer environment.
			"CAPELLA_TOKEN": "",
			"CBCTL_PROFILE": "",
			"CBCTL_QUIET":   "",
			"CI":            "",
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	// Bridge to process env until Capella commands read Deps.Env end-to-end.
	for k, v := range s.Env {
		t.Setenv(k, v)
	}
	return s
}

// Run invokes the CLI with empty stdin and returns the exit code plus captured output.
func (s *Scenario) Run(args ...string) Result {
	s.T.Helper()
	return s.RunWith("", args...)
}

// RunWith invokes the CLI with the given stdin contents.
func (s *Scenario) RunWith(stdin string, args ...string) Result {
	s.T.Helper()
	return s.RunContext(context.Background(), stdin, args...)
}

// RunContext invokes the CLI with ctx, so a test can cancel it mid-command (Ctrl-C).
func (s *Scenario) RunContext(ctx context.Context, stdin string, args ...string) Result {
	s.T.Helper()
	return s.RunReader(ctx, strings.NewReader(stdin), args...)
}

// RunReader invokes the CLI with stdin read from r. Pass ForbiddenStdin to prove a command never
// reads stdin.
func (s *Scenario) RunReader(ctx context.Context, r io.Reader, args ...string) Result {
	s.T.Helper()
	var stdout, stderr bytes.Buffer
	d := s.deps(&stdout, &stderr, r)
	code := cli.Run(ctx, args, d)
	return Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// ForbiddenStdin is a stdin that fails the test when read: a command without a terminal must
// never wait on input it cannot get.
func ForbiddenStdin(t testing.TB, what string) io.Reader {
	return forbiddenReader{t: t, what: what}
}

type forbiddenReader struct {
	t    testing.TB
	what string
}

func (f forbiddenReader) Read([]byte) (int, error) {
	f.t.Errorf("%s read stdin with no terminal attached", f.what)
	return 0, io.EOF
}

func (s *Scenario) deps(stdout, stderr io.Writer, stdin io.Reader) cli.Deps {
	envMap := s.Env
	env := func(k string) string {
		if v, ok := envMap[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	lookup := func(k string) (string, bool) {
		if v, ok := envMap[k]; ok {
			return v, true
		}
		return os.LookupEnv(k)
	}
	tty := s.tty
	return cli.Deps{
		Env:            env,
		LookupEnv:      lookup,
		Stdin:          stdin,
		Stdout:         stdout,
		Stderr:         stderr,
		IsTTY:          func(io.Reader) bool { return tty },
		Cwd:            func() (string, error) { return s.Dir, nil },
		Home:           s.Dir,
		Keyring:        s.Keyring,
		Clock:          s.Clock,
		Prompter:       s.Prompts,
		ResetRedaction: true,
	}
}

// WriteFile writes text under s.Dir and returns the absolute path.
func (s *Scenario) WriteFile(rel, text string) string {
	s.T.Helper()
	path := filepath.Join(s.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.T.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		s.T.Fatalf("write %s: %v", path, err)
	}
	return path
}

// ReadFile reads a file under s.Dir.
func (s *Scenario) ReadFile(rel string) string {
	s.T.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, rel))
	if err != nil {
		s.T.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// Answers queues scripted prompter responses for subsequent Run calls.
func (s *Scenario) Answers(answers ...string) {
	s.T.Helper()
	s.Prompts.Push(answers...)
}

// TTY controls whether Deps.IsTTY reports an interactive terminal (default false).
func (s *Scenario) TTY(on bool) {
	s.tty = on
}
