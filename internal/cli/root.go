// Package cli implements the cbctl Cobra command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
)

// versionString is the --version text shared by every root command built in this process.
var versionString string

// LicenceLine is the second line of --version: cbctl's licence and copyright (see LICENSE and NOTICE).
const LicenceLine = "Licensed under Apache-2.0 · Copyright Couchbase, Inc. and contributors"

// rootCmd is the process-wide root command used by Execute and by tests that predate NewRootCmd.
var rootCmd = buildRootCmd()

// SetVersion configures the CLI version shown by --version / version.
func SetVersion(version, commit, date string) {
	versionString = fmt.Sprintf("%s (commit=%s date=%s)", version, commit, date)
	rootCmd.Version = versionString
	shareVersionWithCapella(rootCmd)
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
	root.SetVersionTemplate(versionTemplate)
	shareVersionWithCapella(root)
	return root
}

// shareVersionWithCapella makes `cbctl capella --version` print what `cbctl --version` does, so
// a script that replaced `capellactl` with `cbctl capella` keeps working.
func shareVersionWithCapella(root *cobra.Command) {
	for _, c := range root.Commands() {
		if c.Name() == "capella" {
			c.Version = root.Version
			c.SetVersionTemplate(strings.Replace(versionTemplate, "{{.Name}}", root.Name(), 1))
		}
	}
}

// capellaAPIVersion is the Capella Management API version the Capella commands use.
const capellaAPIVersion = "v4.0"

// versionTemplate is --version: the version, then the Capella API the commands speak and the
// licence, both of which capellactl's --version carried. The first line is unchanged.
const versionTemplate = "{{.Name}} version {{.Version}}\nCapella Management API " + capellaAPIVersion + "\n" + LicenceLine + "\n"

var enablePrefixMatchingOnce sync.Once

func buildRootCmd() *cobra.Command {
	enablePrefixMatchingOnce.Do(func() { cobra.EnablePrefixMatching = true })
	root := &cobra.Command{
		Use:   "cbctl",
		Short: "Provision Couchbase Server and Capella clusters, buckets, scopes, and collections",
		Long:  "Provision Couchbase Server clusters, buckets, scopes, and collections. Capella operations live under the capella subgroup.",
		// No args → help (match Python no_args_is_help); hardenTree swaps in unknownSubcommand.
		Run:           func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(versionTemplate)
	root.AddCommand(newClusterCmd())
	root.AddCommand(newBucketCmd())
	root.AddCommand(newScopeCmd())
	root.AddCommand(newCollectionCmd())
	root.AddCommand(newKeyspaceCmd())
	root.AddCommand(newImportCmd())
	root.AddCommand(newCapellaCmd())
	root.AddCommand(newConfigureCmd())
	root.SetFlagErrorFunc(flagError)
	root.InitDefaultCompletionCmd() // normally added at Execute; built here so it gets examples too
	writeCompletionToCmdOut(root)
	hardenTree(root)
	installGroupExamples(root)
	return root
}

// removedVerbs maps capellactl command paths (without the leading "cbctl ") that cbctl does not
// provide as commands to the invocation that replaces them. They exit 3 with the replacement as
// the hint instead of falling through to group help.
var removedVerbs = map[string]string{
	"capella completion": "'cbctl completion bash|zsh|fish|powershell'",
}

// hardenTree makes usage mistakes fail loudly. Cobra runs a group's Run with an unknown
// positional (cobra only validates arguments on the root), so "cbctl capella cluster bogus" used
// to print help and exit 0. Every group now answers an unknown subcommand with exit 3 and a
// suggestion, and every argument-count error exits 3.
func hardenTree(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.HasSubCommands() {
			switch {
			case c.RunE == nil:
				// A pure group: no args prints help, anything else is an unknown subcommand.
				c.Run = nil
				c.RunE = unknownSubcommand
				c.Args = cobra.ArbitraryArgs
			case c.Args == nil:
				// A group that also runs itself (configure): it takes flags, not positionals.
				c.Args = func(cmd *cobra.Command, args []string) error {
					if len(args) > 0 {
						return unknownSubcommand(cmd, args)
					}
					return nil
				}
			}
		}
		if c.Args != nil {
			c.Args = usageArgs(c.Args)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// unknownSubcommand is the RunE of every command group. No args prints help (exit 0); an
// unknown or ambiguous subcommand, or a removed capellactl verb, is a usage error (exit 3).
func unknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	word := args[0]
	msg := fmt.Sprintf("unknown command %q for %q", word, cmd.CommandPath())
	key := strings.TrimPrefix(cmd.CommandPath()+" "+word, cmd.Root().Name()+" ")
	if repl, ok := removedVerbs[key]; ok {
		return &CLIError{Code: ExitConfigInvalid, Message: msg, Action: "use " + repl}
	}
	if cands := prefixCandidates(cmd, word); len(cands) > 1 {
		return &CLIError{
			Code:    ExitConfigInvalid,
			Message: fmt.Sprintf("ambiguous command %q for %q", word, cmd.CommandPath()),
			Action:  "did you mean " + strings.Join(cands, ", ") + "?",
		}
	}
	action := fmt.Sprintf("run '%s --help' for the list of commands", cmd.CommandPath())
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	if sugg := cmd.SuggestionsFor(word); len(sugg) > 0 {
		action = "did you mean " + strings.Join(sugg, ", ") + "?"
	}
	return &CLIError{Code: ExitConfigInvalid, Message: msg, Action: action}
}

// prefixCandidates lists the visible subcommands of cmd that start with word. Cobra resolves a
// unique prefix itself (EnablePrefixMatching), so more than one candidate means ambiguity.
func prefixCandidates(cmd *cobra.Command, word string) []string {
	var out []string
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() {
			continue
		}
		if strings.HasPrefix(sub.Name(), word) {
			out = append(out, sub.Name())
		}
	}
	sort.Strings(out)
	return out
}

// usageArgs wraps a positional-argument validator so its errors exit 3.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		err := validate(cmd, args)
		if err == nil {
			return nil
		}
		var cliErr *CLIError
		if errors.As(err, &cliErr) {
			return err
		}
		return &CLIError{
			Code:    ExitConfigInvalid,
			Message: err.Error(),
			Action:  fmt.Sprintf("run '%s --help' for usage", cmd.CommandPath()),
			Err:     err,
		}
	}
}

