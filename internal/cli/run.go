package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/settings"
)

// runMu serializes legacy package-level redirects (logging outputs, error writer, clockFactory,
// ttyCheck) so concurrent Run calls with separate Deps do not race on those globals.
var runMu sync.Mutex

// Run executes one CLI invocation in-process and returns the exit code HandleError would return.
// It never calls os.Exit. SIGINT handling is the caller's responsibility (pass a cancellable ctx).
func Run(ctx context.Context, args []string, d Deps) (code int) {
	d = normalizeDeps(d)

	runMu.Lock()
	defer runMu.Unlock()
	// Reset under runMu: a concurrent Run's secrets stay registered until that run releases the lock.
	if d.ResetRedaction {
		redact.Reset()
	}

	// NewRootCmd is under the lock because cobra.EnablePrefixMatching is process-global.
	root := NewRootCmd(d)
	root.SetArgs(args)
	root.SetIn(d.Stdin)
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	ctx = contextWithDeps(ctx, d)

	prevOut, prevErr := logging.Outputs()
	logging.SetOutputs(d.Stdout, d.Stderr)
	defer logging.SetOutputs(prevOut, prevErr)

	prevErrWriter := errWriter
	SetErrorWriter(d.Stderr)
	defer SetErrorWriter(prevErrWriter)

	prevClock := clockFactory
	clk := d.Clock
	clockFactory = func() engine.Clock { return clk }
	defer func() { clockFactory = prevClock }()

	prevTTY := ttyCheck
	isTTY := d.IsTTY
	ttyCheck = func(v any) bool {
		if r, ok := v.(io.Reader); ok {
			return isTTY(r)
		}
		return false
	}
	defer func() { ttyCheck = prevTTY }()

	defer func() {
		if rec := recover(); rec != nil {
			msg := fmt.Sprintf("internal error: %v", rec)
			fmt.Fprintln(d.Stderr, redact.String(msg))
			code = int(ExitError)
		}
	}()

	err := interruptedError(ctx, classifyCobraError(root.ExecuteContext(ctx)))
	return int(HandleError(err))
}

func normalizeDeps(d Deps) Deps {
	if d.normalized {
		return d
	}
	def := DefaultDeps()
	switch {
	case d.Env == nil:
		d.Env = def.Env
	case !d.envShimmed:
		d.Env = settings.NewLegacyShim(d.Env, func(msg string) { logging.Warning("%s", msg) }).Env
	}
	d.envShimmed = true
	if d.LookupEnv == nil {
		d.LookupEnv = def.LookupEnv
	}
	if d.Stdin == nil {
		d.Stdin = def.Stdin
	}
	if d.Stdout == nil {
		d.Stdout = def.Stdout
	}
	if d.Stderr == nil {
		d.Stderr = def.Stderr
	}
	if d.IsTTY == nil {
		d.IsTTY = def.IsTTY
	}
	if d.Cwd == nil {
		d.Cwd = def.Cwd
	}
	if d.Clock == nil {
		d.Clock = def.Clock
	}
	if d.Keyring == nil {
		d.Keyring = def.Keyring
	}
	if d.Home == "" {
		d.Home = settings.HomeDir(d.Env)
	}
	if d.LegacyHome == "" && d.LegacyKeyring == nil {
		d.LegacyHome, d.LegacyKeyring = legacySources(d)
	}
	if d.warned == nil {
		d.warned = &sync.Map{}
	}
	d.normalized = true
	return d
}

// interruptedError makes a failure that happened after the invocation was cancelled (Ctrl-C,
// SIGTERM) exit 130, whatever error type the library that noticed the cancellation returned.
func interruptedError(ctx context.Context, err error) error {
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %w", context.Canceled, err)
}
