package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/spf13/cobra"
)

// ---- validate --------------------------------------------------------------------------------

func newCapellaValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate capella.yaml offline (schema, references, secrets); no API calls",
		Long: "Validate the config file offline: schema, cross-references and secret references. " +
			"No API call is made and no API key is needed.",
		Example: "  cbctl capella validate\n  cbctl capella -c prod.yaml validate",
		Args:    cobra.NoArgs,
		RunE:    runValidate,
	}
}

func runValidate(cmd *cobra.Command, _ []string) error {
	g, err := readGlobals(cmd)
	if err != nil {
		return err
	}
	jsonMode := g.Output == "json"
	out := newRenderer(cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonMode, g.Quiet)
	cfg, disc, err := loadConfig(cmd, WarnAll)
	if err != nil {
		var ce *capellafile.ConfigError
		if jsonMode && errors.As(err, &ce) {
			issues := ce.Issues
			if issues == nil {
				issues = []capellafile.Issue{}
			}
			out.Object(map[string]any{"valid": false, "file": displayPath(disc.Path), "issues": issues})
		}
		return err
	}
	if jsonMode {
		out.Object(map[string]any{"valid": true, "file": cfg.Path, "counts": cfg.Counts()})
		return nil
	}
	out.Line("%s", cfg.Summary())
	return nil
}

// ---- plan ------------------------------------------------------------------------------------

func newCapellaPlanCmd() *cobra.Command {
	var targets []string
	var detailed bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what apply would change (read-only)",
		Long:  "Show what apply would change. Read-only: makes only GET requests.",
		Example: "  cbctl capella plan\n  cbctl capella plan --target cluster/nova-dev-01\n" +
			"  cbctl capella -o json plan --detailed-exitcode",
		Args: cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	cmd.Flags().StringArrayVar(&targets, "target", nil, "Limit to these resources, KIND/NAME (repeatable)")
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "Exit 2 when there are changes, 0 when none")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		e, err := newEnvRuntime(cmd, af, WarnCount)
		if err != nil {
			return err
		}
		p, err := makePlan(e.Ctx, e, plan.Options{Targets: targets})
		if err != nil {
			return err
		}
		e.printPlan(p)
		if len(p.Blocked()) > 0 {
			return &CLIError{Code: ExitBlocked}
		}
		if detailed && p.HasChanges() {
			return &CLIError{Code: ExitChanges}
		}
		return nil
	}
	return cmd
}

// printPlan shows the plan as JSON or text.
func (e *envRuntime) printPlan(p *plan.Plan) {
	if e.JSON {
		e.Out.Object(plan.RenderPlanJSON(p))
		return
	}
	e.Out.Line("%s", plan.RenderPlanText(p, e.header(), e.Quiet))
}

// ---- apply -----------------------------------------------------------------------------------

