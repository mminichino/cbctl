package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// Mutation is one change to capella.yaml that a Phase 2 resource command makes in file-managed
// mode: the file edit, how to plan it and what to ask.
type Mutation struct {
	// Target is the plan target, for example "cluster/nova-dev-01".
	Target string
	// Question is the confirmation prompt.
	Question string
	// Edit changes the in-memory document.
	Edit func(e *capellafile.Editor) error
	// NoopMessage is printed when neither the file nor Capella needs a change.
	NoopMessage string
	// NoDescendants plans the target without its children.
	NoDescendants bool
	// Options sets engine switches (SkipCapacityCheck, PowerOn, …) before planning.
	Options func(*plan.RunContext)
	// AfterPlan are lines shown under the plan, before the question.
	AfterPlan []string
	// AfterApply runs once the plan has been applied without error (not with --no-apply), for
	// follow-up edits of the saved file.
	AfterApply func(p *plan.Plan)
}

const defaultNoop = "No changes: capella.yaml and Capella already match."

// editedText encodes the edited file. A file that changes is written in the projects: form, so the
// single-project form is converted on its first write (CAP-040 AC2); an unchanged file is left
// as it is.
func editedText(ed *capellafile.Editor) (string, error) {
	text, err := ed.Text()
	if err != nil || text == ed.Original {
		return text, err
	}
	ed.EnsureProjects()
	return ed.Text()
}

// ChangeAndApply edits the file, validates the candidate, plans the change, asks, saves the file
// and applies the plan. It returns the plan, or nil when nothing ran.
func ChangeAndApply(ctx context.Context, e *envRuntime, ed *capellafile.Editor, m Mutation, noApply bool) (*plan.Plan, error) {
	if err := m.Edit(ed); err != nil {
		return nil, err
	}
	text, err := editedText(ed)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", filepath.Base(ed.Path), err)
	}
	cfg, err := validateCandidateEnv(ed, text, envFrom(e.cmd))
	if err != nil {
		return nil, err
	}
	e.Cfg, e.RC.Config = cfg, cfg
	if m.Options != nil {
		m.Options(e.RC)
	}
	p, err := makePlan(ctx, e, plan.Options{Targets: []string{m.Target}, NoDescendants: m.NoDescendants})
	if err != nil {
		return nil, err
	}
	name := filepath.Base(ed.Path)
	if !p.HasChanges() && len(p.Blocked()) == 0 {
		if text != ed.Original && !e.Ephemeral {
			backup, err := ed.Save(text)
			if err != nil {
				return nil, err
			}
			e.note("%s updated (previous version kept as %s)", name, filepath.Base(backup))
		}
		if e.JSON {
			doc := plan.RenderPlanJSON(p)
			doc["result"] = map[string]any{"status": "ok", "actions": []any{}}
			e.Out.Object(doc)
			return nil, nil
		}
		msg := m.NoopMessage
		if msg == "" {
			msg = defaultNoop
		}
		e.Out.Line("%s", msg)
		return nil, nil
	}
	if !e.JSON {
		e.Out.Line("%s", plan.RenderPlanText(p, e.header(), e.Quiet))
		for _, l := range m.AfterPlan {
			e.Out.Line("%s", l)
		}
	}
	if blocked := p.Blocked(); len(blocked) > 0 {
		return nil, Errorf(ExitBlocked, "Resolve each change marked ! (its note says how), then run the command again.",
			"Nothing was written to %s or changed in Capella: the plan above has blocked changes (!)", name)
	}
	ok, err := confirm(e.runtime, m.Question)
	if err != nil {
		return nil, err
	}
	if !ok {
		e.markCancelled()
		if e.Ephemeral {
			e.Out.Line("Cancelled; nothing was changed.")
		} else {
			e.Out.Line("Cancelled; %s and Capella are unchanged.", name)
		}
		return nil, nil
	}
	previous := ed.Original
	if text != previous {
		backup, err := ed.Save(text)
		if err != nil {
			return nil, err
		}
		if !e.Ephemeral {
			e.note("%s updated (previous version kept as %s)", name, filepath.Base(backup))
		}
	}
	if noApply {
		e.Out.Line("Not applied (--no-apply). Run 'cbctl capella apply' to deploy it.")
		return p, nil
	}
	before := e.Client.WritesAccepted()
	restore := func(r *plan.ApplyResult) string {
		// Capella accepted nothing, so the file must not describe what was never made: put it back.
		// It is kept when a write may have landed (accepted, connection lost mid-request, or
		// interrupted) because Capella may have changed.
		var transport *capellaapi.TransportError
		var ambiguous *capellaapi.AmbiguousWriteError
		if r.Interrupted || errors.As(r.Err, &transport) || errors.As(r.Err, &ambiguous) ||
			e.Client.WritesAccepted() != before || text == previous || e.Ephemeral {
			return ""
		}
		if err := writeFileAtomic(ed.Path, previous); err != nil {
			return ""
		}
		return fmt.Sprintf("Capella did not accept the change, so %s was restored: nothing was recorded or changed. "+
			"Correct the values and run the command again.", name)
	}
	if err := applyPlan(e, p, applyOptions{Shown: true, OnFailure: restore}); err != nil {
		return p, err
	}
	if m.AfterApply != nil && !e.Ephemeral {
		m.AfterApply(p)
	}
	return p, nil
}

