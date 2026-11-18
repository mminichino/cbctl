package capellaapply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/resources"
)

// Printer is the part of the output printer the apply writes progress to.
type Printer interface {
	OK(format string, a ...any)
	Info(format string, a ...any)
	Warn(format string, a ...any)
}

// Env carries what Apply needs besides the document. Client is required; every other field has
// a working default.
type Env struct {
	// Client is the authenticated Capella client, bound to the organization.
	Client *capellaapi.Client
	// BaseDir resolves the manifest's relative paths: import files and the credentials directory.
	BaseDir string
	// DryRun plans against Capella (reads only) and changes nothing.
	DryRun bool
	// Yes skips the confirmation prompt and permits the changes that otherwise need -y.
	Yes bool
	// Confirm asks the question on a terminal and returns the answer. Nil means there is no
	// terminal: the apply proceeds unless a change needs -y (C13).
	Confirm func(question string) (bool, error)
	// Printer receives the progress lines (default logging.Current()).
	Printer Printer
	// Out receives the plan text (nil discards it; JSON runs leave it nil).
	Out io.Writer
	// Quiet drops the progress text of the engine.
	Quiet bool
	// Clock supplies time to the engine's waits (default the real clock).
	Clock engine.Clock
	// Getenv reads the environment for the engine (default none).
	Getenv func(string) string
	// Glyphs spells the symbols of plan and progress text (default Unicode).
	Glyphs output.Glyphs
	// Progress and ProgressInline receive the engine's progress lines; Warn its notices.
	Progress       func(string)
	ProgressInline func(text string, start bool)
	Warn           func(what, action string)
	// Connect opens the SDK connection of an import (default gocb over TLS).
	Connect Connector
	// Now supplies the time for step durations (default time.Now).
	Now func() time.Time
}

// BlockedError says the apply refused to change anything: a blocked change in the plan, or a
// change that needs -y and has no terminal to ask. The CLI exits 4.
type BlockedError struct {
	// Msg says what is blocked.
	Msg string
	// Action says how to resolve it.
	Action string
}

// Error implements error.
func (e *BlockedError) Error() string { return e.Msg }

// ExecError is a Capella action that failed during Execute. Err keeps the cause, so timeouts and
// API errors stay recognisable with errors.As.
type ExecError struct {
	// Failed is the action that failed.
	Failed *plan.Action
	// Completed lists the actions that finished before the failure ("kind key").
	Completed []string
	// Err is the cause.
	Err error
}

// Error implements error.
func (e *ExecError) Error() string {
	msg := fmt.Sprintf("Failed: %s %s: %s", e.Failed.Kind, e.Failed.Key, e.Failed.Err)
	if len(e.Completed) > 0 {
		msg += "\nCompleted before the failure: " + strings.Join(e.Completed, ", ")
	}
	return msg
}

// Unwrap returns the cause.
func (e *ExecError) Unwrap() error { return e.Err }

// PlanFailure is a planning failure that is not a manifest problem (for example Capella is
// unreachable or a resource is in a failed state). The CLI exits 1.
type PlanFailure struct{ Msg string }

// Error implements error.
func (e *PlanFailure) Error() string { return e.Msg }

// applier is the state of one Apply call.
type applier struct {
	env  Env
	doc  *manifest.Document
	spec *manifest.CapellaSpec
	name string
	rep  *manifest.Report
	pr   Printer
}