func newCapellaApplyCmd() *cobra.Command {
	var targets []string
	var prune, rotate, powerOn, svc, grp, skipCap, force bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Make Capella match capella.yaml (shows the plan and asks first)",
		Example: "  cbctl capella apply\n  cbctl capella apply --yes\n  cbctl capella apply --target cluster/nova-dev-01\n" +
			"  cbctl capella apply --rotate-passwords --target credential/nova-dev-01/orders_app",
		Args: cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&targets, "target", nil, "Limit to these resources, KIND/NAME (repeatable)")
	f.BoolVar(&prune, "prune", false, "Also delete buckets, scopes, collections, allowed CIDRs and database credentials "+
		"in Capella that are not in the file (asks for each bucket, scope and collection name)")
	f.BoolVar(&rotate, "rotate-passwords", false, "Also set a new password on every planned database credential: "+
		"the configured ${VAR}/${file:...} value, else a generated one (saved locally)")
	f.BoolVar(&powerOn, "power-on", false, "Turn on a turned-off cluster before changing it or what it holds")
	f.BoolVar(&svc, "allow-service-removal", false, "Scale-in may remove the last nodes running a service")
	f.BoolVar(&grp, "allow-service-group-removal", false, "Scale-in may remove a whole service group")
	f.BoolVar(&skipCap, "skip-capacity-check", false, "Scale in even when cluster stats cannot be read (at your own risk)")
	f.BoolVar(&force, "force-overwrite-secret", false, "Let a generated credential replace an existing secret file")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		e, err := newEnvRuntime(cmd, af, WarnCount)
		if err != nil {
			return err
		}
		e.RC.RotatePasswords = rotate
		e.RC.PowerOn = powerOn
		e.RC.AllowServiceRemoval = svc
		e.RC.AllowServiceGroupRemoval = grp
		e.RC.SkipCapacityCheck = skipCap
		e.TreeOptions = resources.Options{ForceOverwriteSecret: force}
		p, err := makePlan(e.Ctx, e, plan.Options{Targets: targets, Prune: prune,
			NoDescendants: cmd.Annotations[noDescendantsAnnotation] != ""})
		if err != nil {
			return err
		}
		return applyPlan(e, p, applyOptions{})
	}
	return cmd
}

// applyOptions tune applyPlan.
type applyOptions struct {
	// After are notes printed once the apply succeeded.
	After []string
	// Shown says the caller already displayed the plan and got the confirmation.
	Shown bool
	// OnFailure is called with the result when an action fails; a returned line is appended to the
	// failure message.
	OnFailure func(*plan.ApplyResult) string
}

// applyPlan shows, confirms and executes p. apply and the file-managed resource commands share it.
func applyPlan(e *envRuntime, p *plan.Plan, o applyOptions) error {
	if blocked := p.Blocked(); len(blocked) > 0 {
		e.printPlan(p)
		err := Errorf(ExitBlocked, "Resolve each change marked ! in the plan above (its note says how), then run the command again.",
			"Apply refused: %d blocked change(s); nothing was changed", len(blocked))
		return err
	}
	if !p.HasChanges() {
		e.printNoChanges(p)
		return nil
	}
	if !o.Shown {
		if !e.JSON {
			e.Out.Line("%s", plan.RenderPlanText(p, e.header(), e.Quiet))
			e.Out.Line("")
		}
		cancelled, err := e.confirmApply(p)
		if err != nil {
			return err
		}
		if cancelled {
			return nil
		}
	}
	started := e.Clock.Monotonic()
	result := plan.Execute(e.Ctx, e.RC, p)
	pinCreatedIDs(e, result.Actions)
	var conn []string
	if result.OK() {
		conn = e.connectionStrings(result)
	}
	if e.JSON {
		if len(conn) > 0 {
			if result.Extra == nil {
				result.Extra = map[string]any{}
			}
			items := make([]any, len(conn))
			for i, c := range conn {
				items[i] = c
			}
			result.Extra["connectionStrings"] = items
		}
		e.Out.Object(plan.RenderApplyJSON(p, result))
	} else {
		if table := plan.RenderSummaryTable(result.Actions); table != "" {
			e.Out.Line("")
			e.Out.Line("%s", table)
		}
		for _, c := range conn {
			e.Out.Line("%s", c)
		}
	}
	if err := e.applyFailure(result, o); err != nil {
		return err
	}
	for _, line := range o.After {
		e.note("%s", line)
	}
	if !e.JSON {
		e.note("Apply complete in %s.", plan.FormatDuration(e.Clock.Monotonic()-started))
	}
	return nil
}

