package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
)

// WarningMode says how much of capella.yaml's warnings a command prints.
type WarningMode int

// Warning modes.
const (
	// WarnNone prints no warnings.
	WarnNone WarningMode = iota
	// WarnCount prints how many there are (plan, apply).
	WarnCount
	// WarnAll prints every warning with its next step (validate).
	WarnAll
)

// envRuntime is a runtime plus the loaded capella.yaml, for the commands that reconcile the
// environment the file describes.
type envRuntime struct {
	*runtime
	Cfg  *capellafile.Config
	Disc capellafile.Discovery
	// RC is the engine's run context; commands set its switches (PowerOn, RotatePasswords, …)
	// before planning.
	RC *plan.RunContext
	// TreeOptions are passed to resources.BuildTreeWith.
	TreeOptions resources.Options
	// Ephemeral marks a throw-away capella.yaml (direct cluster create): messages do not name
	// the file, because the user never sees it.
	Ephemeral bool
}

// notFoundError is the failure when no capella.yaml can be found.
func notFoundError(path string) error {
	return Errorf(ExitConfigInvalid, "run 'cbctl capella sync' (or 'cbctl capella init') to create it", "%s not found", path)
}

// discoverConfig finds the config file for cmd.
func discoverConfig(cmd *cobra.Command) (capellafile.Discovery, error) {
	g, err := readGlobals(cmd)
	if err != nil {
		return capellafile.Discovery{}, err
	}
	cwd, err := cwdFrom(cmd)
	if err != nil {
		return capellafile.Discovery{}, fmt.Errorf("finding the working directory: %w", err)
	}
	d := depsFromCmd(cmd)
	env := d.Env
	profile, err := settings.LoadProfileWithLegacy(g.Profile, env, d.Home, d.LegacyHome)
	if err != nil {
		return capellafile.Discovery{}, Errorf(ExitConfigInvalid, "Fix the profile with 'cbctl configure', or pass --profile with another one.", "%v", err)
	}
	disc, err := capellafile.Discover(flagIfChanged(cmd, "config"), env, profile, cwd)
	if err != nil {
		var ce *capellafile.ConfigError
		if errors.As(err, &ce) {
			return disc, err
		}
		return disc, Errorf(ExitConfigInvalid, "pass --config PATH", "%v", err)
	}
	return disc, nil
}

// loadConfig discovers and loads capella.yaml, printing its warnings as the mode says.
func loadConfig(cmd *cobra.Command, mode WarningMode) (*capellafile.Config, capellafile.Discovery, error) {
	disc, err := discoverConfig(cmd)
	if err != nil {
		return nil, disc, err
	}
	if !disc.Exists {
		return nil, disc, notFoundError(displayPathWith(disc.Path, cmd))
	}
	cfg, err := capellafile.Load(disc.Path, envFrom(cmd))
	if err != nil {
		return nil, disc, err
	}
	notices := capellafile.Warnings(cfg)
	switch {
	case mode == WarnAll:
		for _, n := range notices {
			printNotice(cmd.ErrOrStderr(), n.What, n.Action)
		}
	case mode == WarnCount && len(notices) > 0:
		word := "warnings"
		if len(notices) == 1 {
			word = "warning"
		}
		writeLabeled(cmd.ErrOrStderr(), output.KindWarning, "%d %s; run 'cbctl capella validate' to see them", len(notices), word)
	}
	return cfg, disc, nil
}

// displayPath shows a path relative to the working directory when it is below it.
func displayPath(p string) string {
	return displayPathWith(p, nil)
}

// displayPathWith is displayPath using cmd's Deps when available.
func displayPathWith(p string, cmd *cobra.Command) string {
	wd, err := cwdFrom(cmd)
	if err != nil {
		return p
	}
	// Clean both sides: a working directory spelled with ".." (for example from a TMPDIR set
	// through a relative path) would otherwise never match the cleaned file path.
	wd = filepath.Clean(wd)
	if rel := strings.TrimPrefix(filepath.Clean(p), wd+string(os.PathSeparator)); rel != filepath.Clean(p) {
		return rel
	}
	return p
}

