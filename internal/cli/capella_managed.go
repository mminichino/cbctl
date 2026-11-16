package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
}

const defaultNoop = "No changes: capella.yaml and Capella already match."

// ChangeAndApply edits the file, validates the candidate, plans the change, asks, saves the file
// and applies the plan. It returns the plan, or nil when nothing ran.
func ChangeAndApply(ctx context.Context, e *envRuntime, ed *capellafile.Editor, m Mutation, noApply bool) (*plan.Plan, error) {
	if err := m.Edit(ed); err != nil {
		return nil, err
	}
	text, err := ed.Text()
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
		if text != ed.Original {
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
		e.Out.Line("Cancelled; %s and Capella are unchanged.", name)
		return nil, nil
	}
	previous := ed.Original
	if text != previous {
		backup, err := ed.Save(text)
		if err != nil {
			return nil, err
		}
		e.note("%s updated (previous version kept as %s)", name, filepath.Base(backup))
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
			e.Client.WritesAccepted() != before || text == previous {
			return ""
		}
		if err := writeFileAtomic(ed.Path, previous); err != nil {
			return ""
		}
		return fmt.Sprintf("Capella did not accept the change, so %s was restored: nothing was recorded or changed. "+
			"Correct the values and run the command again.", name)
	}
	return p, applyPlan(e, p, applyOptions{Shown: true, OnFailure: restore})
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
	if len(fresh) > 0 {
		msg = capellafile.RenderIssues("This change is not valid, so nothing was changed:", fresh)
		if len(kept) > 0 {
			msg += "\n" + capellafile.RenderIssues("Already in "+name+" before this command:", kept)
		}
	} else {
		msg = capellafile.RenderIssues(fmt.Sprintf("%s already had %s before this command, so nothing was changed:", name, problems(len(kept))), kept)
	}
	return nil, &CLIError{Code: ExitConfigInvalid, Message: msg, Action: capellafile.FixProblems}
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
					return notInFile("project", name, filepath.Base(ed.Path))
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
			return notInFile("project", name, file)
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
	text, err := ed.Text()
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
		var noop []string
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
				if o.step != 0 {
					// The step starts from what Capella runs now, when the cluster exists there.
					if _, listed, lerr := e.resolveCluster(name, o.project); lerr == nil && len(listed.ServiceGroups) == len(groups) {
						groups = listed.ServiceGroups
					}
				}
				chosen, err := selectGroups(groups, o.group, o.allGroups)
				if err != nil {
					return err
				}
				for _, i := range chosen {
					g := groups[i]
					lo, hi := groupLimits(i, single, o.min, o.max)
					target := o.nodes
					if o.step != 0 {
						target = g.NumOfNodes + o.step
						if target > hi || target < lo {
							noop = append(noop, fmt.Sprintf("%s %s is at its limit of %d nodes", name, groupKey(g), g.NumOfNodes))
							continue
						}
					} else if target < lo || target > hi {
						return usageError(fmt.Sprintf("choose %d-%d nodes for group %s", lo, hi, groupKey(g)),
							"--nodes %d is outside the limits of group %s (%d-%d)", target, groupKey(g), lo, hi)
					}
					gn, err := ed.Group(node, groupKey(g))
					if err != nil {
						return err
					}
					ed.Set(gn, []string{"numOfNodes"}, int64(target))
				}
				return nil
			},
			NoopMessage: fmt.Sprintf("%s is already at the requested node count; nothing to do", name),
			Options:     func(rc *plan.RunContext) { rc.SkipCapacityCheck = o.skipCapacity },
		}, managedFlagsOf(cmd).noApply)
		if err == nil && len(noop) > 0 {
			for _, n := range noop {
				e.note("%s", n)
			}
		}
		return err
	}
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
