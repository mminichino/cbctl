package cli

import (
	"errors"
	"fmt"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

func newCapellaProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Capella project operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaProjectListCmd())
	cmd.AddCommand(newCapellaProjectGetCmd())
	cmd.AddCommand(newCapellaProjectCreateCmd())
	cmd.AddCommand(newCapellaProjectUpdateCmd())
	cmd.AddCommand(newCapellaProjectDeleteCmd())
	cmd.AddCommand(newHiddenApplyAlias("project"))
	return cmd
}

func newCapellaProjectListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		projects, err := rt.Client.ListProjects(rt.Ctx)
		if err != nil {
			return fmt.Errorf("listing projects: %w", err)
		}
		if rt.JSON {
			rt.Out.List(projects)
			return nil
		}
		rows := make([][]string, 0, len(projects))
		for _, p := range projects {
			rows = append(rows, []string{p.Name, p.ID, p.Description})
		}
		rt.Out.Table([]string{"NAME", "ID", "DESCRIPTION"}, rows)
		return nil
	}
	return cmd
}

func newCapellaProjectGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get NAME",
		Short: "Show one project",
		Args:  cobra.ExactArgs(1),
	}
	af := addAPIFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		p, err := rt.resolveProject(args[0])
		if err != nil {
			return err
		}
		rt.Out.Object(p)
		return nil
	}
	return cmd
}

func newCapellaProjectCreateCmd() *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
	}
	af := addAPIFlags(cmd)
	cmd.Flags().StringVar(&description, "description", "", "Project description")
	defer func() { wrapManaged(cmd, addManagedFlags(cmd), managedProjectCreate(&description)) }()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		name := args[0]
		// Capella allows two projects with the same name, so a 409 cannot be relied on: look
		// first, and never create a second project the name would then not identify.
		projects, err := rt.Client.ListProjects(rt.Ctx)
		if err != nil {
			return fmt.Errorf("listing projects: %w", err)
		}
		for _, p := range projects {
			if p.Name == name {
				return projectExistsError(name)
			}
		}
		id, err := rt.Client.CreateProject(rt.Ctx, name, description)
		if err != nil {
			if isAmbiguous(err) {
				// The request may have been applied: re-observe by name once.
				if existing := rt.findProject(name); existing != nil {
					id, err = existing.ID, nil
				}
			}
			if errors.Is(err, capellaapi.ErrConflict) {
				return projectExistsError(name)
			}
			if err != nil {
				return fmt.Errorf("creating project %s: %w", name, err)
			}
		}
		rt.report(capellaapi.Project{ID: id, Name: name, Description: description},
			"created project %s (%s)", name, id)
		return nil
	}
	return cmd
}

// projectExistsError refuses to create a second project with a name that is taken (exit 4, as
// capellactl): Capella would accept it, and the name would then identify neither.
func projectExistsError(name string) error {
	return Errorf(ExitBlocked, fmt.Sprintf("run 'cbctl capella sync --project %s' to add it to capella.yaml, or choose another name", name),
		"project %s already exists", name)
}

// findProject returns the project with this exact name, or nil when it is not listed or the
// listing fails.
func (r *runtime) findProject(name string) *capellaapi.Project {
	projects, err := r.Client.ListProjects(r.Ctx)
	if err != nil {
		return nil
	}
	for i := range projects {
		if projects[i].Name == name {
			return &projects[i]
		}
	}
	return nil
}

func newCapellaProjectUpdateCmd() *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Update a project",
		Args:  cobra.ExactArgs(1),
	}
	af := addAPIFlags(cmd)
	cmd.Flags().StringVar(&description, "description", "", "New project description")
	defer func() { wrapManaged(cmd, addManagedFlags(cmd), managedProjectUpdate(&description)) }()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("description") {
			return usageError("pass --description", "nothing to update")
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		p, err := rt.resolveProject(args[0])
		if err != nil {
			return err
		}
		if err := rt.updateProject(p.ID, description); err != nil {
			return fmt.Errorf("updating project %s: %w", p.Name, err)
		}
		rt.report(capellaapi.Project{ID: p.ID, Name: p.Name, Description: description}, "updated project %s", p.Name)
		return nil
	}
	return cmd
}

// updateProject sets the description with a read-modify-write on the project's ETag. A stale
// version (412) is re-read and retried once.
func (r *runtime) updateProject(id, description string) error {
	for attempt := 0; ; attempt++ {
		cur, etag, err := r.Client.GetProject(r.Ctx, id)
		if err != nil {
			return err
		}
		err = r.Client.UpdateProject(r.Ctx, id, cur.Name, description, etag)
		if errors.Is(err, capellaapi.ErrPreconditionFailed) && attempt == 0 {
			logging.Debug("project %s changed while updating; retrying", cur.Name)
			continue
		}
		return err
	}
}

