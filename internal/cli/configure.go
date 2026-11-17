package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

type configureFlags struct {
	profile     string
	orgID       string
	apiHost     string
	apiKeyStdin bool
	configFile  string
	doImport    bool
	noImport    bool
	yes         bool
	output      string
	format      string
}

// activeProfile is the profile save/remove act on: --profile, then CBCTL_PROFILE (or
// capellactl's CAPELLACTL_PROFILE), then "default".
func activeProfile(cmd *cobra.Command, flag string) string {
	if flag != "" {
		return flag
	}
	if v := envFrom(cmd)("CBCTL_PROFILE"); v != "" {
		return v
	}
	return "default"
}

// checkFormat validates --output-format: cbctl (default) or legacy (capellactl's JSON shapes).
func checkFormat(f *configureFlags) error {
	switch f.format {
	case "", "cbctl", "legacy":
		return nil
	}
	return Errorf(ExitConfigInvalid, "Pass --output-format cbctl or --output-format legacy.", "--output-format must be cbctl or legacy (got %q)", f.format)
}

// keyringFactory, when set, builds the keyring used by configure instead of Deps.Keyring. Tests
// that drive the package-level rootCmd replace it with a MemKeyring.
var keyringFactory func() settings.Keyring

// configureKeyring is the keychain configure stores API keys in: the invocation's Deps.Keyring
// (the OS keychain by default), unless a test replaced keyringFactory.
func configureKeyring(cmd *cobra.Command) settings.Keyring {
	if keyringFactory != nil {
		return keyringFactory()
	}
	if kr := depsFromCmd(cmd).Keyring; kr != nil {
		return kr
	}
	return settings.OSKeyring{}
}

// newConfigureCmd builds `cbctl configure` (also attached under `capella`).
func newConfigureCmd() *cobra.Command {
	var f configureFlags
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Save a Capella API key and organization to a profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureSave(cmd.Context(), cmd, &f)
		},
	}
	cmd.Flags().StringVar(&f.profile, "profile", "", "Profile name (default: default)")
	cmd.Flags().StringVar(&f.orgID, "org-id", "", "Organization ID")
	cmd.Flags().StringVar(&f.apiHost, "api-host", "", "Capella API host or URL")
	cmd.Flags().BoolVar(&f.apiKeyStdin, "api-key-stdin", false, "Read API key from stdin")
	cmd.Flags().StringVar(&f.configFile, "config-file", "", "Path of the capella.yaml this profile uses")
	cmd.Flags().BoolVar(&f.doImport, "import", false, "After saving, discover your projects and clusters and write capella.yaml (asked on a terminal)")
	cmd.Flags().BoolVar(&f.noImport, "no-import", false, "After saving, do not offer to write capella.yaml")
	cmd.Flags().StringVarP(&f.output, "output", "o", "text", "Output format: text|json")
	// capellactl spelled --api-host as --api-base-url; accept it without listing it in help.
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "api-base-url" {
			name = "api-host"
		}
		return pflag.NormalizedName(name)
	})

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved Capella profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureList(cmd, &f)
		},
	}
	listCmd.Flags().StringVarP(&f.output, "output", "o", "text", "Output format: text|json")
	listCmd.Flags().StringVar(&f.format, "output-format", "cbctl", "JSON shape: cbctl|legacy (capellactl's profiles map)")

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show resolved Capella settings for a profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureShow(cmd, &f)
		},
	}
	showCmd.Flags().StringVar(&f.profile, "profile", "", "Profile name")
	showCmd.Flags().StringVarP(&f.output, "output", "o", "text", "Output format: text|json")
	showCmd.Flags().StringVar(&f.format, "output-format", "cbctl", "JSON shape: cbctl|legacy (capellactl's {field: {value, source}})")

	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a Capella profile and its keychain entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureRemove(cmd, &f)
		},
	}
	removeCmd.Flags().StringVar(&f.profile, "profile", "", "Profile name")
	removeCmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip confirmation")

	cmd.AddCommand(listCmd, showCmd, removeCmd, newConfigureMigrateCmd())
	return cmd
}

