package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/secrets"
	"github.com/mminichino/cbctl/internal/settings"
)

// Doctor item kinds.
const (
	doctorLegacyEnv       = "legacy-env"
	doctorLegacyProfile   = "legacy-profile"
	doctorLegacyKeychain  = "legacy-keychain"
	doctorLegacyCredsDir  = "legacy-credentials-dir"
	doctorCredsNotIgnored = "credentials-not-ignored"
	doctorAPIVersion      = "apiversion-capellactl"
)

// DoctorItem is one thing left from capellactl, with the action that resolves it.
type DoctorItem struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Action string `json:"action"`
}

// DoctorReport is the outcome of `capella doctor`.
type DoctorReport struct {
	Items []DoctorItem `json:"items"`
}

func newCapellaDoctorCmd() *cobra.Command {
	var detailed bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "List settings, profiles, keys and files left from capellactl (offline, read-only)",
		Long: `Inspect this machine and directory for capellactl leftovers: legacy environment variables,
profiles and keychain entries not yet copied to cbctl, a ./.capellactl/credentials directory,
credential files git would commit, and capella.yaml files still on apiVersion capellactl/v1.
Nothing is changed and Capella is not contacted. With --detailed-exitcode, findings exit 4.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := readGlobals(cmd)
			if err != nil {
				return err
			}
			rep := runDoctor(cmd)
			if g.Output == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			} else {
				out := cmd.OutOrStdout()
				if len(rep.Items) == 0 {
					logging.Success("No capellactl leftovers found.")
				}
				arrow := outGlyphs(out).Arrow
				for _, it := range rep.Items {
					writeProse(out, "%s: %s\n  %s %s", it.Kind, it.Detail, arrow, it.Action)
				}
			}
			if detailed && len(rep.Items) > 0 {
				return &CLIError{Code: ExitBlocked}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "Exit 4 when anything is found")
	return cmd
}

// runDoctor collects the report. Every probe is best effort: a probe that cannot run (no git,
// unreadable keychain) reports nothing rather than failing the command.
func runDoctor(cmd *cobra.Command) *DoctorReport {
	d := depsFromCmd(cmd)
	rep := &DoctorReport{Items: []DoctorItem{}}
	add := func(kind, detail, action string) {
		rep.Items = append(rep.Items, DoctorItem{Kind: kind, Detail: detail, Action: action})
	}

	for _, legacy := range settings.LegacyEnvInUse(d.Env) {
		add(doctorLegacyEnv, fmt.Sprintf("%s is set", legacy),
			fmt.Sprintf("Set %s instead and unset %s.", settings.LegacyEnv[legacy], legacy))
	}

	legacyHome := legacyHomeFor(d)
	if filepath.Clean(legacyHome) != filepath.Clean(d.Home) {
		legacy, _, err := settings.LoadLegacyProfiles(legacyHome)
		if err != nil {
			add(doctorLegacyProfile, err.Error(), "Fix or remove capellactl's profiles.yaml.")
		}
		current, cerr := settings.ProfileStore{Path: settings.ProfilesPath(d.Home)}.Load()
		if cerr != nil {
			current = map[string]settings.Profile{}
		}
		lkr := legacyKeyringFor(d)
		for _, n := range profileNames(legacy) {
			if cur, ok := current[n]; !ok || cur != legacy[n] {
				add(doctorLegacyProfile, fmt.Sprintf("profile %s is only in %s", n, settings.ProfilesPath(legacyHome)),
					fmt.Sprintf("Run 'cbctl configure migrate --profile %s'.", n))
			}
			if lkr == nil {
				continue
			}
			if k, err := lkr.Get(n); err == nil && k != "" {
				if ck, cerr := d.Keyring.Get(n); cerr != nil || ck == "" {
					add(doctorLegacyKeychain, fmt.Sprintf("the API key of profile %s is only in the capellactl keychain entry", n),
						fmt.Sprintf("Run 'cbctl configure migrate --profile %s'.", n))
				}
			}
		}
	}

	cwd, err := d.Cwd()
	if err != nil {
		return rep
	}
	base := cwd
	var cfgPath string
	if disc, derr := discoverConfig(cmd); derr == nil && disc.Exists {
		cfgPath = disc.Path
		base = filepath.Dir(disc.Path)
	}
	legacyDir := filepath.Join(base, filepath.FromSlash(secrets.LegacyDirName))
	if st, err := os.Stat(legacyDir); err == nil && st.IsDir() {
		add(doctorLegacyCredsDir, fmt.Sprintf("%s holds capellactl's generated passwords", legacyDir),
			fmt.Sprintf("Move its files to %s (cbctl uses the legacy directory only while the new one does not exist).",
				filepath.Join(base, filepath.FromSlash(secrets.DefaultDirName))))
	}
	configured := ""
	if cfgPath != "" {
		configured = rawString(cfgPath, "credentialsOutput")
	}
	dir, _, _ := secrets.ResolveCredentialsDir(configured, base)
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		if what, _, bad := secrets.GitWarning(dir); bad {
			add(doctorCredsNotIgnored, what, "Add .cbctl/ (and .capellactl/) to .gitignore so saved passwords are never committed.")
		}
	}
	if cfgPath != "" && rawString(cfgPath, "apiVersion") == capellafile.LegacyAPIVersion {
		add(doctorAPIVersion, fmt.Sprintf("%s uses apiVersion %s", displayPath(cfgPath), capellafile.LegacyAPIVersion),
			"Change it to 'apiVersion: cbctl/v1' (both are read the same way; capellactl/v1 is kept for rollback).")
	}
	return rep
}

// rawString reads one top-level string key of a YAML file; "" when absent or unreadable.
func rawString(path, key string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc map[string]any
	if yaml.Unmarshal(raw, &doc) != nil {
		return ""
	}
	s, _ := doc[key].(string)
	return strings.TrimSpace(s)
}

func profileNames(m map[string]settings.Profile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
