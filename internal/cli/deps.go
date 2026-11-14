package cli

import (
	"io"
	"net/http"
	"os"
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
	// Cwd returns the working directory (default os.Getwd).
	Cwd func() (string, error)
	// HTTPClient is used for Capella API calls when set.
	HTTPClient *http.Client
	// Version is the --version text; empty keeps the value from SetVersion.
	Version string
}

// DefaultDeps returns the dependencies of the real process.
func DefaultDeps() Deps {
	return Deps{
		Env:       os.Getenv,
		LookupEnv: os.LookupEnv,
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Cwd:       os.Getwd,
	}
}
