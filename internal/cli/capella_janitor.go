package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/ci/janitor"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// janitorResult is the JSON document of the janitor.
type janitorResult struct {
	Prefix   string          `json:"prefix"`
	Status   string          `json:"status"` // deleted | dry-run | nothing | cancelled
	Projects []janitorTarget `json:"projects"`
	Failed   int             `json:"failed,omitempty"`
}

type janitorTarget struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Created string `json:"created,omitempty"`
}

// newCapellaJanitorCmd is the hidden test-cleanup command: it deletes every stale project
// whose name starts with a prefix (live tests name their projects with a run prefix).
func newCapellaJanitorCmd() *cobra.Command {
	var (
		prefix          string
		olderThanHours  float64
		dryRun          bool
		failOnLeftovers bool
		timeoutMinutes  float64
	)
	cmd := capellaLeaf("janitor", "Delete leftover CI projects whose name starts with --prefix", cobra.NoArgs,
		func([]string) error {
			p := strings.TrimSpace(prefix)
			if len(p) < 4 || !strings.HasSuffix(p, "-") {
				return Errorf(ExitConfigInvalid,
					"Use a short unique prefix that ends with '-' (for example cbctl-ci-).",
					"--prefix must end with '-' and have at least 4 characters (got %q)", p)
			}
			return nil
		},
		func(rt *runtime, _ []string) error {
			return rt.runJanitor(strings.TrimSpace(prefix), time.Duration(olderThanHours*float64(time.Hour)),
				dryRun, failOnLeftovers, time.Duration(timeoutMinutes*float64(time.Minute)))
		})
	cmd.Hidden = true
	cmd.Long = "Delete leftover Capella projects whose name starts with --prefix and that are older\n" +
		"than --older-than-hours. Deletion protection is turned off and snapshot backups are discarded.\n\n" +
		"Example:\n  cbctl capella janitor --prefix cbctl-ci- --older-than-hours 24 --yes"
	cmd.Flags().StringVar(&prefix, "prefix", janitor.Prefix, "Project name prefix to delete (must end with '-', at least 4 characters)")
	cmd.Flags().Float64Var(&olderThanHours, "older-than-hours", 24, "Only delete projects older than this many hours")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List leftovers without deleting")
	cmd.Flags().BoolVar(&failOnLeftovers, "fail-on-leftovers", false, "Exit 1 when any leftover project was found")
	cmd.Flags().Float64Var(&timeoutMinutes, "timeout-minutes", 60, "Per-project delete timeout in minutes")
	return cmd
}

func (rt *runtime) runJanitor(prefix string, olderThan time.Duration, dryRun, failOnLeftovers bool, timeout time.Duration) error {
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	now := rt.Clock.Now()
	if now.IsZero() {
		now = clockFactory().Now().UTC()
	}
	found := janitor.Stale(projects, prefix, olderThan, now)
	res := janitorResult{Prefix: prefix, Status: "nothing", Projects: []janitorTarget{}}
	for _, p := range found {
		created := "unknown"
		if p.Audit != nil && p.Audit.CreatedAt != "" {
			created = p.Audit.CreatedAt
		}
		res.Projects = append(res.Projects, janitorTarget{Project: p.Name, ID: p.ID, Created: created})
	}
	if len(found) == 0 {
		rt.report(res, "no %s* projects older than %g hours", prefix, olderThan.Hours())
		return nil
	}
	for _, t := range res.Projects {
		rt.Out.Line("leftover project %s (created %s)", t.Project, t.Created)
	}
	if dryRun {
		res.Status = "dry-run"
		rt.report(res, "%d leftover project(s), 0 failed", len(found))
		if failOnLeftovers {
			return Errorf(ExitError, "", "%d leftover project(s) found", len(found))
		}
		return nil
	}
	ok, err := confirm(rt, fmt.Sprintf("Delete %d leftover project(s) starting with %q and all their clusters and data?", len(found), prefix))
	if err != nil {
		return err
	}
	if !ok {
		res.Status = "cancelled"
		rt.report(res, "cancelled; nothing was deleted")
		return nil
	}

	failed := 0
	for _, p := range found {
		steps, err := janitor.DeleteProjectTree(rt.Ctx, rt.Client, p, timeout, rt.Clock)
		for _, s := range steps {
			rt.Out.Line("  %s", s)
		}
		if err != nil {
			failed++
			logging.Error("%s", err.Error())
			fmt.Fprintf(rt.errOut(), "  FAILED: %v\n", err)
		}
	}
	res.Failed = failed
	res.Status = "deleted"
	rt.report(res, "%d leftover project(s) deleted, %d failed", len(found), failed)
	if failed > 0 || failOnLeftovers {
		return Errorf(ExitError, "", "%d leftover project(s) deleted, %d failed", len(found), failed)
	}
	return nil
}
