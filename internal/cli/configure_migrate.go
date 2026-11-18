package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/settings"
)

type migrateFlags struct {
	from         string
	profiles     []string
	dryRun       bool
	force        bool
	removeLegacy bool
	output       string
}

// newConfigureMigrateCmd builds `cbctl configure migrate` (hidden alias import-capellactl).
func newConfigureMigrateCmd() *cobra.Command {
	var f migrateFlags
	cmd := &cobra.Command{
		Use:     "migrate",
		Aliases: []string{"import-capellactl"},
		Short:   "Copy capellactl profiles and keychain API keys into cbctl",
		Long: `Copy capellactl's profiles (profiles.yaml in its config directory) and the API keys it stored
in the OS keychain (service "capellactl") into cbctl. Profiles cbctl already has with the same
settings are skipped; different ones are conflicts (exit 4) unless --force. API keys are never
printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureMigrate(cmd, &f)
		},
	}
	cmd.Flags().StringVar(&f.from, "from", "", "capellactl config directory (default: CAPELLACTL_HOME or ~/.config/capellactl)")
	cmd.Flags().StringSliceVar(&f.profiles, "profile", nil, "Only migrate this profile (repeatable; default: all)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Report what would be copied; write nothing")
	cmd.Flags().BoolVar(&f.force, "force", false, "Overwrite cbctl profiles and keys that differ")
	cmd.Flags().BoolVar(&f.removeLegacy, "remove-legacy", false, "Delete each fully migrated profile and key from capellactl")
	cmd.Flags().StringVarP(&f.output, "output", "o", "text", "Output format: text|json")
	return cmd
}

// legacyHomeFor is capellactl's config directory for d: the one the invocation reads profiles
// from, or capellactl's default location when automatic fallback is off (explicit cbctl home).
func legacyHomeFor(d Deps) string {
	if d.LegacyHome != "" {
		return d.LegacyHome
	}
	return settings.LegacyHome(d.Env)
}

// legacyKeyringFor is capellactl's keychain for d: the injected one, or the OS keychain under
// the capellactl service when cbctl uses the OS keychain.
func legacyKeyringFor(d Deps) settings.Keyring {
	if d.LegacyKeyring != nil {
		return d.LegacyKeyring
	}
	if k, ok := d.Keyring.(settings.OSKeyring); ok && k.Service == "" {
		return settings.OSKeyring{Service: settings.LegacyKeyringService}
	}
	return nil
}

func runConfigureMigrate(cmd *cobra.Command, f *migrateFlags) error {
	if f.output != "text" && f.output != "json" {
		return Errorf(ExitConfigInvalid, "Pass -o text or -o json.", "--output must be text or json (got %q)", f.output)
	}
	if f.removeLegacy && f.dryRun {
		return Errorf(ExitConfigInvalid, "Run with --dry-run first, then again with --remove-legacy.", "--remove-legacy cannot be combined with --dry-run")
	}
	d := depsFromCmd(cmd)
	from := f.from
	if from == "" {
		from = legacyHomeFor(d)
	}
	rep, err := settings.MigrateLegacy(cmd.Context(), settings.MigrateOptions{
		FromDir: from, ToDir: d.Home, Profiles: f.profiles,
		DryRun: f.dryRun, Force: f.force, RemoveLegacy: f.removeLegacy,
		Keyring: d.Keyring, LegacyKeyring: legacyKeyringFor(d),
	})
	if err != nil {
		var pe *settings.LegacyProfileError
		if errors.As(err, &pe) || errors.Is(err, settings.ErrUnknownLegacyProfile) {
			return Errorf(ExitConfigInvalid, "Pass --profile with a profile that capellactl has (cbctl configure migrate --dry-run lists them).", "%v", err)
		}
		return err
	}
	for _, w := range rep.Warnings {
		warnOnce(d, w)
	}
	if f.output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		printMigration(rep)
	}
	if len(rep.Conflicts) > 0 {
		return &CLIError{Code: ExitBlocked, Message: fmt.Sprintf("%d profile(s) conflict with cbctl's; nothing was written for them", len(rep.Conflicts)),
			Action: "Compare with 'cbctl configure show --profile NAME', then re-run with --force to overwrite."}
	}
	return nil
}

func printMigration(rep *settings.MigrationReport) {
	if rep.Nothing() {
		logging.Info("nothing to migrate from %s", rep.From)
		return
	}
	imported := logging.Success
	verb := "imported"
	if rep.DryRun {
		verb = "would import"
		imported = logging.Info // a dry run changes nothing, so it is not a success to report
	}
	missing := map[string]bool{}
	for _, n := range rep.KeyMissing {
		missing[n] = true
	}
	for _, n := range rep.Imported {
		note := ""
		if missing[n] {
			note = fmt.Sprintf(" (without its API key; run 'cbctl configure --profile %s --api-key-stdin')", n)
		}
		imported("%s profile %s%s", verb, n, note)
	}
	for _, n := range rep.Skipped {
		logging.Info("skipped profile %s (already in cbctl)", n)
	}
	names := make([]string, 0, len(rep.Conflicts))
	for n := range rep.Conflicts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		logging.Warning("conflict: profile %s: %s", n, rep.Conflicts[n])
	}
	if len(rep.Removed) > 0 {
		logging.Success("removed from capellactl: %s", strings.Join(rep.Removed, ", "))
	}
}