// validateCandidateEnv loads text as the editor's file would be, without writing it. An invalid
// candidate is reported with only the problems this change caused, apart from those the file
// already had.
func validateCandidateEnv(ed *capellafile.Editor, text string, env func(string) string) (*capellafile.Config, error) {
	name, dir := filepath.Base(ed.Path), filepath.Dir(ed.Path)
	if env == nil {
		env = DefaultDeps().Env
	}
	cfg, err := capellafile.LoadText(text, name, dir, env)
	if err == nil {
		cfg.Path = ed.Path
		return cfg, nil
	}
	var ce *capellafile.ConfigError
	if !errors.As(err, &ce) {
		return nil, err
	}
	key := func(i capellafile.Issue) string { return i.Where + "\x00" + i.Message }
	before := map[string]bool{}
	if _, oldErr := capellafile.LoadText(ed.Original, name, dir, env); oldErr != nil {
		var oce *capellafile.ConfigError
		if errors.As(oldErr, &oce) {
			for _, i := range oce.Issues {
				before[key(i)] = true
			}
		}
	}
	var fresh, kept []capellafile.Issue
	for _, i := range ce.Issues {
		if before[key(i)] {
			kept = append(kept, i)
		} else {
			fresh = append(fresh, i)
		}
	}
	problems := func(n int) string {
		if n == 1 {
			return "1 problem"
		}
		return fmt.Sprintf("%d problems", n)
	}
	var msg string
	shown := kept
	if len(fresh) > 0 {
		shown = fresh
		msg = capellafile.RenderIssues("This change is not valid, so nothing was changed:", fresh)
		if len(kept) > 0 {
			msg += "\n" + capellafile.RenderIssues("Already in "+name+" before this command:", kept)
		}
	} else {
		msg = capellafile.RenderIssues(fmt.Sprintf("%s already had %s before this command, so nothing was changed:", name, problems(len(kept))), kept)
	}
	return nil, &CLIError{Code: ExitConfigInvalid, Message: msg, Action: capellafile.FixProblemsFor(shown)}
}

// ---- switching a Phase 2 command to file-managed mode ----------------------------------------

type managedFlags struct{ direct, noApply bool }

// addManagedFlags adds --direct and --no-apply to a resource command.
func addManagedFlags(cmd *cobra.Command) *managedFlags {
	mf := &managedFlags{}
	cmd.Flags().BoolVar(&mf.direct, "direct", false, "Act on Capella only; capella.yaml is not updated")
	cmd.Flags().BoolVar(&mf.noApply, "no-apply", false, "Only record the change in capella.yaml; deploy it later with apply")
	return mf
}

// readAPIFlags reads --token, --org-id and --api-host from cmd.
func readAPIFlags(cmd *cobra.Command) *apiFlags {
	f := &apiFlags{}
	f.token, _ = cmd.Flags().GetString("token")
	f.orgID, _ = cmd.Flags().GetString("org-id")
	f.apiHost, _ = cmd.Flags().GetString("api-host")
	return f
}

// managedRequested says whether the command should edit capella.yaml: the file exists or was
// named explicitly, and --direct was not given.
func managedRequested(cmd *cobra.Command, mf *managedFlags) (bool, error) {
	if mf.direct && mf.noApply {
		return false, usageError("use either --direct or --no-apply", "--direct and --no-apply are mutually exclusive")
	}
	disc, err := discoverConfig(cmd)
	if err != nil {
		return false, err
	}
	managed := disc.Managed() && !mf.direct
	if mf.noApply && !managed {
		return false, usageError("create capella.yaml first ('cbctl capella sync' or 'cbctl capella init'), or drop --no-apply",
			"--no-apply needs capella.yaml")
	}
	if mf.direct && disc.Managed() {
		printNotice(cmd.ErrOrStderr(), "capella.yaml was not updated", "run 'cbctl capella sync' to refresh it from Capella")
	}
	return managed, nil
}

