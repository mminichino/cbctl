package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
)

// apiKeyMissing is shown when no Capella API key can be found.
const apiKeyMissing = "Capella API key not set; use --token, set CAPELLA_API_KEY (or put it in .env), or run 'cbctl configure'"

// defaultTimeout is how long async Capella operations are waited for.
const defaultTimeout = 60 * time.Minute

// clockFactory builds the clock used for waits and API retries. Tests replace it with a fake.
var clockFactory = func() engine.Clock { return engine.RealClock{} }

// restoreLogging puts the log writers back after a command that redirected them (JSON mode).
var restoreLogging func()

func init() {
	// OnFinalize runs after every command, including failed ones.
	cobra.OnFinalize(func() {
		if restoreLogging != nil {
			restoreLogging()
			restoreLogging = nil
		}
	})
}

// globalFlags are the persistent flags of the capella command group.
type globalFlags struct {
	Output      string
	Yes         bool
	Quiet       bool
	NoWait      bool
	Timeout     time.Duration
	LogLevel    string
	LogLevelSet bool
	Profile     string
}

// addCapellaGlobalFlags registers the persistent flags on the capella parent command. Their
// shorthands (-o, -y, -q) are free of the legacy leaf shorthands (-u, -p, -b, -s).
func addCapellaGlobalFlags(cmd *cobra.Command) {
	pf := cmd.PersistentFlags()
	pf.StringP("output", "o", "text", "Output format: text|json")
	pf.BoolP("yes", "y", false, "Skip confirmation prompts")
	pf.Bool("auto-approve", false, "Alias for --yes")
	pf.BoolP("quiet", "q", false, "Only log warnings and errors, no progress output (env CBCTL_QUIET=1)")
	pf.Bool("no-wait", false, "Return once the API accepts an asynchronous operation")
	t := capellaTimeout(defaultTimeout)
	pf.Var(&t, "timeout", "Maximum time to wait for asynchronous operations (for example 90m, 2h, or 90 for seconds; at most 24h)")
	pf.String("log-level", "info", "Log level: error|warn|info|debug")
	pf.String("profile", "", "Configuration profile name")
	pf.StringP("config", "c", "", "Path of capella.yaml (env CBCTL_CONFIG; default ./capella.yaml)")
}

// maxTimeout bounds --timeout; Capella operations that take longer are not worth waiting for.
const maxTimeout = 24 * time.Hour

// capellaTimeout is the --timeout value. It takes Go durations with case-insensitive units
// ("90S", "1H30M") and a bare number of seconds ("90"), capellactl style.
type capellaTimeout time.Duration

// Set implements pflag.Value.
func (t *capellaTimeout) Set(s string) error {
	v := strings.ToLower(strings.TrimSpace(s))
	if v != "" && strings.Trim(v, "0123456789") == "" {
		v += "s"
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("%q is not a duration; use for example 90m, 2h or 90 (seconds)", s)
	}
	if d <= 0 {
		return fmt.Errorf("must be positive (got %q)", s)
	}
	if d > maxTimeout {
		return fmt.Errorf("must be at most 24h (got %q)", s)
	}
	*t = capellaTimeout(d)
	return nil
}

// String implements pflag.Value; the text parses with time.ParseDuration (pflag.GetDuration).
func (t *capellaTimeout) String() string { return time.Duration(*t).String() }

// Type implements pflag.Value. It is "duration" so GetDuration keeps working.
func (t *capellaTimeout) Type() string { return "duration" }

// envQuiet reads CBCTL_QUIET: 1/true/yes/on mean quiet; empty, 0/false/no/off mean not quiet;
// anything else warns and means not quiet.
func envQuiet(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "", "0", "false", "no", "off":
		return false
	}
	logging.Warning("CBCTL_QUIET=%q is not one of 1, true, yes, on, 0, false, no, off; not quiet", v)
	return false
}