// printNoChanges prints the "nothing to do" result of apply.
func (e *envRuntime) printNoChanges(p *plan.Plan) {
	if e.JSON {
		doc := plan.RenderPlanJSON(p)
		doc["result"] = map[string]any{"status": "ok", "actions": []any{}}
		e.Out.Object(doc)
		return
	}
	var deferred []*plan.Action
	for _, a := range p.Visible() {
		if a.Type == plan.Deferred {
			deferred = append(deferred, a)
		}
	}
	if len(deferred) > 0 {
		for _, a := range deferred {
			for _, l := range plan.ActionLines(a) {
				e.Out.Line("%s", l)
			}
		}
	}
	if len(p.Unmanaged) > 0 {
		e.Out.Line("Unmanaged (not in capella.yaml):")
		for _, label := range p.Unmanaged {
			e.Out.Line("    %s", label)
		}
	}
	e.Out.Line("No changes. Capella matches capella.yaml.")
}

// confirmApply asks before applying; cancelled reports a "no" answer.
func (e *envRuntime) confirmApply(p *plan.Plan) (cancelled bool, err error) {
	if e.Yes {
		return false, nil
	}
	if err := needTerminal(e.runtime); err != nil {
		return false, err
	}
	var destructive, rotating []*plan.Action
	n := 0
	for _, a := range p.Visible() {
		if a.Type == plan.Delete || a.Type == plan.Replace || strings.Contains(a.Label, "scale in") {
			destructive = append(destructive, a)
		}
		if a.Type == plan.Update && truthyData(a.Data["rotate"]) {
			rotating = append(rotating, a)
		}
		if a.Type.IsWrite() {
			n++
		}
	}
	say := func(format string, a ...any) { fmt.Fprintf(e.errOut(), format+"\n", a...) }
	if len(destructive) > 0 {
		say("Destructive changes:")
		for _, a := range destructive {
			say("  %s %s", a.Kind, a.Key)
		}
	}
	if len(rotating) > 0 {
		say("Passwords to rotate:")
		for _, a := range rotating {
			say("  %s", a.Key)
		}
	}
	for _, a := range destructive {
		if a.Type != plan.Delete || (a.Kind != "bucket" && a.Kind != "scope" && a.Kind != "collection") {
			continue
		}
		ok, err := e.confirmDataDelete(a)
		if err != nil {
			return false, err
		}
		if !ok {
			say("Apply cancelled; nothing was changed.")
			return true, nil
		}
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	ok, err := confirm(e.runtime, fmt.Sprintf("Apply %d change%s to %s?", n, plural, projectsLabel(e.Cfg)))
	if err != nil {
		return false, err
	}
	if !ok {
		say("Apply cancelled.")
		return true, nil
	}
	return false, nil
}

func truthyData(v any) bool {
	b, _ := v.(bool)
	return b
}

// confirmDataDelete shows what a bucket, scope or collection delete loses and asks for its name.
func (e *envRuntime) confirmDataDelete(a *plan.Action) (bool, error) {
	lines := e.dataDeleteLines(a)
	for _, l := range lines {
		fmt.Fprintf(e.errOut(), "  %s\n", l)
	}
	name := ""
	if a.Observed != nil {
		name, _ = a.Observed.Body["name"].(string)
	}
	if name == "" {
		if d, ok := plan.AsObj(a.Desired); ok {
			name, _ = d["name"].(string)
		}
	}
	return confirmTyped(e.runtime, fmt.Sprintf("Type the %s name to confirm deletion of %s", a.Kind, a.Key), name)
}

// dataDeleteLines describes what deleting a bucket, scope or collection removes.
func (e *envRuntime) dataDeleteLines(a *plan.Action) []string {
	bucket := a
	if a.Kind != "bucket" {
		bucket = a.Ancestor("bucket")
	}
	if bucket == nil {
		return nil
	}
	lines := []string{}
	switch a.Kind {
	case "bucket":
		lines = append(lines, fmt.Sprintf("deletes bucket %s with every scope, collection and document in it", bucket.Key))
		if items, ok := e.bucketItems(bucket); ok {
			lines = append(lines, fmt.Sprintf("items stored: %d", items))
		}
	case "scope":
		lines = append(lines, fmt.Sprintf("deletes scope %s with every collection and document in it", a.Key))
	default:
		lines = append(lines, fmt.Sprintf("deletes collection %s with every document in it", a.Key))
	}
	return lines
}

// bucketItems reads the document count Capella reports for the bucket; it is informational.
func (e *envRuntime) bucketItems(bucket *plan.Action) (int64, bool) {
	cluster := bucket.Parent
	if cluster == nil || bucket.ID() == "" {
		return 0, false
	}
	project := cluster.Ancestor("project")
	if project == nil {
		return 0, false
	}
	org, err := e.Client.OrgPath()
	if err != nil {
		return 0, false
	}
	path := fmt.Sprintf("%s/projects/%s/clusters/%s/buckets/%s", org, project.ID(), cluster.ID(), bucket.ID())
	if free, _ := cluster.Data["free"].(bool); free {
		path = fmt.Sprintf("%s/projects/%s/clusters/%s/buckets/freeTier/%s", org, project.ID(), cluster.ID(), bucket.ID())
	}
	var body plan.Obj
	if _, err := e.Client.Get(e.Ctx, path, nil, &body); err != nil {
		return 0, false
	}
	stats, _ := plan.AsObj(body["stats"])
	n, ok := plan.AsInt(stats["itemCount"])
	if !ok {
		n, ok = plan.AsInt(stats["items"])
	}
	return n, ok
}

// connectionStrings lists the connection string of each cluster created in this run.
func (e *envRuntime) connectionStrings(result *plan.ApplyResult) []string {
	var lines []string
	org, err := e.Client.OrgPath()
	if err != nil {
		return nil
	}
	for _, a := range result.Actions {
		if a.Kind != "cluster" || a.Status != "done" || a.ID() == "" || (a.Type != plan.Create && a.Type != plan.Replace) {
			continue
		}
		project := a.Ancestor("project")
		if project == nil || project.ID() == "" {
			continue
		}
		path := fmt.Sprintf("%s/projects/%s/clusters/%s", org, project.ID(), a.ID())
		if free, _ := a.Data["free"].(bool); free {
			path = fmt.Sprintf("%s/projects/%s/clusters/freeTier/%s", org, project.ID(), a.ID())
		}
		var body plan.Obj
		if _, err := e.Client.Get(e.Ctx, path, nil, &body); err != nil {
			continue
		}
		if cs, _ := body["connectionString"].(string); cs != "" {
			lines = append(lines, fmt.Sprintf("connectionString (%s): %s", a.Key, cs))
		}
	}
	return lines
}

// applyFailure turns a failed or interrupted result into the command's error.
func (e *envRuntime) applyFailure(result *plan.ApplyResult, o applyOptions) error {
	if result.Interrupted {
		detail := "Interrupted"
		if result.Failed != nil && result.Failed.Err != "" {
			detail = result.Failed.Err
		}
		return Errorf(ExitInterrupted, "Run the command again to continue; completed changes are not repeated.", "%s", detail)
	}
	failed := result.Failed
	if failed == nil {
		return nil
	}
	code := ExitError
	var wt *engine.WaitTimeout
	var st *engine.StatusTimeout
	if errors.As(result.Err, &wt) || errors.As(result.Err, &st) {
		code = ExitTimeout
	}
	msg := fmt.Sprintf("Failed: %s %s: %s", failed.Kind, failed.Key, failed.Err)
	var done []string
	for _, a := range result.Actions {
		if a.Status == "done" {
			done = append(done, a.Kind+" "+a.Key)
		}
	}
	if len(done) > 0 {
		msg += "\nCompleted before the failure: " + strings.Join(done, ", ")
	}
	if o.OnFailure != nil {
		if undone := o.OnFailure(result); undone != "" {
			msg += "\n" + undone
		}
	}
	action := failureAction(failed, result.Err, "Then run the command again; completed changes are not repeated.")
	return &CLIError{Code: code, Message: msg, Action: action, Err: result.Err}
}

// pinCreatedIDs records the id of each project and cluster created in this run in the file, so a
// later rename in the Capella UI shows as a rename and never as a new resource.
func pinCreatedIDs(e *envRuntime, actions []*plan.Action) {
	var created []*plan.Action
	for _, a := range actions {
		if (a.Kind == "project" || a.Kind == "cluster") && a.Type == plan.Create && a.Status == "done" && a.ID() != "" {
			created = append(created, a)
		}
	}
	e.editIDs(created, "recorded the ids of the new project and clusters", func(ed *capellafile.Editor, a *plan.Action, project, name string) bool {
		return ed.SetID(a.Kind, project, name, a.ID())
	})
}

// unpinDeleted drops the id of each project and cluster destroy deleted: those ids are gone and a
// pinned id that does not exist stops apply.
func unpinDeleted(e *envRuntime, actions []*plan.Action) {
	var gone []*plan.Action
	for _, a := range actions {
		if (a.Kind == "project" || a.Kind == "cluster") && a.Type == plan.Delete && a.Status == "done" {
			gone = append(gone, a)
		}
	}
	e.editIDs(gone, "removed the ids of the deleted project and clusters", func(ed *capellafile.Editor, a *plan.Action, project, name string) bool {
		return ed.ClearID(a.Kind, project, name)
	})
}

// editIDs applies fn to the file for each action and saves it when something changed.
func (e *envRuntime) editIDs(actions []*plan.Action, what string, fn func(*capellafile.Editor, *plan.Action, string, string) bool) {
	path := e.Cfg.Path
	if path == "" {
		path = e.Disc.Path
	}
	if len(actions) == 0 || path == "" || e.Ephemeral {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	ed, err := capellafile.OpenEditor(path)
	if err != nil {
		printNotice(e.errOut(), fmt.Sprintf("could not update %s: %v", displayPath(path), err), "")
		return
	}
	changed := false
	for _, a := range actions {
		d, _ := plan.AsObj(a.Desired)
		name, _ := d["name"].(string)
		if name == "" {
			name = a.Key
		}
		project := ""
		if a.Kind == "cluster" {
			if p := a.Ancestor("project"); p != nil {
				project = p.Key
			}
		}
		if fn(ed, a, project, name) {
			changed = true
		}
	}
	if !changed {
		return
	}
	text, err := ed.Text()
	if err == nil {
		var backup string
		if backup, err = ed.Save(text); err == nil {
			if backup != "" {
				e.note("%s: %s (previous version kept as %s)", displayPath(path), what, displayPath(backup))
			}
			return
		}
	}
	printNotice(e.errOut(), fmt.Sprintf("could not update %s: %v", displayPath(path), err),
		"edit the file by hand or run 'cbctl capella sync'")
}

// ---- destroy ---------------------------------------------------------------------------------

const destroyOrderNote = "credentials → collections → scopes → buckets → CIDRs → schedule → cluster → project"

func newCapellaEnvDestroyCmd() *cobra.Command {
	var discard bool
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Delete every resource capella.yaml declares, in reverse order",
		Long: "Delete every resource the file declares, in reverse order. capella.yaml is kept (only the ids of what " +
			"was deleted are dropped), so 'apply' builds it all again.",
		Example: "  cbctl capella destroy\n  cbctl capella --yes destroy\n  cbctl capella destroy --discard-snapshots",
		Args:    cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	cmd.Flags().BoolVar(&discard, "discard-snapshots", false, "Also delete the clusters' snapshot backups (kept by default)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		e, err := newEnvRuntime(cmd, af, WarnNone)
		if err != nil {
			return err
		}
		return runDestroy(e, discard)
	}
	return cmd
}

func runDestroy(e *envRuntime, discard bool) error {
	protected, err := e.protectedClusters()
	if err != nil {
		return err
	}
	if len(protected) > 0 {
		lines := make([]string, len(protected))
		for i, n := range protected {
			lines[i] = "deletion protection is on for " + n
		}
		return Errorf(ExitBlocked,
			"Run 'cbctl capella cluster protect NAME --off' for each cluster above if you really mean to destroy it, then run 'cbctl capella destroy' again.",
			"%s\nNothing was deleted.", strings.Join(lines, "\n"))
	}
	p, err := makePlan(e.Ctx, e, plan.Options{})
	if err != nil {
		return err
	}
	deletes, kept, err := e.destroyPlan(p, discard)
	if err != nil {
		return err
	}
	if len(deletes.Actions) == 0 {
		for _, l := range kept {
			e.Out.Line("%s", l)
		}
		e.Out.Line("nothing to destroy: none of the resources in capella.yaml exist in Capella")
		if e.JSON {
			e.Out.Object(plan.RenderPlanJSON(deletes))
		}
		return nil
	}
	if e.JSON {
		e.Out.Object(plan.RenderPlanJSON(deletes))
	} else {
		e.Out.Line("%s", plan.RenderPlanText(deletes, e.header(), e.Quiet))
		e.Out.Line("")
		for _, l := range kept {
			e.Out.Line("%s", l)
		}
	}
	names := e.Cfg.ProjectNames()
	what := "the project name"
	if len(names) != 1 {
		what = "the project names, comma-separated"
	}
	snap := "kept"
	if discard {
		snap = "deleted"
	}
	ok, err := confirmTypedFunc(e.runtime, "Type "+what+" to confirm",
		fmt.Sprintf("This deletes everything listed above (%s). Snapshot backups are %s.", destroyOrderNote, snap),
		func(typed string) bool { return sameNames(strings.Split(typed, ","), names) })
	if err != nil {
		return err
	}
	if !ok {
		e.Out.Line("Destroy cancelled; nothing was deleted.")
		return nil
	}
	started := e.Clock.Monotonic()
	result := plan.Execute(e.Ctx, e.RC, deletes)
	if !e.JSON {
		if table := plan.RenderSummaryTable(result.Actions); table != "" {
			e.Out.Line("")
			e.Out.Line("%s", table)
		}
	}
	if result.Failed != nil || result.Interrupted {
		const again = "Run 'cbctl capella destroy' again; what is already deleted is skipped."
		if result.Interrupted {
			detail := "Interrupted"
			if result.Failed != nil && result.Failed.Err != "" {
				detail = fmt.Sprintf("%s %s: %s", result.Failed.Kind, result.Failed.Key, result.Failed.Err)
			}
			return Errorf(ExitInterrupted, again, "%s", detail)
		}
		code := ExitError
		var wt *engine.WaitTimeout
		var st *engine.StatusTimeout
		if errors.As(result.Err, &wt) || errors.As(result.Err, &st) {
			code = ExitTimeout
		}
		return &CLIError{Code: code, Err: result.Err,
			Message: fmt.Sprintf("Failed: %s %s: %s", result.Failed.Kind, result.Failed.Key, result.Failed.Err),
			Action:  "Fix the problem, then run 'cbctl capella destroy' again; what is already deleted is skipped."}
	}
	for _, l := range kept {
		fmt.Fprintln(e.errOut(), l)
	}
	unpinDeleted(e, result.Actions)
	if e.JSON {
		e.Out.Object(map[string]any{"plan": plan.RenderPlanJSON(deletes)["plan"], "result": result.ToJSON()})
	}
	e.note("Destroy complete in %s. capella.yaml still describes the environment: run 'cbctl capella apply' to build it again.",
		plan.FormatDuration(e.Clock.Monotonic()-started))
	return nil
}

func sameNames(typed, want []string) bool {
	got := make([]string, 0, len(typed))
	for _, t := range typed {
		got = append(got, strings.TrimSpace(t))
	}
	if len(got) != len(want) {
		return false
	}
	count := map[string]int{}
	for _, w := range want {
		count[w]++
	}
	for _, g := range got {
		count[g]--
	}
	for _, c := range count {
		if c != 0 {
			return false
		}
	}
	return true
}

// protectedClusters lists the declared clusters that exist in Capella with deletion protection on.
func (e *envRuntime) protectedClusters() ([]string, error) {
	org, err := e.Client.OrgPath()
	if err != nil {
		return nil, err
	}
	projects, err := e.Client.ListProjects(e.Ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	var names []string
	for _, entry := range e.Cfg.Projects() {
		pinned, _ := entry.Data["id"].(string)
		for _, proj := range projects {
			if (pinned != "" && proj.ID != pinned) || (pinned == "" && proj.Name != entry.Name()) {
				continue
			}
			clusters, err := capellaapi.Paginate[plan.Obj](e.Ctx, e.Client, org+"/projects/"+proj.ID+"/clusters", "data", nil)
			if err != nil {
				return nil, fmt.Errorf("listing clusters of %s: %w", proj.Name, err)
			}
			for _, c := range clusters {
				cname, _ := c["name"].(string)
				cid, _ := c["id"].(string)
				if !declaresCluster(entry.Clusters, cid, cname) {
					continue
				}
				if resources.DeletionProtected(c) {
					names = append(names, cname)
				}
			}
		}
	}
	return names, nil
}

func declaresCluster(declared []plan.Obj, id, name string) bool {
	for _, d := range declared {
		if did, _ := d["id"].(string); did != "" {
			if did == id {
				return true
			}
			continue
		}
		if dn, _ := d["name"].(string); dn == name {
			return true
		}
	}
	return false
}

// destroyPlan derives a delete for every declared resource that exists, children first. A project is
// kept when Capella holds clusters the file does not declare; a turned-off cluster's contents are
// left to the cluster delete.
func (e *envRuntime) destroyPlan(p *plan.Plan, discard bool) (*plan.Plan, []string, error) {
	var deletes []*plan.Action
	var kept []string
	skipBelow := map[*plan.Action]bool{}
	for _, a := range p.Actions {
		if a.Kind != "cluster" || a.Observed == nil {
			continue
		}
		if state := a.Data["state"]; state != nil && state != "healthy" {
			skipBelow[a] = true
		}
	}
	for i := len(p.Actions) - 1; i >= 0; i-- {
		a := p.Actions[i]
		if a.Observed == nil || a.Kind == "deletion-protection" || a.Type == plan.Delete {
			continue
		}
		underOff := false
		for anc := a.Parent; anc != nil; anc = anc.Parent {
			underOff = underOff || skipBelow[anc]
		}
		if underOff {
			continue
		}
		if a.Kind == "project" {
			others, err := e.undeclaredClusters(a)
			if err != nil {
				return nil, nil, err
			}
			if len(others) > 0 {
				kept = append(kept, fmt.Sprintf("project %s kept: contains undeclared cluster %s", a.Key, strings.Join(others, ", ")))
				continue
			}
		}
		d, err := a.Reconciler.DeleteAction(e.Ctx, e.RC, *a.Observed, a.Parent)
		if err != nil {
			return nil, nil, err
		}
		if a.Kind == "cluster" {
			d.Data["discard_snapshots"] = discard
		}
		deletes = append(deletes, d)
	}
	return &plan.Plan{Actions: deletes}, kept, nil
}

// undeclaredClusters names the clusters of the project that capella.yaml does not declare.
func (e *envRuntime) undeclaredClusters(project *plan.Action) ([]string, error) {
	d, _ := plan.AsObj(project.Desired)
	declared := map[string]bool{}
	if list, ok := plan.AsList(d["_clusters"]); ok {
		for _, item := range list {
			if c, ok := plan.AsObj(item); ok {
				if id, _ := c["id"].(string); id != "" {
					declared[id] = true
				}
				if n, _ := c["name"].(string); n != "" {
					declared[n] = true
				}
			}
		}
	}
	clusters, err := e.Client.ListClusters(e.Ctx, project.Observed.ID)
	if err != nil {
		return nil, fmt.Errorf("listing clusters of %s: %w", project.Key, err)
	}
	var out []string
	for _, c := range clusters {
		if !declared[c.ID] && !declared[c.Name] {
			out = append(out, c.Name)
		}
	}
	return out, nil
}

// ---- drift -----------------------------------------------------------------------------------

func newCapellaDriftCmd() *cobra.Command {
	var detailed bool
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Report what differs between Capella and capella.yaml (read-only)",
		Long: "Report what differs between Capella and capella.yaml, including resources made outside cbctl. " +
			"Read-only: only GET requests. Changed and missing items are fixed by 'apply'; unmanaged ones are removed " +
			"by 'apply --prune' (never clusters or projects) or brought into the file by 'sync'.",
		Example: "  cbctl capella drift\n  cbctl capella drift --detailed-exitcode     (CI: exit 2 on drift)\n  cbctl capella -o json drift",
		Args:    cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "Exit 2 when Capella and capella.yaml differ, 0 when not")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		e, err := newEnvRuntime(cmd, af, WarnCount)
		if err != nil {
			return err
		}
		return runDrift(e, detailed)
	}
	return cmd
}

func runDrift(e *envRuntime, detailed bool) error {
	p, err := makePlan(e.Ctx, e, plan.Options{AllUnmanaged: true})
	if err != nil {
		return err
	}
	var changed, missing []*plan.Action
	for _, a := range p.Visible() {
		switch {
		case a.Type == plan.Update || a.Type == plan.Replace || a.Type == plan.Delete,
			a.Type == plan.Blocked && a.Observed != nil:
			changed = append(changed, a)
		case a.Type == plan.Create, a.Type == plan.Blocked && a.Observed == nil:
			missing = append(missing, a)
		}
	}
	found := len(changed)+len(missing)+len(p.Unmanaged) > 0
	if e.JSON {
		toJSON := func(list []*plan.Action) []any {
			out := make([]any, 0, len(list))
			for _, a := range list {
				out = append(out, a.ToJSON())
			}
			return out
		}
		unmanaged := make([]any, 0, len(p.Unmanaged))
		for _, u := range p.Unmanaged {
			unmanaged = append(unmanaged, u)
		}
		e.Out.Object(map[string]any{"changed": toJSON(changed), "missing": toJSON(missing), "unmanaged": unmanaged})
	} else {
		e.Out.Line("%s", e.header())
		sections := []struct {
			title string
			items []*plan.Action
		}{
			{"Changed (capella.yaml differs from Capella; 'apply' changes Capella):", changed},
			{"Missing (in capella.yaml, not in Capella; 'apply' creates them):", missing},
		}
		for _, s := range sections {
			if len(s.items) == 0 {
				continue
			}
			e.Out.Line("")
			e.Out.Line("%s", s.title)
			for _, a := range s.items {
				for _, l := range plan.ActionLines(a) {
					e.Out.Line("%s", l)
				}
			}
		}
		if len(p.Unmanaged) > 0 {
			e.Out.Line("")
			e.Out.Line("Unmanaged (in Capella, not in capella.yaml; 'apply --prune' deletes them, 'sync' adds them to the file):")
			for _, label := range p.Unmanaged {
				e.Out.Line("    %s", label)
			}
		}
		e.Out.Line("")
		if found {
			e.Out.Line("Drift: %d changed, %d missing, %d unmanaged.", len(changed), len(missing), len(p.Unmanaged))
		} else {
			e.Out.Line("No drift: Capella matches capella.yaml.")
		}
	}
	if detailed && found {
		return &CLIError{Code: ExitChanges}
	}
	return nil
}