// wrapManaged makes cmd run managed in file-managed mode and its original behaviour otherwise.
func wrapManaged(cmd *cobra.Command, mf *managedFlags, managed func(cmd *cobra.Command, args []string) error) {
	direct := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		on, err := managedRequested(cmd, mf)
		if err != nil {
			return err
		}
		if on {
			return managed(cmd, args)
		}
		return direct(cmd, args)
	}
}

// newManagedEnv opens capella.yaml for editing and builds the runtime. The file is not loaded:
// ChangeAndApply validates the candidate and reports what the change caused.
func newManagedEnv(cmd *cobra.Command) (*envRuntime, *capellafile.Editor, error) {
	disc, err := discoverConfig(cmd)
	if err != nil {
		return nil, nil, err
	}
	if !disc.Exists {
		return nil, nil, notFoundError(displayPathWith(disc.Path, cmd))
	}
	ed, err := capellafile.OpenEditor(disc.Path)
	if err != nil {
		return nil, nil, err
	}
	extra := runtimeExtras{}
	if org, derr := capellafile.DeclaredOrgID(disc.Path, envFrom(cmd)); derr == nil && org != "" {
		extra = runtimeExtras{FileOrgID: org, FileOrgSource: filepath.Base(disc.Path)}
	}
	rt, err := newRuntimeWith(cmd, readAPIFlags(cmd), extra)
	if err != nil {
		return nil, nil, err
	}
	e := &envRuntime{runtime: rt, Disc: disc}
	e.RC = newRunContext(rt, nil)
	return e, ed, nil
}

// notInFile is the failure when a command names a resource capella.yaml does not declare.
func notInFile(kind, name, file string) error {
	return Errorf(ExitConfigInvalid, "run 'cbctl capella sync' to add it", "%s %s is not in %s", kind, name, file)
}

// projectNotInFile is notInFile for a project. When the name is a cluster of the file, the error
// says so and suggests the command with its project (didYouMean gets the project name).
func projectNotInFile(ed *capellafile.Editor, name string, didYouMean func(project string) string) error {
	if proj, node, err := ed.FindCluster(name, ""); err == nil && node != nil && proj != nil {
		if pn := ed.Child(proj, "name"); pn != nil && pn.Value != "" {
			return usageError("Did you mean: "+didYouMean(pn.Value), "%s is a cluster in project %s, not a project", name, pn.Value)
		}
	}
	return Errorf(ExitConfigInvalid, fmt.Sprintf("run 'cbctl capella sync --project %s' to add it", name),
		"project %s is not in %s", name, filepath.Base(ed.Path))
}

// findClusterNode locates a cluster in the file.
func findClusterNode(ed *capellafile.Editor, name, project string) (*yaml.Node, error) {
	_, node, err := ed.FindCluster(name, project)
	var amb *capellafile.AmbiguousError
	if errors.As(err, &amb) {
		return nil, usageError("add --project NAME", "%v", err)
	}
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, notInFile("cluster", name, filepath.Base(ed.Path))
	}
	return node, nil
}

// ---- project create / update / delete -------------------------------------------------------

func managedProjectCreate(description *string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name := args[0]
		mf := managedFlagsOf(cmd)
		cleanup, err := startNamedConfig(cmd)
		if err != nil {
			return err
		}
		defer cleanup()
		e, ed, err := newManagedEnv(cmd)
		if err != nil {
			return err
		}
		_, err = ChangeAndApply(e.Ctx, e, ed, Mutation{
			Target:   "project/" + name,
			Question: fmt.Sprintf("Add project %s to %s and create it in Capella?", name, filepath.Base(ed.Path)),
			Edit: func(ed *capellafile.Editor) error {
				if node, ok := ed.FindProject(name); ok {
					if cmd.Flags().Changed("description") {
						ed.Set(node, []string{"description"}, *description)
					}
					return nil
				}
				obj := capellafile.Obj{"name": name}
				if *description != "" {
					obj["description"] = *description
				}
				ed.AddProject(obj)
				return nil
			},
			NoDescendants: true,
		}, mf.noApply)
		return err
	}
}

