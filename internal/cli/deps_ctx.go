package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
)

type depsCtxKey struct{}

// contextWithDeps stores d on ctx so Capella commands can read Env/Cwd/IO without process globals.
func contextWithDeps(ctx context.Context, d Deps) context.Context {
	return context.WithValue(ctx, depsCtxKey{}, d)
}

// depsFromCmd returns the Deps attached to cmd's context, or DefaultDeps when absent
// (Execute / legacy tests that still call ExecuteContext directly).
func depsFromCmd(cmd *cobra.Command) Deps {
	if cmd != nil {
		if ctx := cmd.Context(); ctx != nil {
			if d, ok := ctx.Value(depsCtxKey{}).(Deps); ok {
				return normalizeDeps(d)
			}
		}
	}
	return DefaultDeps()
}

// envFrom returns the Env lookup from cmd's Deps.
func envFrom(cmd *cobra.Command) func(string) string {
	return depsFromCmd(cmd).Env
}

// cwdFrom returns the working directory via cmd's Deps.
func cwdFrom(cmd *cobra.Command) (string, error) {
	return depsFromCmd(cmd).Cwd()
}

// stderrFrom returns the stderr writer for cmd (Deps or cobra).
func stderrFrom(cmd *cobra.Command) io.Writer {
	if cmd != nil {
		if w := cmd.ErrOrStderr(); w != nil {
			return w
		}
	}
	return depsFromCmd(cmd).Stderr
}

// stdinFrom returns the stdin reader for cmd (Deps or cobra).
func stdinFrom(cmd *cobra.Command) io.Reader {
	if cmd != nil {
		if r := cmd.InOrStdin(); r != nil {
			return r
		}
	}
	return depsFromCmd(cmd).Stdin
}
