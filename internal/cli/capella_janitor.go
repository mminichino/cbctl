package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// minJanitorPrefix is the shortest --prefix the janitor accepts, so a typo cannot match everything.
const minJanitorPrefix = 3

// janitorResult is the JSON document of the janitor.
type janitorResult struct {
	Prefix   string          `json:"prefix"`
	Status   string          `json:"status"` // deleted | accepted | nothing | cancelled
	Projects []janitorTarget `json:"projects"`
}

type janitorTarget struct {
	Project  string   `json:"project"`
	ID       string   `json:"id"`
	Clusters []string `json:"clusters"`
}

// newCapellaJanitorCmd is the hidden test-cleanup command: it deletes every project whose name
// starts with a prefix, together with its clusters (live tests name their projects with a run
// prefix, so a crashed run leaves only prefixed leftovers).
func newCapellaJanitorCmd() *cobra.Command {
	var prefix string
	cmd := capellaLeaf("janitor", "Delete every project whose name starts with --prefix, with its clusters (test cleanup)", cobra.NoArgs,
		func([]string) error {
			if len(strings.TrimSpace(prefix)) < minJanitorPrefix {
				return usageError(fmt.Sprintf("pass --prefix with at least %d characters", minJanitorPrefix), "--prefix is required and must have at least %d characters", minJanitorPrefix)
			}
			return nil
		},
		func(rt *runtime, _ []string) error { return rt.runJanitor(strings.TrimSpace(prefix)) })
	cmd.Hidden = true
	cmd.Long = "Delete every project whose name starts with --prefix, after deleting its clusters (deletion protection is\n" +
		"turned off and snapshot backups are discarded). Meant for cleaning up after live tests.\n\n" +
		"Example:\n  cbctl capella janitor --prefix cbctl-it- --yes"
	cmd.Flags().StringVar(&prefix, "prefix", "", "Project name prefix to delete (at least 3 characters)")
	return cmd
}

func (rt *runtime) runJanitor(prefix string) error {
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	res := janitorResult{Prefix: prefix, Status: "nothing", Projects: []janitorTarget{}}
	clusters := map[string][]capellaapi.Cluster{}
	var matched []capellaapi.Project
	for _, p := range projects {
		if !strings.HasPrefix(p.Name, prefix) {
			continue
		}
		cs, err := rt.Client.ListClusters(rt.Ctx, p.ID)
		if err != nil {
			return fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		matched = append(matched, p)
		clusters[p.ID] = cs
		names := make([]string, len(cs))
		for i, c := range cs {
			names[i] = c.Name
		}
		res.Projects = append(res.Projects, janitorTarget{Project: p.Name, ID: p.ID, Clusters: names})
	}
	if len(matched) == 0 {
		rt.report(res, "no project starts with %q; nothing to do", prefix)
		return nil
	}
	for _, t := range res.Projects {
		rt.Out.Line("  - project %s  clusters: %s", t.Project, joinOrNone(t.Clusters))
	}
	ok, err := confirm(rt, fmt.Sprintf("Delete %d project(s) starting with %q and all their clusters and data?", len(matched), prefix))
	if err != nil {
		return err
	}
	if !ok {
		res.Status = "cancelled"
		rt.report(res, "cancelled; nothing was deleted")
		return nil
	}

	// Start every cluster delete first so the clusters are destroyed in parallel, then wait.
	type pending struct {
		project capellaapi.Project
		cluster capellaapi.Cluster
	}
	var waits []pending
	for _, p := range matched {
		for _, c := range clusters[p.ID] {
			if err := rt.janitorDeleteCluster(&p, &c); err != nil {
				return err
			}
			waits = append(waits, pending{p, c})
		}
	}
	if rt.NoWait {
		res.Status = "accepted"
		rt.report(res, "deletion accepted; delete the projects once their clusters are gone")
		return nil
	}
	for _, w := range waits {
		if _, err := rt.waitCluster(w.project.ID, w.cluster.ID, w.cluster.Name, engine.OpDelete, isFreeCluster(&w.cluster)); err != nil {
			return err
		}
	}
	for _, p := range matched {
		if err := rt.Client.DeleteProject(rt.Ctx, p.ID); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
			return fmt.Errorf("deleting project %s: %w", p.Name, err)
		}
		logging.Info("deleted project %s", p.Name)
	}
	res.Status = "deleted"
	rt.report(res, "deleted %d project(s) starting with %q", len(matched), prefix)
	return nil
}

// janitorDeleteCluster turns deletion protection off and starts the delete.
func (rt *runtime) janitorDeleteCluster(p *capellaapi.Project, c *capellaapi.Cluster) error {
	if c.CurrentState == "destroying" {
		return nil // already on its way out
	}
	var err error
	if isFreeCluster(c) {
		err = rt.Client.DeleteFreeCluster(rt.Ctx, p.ID, c.ID)
	} else {
		if c.DeletionProtection != nil && *c.DeletionProtection {
			if err := rt.Client.SetDeletionProtection(rt.Ctx, p.ID, c.ID, false); err != nil {
				return fmt.Errorf("turning off deletion protection of %s: %w", c.Name, err)
			}
		}
		err = rt.Client.DeleteCluster(rt.Ctx, p.ID, c.ID, false)
	}
	if err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
		return fmt.Errorf("deleting cluster %s: %w", c.Name, conflictAsBlocked(err, c.Name))
	}
	logging.Info("delete accepted for cluster %s", c.Name)
	return nil
}

func joinOrNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