// emptyConfig is the file 'project create' starts from when --config names a missing file.
const emptyConfig = "apiVersion: cbctl/v1\nprojects: []\n"

// startNamedConfig lets 'project create' record a first project in a file that --config (or
// CBCTL_CONFIG) names but that does not exist yet, as capellactl does. The returned cleanup
// removes the file again when the command recorded nothing in it (refused, cancelled, failed).
func startNamedConfig(cmd *cobra.Command) (func(), error) {
	disc, err := discoverConfig(cmd)
	if err != nil {
		return nil, err
	}
	if disc.Exists || !disc.Explicit() {
		return func() {}, nil
	}
	if err := os.WriteFile(disc.Path, []byte(emptyConfig), 0o644); err != nil {
		return nil, fmt.Errorf("creating %s: %w", displayPathWith(disc.Path, cmd), err)
	}
	return func() {
		if b, rerr := os.ReadFile(disc.Path); rerr == nil && string(b) == emptyConfig {
			// Best effort: a leftover empty file is harmless and validates.
			_ = os.Remove(disc.Path)
		}
	}, nil
}

func managedProjectUpdate(description *string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !cmd.Flags().Changed("description") {
			return usageError("pass --description", "nothing to update")
		}
		e, ed, err := newManagedEnv(cmd)
		if err != nil {
			return err
		}
		_, err = ChangeAndApply(e.Ctx, e, ed, Mutation{
			Target:   "project/" + name,
			Question: fmt.Sprintf("Record the new description of project %s in %s and apply it?", name, filepath.Base(ed.Path)),
			Edit: func(ed *capellafile.Editor) error {
				node, ok := ed.FindProject(name)
				if !ok {
					return projectNotInFile(ed, name, func(p string) string {
						return fmt.Sprintf("cbctl capella project update %s --description %q", p, *description)
					})
				}
				ed.Set(node, []string{"description"}, *description)
				return nil
			},
			NoDescendants: true,
		}, managedFlagsOf(cmd).noApply)
		return err
	}
}

// managedProjectDelete deletes the project in Capella with the Phase 2 command (cascade rules and
// protection checks included) and then removes it from the file. With --no-apply only the file
// changes.
func managedProjectDelete(direct func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name := args[0]
		mf := managedFlagsOf(cmd)
		disc, err := discoverConfig(cmd)
		if err != nil {
			return err
		}
		if !disc.Exists {
			return notFoundError(displayPath(disc.Path))
		}
		ed, err := capellafile.OpenEditor(disc.Path)
		if err != nil {
			return err
		}
		file := filepath.Base(ed.Path)
		if _, ok := ed.FindProject(name); !ok {
			if mf.noApply {
				return notInFile("project", name, file)
			}
			// capella.yaml has nothing to remove: act on Capella only (a project already gone is a
			// no-op there, so a rerun after the delete succeeds).
			return direct(cmd, args)
		}
		if mf.noApply {
			rt := &runtime{cmd: cmd}
			g, err := readGlobals(cmd)
			if err != nil {
				return err
			}
			rt.Yes, rt.Quiet = g.Yes, g.Quiet
			rt.Out = newRenderer(cmd.OutOrStdout(), cmd.ErrOrStderr(), g.Output == "json", g.Quiet)
			ok, err := confirm(rt, fmt.Sprintf("Remove project %s from %s (Capella keeps it until you delete it)?", name, file))
			if err != nil || !ok {
				return err
			}
			return removeProjectFromFile(cmd, ed, name, true)
		}
		if err := direct(cmd, args); err != nil {
			return err
		}
		if cmd.Annotations[cancelledAnnotation] != "" {
			return nil
		}
		return removeProjectFromFile(cmd, ed, name, false)
	}
}

func removeProjectFromFile(cmd *cobra.Command, ed *capellafile.Editor, name string, announce bool) error {
	file := filepath.Base(ed.Path)
	ed.RemoveProject(name)
	text, err := editedText(ed)
	if err != nil {
		return err
	}
	backup, err := ed.Save(text)
	if err != nil {
		return err
	}
	g, _ := readGlobals(cmd)
	if !g.Quiet && backup != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s updated: removed project %s (previous version kept as %s)\n", file, name, filepath.Base(backup))
	}
	if announce && g.Output != "json" {
		fmt.Fprintf(cmd.OutOrStdout(), "Removed project %s from %s. Capella still has it; delete it with 'cbctl capella project delete %s --direct'.\n", name, file, name)
	}
	return nil
}

