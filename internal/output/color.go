package output

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ColorMode is the value of the --color flag.
type ColorMode int

// Color modes.
const (
	// ColorAuto decides from the environment and from whether the stream is a terminal.
	ColorAuto ColorMode = iota
	// ColorAlways colors every stream, whatever the environment says.
	ColorAlways
	// ColorNever colors nothing.
	ColorNever
)

// String returns the flag spelling of m.
func (m ColorMode) String() string {
	switch m {
	case ColorAlways:
		return "always"
	case ColorNever:
		return "never"
	default:
		return "auto"
	}
}

// ParseColorMode parses "auto", "always" or "never" (case-insensitive, surrounding space ignored).
// An empty string is auto. Anything else is an error the caller reports as a usage error.
func ParseColorMode(s string) (ColorMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return ColorAuto, nil
	case "always":
		return ColorAlways, nil
	case "never":
		return ColorNever, nil
	}
	return ColorAuto, fmt.Errorf("--color must be auto, always or never (got %q)", s)
}

// Decider decides whether a stream gets color. Its inputs are injected so a test (or one
// in-process invocation) never reads the process environment or terminal.
type Decider struct {
	// Mode is the --color flag.
	Mode ColorMode
	// Env reads an environment variable. Nil means no variable is set.
	Env func(string) string
	// IsTerm reports whether w (usually an *os.File) is an interactive terminal. Nil means never.
	IsTerm func(w any) bool
}

// SystemDecider is a Decider for the real process: os.Getenv and a terminal check on *os.File.
func SystemDecider() Decider {
	return Decider{Env: os.Getenv, IsTerm: IsTerminal}
}

// IsTerminal reports whether w is an *os.File attached to a terminal. A wrapper that exposes the
// file through Underlying() is looked through.
func IsTerminal(w any) bool {
	if u, ok := w.(interface{ Underlying() io.Reader }); ok {
		w = u.Underlying()
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (d Decider) env(key string) string {
	if d.Env == nil {
		return ""
	}
	return d.Env(key)
}

// Color reports whether w should get color. The decision is made for that one writer, so stdout
// and stderr can differ. The precedence is documented on the package.
func (d Decider) Color(w io.Writer) bool {
	return d.decide(w) && platformColorOK(w)
}

func (d Decider) decide(w io.Writer) bool {
	switch d.Mode {
	case ColorNever:
		return false
	case ColorAlways:
		return true
	}
	if d.env("NO_COLOR") != "" || d.env("TERM") == "dumb" {
		return false
	}
	if v := d.env("CLICOLOR_FORCE"); v != "" && v != "0" {
		return true
	}
	// FORCE_COLOR is an alias of CLICOLOR_FORCE at the same precedence; "0" means off.
	switch v := d.env("FORCE_COLOR"); v {
	case "":
	case "0":
		return false
	default:
		return true
	}
	if d.env("CLICOLOR") == "0" {
		return false
	}
	return d.IsTerm != nil && d.IsTerm(w)
}