func runConfigureSave(ctx context.Context, cmd *cobra.Command, f *configureFlags) error {
	d := depsFromCmd(cmd)
	if f.doImport && f.noImport {
		return usageError("use either --import or --no-import", "--import and --no-import are mutually exclusive")
	}
	profile := activeProfile(cmd, f.profile)
	// One reader for every answer: a second bufio.Reader would lose what the first buffered.
	in := bufio.NewReader(stdinFrom(cmd))
	raw, err := readAPIKey(cmd, in, f.apiKeyStdin)
	if err != nil {
		return err
	}
	key, fixes := settings.CleanAPIKey(raw)
	if key == "" {
		return Errorf(ExitConfigInvalid, "pass --api-key-stdin or run in a terminal", "no API key supplied")
	}
	redact.Register(key)
	for _, fix := range fixes {
		fmt.Fprintf(stderrFrom(cmd), "note: %s from the pasted key\n", fix)
	}

	apiHost := ""
	if cmd.Flags().Changed("api-host") {
		apiHost = f.apiHost
	}
	base := ""
	if apiHost != "" {
		base, err = settings.NormalizeBaseURL(apiHost)
		if err != nil {
			return Errorf(ExitConfigInvalid, "Pass --api-host as a host name such as cloudapi.cloud.couchbase.com, or an https:// URL.", "%v", err)
		}
	} else {
		resolved, rerr := resolveSettings(cmd, settings.Inputs{Profile: profile})
		if rerr != nil {
			return rerr
		}
		base = resolved.BaseURL.Value
	}

	client, err := capellaapi.New(key, capellaapi.WithBaseURL(base))
	if err != nil {
		return err
	}
	orgs, err := client.ListOrganizations(ctx)
	if err != nil {
		if errors.Is(err, capellaapi.ErrUnauthorized) {
			return Errorf(ExitError, rejectionChecklist(key, base), "the API key was rejected by Capella (401); nothing was saved")
		}
		if errors.Is(err, capellaapi.ErrForbidden) {
			return Errorf(ExitError, rejectionChecklist(key, base), "the API key was rejected by Capella (403); nothing was saved")
		}
		return err
	}

	orgID := f.orgID
	orgName := ""
	if orgID != "" {
		found := false
		for _, o := range orgs {
			if o.ID == orgID {
				found = true
				orgName = o.Name
				break
			}
		}
		if !found {
			return Errorf(ExitConfigInvalid, "Check --org-id, or use an API key created in that organization.", "organization %s is not visible to this key", orgID)
		}
	} else if len(orgs) == 1 {
		orgID = orgs[0].ID
		orgName = orgs[0].Name
	} else if len(orgs) == 0 {
		return Errorf(ExitConfigInvalid, "Use an organization API key (Capella UI: Organization Settings > API Keys).", "no organizations visible to this API key")
	} else if !f.apiKeyStdin && d.IsTTY(stdinFrom(cmd)) {
		errOut := stderrFrom(cmd)
		for i, o := range orgs {
			fmt.Fprintf(errOut, "%d) %s (%s)\n", i+1, o.Name, o.ID)
		}
		fmt.Fprint(errOut, "Select organization number: ")
		line, _ := in.ReadString('\n')
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &n); err != nil || n < 1 || n > len(orgs) {
			return Errorf(ExitConfigInvalid, "pass --org-id", "invalid organization selection")
		}
		orgID = orgs[n-1].ID
		orgName = orgs[n-1].Name
	} else {
		// With --api-key-stdin, stdin held the key, so there is nothing left to answer a question.
		var b strings.Builder
		for _, o := range orgs {
			fmt.Fprintf(&b, "%s (%s)\n", o.Name, o.ID)
		}
		return Errorf(ExitConfigInvalid, "run 'cbctl configure --org-id ID' with the ID of the one to use",
			"this key sees several organizations; nothing was saved:\n%s", strings.TrimSpace(b.String()))
	}

	home := d.Home
	if home == "" {
		home = settings.HomeDir(d.Env)
	}
	kr := configureKeyring(cmd)
	// Without a keychain the profile (organization, API host) is still saved, as in capellactl:
	// the key is never written to disk, so it must come from CAPELLA_API_KEY.
	keyStored := kr.Set(profile, key) == nil
	undoKey := func() {
		if keyStored {
			_ = kr.Delete(profile) // best effort: the save failed, so nothing must be left half-saved
		}
	}
	store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
	profiles, err := store.Load()
	if err != nil {
		undoKey()
		return err
	}
	if profiles == nil {
		profiles = map[string]settings.Profile{}
	}
	configFile := profiles[profile].ConfigFile
	if cmd.Flags().Changed("config-file") {
		configFile = f.configFile
	}
	profiles[profile] = settings.Profile{OrganizationID: orgID, OrganizationName: orgName, APIBaseURL: base, ConfigFile: configFile}
	if err := store.Save(profiles); err != nil {
		undoKey()
		return err
	}

	if f.output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		doc := map[string]string{
			"profile":        profile,
			"organizationId": orgID,
			"apiBaseUrl":     base,
		}
		if configFile != "" {
			doc["configFile"] = configFile
		}
		return enc.Encode(doc)
	}
	logging.Success("Saved profile %q (organization %s %s)", profile, orgName, orgID)
	if keyStored {
		logging.Success("API key stored in the OS keychain")
	} else {
		logging.Warning("no OS keychain available; set CAPELLA_API_KEY in your environment instead (the key was not saved)")
	}
	interactive := !f.apiKeyStdin && d.IsTTY(stdinFrom(cmd))
	return configureFollowUp(ctx, cmd, f, in, followUp{key: key, base: base, orgID: orgID, orgName: orgName, interactive: interactive})
}