func managedFlagsOf(cmd *cobra.Command) *managedFlags {
	mf := &managedFlags{}
	mf.direct, _ = cmd.Flags().GetBool("direct")
	mf.noApply, _ = cmd.Flags().GetBool("no-apply")
	return mf
}

// ---- cluster protect / scale ----------------------------------------------------------------

func managedClusterProtect(onFlag *bool, project *string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name, on := args[0], *onFlag
		e, ed, err := newManagedEnv(cmd)
		if err != nil {
			return err
		}
		word := "on"
		question := fmt.Sprintf("Turn deletion protection on for %s in %s and Capella?", name, filepath.Base(ed.Path))
		if on {
			// Turning protection on only makes things safer: like the direct command (and
			// capellactl) it needs no confirmation.
			e.Yes = true
		}
		if !on {
			word = "off"
			question = fmt.Sprintf("Turn off deletion protection for %s? The cluster and its buckets can then be deleted.", name)
		}
		_, err = ChangeAndApply(e.Ctx, e, ed, Mutation{
			Target:      "cluster/" + name,
			Question:    question,
			NoopMessage: fmt.Sprintf("deletion protection is already %s for %s; nothing to do", word, name),
			Edit: func(ed *capellafile.Editor) error {
				node, err := findClusterNode(ed, name, *project)
				if err != nil {
					return err
				}
				ed.Set(node, []string{"deletionProtection"}, on)
				return nil
			},
		}, managedFlagsOf(cmd).noApply)
		return err
	}
}