// readGlobals reads and validates the persistent flags as seen by cmd. A flag the command does
// not have keeps its default, so it also works on commands outside the capella group.
func readGlobals(cmd *cobra.Command) (globalFlags, error) {
	g := globalFlags{Output: "text", Timeout: defaultTimeout, LogLevel: "info"}
	fs := cmd.Flags()
	if v, err := fs.GetString("output"); err == nil {
		g.Output = v
	}
	if v, err := fs.GetBool("yes"); err == nil {
		g.Yes = v
	}
	if v, err := fs.GetBool("auto-approve"); err == nil && v {
		g.Yes = true
	}
	if v, err := fs.GetBool("quiet"); err == nil {
		g.Quiet = v
	}
	g.Quiet = g.Quiet || envQuiet(envFrom(cmd)("CBCTL_QUIET"))
	if v, err := fs.GetBool("no-wait"); err == nil {
		g.NoWait = v
	}
	if v, err := fs.GetDuration("timeout"); err == nil {
		g.Timeout = v
	}
	if v, err := fs.GetString("log-level"); err == nil {
		g.LogLevel = v
		g.LogLevelSet = fs.Changed("log-level")
	}
	if v, err := fs.GetString("profile"); err == nil {
		g.Profile = v
	}

	switch strings.ToLower(g.Output) {
	case "text", "json":
		g.Output = strings.ToLower(g.Output)
	default:
		return g, Errorf(ExitConfigInvalid, "use -o text or -o json", "invalid output format %q", g.Output)
	}
	if _, err := logging.ParseLevel(g.LogLevel); err != nil {
		return g, Errorf(ExitConfigInvalid, "use one of error, warn, info, debug", "invalid log level %q", g.LogLevel)
	}
	if g.Timeout <= 0 {
		return g, Errorf(ExitConfigInvalid, "pass a duration such as 90m or 2h", "--timeout must be positive")
	}
	return g, nil
}

// logLevel returns the effective level: an explicit --log-level wins, otherwise --quiet means warn.
func (g globalFlags) logLevel() logging.Level {
	if !g.LogLevelSet && g.Quiet {
		return logging.LevelWarn
	}
	lvl, _ := logging.ParseLevel(g.LogLevel) // validated by readGlobals
	return lvl
}

// capellaPersistentPreRun applies --log-level/--quiet to every command under capella, legacy
// leaves included, and rejects invalid global flag values early. It never touches the log
// writers, so legacy leaves behave exactly as before.
func capellaPersistentPreRun(cmd *cobra.Command, _ []string) error {
	g, err := readGlobals(cmd)
	if err != nil {
		return err
	}
	logging.SetLevel(g.logLevel())
	return nil
}

// apiFlags are the per-command Capella API flags of the new commands.
type apiFlags struct {
	token   string
	orgID   string
	apiHost string
}

// addAPIFlags adds --token, --org-id and --api-host to cmd and returns the value holder. Unlike
// the legacy leaves, --api-host has no default, so an untouched flag never shadows env or profile.
func addAPIFlags(cmd *cobra.Command) *apiFlags {
	f := &apiFlags{}
	cmd.Flags().StringVar(&f.token, "token", "", "Capella API v4 token")
	cmd.Flags().StringVar(&f.orgID, "org-id", "", "Capella organization ID")
	cmd.Flags().StringVar(&f.apiHost, "api-host", "", "Capella API host or URL")
	return f
}

// runtime is everything a Capella command needs: an authenticated client bound to an
// organization, the output renderer and the global behaviour flags.
type runtime struct {
	Ctx      context.Context
	Client   *capellaapi.Client
	Settings *settings.Settings
	Out      Renderer
	JSON     bool
	Yes      bool
	Quiet    bool
	NoWait   bool
	Timeout  time.Duration
	Clock    engine.Clock

	cmd *cobra.Command
}

// newRuntime builds the runtime for cmd: it applies the global flags, resolves settings, creates
// the API client and resolves the organization (auto-discovering it when exactly one is visible).
func newRuntime(cmd *cobra.Command, f *apiFlags) (*runtime, error) {
	return newRuntimeWith(cmd, f, runtimeExtras{})
}

// runtimeExtras are the inputs only the declarative commands have: what the config file declares.
type runtimeExtras struct {
	// FileOrgID is the organizationId of capella.yaml, the last resort for the organization.
	FileOrgID     string
	FileOrgSource string
}

