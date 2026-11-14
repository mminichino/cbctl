// Package cli implements the cbctl Cobra command tree.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
)

// versionString is the --version text shared by every root command built in this process.
var versionString string

// rootCmd is the process-wide root command used by Execute and by tests that predate NewRootCmd.
var rootCmd = buildRootCmd()

// SetVersion configures the CLI version shown by --version / version.
func SetVersion(version, commit, date string) {
	versionString = fmt.Sprintf("%s (commit=%s date=%s)", version, commit, date)
	rootCmd.Version = versionString
}

// NewRootCmd builds a fresh cbctl command tree. It shares no command or flag state with any
// other tree, so it is safe to call once per in-process invocation.
func NewRootCmd(d Deps) *cobra.Command {
	root := buildRootCmd()
	if d.Version != "" {
		root.Version = d.Version
	} else {
		root.Version = versionString
	}
	return root
}

func buildRootCmd() *cobra.Command {
	cobra.EnablePrefixMatching = true
	root := &cobra.Command{
		Use:   "cbctl",
		Short: "Provision Couchbase Server and Capella clusters, buckets, scopes, and collections",
		Long:  "Provision Couchbase Server clusters, buckets, scopes, and collections. Capella operations live under the capella subgroup.",
		// No args → help (match Python no_args_is_help).
		Run:           func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newClusterCmd())
	root.AddCommand(newBucketCmd())
	root.AddCommand(newScopeCmd())
	root.AddCommand(newCollectionCmd())
	root.AddCommand(newKeyspaceCmd())
	root.AddCommand(newImportCmd())
	root.AddCommand(newCapellaCmd())
	root.AddCommand(newConfigureCmd())
	return root
}

// Execute runs the root command under a signal-aware context.
func Execute() error {
	if wd, err := os.Getwd(); err == nil {
		path, names, err := settings.LoadDotEnv(wd)
		if err != nil {
			logging.Warning("ignoring .env: %v", err)
		} else if path != "" {
			logging.Debug("loaded .env from %s (%v)", path, names)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func exitErr() {
	os.Exit(1)
}