// printNotice writes "[warning] - what" and its "  → action" line.
func printNotice(w io.Writer, what, action string) {
	writeNotice(w, what, action)
}

// newEnvRuntime builds the runtime for a declarative command: it finds and loads capella.yaml
// (a bad file is exit 3), resolves settings with the file's organizationId as the last resort,
// and prepares the engine's run context.
func newEnvRuntime(cmd *cobra.Command, af *apiFlags, mode WarningMode) (*envRuntime, error) {
	cfg, disc, err := loadConfig(cmd, mode)
	if err != nil {
		return nil, err
	}
	rt, err := newRuntimeWith(cmd, af, runtimeExtras{FileOrgID: cfg.OrgID, FileOrgSource: cfg.Source})
	if err != nil {
		return nil, err
	}
	e := &envRuntime{runtime: rt, Cfg: cfg, Disc: disc}
	e.RC = newRunContext(rt, cfg)
	return e, nil
}

// newRunContext connects the runtime's client, clock and output to the engine.
func newRunContext(rt *runtime, cfg *capellafile.Config) *plan.RunContext {
	rc := plan.NewRunContext(rt.Client, cfg)
	rc.Clock = rt.Clock
	rc.Timeout = rt.Timeout
	rc.NoWait = rt.NoWait
	rc.Env = envFrom(rt.cmd)
	rc.Glyphs = outGlyphs(rt.errOut())
	pw := output.NewProgress(rt.errOut(), rt.Quiet, ttyCheck(rt.errOut()) && output.ProgressAllowed(rc.Env))
	rc.Progress = pw.Line
	rc.ProgressInline = pw.Inline
	rc.Warn = func(what, action string) { printNotice(rt.errOut(), what, action) }
	rc.Prompt = &typedPrompter{rt: rt}
	return rc
}

var (
	pinnedMissing = regexp.MustCompile(`^(project|cluster) \S+ not found$`)
)

// planError turns planner failures into CLI errors.
func planError(err error) error {
	var te *plan.TargetError
	if errors.As(err, &te) {
		return Errorf(ExitConfigInvalid, "pass --target KIND/NAME, for example cluster/nova-dev-01", "%s", te.Msg)
	}
	var pe *plan.PlanError
	if errors.As(err, &pe) {
		action := "Fix the problem named above, then run the command again."
		if pinnedMissing.MatchString(pe.Msg) {
			action = "remove the id from capella.yaml or run 'cbctl capella sync', then run the command again"
		}
		return Errorf(ExitError, action, "%s", pe.Msg)
	}
	return err
}

// makePlan diffs capella.yaml against Capella. PlanError becomes exit 1, a bad --target exit 3.
func makePlan(ctx context.Context, e *envRuntime, opts plan.Options) (*plan.Plan, error) {
	p, err := plan.BuildPlan(ctx, e.RC, resources.BuildTreeWith(e.TreeOptions), opts)
	if err != nil {
		return nil, planError(err)
	}
	return p, nil
}

// planHeader is the first line of plan output: the projects of the file and the organization.
func planHeader(cfg *capellafile.Config, orgID string) string {
	names := cfg.ProjectNames()
	label := "Projects " + strings.Join(names, ", ")
	if len(names) == 1 {
		label = "Project " + names[0]
	}
	return fmt.Sprintf("%s %s organization %s", label, stdoutGlyphs().Dot, orgID)
}

func (e *envRuntime) header() string { return planHeader(e.Cfg, e.Client.OrgID()) }

// projectsLabel is "project X" or "projects A, B".
func projectsLabel(cfg *capellafile.Config) string {
	names := cfg.ProjectNames()
	if len(names) == 1 {
		return "project " + names[0]
	}
	return "projects " + strings.Join(names, ", ")
}

