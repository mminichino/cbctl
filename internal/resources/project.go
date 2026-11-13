package resources

import (
	"context"
	"fmt"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
)

const kindProject = "project"

// projectRec reconciles projects. The natural key is the name; a pinned id wins over it.
type projectRec struct {
	plan.Base
	all []plan.Observed
}

func newProject(children ...plan.Reconciler) *projectRec {
	r := &projectRec{Base: plan.NewBase(kindProject, plan.Flags{}, children...)}
	r.Bind(r)
	return r
}

// DesiredItems returns one item per project of the file, in both the projects: and the
// single-project form: the project mapping without "clusters" plus "_clusters".
func (r *projectRec) DesiredItems(parent any) []any {
	cfg, ok := parent.(*capellafile.Config)
	if !ok || cfg == nil {
		return nil
	}
	var items []any
	for _, entry := range cfg.Projects() {
		d := Obj{}
		for k, v := range entry.Data {
			if k != "clusters" {
				d[k] = v
			}
		}
		d["_clusters"] = entry.Clusters
		items = append(items, d)
	}
	return items
}

func (r *projectRec) Key(desired any, _ *plan.Action) string {
	return fmt.Sprint(objOf(desired)["name"])
}

func (r *projectRec) Observe(ctx context.Context, rc *plan.RunContext, _ *plan.Action) ([]plan.Observed, error) {
	api, err := newRawAPI(rc)
	if err != nil {
		return nil, err
	}
	projects, err := api.list(ctx, api.projects(), "data")
	if err != nil {
		return nil, err
	}
	r.all = make([]plan.Observed, 0, len(projects))
	for _, p := range projects {
		r.all = append(r.all, plan.Observed{ID: strOf(p, "id"), Body: p})
	}
	return r.all, nil
}

// renamedFrom returns (project, cluster) when a Capella project that is not in the file holds one
// of this project's declared clusters: the project was probably renamed in the Capella UI.
func (r *projectRec) renamedFrom(ctx context.Context, rc *plan.RunContext, desired Obj) (string, string, error) {
	declared := map[string]bool{}
	for _, c := range objsOf(desired["_clusters"]) {
		if n, ok := c["name"].(string); ok {
			declared[n] = true
		}
	}
	if len(declared) == 0 {
		return "", "", nil
	}
	inFile := map[string]bool{}
	if cfg, ok := rc.Config.(*capellafile.Config); ok && cfg != nil {
		for _, name := range cfg.ProjectNames() {
			inFile[name] = true
		}
	}
	api, err := newRawAPI(rc)
	if err != nil {
		return "", "", err
	}
	for _, o := range r.all {
		name := strOf(o.Body, "name")
		if inFile[name] {
			continue
		}
		clusters, err := api.list(ctx, api.clusters(o.ID), "data")
		if err != nil {
			return "", "", err
		}
		for _, c := range clusters {
			if n := strOf(c, "name"); declared[n] {
				return name, n, nil
			}
		}
	}
	return "", "", nil
}

// Match prefers a pinned id (which must exist) over the name.
func (r *projectRec) Match(desired any, observed []plan.Observed) ([]plan.Observed, error) {
	d := objOf(desired)
	if pinned := strOf(d, "id"); pinned != "" {
		for _, o := range observed {
			if o.ID == pinned {
				return []plan.Observed{o}, nil
			}
		}
		return nil, &plan.PlanError{Msg: fmt.Sprintf("project %s not found", pinned)}
	}
	return plan.MatchByName(d, observed), nil
}

func (r *projectRec) Diff(ctx context.Context, rc *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if observed == nil {
		if strOf(desired, "id") == "" {
			project, cluster, err := r.renamedFrom(ctx, rc, desired)
			if err != nil {
				return nil, err
			}
			if project != "" {
				a := newAction(r, kindProject, key, plan.Blocked, desired, nil, parent)
				a.Topic = "idempotency"
				a.Reason = fmt.Sprintf("no project named %s; project %s has its cluster %s, so it was probably renamed in Capella. "+
					"Run 'cbctl capella sync' to adopt the new name, or rename it back in Capella (apply would create a second project)",
					key, project, cluster)
				return a, nil
			}
		}
		return newAction(r, kindProject, key, plan.Create, desired, nil, parent), nil
	}
	skip := map[string]bool{"_clusters": true, "id": true}
	if strOf(desired, "id") == "" {
		skip["name"] = true
	}
	changes := plan.FieldChanges(desired, observed.Body, "", skip)
	t := plan.Noop
	if len(changes) > 0 {
		t = plan.Update
	}
	a := newAction(r, kindProject, key, t, desired, observed, parent)
	a.Changes = changes
	for _, c := range changes {
		if c.Path == "name" {
			a.Notes = append(a.Notes, plan.RenamedNote)
			break
		}
	}
	return a, nil
}

// DeleteAction is the destroy step for a project, applied last.
func (r *projectRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	name := strOf(o.Body, "name")
	obs := o
	return newAction(r, kindProject, name, plan.Delete, Obj{"name": o.Body["name"]}, &obs, parent), nil
}

func (r *projectRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	d := objOf(a.Desired)
	switch a.Type {
	case plan.Delete:
		if a.Observed == nil || a.Observed.ID == "" {
			return fmt.Errorf("project %s has no id to delete", a.Key)
		}
		return rc.Client.DeleteProject(ctx, a.Observed.ID)
	case plan.Create:
		description := ""
		if v := d["description"]; v != nil {
			description = fmt.Sprint(v)
		}
		id, err := rc.Client.CreateProject(ctx, strOf(d, "name"), description)
		if err != nil {
			return err
		}
		a.CreatedID = id
	case plan.Update:
		if a.Observed == nil || a.Observed.ID == "" {
			return fmt.Errorf("project %s has no id to update", a.Key)
		}
		api, err := newRawAPI(rc)
		if err != nil {
			return err
		}
		current, etag, err := api.get(ctx, api.project(a.Observed.ID))
		if err != nil {
			return err
		}
		name := strOf(current, "name")
		if name == "" || strOf(d, "id") != "" {
			name = strOf(d, "name")
		}
		description := fmt.Sprint(orEmpty(current["description"]))
		if has(d, "description") {
			description = fmt.Sprint(orEmpty(d["description"]))
		}
		return rc.Client.UpdateProject(ctx, a.Observed.ID, name, description, etag)
	}
	return nil
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}