// Apply runs the Capella pipeline for doc: observe, translate, pre-check, plan, confirm, execute,
// then import. The report is returned even when err is not nil: it lists the steps that
// completed and ends with the one that failed. ctx is checked between steps; when it is done
// Apply returns ctx's error and the completed steps stay in place. Nothing reads or writes a
// capella.yaml.
func Apply(ctx context.Context, env Env, doc *manifest.Document) (*manifest.Report, error) {
	if doc == nil || doc.Capella == nil {
		return nil, errors.New("capellaapply: the document is not a CouchbaseCapella")
	}
	if env.Client == nil {
		return nil, errors.New("capellaapply: no Capella client")
	}
	if env.Printer == nil {
		env.Printer = logging.Current()
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Connect == nil {
		env.Connect = sdkConnector
	}
	if env.Clock == nil {
		env.Clock = engine.RealClock{}
	}
	a := &applier{
		env:  env,
		doc:  doc,
		spec: doc.Capella,
		name: doc.Envelope.Metadata.Name,
		rep:  &manifest.Report{Kind: doc.Envelope.Kind, Name: doc.Envelope.Metadata.Name, Steps: []manifest.Step{}},
		pr:   env.Printer,
	}
	err := a.run(ctx)
	if err != nil {
		a.rep.Status = manifest.ReportFailed
		a.rep.Error = err.Error()
	}
	a.rep.Finish()
	return a.rep, err
}

func (a *applier) run(ctx context.Context) error {
	rem, err := observe(ctx, a.env.Client, a.spec.Project, a.name)
	if err != nil {
		return a.planFailure(err)
	}
	// The immutable-field check goes first: a provider change would otherwise surface as a
	// confusing disk error from the translation of the old cluster's settings.
	if err := precheck(a.doc, rem); err != nil {
		return err
	}
	tr, err := Translate(a.doc, TranslateOptions{BaseDir: a.env.BaseDir, CIDR: resolveCIDR(a.doc, rem), Observed: rem.observed()})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, w := range tr.Warnings {
		if !seen[w] {
			seen[w] = true
			a.warn("%s", w)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rc := a.runContext(tr)
	p, err := plan.BuildPlan(ctx, rc, resources.BuildTreeWith(resources.Options{}), plan.Options{})
	if err != nil {
		return a.planFailure(err)
	}
	a.showPlan(p, rem)
	for _, line := range p.Unmanaged {
		a.pr.Info("%s is in Capella but not in the manifest; left in place", line)
	}
	if blocked := p.Blocked(); len(blocked) > 0 {
		return a.blocked(blocked)
	}
	a.noteCredentials(p)
	if a.env.DryRun {
		a.planned(p)
		return nil
	}
	if p.HasChanges() {
		if cancelled, err := a.confirm(p); err != nil || cancelled {
			if cancelled {
				a.pr.Info("Apply cancelled; nothing was changed")
				a.stepsFromPlan(p, manifest.StepSkipped, "cancelled")
			}
			return err
		}
	}
	return a.execute(ctx, rc, p)
}

// runContext is the engine's context for this document: in-memory (Ephemeral), with the
// manifest's wait settings (C15) and the engine's output wired to the printers.
func (a *applier) runContext(tr *Translation) *plan.RunContext {
	rc := plan.NewRunContext(a.env.Client, tr.Config)
	rc.Clock = a.env.Clock
	rc.Ephemeral = true
	rc.Timeout = a.spec.WaitTimeout()
	rc.NoWait = !a.spec.WaitEnabled()
	rc.Env = a.env.Getenv
	rc.Glyphs = a.env.Glyphs
	if !a.env.Quiet {
		rc.Progress = a.env.Progress
		rc.ProgressInline = a.env.ProgressInline
	}
	rc.Warn = a.env.Warn
	rc.OnGeneratedPassword = func(_, name, password string) {
		a.rep.Passwords = append(a.rep.Passwords, manifest.GeneratedPassword{Credential: name, Password: password})
	}
	return rc
}

// planFailure wraps a planning or reading failure so it keeps its cause.
func (a *applier) planFailure(err error) error {
	var pe *plan.PlanError
	if errors.As(err, &pe) {
		return &PlanFailure{Msg: pe.Msg}
	}
	return err
}

// showPlan prints the plan text when there is something to show.
func (a *applier) showPlan(p *plan.Plan, rem *remote) {
	if a.env.Out == nil || (!p.HasChanges() && len(p.Blocked()) == 0) {
		return
	}
	g := a.env.Glyphs
	if g.Arrow == "" {
		g = output.UnicodeGlyphs
	}
	header := fmt.Sprintf("Project %s %s organization %s", a.spec.Project, g.Dot, a.env.Client.OrgID())
	_, _ = fmt.Fprintln(a.env.Out, plan.RenderPlanText(p, header, a.env.Quiet, g))
}

// blocked records the blocked actions as failed steps and returns the refusal.
func (a *applier) blocked(blocked []*plan.Action) error {
	var reasons []string
	for _, b := range blocked {
		reason := fmt.Sprintf("%s %s: %s", b.Kind, b.Key, b.Reason)
		reasons = append(reasons, reason)
		a.rep.Add(manifest.Step{Name: b.Kind, Target: b.Key, Status: manifest.StepFailed, Error: b.Reason})
	}
	return &BlockedError{
		Msg:    fmt.Sprintf("Apply refused: %d blocked change(s); nothing was changed\n%s", len(blocked), strings.Join(reasons, "\n")),
		Action: "Resolve each blocked change named above (its note says how), then run the command again.",
	}
}

// noteCredentials says, for each credential that already exists, that apply leaves its password
// alone (D3): Capella never returns passwords, so a changed ${VAR} cannot be told from an
// unchanged one.
func (a *applier) noteCredentials(p *plan.Plan) {
	for _, act := range p.Visible() {
		if act.Kind != "credential" || act.Observed == nil || act.Observed.ID == "" {
			continue
		}
		a.pr.Info("credential %s exists; password not changed", credentialName(act))
	}
}

func credentialName(act *plan.Action) string {
	if d, ok := plan.AsObj(act.Desired); ok {
		if n, _ := d["name"].(string); n != "" {
			return n
		}
	}
	return act.Key
}

// planned records a dry run: write actions as planned, the rest as unchanged.
func (a *applier) planned(p *plan.Plan) {
	for _, act := range p.Visible() {
		status := manifest.StepUnchanged
		if act.Type.IsWrite() {
			status = manifest.StepPlanned
		}
		a.rep.Add(manifest.Step{Name: act.Kind, Target: act.Key, Status: status, Detail: detailOf(act)})
	}
	if hasImports(a.spec) {
		a.rep.Add(manifest.Step{Name: "import", Status: manifest.StepPlanned, Detail: "runs after the cluster is healthy"})
	}
}

// stepsFromPlan records every visible action with one status.
func (a *applier) stepsFromPlan(p *plan.Plan, status, detail string) {
	for _, act := range p.Visible() {
		a.rep.Add(manifest.Step{Name: act.Kind, Target: act.Key, Status: status, Detail: detail})
	}
}

// detailOf is the one-line description of what an action changes.
func detailOf(act *plan.Action) string {
	switch {
	case act.Label != "":
		return act.Label
	case act.Summary != "":
		return act.Summary
	}
	return ""
}

// destructive reports why an action needs -y without a terminal (C13): a scale-in, or turning
// deletion protection off. It returns "" for everything else.
func destructive(act *plan.Action) string {
	if act.Type == plan.Delete || act.Type == plan.Replace {
		return fmt.Sprintf("%s %s would be deleted", act.Kind, act.Key)
	}
	if strings.Contains(act.Label, "scale in") {
		return fmt.Sprintf("%s %s scales in", act.Kind, act.Key)
	}
	for _, c := range act.Changes {
		before, bok := c.Before.(bool)
		after, aok := c.After.(bool)
		if c.Path == "deletionProtection" && bok && aok && before && !after {
			return fmt.Sprintf("%s %s turns deletion protection off", act.Kind, act.Key)
		}
	}
	return ""
}

// confirm applies C13. On a terminal (Confirm set) it asks unless -y; without one, create,
// update and scale-out go ahead, and a destructive change needs -y (BlockedError).
func (a *applier) confirm(p *plan.Plan) (cancelled bool, err error) {
	if a.env.Yes {
		return false, nil
	}
	if a.env.Confirm == nil {
		var reasons []string
		for _, act := range p.Visible() {
			if r := destructive(act); r != "" {
				reasons = append(reasons, r)
			}
		}
		if len(reasons) > 0 {
			return false, &BlockedError{
				Msg:    "confirmation required but no terminal is attached: " + strings.Join(reasons, "; "),
				Action: "re-run with --yes",
			}
		}
		return false, nil
	}
	writes := 0
	for _, act := range p.Visible() {
		if act.Type.IsWrite() {
			writes++
		}
	}
	plural := "s"
	if writes == 1 {
		plural = ""
	}
	ok, err := a.env.Confirm(fmt.Sprintf("Apply %d change%s to project %s?", writes, plural, a.spec.Project))
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// execute runs the plan, records one step per action, then the imports.
func (a *applier) execute(ctx context.Context, rc *plan.RunContext, p *plan.Plan) error {
	res := plan.Execute(ctx, rc, p)
	a.stepsFromResult(res)
	if res.Interrupted {
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}
	if res.Failed != nil {
		var done []string
		for _, act := range res.Actions {
			if act.Status == "done" {
				done = append(done, act.Kind+" "+act.Key)
			}
		}
		return &ExecError{Failed: res.Failed, Completed: done, Err: res.Err}
	}
	for _, act := range res.Actions {
		if act.Status == "done" && act.Type.IsWrite() {
			a.pr.OK("%s %s %s", act.Kind, act.Key, doneVerb(act))
		}
	}
	cluster, err := a.clusterAfter(ctx, res)
	if err != nil {
		return err
	}
	if cluster != nil {
		a.rep.ConnectionString = cluster.ConnectionString
		if a.rep.ConnectionString != "" {
			a.pr.Info("Connection string: %s", a.rep.ConnectionString)
		}
	}
	if err := a.imports(ctx, cluster); err != nil {
		return err
	}
	a.rep.Summary = a.summary()
	return nil
}

func doneVerb(act *plan.Action) string {
	switch act.Type {
	case plan.Create:
		return "created"
	case plan.Delete:
		return "deleted"
	}
	return "updated"
}

// stepsFromResult records one step per visible action of the executed plan.
func (a *applier) stepsFromResult(res *plan.ApplyResult) {
	for _, act := range res.Actions {
		if act.Hidden && !act.Type.IsWrite() {
			continue
		}
		s := manifest.Step{Name: act.Kind, Target: act.Key, Detail: detailOf(act), Duration: act.Duration}
		switch act.Status {
		case "done":
			s.Status = manifest.StepDone
		case "failed":
			s.Status, s.Error = manifest.StepFailed, act.Err
		case "unchanged":
			s.Status = manifest.StepUnchanged
		default: // pending, skipped, deferred, interrupted: not run
			if act.Type.IsWrite() {
				s.Status, s.Detail = manifest.StepSkipped, "not run"
			} else {
				s.Status = manifest.StepUnchanged
			}
		}
		a.rep.Add(s)
	}
}

// clusterAfter reads the cluster once the plan has run, for the connection string and the state
// the imports need. It is nil when the cluster cannot be found.
func (a *applier) clusterAfter(ctx context.Context, res *plan.ApplyResult) (*capellaapi.Cluster, error) {
	for _, act := range res.Actions {
		if act.Kind != "cluster" || act.ID() == "" {
			continue
		}
		project := act.Ancestor("project")
		if project == nil || project.ID() == "" {
			continue
		}
		c, err := a.env.Client.GetCluster(ctx, project.ID(), act.ID())
		if err != nil {
			return nil, fmt.Errorf("reading cluster %s: %w", a.name, err)
		}
		return c, nil
	}
	return nil, nil
}

// imports runs the declared imports against a healthy cluster. A cluster that is not healthy
// (wait disabled) skips them rather than failing.
func (a *applier) imports(ctx context.Context, cluster *capellaapi.Cluster) error {
	if !hasImports(a.spec) {
		return nil
	}
	if cluster == nil || cluster.CurrentState != "healthy" || cluster.ConnectionString == "" {
		state := "not found"
		if cluster != nil {
			state = cluster.CurrentState
		}
		a.rep.Add(manifest.Step{Name: "import", Status: manifest.StepSkipped,
			Detail: fmt.Sprintf("the cluster is %s; run apply again once it is healthy", state)})
		a.warn("imports skipped: the cluster is %s", state)
		return nil
	}
	return a.runImports(ctx, cluster.ConnectionString)
}

// timed runs fn as one step and records it. A non-nil error records the step as failed.
func (a *applier) timed(name, target string, fn func() (manifest.Step, error)) error {
	start := a.env.Now()
	s, err := fn()
	s.Name, s.Target = name, target
	s.Duration = a.env.Now().Sub(start)
	if err != nil {
		s.Status = manifest.StepFailed
		s.Error = err.Error()
		a.rep.Add(s)
		return err
	}
	a.rep.Add(s)
	return nil
}

func (a *applier) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.rep.Warn(msg)
	a.pr.Warn("%s", msg)
}

// summary describes what the document holds, for the closing line.
func (a *applier) summary() string {
	parts := []string{"cluster " + a.name}
	if n := len(a.spec.Buckets); n > 0 {
		parts = append(parts, plural(n, "bucket"))
	}
	if n := len(a.spec.Credentials); n > 0 {
		parts = append(parts, plural(n, "credential"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// hasImports reports whether any bucket declares an import.
func hasImports(s *manifest.CapellaSpec) bool {
	for _, b := range s.Buckets {
		if len(b.Import) > 0 {
			return true
		}
	}
	return false
}