// note prints a one-line "[info]" note on stderr unless --quiet.
func (rt *runtime) note(format string, a ...any) {
	rt.labeled(output.KindInfo, format, a...)
}

// noteOK prints a one-line "[ok]" result on stderr unless --quiet. Workflow results stay on
// stderr, where their notes always were, so stdout keeps only the plan and the summary table.
func (rt *runtime) noteOK(format string, a ...any) {
	rt.labeled(output.KindOK, format, a...)
}

func (rt *runtime) labeled(kind, format string, a ...any) {
	if rt.Quiet {
		return
	}
	writeLabeled(rt.errOut(), kind, format, a...)
}

// ---- confirmations ---------------------------------------------------------------------------

// terminalIn returns the input stream and reports whether a person can answer on it.
func (rt *runtime) terminalIn() (io.Reader, bool) {
	in := stdinFrom(rt.cmd)
	return in, ttyCheck(in)
}

// needTerminal fails with ExitBlocked unless --yes was given or a terminal is attached.
func needTerminal(rt *runtime) error {
	if rt.Yes {
		return nil
	}
	if _, ok := rt.terminalIn(); !ok {
		return noApprovalError(rt.cmd)
	}
	return nil
}

// confirmTypedFunc asks for a typed answer judged by check; notes are printed first. --yes answers
// true; without a terminal it fails with ExitBlocked.
func confirmTypedFunc(rt *runtime, prompt, notes string, check func(typed string) bool) (bool, error) {
	if rt.Yes {
		return true, nil
	}
	in, ok := rt.terminalIn()
	if !ok {
		return false, noApprovalError(rt.cmd)
	}
	out := rt.errOut()
	if notes != "" {
		writeProse(out, "%s", notes)
	}
	if p := prompterFor(rt); p != nil {
		typed, err := p.Ask(prompt, "")
		if err != nil {
			return false, err
		}
		return check(strings.TrimSpace(typed)), nil
	}
	writePrompt(out, "%s: ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return check(strings.TrimSpace(line)), nil
}

// typedPrompter lets the engine ask for typed-name confirmations.
type typedPrompter struct{ rt *runtime }

// ConfirmTyped implements plan.Prompter.
func (p *typedPrompter) ConfirmTyped(prompt, expected string) (bool, error) {
	return confirmTyped(p.rt, prompt, expected)
}

// ---- error enrichment ------------------------------------------------------------------------

const serverErrorHint = "Capella returned an internal error for this %s. This is often caused by a value Capella does not " +
	"support (for example a region, size or disk not offered for the provider) rather than an outage: check the " +
	"values in the plan above and try again; if they are correct, retry later or contact Capella support."

// failureAction builds the action line of a failed apply from the Capella error.
func failureAction(failed *plan.Action, err error, rerun string) string {
	var steps []string
	var api *capellaapi.APIError
	hint := ""
	if errors.As(err, &api) {
		hint = api.Hint
		if api.Status >= 500 && (api.Method == "POST" || api.Method == "PUT") {
			hint = fmt.Sprintf(serverErrorHint, failed.Kind)
		}
	}
	if hint != "" && !strings.HasPrefix(hint, "requires:") {
		steps = append(steps, strings.TrimRight(hint, ".")+".")
	}
	if errors.Is(err, capellaapi.ErrForbidden) && api != nil {
		if roles := capellaapi.RequiredRoles(api.Method, api.Path); roles != "" {
			steps = append(steps, fmt.Sprintf("Ask an Organization Owner to give the API key the %s role.", roles))
		}
	}
	if len(steps) > 0 {
		steps = append(steps, rerun)
	} else {
		steps = append(steps, "Fix the problem, then "+strings.ToLower(rerun[:1])+rerun[1:])
	}
	return strings.Join(steps, " ")
}