// projectDeleteDoc is the JSON document of project delete.
type projectDeleteDoc struct {
	Project         string   `json:"project"`
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	DeletedClusters []string `json:"deletedClusters"`
}

func newCapellaProjectDeleteCmd() *cobra.Command {
	var cascade, discardSnapshots bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a project (--cascade deletes its clusters first)",
		Args:  cobra.ExactArgs(1),
	}
	af := addAPIFlags(cmd)
	cmd.Flags().BoolVar(&cascade, "cascade", false, "Delete the project's clusters first")
	cmd.Flags().BoolVar(&discardSnapshots, "discard-snapshots", false, "With --cascade, also discard the clusters' snapshot backups")
	defer func() { wrapManaged(cmd, addManagedFlags(cmd), managedProjectDelete(cmd.RunE)) }()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		projects, err := rt.Client.ListProjects(rt.Ctx)
		if err != nil {
			return fmt.Errorf("listing projects: %w", err)
		}
		if !hasProjectNamed(projects, args[0]) {
			// Deleting what is already gone is not an error, so a rerun (or a script) can repeat it.
			rt.report(projectDeleteDoc{Project: args[0], Status: "absent", DeletedClusters: []string{}},
				"project %s not found; nothing to do", args[0])
			return nil
		}
		p, err := pickProject(projects, args[0])
		if err != nil {
			return err
		}
		var clusters []capellaapi.Cluster
		if cascade {
			if clusters, err = rt.Client.ListClusters(rt.Ctx, p.ID); err != nil {
				return fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
			}
			// Abort before anything is deleted.
			for i := range clusters {
				t := &clusterTarget{Project: p, Cluster: &clusters[i]}
				if err := t.refuseIfProtected(); err != nil {
					return err
				}
			}
		}
		// Deleting a project cannot be undone, so the name is typed as for cluster delete.
		if cascade {
			rt.note("This deletes project %s and its %d cluster(s) with all their data.", p.Name, len(clusters))
		}
		ok, err := confirmTyped(rt, "Type the project name to confirm deletion", p.Name)
		if err != nil {
			return err
		}
		if !ok {
			rt.markCancelled()
			rt.report(statusDoc{Status: "cancelled"}, "cancelled; nothing was deleted")
			return nil
		}

		doc := projectDeleteDoc{Project: p.Name, ID: p.ID, Status: "deleted", DeletedClusters: []string{}}
		for i := range clusters {
			c := &clusters[i]
			if err := rt.deleteClusterForProject(p, c, discardSnapshots); err != nil {
				return err
			}
			doc.DeletedClusters = append(doc.DeletedClusters, c.Name)
		}
		if rt.NoWait && len(clusters) > 0 {
			// The project cannot be deleted until the clusters are gone.
			doc.Status = "accepted"
			rt.report(doc, "cluster deletion accepted; delete project %s once the clusters are gone", p.Name)
			return nil
		}

		if err := rt.Client.DeleteProject(rt.Ctx, p.ID); err != nil {
			if errors.Is(err, capellaapi.ErrConflict) {
				return Errorf(ExitError, "delete its clusters first or pass --cascade", "project %s is not empty", p.Name)
			}
			return fmt.Errorf("deleting project %s: %w", p.Name, err)
		}
		rt.report(doc, "deleted project %s", p.Name)
		return nil
	}
	return cmd
}

// hasProjectNamed reports whether name is the name (or, when it looks like one, the ID) of a
// listed project.
func hasProjectNamed(projects []capellaapi.Project, name string) bool {
	for _, p := range projects {
		if p.Name == name || (uuidPattern.MatchString(name) && p.ID == name) {
			return true
		}
	}
	return false
}

// deleteClusterForProject deletes one cluster of a project being deleted and, unless --no-wait,
// waits for it to disappear.
func (r *runtime) deleteClusterForProject(p *capellaapi.Project, c *capellaapi.Cluster, discardSnapshots bool) error {
	var err error
	if isFreeCluster(c) {
		err = r.Client.DeleteFreeCluster(r.Ctx, p.ID, c.ID)
	} else {
		err = r.Client.DeleteCluster(r.Ctx, p.ID, c.ID, !discardSnapshots)
	}
	if err != nil {
		if errors.Is(err, capellaapi.ErrNotFound) {
			return nil // already gone
		}
		return fmt.Errorf("deleting cluster %s: %w", c.Name, err)
	}
	logging.Info("delete accepted for cluster %s", c.Name)
	if r.NoWait {
		return nil
	}
	_, err = r.waitCluster(p.ID, c.ID, c.Name, engine.OpDelete, isFreeCluster(c))
	return err
}
