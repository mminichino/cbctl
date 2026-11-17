package cli

import (
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
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
	// LegacyHome is capellactl's config directory, read for profiles cbctl does not have. Empty
	// (the default when Home or CBCTL_HOME points somewhere explicit): no legacy profiles.
	LegacyHome string
	// LegacyKeyring is capellactl's keychain, read for API keys cbctl's keychain does not have.
	LegacyKeyring settings.Keyring

	normalized bool
	envShimmed bool
	warned     *sync.Map
}

// processEnv is the process environment with capellactl variables as fallbacks; one shim per
// process so each legacy variable is reported once.
var processEnv = settings.NewLegacyShim(os.Getenv, func(msg string) { logging.Warning("%s", msg) })

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
	env := processEnv.Env
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

		envShimmed: true,
	}
}

// legacySources decides where capellactl profiles and keys are read from. They are consulted
// only when cbctl uses its default home (or CAPELLACTL_HOME is set), so a test or a caller with
// an explicit home never sees the developer's capellactl settings.
func legacySources(d Deps) (string, settings.Keyring) {
	explicit := d.Env("CAPELLACTL_HOME") != ""
	defaultHome := d.Env("CBCTL_HOME") == "" && d.Home == settings.HomeDir(d.Env)
	if !explicit && !defaultHome {
		return "", nil
	}
	var kr settings.Keyring
	if k, ok := d.Keyring.(settings.OSKeyring); ok && k.Service == "" {
		kr = settings.OSKeyring{Service: settings.LegacyKeyringService}
	}
	return settings.LegacyHome(d.Env), kr
}

// warnOnce prints msg as a warning once per invocation.
func warnOnce(d Deps, msg string) {
	if d.warned != nil {
		if _, seen := d.warned.LoadOrStore(msg, true); seen {
			return
		}
	}
	logging.Warning("%s", msg)
}

// resolveSettings is settings.Resolve with the invocation's environment, home, keychains and
// capellactl fallbacks filled in.
func resolveSettings(cmd *cobra.Command, in settings.Inputs) (*settings.Settings, error) {
	d := depsFromCmd(cmd)
	if in.Env == nil {
		in.Env = d.Env
	}
	if in.Home == "" {
		in.Home = d.Home
	}
	if in.Keyring == nil {
		in.Keyring = d.Keyring
	}
	if in.LegacyHome == "" {
		in.LegacyHome = d.LegacyHome
	}
	if in.LegacyKeyring == nil {
		in.LegacyKeyring = d.LegacyKeyring
	}
	return settings.Resolve(in)
}