// fileGroups reads the service groups of a cluster mapping of the file.
func fileGroups(node *yaml.Node) (groups []capellaapi.ServiceGroup, single bool, err error) {
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return nil, false, err
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, false, err
	}
	var doc struct {
		ConfigurationType string                    `json:"configurationType"`
		ServiceGroups     []capellaapi.ServiceGroup `json:"serviceGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false, err
	}
	return doc.ServiceGroups, doc.ConfigurationType == "singleNode", nil
}

func managedClusterScale(o *scaleOptions) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := scaleFlagsCheck(cmd, o); err != nil {
			return err
		}
		e, ed, err := newManagedEnv(cmd)
		if err != nil {
			return err
		}
		if o.step != 0 {
			return managedClusterStep(e, ed, name, o)
		}
		_, err = ChangeAndApply(e.Ctx, e, ed, Mutation{
			Target:   "cluster/" + name,
			Question: fmt.Sprintf("Record the new node count in %s and apply it to %s?", filepath.Base(ed.Path), name),
			Edit: func(ed *capellafile.Editor) error {
				node, err := findClusterNode(ed, name, o.project)
				if err != nil {
					return err
				}
				groups, single, err := fileGroups(node)
				if err != nil {
					return fmt.Errorf("reading the service groups of %s: %w", name, err)
				}
				chosen, err := selectGroups(groups, o.group, o.allGroups)
				if err != nil {
					return err
				}
				for _, i := range chosen {
					g := groups[i]
					lo, hi := groupLimits(i, single, o.min, o.max)
					if target := o.nodes; target < lo || target > hi {
						return usageError(fmt.Sprintf("choose %d-%d nodes for group %s", lo, hi, groupKey(g)),
							"--nodes %d is outside the limits of group %s (%d-%d)", target, groupKey(g), lo, hi)
					}
					gn, err := ed.Group(node, groupKey(g))
					if err != nil {
						return err
					}
					ed.Set(gn, []string{"numOfNodes"}, int64(o.nodes))
				}
				return nil
			},
			NoopMessage: fmt.Sprintf("%s is already at the requested node count; nothing to do", name),
			Options:     func(rc *plan.RunContext) { rc.SkipCapacityCheck = o.skipCapacity },
		}, managedFlagsOf(cmd).noApply)
		return err
	}
}

// managedClusterStep is 'cluster scale --step' with capella.yaml: one node more or less per group,
// counted from what Capella runs once any running change is done. Groups at their limit are a
// successful no-op that touches nothing; the others are recorded in the file and applied. The
// outcome is reported per group, as one scale result (or a list with --all-groups) under -o json.
func managedClusterStep(e *envRuntime, ed *capellafile.Editor, name string, o *scaleOptions) error {
	p, listed, err := e.resolveCluster(name, o.project)
	if err != nil {
		return err
	}
	if isFreeCluster(listed) {
		return blockedError("create a provisioned cluster for that ('cbctl capella cluster create')", "%s", freeTierBlocked)
	}
	if engine.Transitional[listed.CurrentState] {
		if e.NoWait {
			return blockedError("wait until it is healthy (see 'cbctl capella cluster get "+listed.Name+"'), then run the command again",
				"cluster %s is %s", listed.Name, listed.CurrentState)
		}
		logging.Info("cluster %s is %s; waiting for it before scaling", listed.Name, listed.CurrentState)
		if _, err := e.waitCluster(p.ID, listed.ID, listed.Name, engine.OpReady, false); err != nil {
			return err
		}
		if listed, err = e.readCluster(p.ID, listed.ID, false); err != nil {
			return err
		}
	}
	chosen, err := selectGroups(listed.ServiceGroups, o.group, o.allGroups)
	if err != nil {
		return err
	}
	single := listed.ConfigurationType == "singleNode"
	results := make([]scaleResult, 0, len(chosen))
	moving := false
	for _, i := range chosen {
		g := listed.ServiceGroups[i]
		lo, hi := groupLimits(i, single, o.min, o.max)
		r := scaleResult{Cluster: listed.Name, ClusterID: listed.ID, Group: groupKey(g), From: g.NumOfNodes, To: g.NumOfNodes, step: o.step, Status: "scaled"}
		if to := g.NumOfNodes + o.step; to > hi || to < lo {
			r.Status, r.limit = "at_limit", hi
			if o.step < 0 {
				r.limit = lo
			}
		} else {
			r.To, moving = to, true
		}
		results = append(results, r)
	}
	if !moving {
		e.printScale(results)
		return nil
	}

	var moves []string
	for _, r := range results {
		if r.Status == "scaled" {
			moves = append(moves, fmt.Sprintf("%s %d → %d", r.Group, r.From, r.To))
		}
	}
	out := e.Out
	if e.JSON {
		// The scale result is the document; the plan and apply documents are not printed.
		e.Out = newRenderer(io.Discard, stderrFrom(e.cmd), true, e.Quiet)
	}
	noApply := managedFlagsOf(e.cmd).noApply
	applied, err := ChangeAndApply(e.Ctx, e, ed, Mutation{
		Target:   "cluster/" + name,
		Question: fmt.Sprintf("Apply %s to %s and record it in %s?", strings.Join(moves, ", "), name, filepath.Base(ed.Path)),
		Edit: func(ed *capellafile.Editor) error {
			node, err := findClusterNode(ed, name, o.project)
			if err != nil {
				return err
			}
			for _, r := range results {
				if r.Status != "scaled" {
					continue
				}
				gn, err := ed.Group(node, r.Group)
				if err != nil {
					return usageError("run 'cbctl capella sync' first, then run the command again", "%v", err)
				}
				ed.Set(gn, []string{"numOfNodes"}, int64(r.To))
			}
			return nil
		},
		NoopMessage: fmt.Sprintf("%s is already at the requested node count; nothing to do", name),
		Options:     func(rc *plan.RunContext) { rc.SkipCapacityCheck = o.skipCapacity },
	}, noApply)
	e.Out = out
	var ce *CLIError
	switch {
	case err != nil && errors.As(err, &ce) && ce.Code == ExitBlocked:
		e.printScale(markScale(results, "blocked"))
		return err
	case err != nil:
		return err
	case applied == nil && e.cmd.Annotations[cancelledAnnotation] != "":
		e.printScale(markScale(results, "cancelled"))
		return nil
	case e.NoWait && !noApply:
		for i := range results {
			if results[i].Status == "scaled" {
				results[i].Status = "accepted"
			}
		}
	}
	e.printScale(results)
	return nil
}

// scaleFlagsCheck validates the combination of --nodes and --step.
func scaleFlagsCheck(cmd *cobra.Command, o *scaleOptions) error {
	nodes, step := cmd.Flags().Changed("nodes"), cmd.Flags().Changed("step")
	switch {
	case nodes && step:
		return usageError("use either --nodes or --step", "--nodes and --step are mutually exclusive")
	case !nodes && !step:
		return usageError("pass --nodes N or --step +1|-1", "nothing to do: neither --nodes nor --step was given")
	case nodes && o.nodes < 1:
		return usageError("pass a node count of at least 1", "--nodes must be at least 1")
	case step && o.step != 1 && o.step != -1:
		return usageError("use --step +1 or --step -1", "--step must be +1 or -1, got %d", o.step)
	}
	return nil
}
