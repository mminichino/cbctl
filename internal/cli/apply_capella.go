package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/capellaapply"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/settings"
)

// capellaConnector opens the SDK connection of a Capella import; nil uses the gocb default.
// Tests replace it to run imports without a cluster.
var capellaConnector capellaapply.Connector

// applyCapellaDoc applies a CouchbaseCapella document through capellaapply. The report comes
// back even on failure, so the partial progress is shown; the error is mapped to the exit codes
// of the Capella commands (3 invalid manifest, 4 blocked, 5 timeout, 130 Ctrl-C, 1 otherwise).
func applyCapellaDoc(ctx context.Context, env *applyEnv, doc *manifest.Document) (*manifest.Report, error) {
	client, err := capellaClientFor(ctx, env, doc)
	if err != nil {
		return nil, err
	}
	g, err := readGlobals(env.Cmd)
	if err != nil {
		return nil, err
	}
	d := env.Deps
	stderr := stderrFrom(env.Cmd)
	progress := output.NewProgress(stderr, g.Quiet, ttyCheck(stderr) && output.ProgressAllowed(d.Env))
	defer progress.Close()

	ae := capellaapply.Env{
		Client:         client,
		BaseDir:        env.Set.BaseDir,
		DryRun:         env.DryRun,
		Yes:            env.Yes,
		Printer:        logging.Current(),
		Quiet:          g.Quiet,
		Clock:          clockFactory(),
		Getenv:         d.Env,
		Glyphs:         outGlyphs(stderr),
		Progress:       progress.Line,
		ProgressInline: progress.Inline,
		Warn:           func(what, action string) { logging.Current().Notice(what, action) },
		Connect:        capellaConnector,
	}
	if g.Output != "json" {
		ae.Out = env.Cmd.OutOrStdout()
	}
	if ttyCheck(stdinFrom(env.Cmd)) {
		rt := &runtime{cmd: env.Cmd}
		ae.Confirm = func(question string) (bool, error) { return confirm(rt, question) }
	}

	rep, err := capellaapply.Apply(ctx, ae, doc)
	if rep != nil && g.Output != "json" && len(rep.Passwords) > 0 {
		items := make([]output.NamedSecret, len(rep.Passwords))
		for i, p := range rep.Passwords {
			items[i] = output.NamedSecret{Name: p.Credential, Password: p.Password}
		}
		logging.Current().Renderer().Secrets(nil, "credentials", items)
	}
	return rep, capellaApplyError(err)
}

// capellaClientFor builds the authenticated client for doc: the API key from the environment or
// the profile, the organization from the environment, the profile or the document's own
// organizationId. Each document gets its own client, so documents of different organizations
// work in one run.
func capellaClientFor(ctx context.Context, env *applyEnv, doc *manifest.Document) (*capellaapi.Client, error) {
	cmd := env.Cmd
	g, err := readGlobals(cmd)
	if err != nil {
		return nil, err
	}
	d := env.Deps
	resolved, err := resolveSettings(cmd, settings.Inputs{
		Profile:       g.Profile,
		FileOrgID:     doc.Capella.OrganizationID,
		FileOrgSource: "spec.organizationId of " + doc.Envelope.Metadata.Name,
	})
	if err != nil {
		return nil, Errorf(ExitConfigInvalid, settingsAction, "%v", err)
	}
	for _, w := range resolved.Warnings {
		warnOnce(d, w)
	}
	if !resolved.APIKey.IsSet() {
		return nil, Errorf(ExitConfigInvalid, "Run 'cbctl configure' to save an API key, or set CAPELLA_API_KEY.", "%s", apiKeyMissing)
	}
	opts := []capellaapi.Option{
		capellaapi.WithBaseURL(resolved.BaseURL.Value),
		capellaapi.WithOrgID(resolved.OrgID.Value),
		capellaapi.WithClock(clockFactory()),
		capellaapi.WithUserAgent(cliVersion()),
	}
	if d.HTTPClient != nil {
		opts = append(opts, capellaapi.WithHTTPClient(d.HTTPClient))
	}
	client, err := capellaapi.New(resolved.APIKey.Value, opts...)
	if err != nil {
		return nil, Errorf(ExitConfigInvalid, settingsAction, "%v", err)
	}
	if !resolved.OrgID.IsSet() {
		org, err := discoverOrg(ctx, client)
		if err != nil {
			return nil, err
		}
		client.SetOrgID(org)
	}
	return client, nil
}

// capellaApplyError maps the errors of capellaapply to CLI errors.
func capellaApplyError(err error) error {
	if err == nil {
		return nil
	}
	var verr *manifest.ValidationError
	var blocked *capellaapply.BlockedError
	var exec *capellaapply.ExecError
	var pf *capellaapply.PlanFailure
	var te *engine.WaitTimeout
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.As(err, &verr):
		return err // HandleError prints the issues and exits 3
	case errors.As(err, &blocked):
		return &CLIError{Code: ExitBlocked, Message: blocked.Msg, Action: blocked.Action, Err: err}
	case errors.As(err, &exec):
		code := wrappedExitCode(err)
		if errors.As(err, &te) {
			code = ExitTimeout
		}
		return &CLIError{Code: code, Message: exec.Error(), Action: "Fix the problem named above, then run the command again; steps already done are skipped.", Err: err}
	case errors.As(err, &pf):
		return &CLIError{Code: ExitError, Message: pf.Msg, Action: wrappedErrorAction, Err: err}
	}
	return failure("", fmt.Errorf("applying: %w", err))
}
