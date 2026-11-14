package cli

import (
	"io"
	"net/http"
	"os"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/settings"
	"golang.org/x/term"
)

// Deps are the process-level dependencies of one CLI invocation. Tests build their own so that
// invocations share no state; DefaultDeps wires the real process.
type Deps struct {
	// Env reads an environment variable (default os.Getenv).
	Env func(string) string
	// LookupEnv reads an environment variable and reports whether it is set.
	LookupEnv func(string) (string, bool)
	// Stdin is the standard input (default os.Stdin).
	Stdin io.Reader
	// Stdout and Stderr are the output streams.
	Stdout, Stderr io.Writer
	// IsTTY reports whether r is an interactive terminal (default: term.IsTerminal on *os.File).
	IsTTY func(io.Reader) bool
	// Cwd returns the working directory (default os.Getwd).
	Cwd func() (string, error)
	// Home is the settings home directory; empty means settings.HomeDir(Env).
	Home string
	// Keyring stores Capella API keys (default settings.OSKeyring{}).
	Keyring settings.Keyring
	// Clock is used for Capella waits and retries (default engine.RealClock{}).
	Clock engine.Clock
	// HTTPClient is used for Capella API calls when set.
	HTTPClient *http.Client
	// Prompter answers interactive questions (default: a terminal prompter on Stdin/Stderr).
	Prompter Prompter
	// Version is the --version text; empty keeps the value from SetVersion.
	Version string
	// ResetRedaction clears the redaction registry at the start of Run (tests).
	ResetRedaction bool
}

// Prompter is the interactive prompt surface used by Capella commands and the init wizard.
type Prompter interface {
	Confirm(question string, def bool) (bool, error)
	Typed(prompt, expected string) (bool, error)
	Ask(question, def string) (string, error)
	Select(question string, options []string) (int, error)
	Secret(question string) (string, error)
}

// DefaultDeps returns the dependencies of the real process.
func DefaultDeps() Deps {
	env := func(k string) string { return os.Getenv(k) }
	return Deps{
		Env:       env,
		LookupEnv: os.LookupEnv,
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		IsTTY: func(r io.Reader) bool {
			f, ok := r.(*os.File)
			return ok && term.IsTerminal(int(f.Fd()))
		},
		Cwd:     os.Getwd,
		Home:    settings.HomeDir(env),
		Keyring: settings.OSKeyring{},
		Clock:   engine.RealClock{},
	}
}