// rejectionChecklist is what to check when Capella rejects the key. It names the key's length and
// last characters, never the key.
func rejectionChecklist(key, base string) string {
	tail := "?"
	if len(key) >= 8 {
		tail = key[len(key)-4:]
	}
	lines := []string{
		fmt.Sprintf("The key received has %d characters and ends in …%s. Check that:", len(key), tail),
		"  1. this machine's public IP is in the API key's allowed IP list (Capella UI → Organization settings →",
		"     API Keys → your key). Find your IP with: curl -s https://checkip.amazonaws.com",
		"  2. you copied the API Key Token (shown once when the key is created), not the API Key ID or name;",
		"  3. the key has not expired or been deleted;",
		fmt.Sprintf("  4. the API URL is right (now: %s; change with --api-host).", base),
	}
	if len(key) >= 1000 {
		lines = append(lines, "  Note: the key is 1000+ characters; the terminal may have cut it. Use --api-key-stdin.")
	}
	lines = append(lines,
		"Test the key without cbctl (prints 200 when it works):",
		fmt.Sprintf(`  curl -s -o /dev/null -w '%%{http_code}\n' -H "Authorization: Bearer $CAPELLA_API_KEY" %s/v4/organizations`, base),
		"Avoid pasting into the hidden prompt: pbpaste | cbctl configure --api-key-stdin")
	return strings.Join(lines, "\n")
}