// newRuntimeWith is newRuntime with the organization declared in the config file folded into the
// precedence (flag, environment, profile, file).
func newRuntimeWith(cmd *cobra.Command, f *apiFlags, extra runtimeExtras) (*runtime, error) {
	g, err := readGlobals(cmd)
	if err != nil {
		return nil, err
	}
	logging.SetLevel(g.logLevel())
	jsonMode := g.Output == "json"
	if jsonMode && restoreLogging == nil {
		// stdout carries only the JSON document; info logs move to stderr until the command ends.
		prevOut, _ := logging.Outputs()
		logging.SetOutputs(cmd.ErrOrStderr(), nil)
		restoreLogging = func() { logging.SetOutputs(prevOut, nil) }
	}
	if f == nil {
		f = &apiFlags{}
	}

	d := depsFromCmd(cmd)
	resolved, err := resolveSettings(cmd, settings.Inputs{
		Profile:       g.Profile,
		Token:         f.token,
		OrgID:         f.orgID,
		APIHost:       flagIfChanged(cmd, "api-host"),
		ConfigFile:    flagIfChanged(cmd, "config"),
		FileOrgID:     extra.FileOrgID,
		FileOrgSource: extra.FileOrgSource,
	})
	if err != nil {
		return nil, Errorf(ExitConfigInvalid, "", "%v", err)
	}
	for _, w := range resolved.Warnings {
		warnOnce(d, w)
	}
	if !resolved.APIKey.IsSet() {
		msg := apiKeyMissing
		if resolved.Profile != "" && resolved.Profile != "default" {
			msg += fmt.Sprintf(" (profile %q)", resolved.Profile)
		}
		return nil, Errorf(ExitConfigInvalid, "", "%s", msg)
	}

	if g.NoWait && cmd.Flags().Changed("timeout") {
		logging.Debug("--timeout is ignored with --no-wait")
	}

	clk := clockFactory()
	client, err := capellaapi.New(resolved.APIKey.Value,
		capellaapi.WithBaseURL(resolved.BaseURL.Value),
		capellaapi.WithOrgID(resolved.OrgID.Value),
		capellaapi.WithClock(clk),
		capellaapi.WithUserAgent(cliVersion()),
	)
	if err != nil {
		return nil, Errorf(ExitConfigInvalid, "", "%v", err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		// cobra's Execute always sets a context; nil means a caller invoked RunE directly, and a
		// Background fallback would silently drop Ctrl-C and deadlines.
		return nil, Errorf(ExitError, "", "internal error: %s has no context", cmd.CommandPath())
	}
	if !resolved.OrgID.IsSet() {
		org, err := discoverOrg(ctx, client)
		if err != nil {
			return nil, err
		}
		client.SetOrgID(org)
		logging.Debug("using organization %s", org)
	}

	return &runtime{
		Ctx:      ctx,
		Client:   client,
		Settings: resolved,
		Out:      newRenderer(cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonMode, g.Quiet),
		JSON:     jsonMode,
		Yes:      g.Yes,
		Quiet:    g.Quiet,
		NoWait:   g.NoWait,
		Timeout:  g.Timeout,
		Clock:    clk,
		cmd:      cmd,
	}, nil
}

// discoverOrg picks the organization when none is configured: the only one the key can see.
func discoverOrg(ctx context.Context, c *capellaapi.Client) (string, error) {
	orgs, err := c.ListOrganizations(ctx)
	if err != nil {
		return "", fmt.Errorf("listing organizations: %w", err)
	}
	switch len(orgs) {
	case 0:
		return "", Errorf(ExitConfigInvalid, "check that the API key belongs to an organization", "no organizations visible to this API key")
	case 1:
		return orgs[0].ID, nil
	}
	names := make([]string, len(orgs))
	for i, o := range orgs {
		names[i] = fmt.Sprintf("%s (%s)", o.Name, o.ID)
	}
	return "", Errorf(ExitConfigInvalid, "pass --org-id or save one with 'cbctl configure'",
		"several organizations: %s", strings.Join(names, ", "))
}

// cliVersion returns the bare version for the User-Agent ("" when unset).
func cliVersion() string {
	if fields := strings.Fields(versionString); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
