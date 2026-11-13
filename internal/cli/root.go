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

// rootCmd is the cbctl root command.
var rootCmd = &cobra.Command{
	Use:   "cbctl",
	Short: "Provision Couchbase Server and Capella clusters, buckets, scopes, and collections",
	Long:  "Provision Couchbase Server clusters, buckets, scopes, and collections. Capella operations live under the capella subgroup.",
}

// SetVersion configures the CLI version shown by --version / version.
func SetVersion(version, commit, date string) {
	rootCmd.Version = fmt.Sprintf("%s (commit=%s date=%s)", version, commit, date)
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

func init() {
	cobra.EnablePrefixMatching = true
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	rootCmd.AddCommand(newClusterCmd())
	rootCmd.AddCommand(newBucketCmd())
	rootCmd.AddCommand(newScopeCmd())
	rootCmd.AddCommand(newCollectionCmd())
	rootCmd.AddCommand(newKeyspaceCmd())
	rootCmd.AddCommand(newImportCmd())
	rootCmd.AddCommand(newCapellaCmd())
	rootCmd.AddCommand(newConfigureCmd())

	// No args → help (match Python no_args_is_help).
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	}
}

func exitErr() {
	os.Exit(1)
}