func readAPIKey(cmd *cobra.Command, in *bufio.Reader, fromStdin bool) (string, error) {
	d := depsFromCmd(cmd)
	src := stdinFrom(cmd)
	errOut := stderrFrom(cmd)
	if fromStdin {
		raw, err := io.ReadAll(in)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	if d.IsTTY(src) {
		fmt.Fprint(errOut, "Capella API key (input hidden): ")
		if f, ok := src.(*os.File); ok {
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(errOut)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return line, nil
	}
	return "", Errorf(ExitConfigInvalid, "pass --api-key-stdin or run in a terminal", "no API key supplied")
}

func runConfigureList(cmd *cobra.Command, f *configureFlags) error {
	if err := checkFormat(f); err != nil {
		return err
	}
	d := depsFromCmd(cmd)
	home := d.Home
	if home == "" {
		home = settings.HomeDir(d.Env)
	}
	store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
	profiles, err := store.Load()
	if err != nil {
		return err
	}
	kr := configureKeyring(cmd)
	type row struct {
		Profile          string `json:"profile"`
		Organization     string `json:"organization"`
		OrganizationName string `json:"organizationName,omitempty"`
		APIHost          string `json:"apiHost"`
		Key              string `json:"key"`
	}
	if f.output == "json" && f.format == "legacy" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(profiles) // capellactl: the profiles map as stored
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := []row{}
	for _, name := range names {
		p := profiles[name]
		keyStatus := "missing"
		if _, err := kr.Get(name); err == nil {
			keyStatus = "stored"
		}
		rows = append(rows, row{Profile: name, Organization: p.OrganizationID, OrganizationName: p.OrganizationName, APIHost: p.APIBaseURL, Key: keyStatus})
	}
	if f.output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "PROFILE  ORGANIZATION  API HOST  KEY\n")
	for _, r := range rows {
		org := r.Organization
		if r.OrganizationName != "" {
			org = r.OrganizationName + " (" + r.Organization + ")" // as capellactl showed it
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", r.Profile, org, r.APIHost, r.Key)
	}
	return nil
}

func runConfigureShow(cmd *cobra.Command, f *configureFlags) error {
	if err := checkFormat(f); err != nil {
		return err
	}
	resolved, err := resolveSettings(cmd, settings.Inputs{
		Profile: f.profile, ConfigFile: flagIfChanged(cmd, "config"),
	})
	if err != nil {
		return err
	}
	type row struct {
		Field  string `json:"field"`
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	src := func(v settings.Value) string {
		if v.Detail != "" {
			return v.Source + " " + v.Detail
		}
		return v.Source
	}
	rows := []row{
		{Field: "profile", Value: resolved.Profile, Source: "--profile / CBCTL_PROFILE / default"},
		{Field: "api key", Value: redact.MaskTail(resolved.APIKey.Value), Source: src(resolved.APIKey)},
		{Field: "organization", Value: orNotSet(resolved.OrgID.Value), Source: src(resolved.OrgID)},
		{Field: "api base URL", Value: resolved.BaseURL.Value, Source: src(resolved.BaseURL)},
		{Field: "config file", Value: orNotSet(resolved.ConfigFile.Value), Source: src(resolved.ConfigFile)},
	}
	if f.output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if f.format == "legacy" {
			legacy := make(map[string]map[string]string, len(rows))
			for _, r := range rows {
				legacy[r.Field] = map[string]string{"value": r.Value, "source": r.Source}
			}
			return enc.Encode(legacy)
		}
		return enc.Encode(rows)
	}
	for _, r := range rows {
		fmt.Fprintf(cmd.OutOrStdout(), "%s | %s | %s\n", r.Field, r.Value, r.Source)
	}
	return nil
}

func runConfigureRemove(cmd *cobra.Command, f *configureFlags) error {
	d := depsFromCmd(cmd)
	profile := activeProfile(cmd, f.profile)
	home := d.Home
	if home == "" {
		home = settings.HomeDir(d.Env)
	}
	store := settings.ProfileStore{Path: settings.ProfilesPath(home)}
	profiles, err := store.Load()
	if err != nil {
		return err
	}
	kr := configureKeyring(cmd)
	if _, ok := profiles[profile]; !ok {
		// A keychain entry left without its profile is still removed.
		if _, err := kr.Get(profile); err == nil && kr.Delete(profile) == nil {
			logging.Success("Removed the keychain entry of profile %q", profile)
			return nil
		}
		logging.Info("profile %q: nothing to remove", profile)
		return nil
	}
	if !f.yes {
		in := stdinFrom(cmd)
		if !d.IsTTY(in) {
			return noApprovalError(cmd)
		}
		fmt.Fprintf(stderrFrom(cmd), "Remove profile %q? [y/N] ", profile)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes" {
			return nil
		}
	}
	_, getErr := kr.Get(profile)
	hadKey := getErr == nil && kr.Delete(profile) == nil
	delete(profiles, profile)
	if err := store.Save(profiles); err != nil {
		return err
	}
	if hadKey {
		logging.Success("Removed profile %q (keychain entry deleted)", profile)
		return nil
	}
	logging.Success("Removed profile %q", profile)
	return nil
}

func orNotSet(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
