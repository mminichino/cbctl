package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

// ExitCode is a process exit status.
type ExitCode int

// Exit codes used by cbctl.
const (
	ExitOK            ExitCode = 0
	ExitError         ExitCode = 1
	ExitChanges       ExitCode = 2 // reserved: Phase 3
	ExitConfigInvalid ExitCode = 3
	ExitBlocked       ExitCode = 4
	ExitTimeout       ExitCode = 5
	ExitInterrupted   ExitCode = 130
)

// CLIError is a structured CLI failure with an optional action hint.
type CLIError struct {
	Code    ExitCode
	Message string
	Action  string
	Err     error
}

// Error implements error.
func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the underlying error.
func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Errorf builds a CLIError.
func Errorf(code ExitCode, action, format string, a ...any) *CLIError {
	return &CLIError{Code: code, Message: fmt.Sprintf(format, a...), Action: action}
}

var errWriter io.Writer = os.Stderr

// SetErrorWriter overrides stderr for HandleError (tests).
func SetErrorWriter(w io.Writer) {
	if w != nil {
		errWriter = w
	}
}

// HandleError prints err to stderr (redacted) and returns the exit code. Nil → ExitOK.
func HandleError(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(errWriter)
		return ExitInterrupted
	}

	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		printError(cliErr.Message)
		printAction(cliErr.Action)
		if cliErr.Code != 0 {
			return cliErr.Code
		}
		return ExitError
	}

	var apiErr *capellaapi.APIError
	if errors.As(err, &apiErr) {
		printError(apiErr.Error())
		printAction(apiErr.Hint)
		return ExitError
	}

	var amb *capellaapi.AmbiguousWriteError
	if errors.As(err, &amb) {
		printError(amb.Error())
		printAction("re-run the command; check the resource before retrying")
		return ExitError
	}

	var tr *capellaapi.TransportError
	if errors.As(err, &tr) {
		printError(tr.Error())
		printAction("re-run the command; check the resource before retrying")
		return ExitError
	}

	var wt *engine.WaitTimeout
	if errors.As(err, &wt) {
		printError(wt.Error())
		printAction("run the command again to keep waiting, or pass a longer --timeout")
		return ExitTimeout
	}
	var st *engine.StatusTimeout
	if errors.As(err, &st) {
		printError(st.Error())
		printAction("run the command again to keep waiting, or pass a longer --timeout")
		return ExitTimeout
	}

	var wf *engine.WaitFailed
	if errors.As(err, &wf) {
		printError(wf.Error())
		printAction("open the cluster's activity log in the Capella UI")
		return ExitError
	}
	var sf *engine.StatusFailed
	if errors.As(err, &sf) {
		printError(sf.Error())
		printAction("open the cluster's activity log in the Capella UI")
		return ExitError
	}

	printError(err.Error())
	return ExitError
}

func printError(msg string) {
	fmt.Fprintf(errWriter, "[error] - %s\n", redact.String(msg))
}

func printAction(action string) {
	if action == "" {
		return
	}
	fmt.Fprintf(errWriter, "  → %s\n", redact.String(action))
}