// flagError maps a flag parse error (unknown flag, bad value, missing value) to exit 3.
func flagError(cmd *cobra.Command, err error) error {
	return &CLIError{
		Code:    ExitConfigInvalid,
		Message: err.Error(),
		Action:  fmt.Sprintf("run '%s --help' for usage", cmd.CommandPath()),
		Err:     err,
	}
}

// cobraUsagePrefixes are the messages cobra returns, untyped, for flag constraints it checks
// after parsing (MarkFlagRequired, flag groups).
var cobraUsagePrefixes = []string{
	"required flag(s) ",
	"if any flags in the group ",
	"at least one of the flags in the group ",
	"none of the others can be; ",
}

// classifyCobraError turns cobra's untyped flag-constraint errors into exit-3 usage errors.
// Every other error is returned unchanged.
func classifyCobraError(err error) error {
	if err == nil {
		return nil
	}
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return err
	}
	msg := err.Error()
	for _, p := range cobraUsagePrefixes {
		if strings.HasPrefix(msg, p) || strings.Contains(msg, p) {
			return &CLIError{Code: ExitConfigInvalid, Message: msg, Err: err,
				Action: "run the command again with --help to see its flags"}
		}
	}
	return err
}

// Execute runs the root command under a signal-aware context.
func Execute() error {
	d := DefaultDeps()
	if wd, err := d.Cwd(); err == nil {
		path, values, names, err := settings.LoadDotEnv(wd, d.LookupEnv)
		if err != nil {
			logging.Warning("ignoring .env: %v", err)
		} else if path != "" {
			if err := settings.ApplyEnv(values); err != nil {
				logging.Warning("applying .env: %v", err)
			} else {
				logging.Debug("loaded .env from %s (%v)", path, names)
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = contextWithDeps(ctx, d)
	root := NewRootCmd(d)
	root.SetIn(d.Stdin)
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	return interruptedError(ctx, classifyCobraError(root.ExecuteContext(ctx)))
}
