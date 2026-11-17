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
	Prefix     string          `json:"prefix"`
	Status     string          `json:"status"` // deleted | dry-run | nothing | cancelled
	Projects   []janitorTarget `json:"projects"`
	Failed     int             `json:"failed,omitempty"`
	Skipped    []string        `json:"skipped,omitempty"`    // not attempted: --max-total-minutes ran out
	Unreadable []string        `json:"unreadable,omitempty"` // kept: no readable audit.createdAt
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
		totalMinutes    float64
	)
	cmd := capellaLeaf("janitor", "Delete leftover CI projects whose name starts with --prefix", cobra.NoArgs,
		func([]string) error {
			p := strings.TrimSpace(prefix)
			if len(p) < 4 || !strings.HasSuffix(p, "-") {
				return Errorf(ExitConfigInvalid,
					"Use a short unique prefix that ends with '-' (for example cbctl-ci-).",
					"--prefix must end with '-' and have at least 4 characters (got %q)", p)
			}
			if timeoutMinutes <= 0 || totalMinutes < 0 {
				return Errorf(ExitConfigInvalid, "Pass --timeout-minutes 1 or more and --max-total-minutes 0 or more.", "--timeout-minutes must be positive and --max-total-minutes not negative")
			}
			return nil
		},
		func(rt *runtime, _ []string) error {
			return rt.runJanitor(janitorOptions{
				prefix:          strings.TrimSpace(prefix),
				olderThan:       time.Duration(olderThanHours * float64(time.Hour)),
				dryRun:          dryRun,
				failOnLeftovers: failOnLeftovers,
				timeout:         time.Duration(timeoutMinutes * float64(time.Minute)),
				budget:          time.Duration(totalMinutes * float64(time.Minute)),
			})
		})
	cmd.Hidden = true
	cmd.Long = "Delete leftover Capella projects whose name starts with --prefix and that are older\n" +
		"than --older-than-hours. Deletion protection is turned off and snapshot backups are discarded.\n" +
		"It never touches a project whose name does not start with --prefix. Projects whose creation\n" +
		"time cannot be read are kept and listed. One project that fails does not stop the others;\n" +
		"any failure exits 1. --max-total-minutes bounds the whole run: projects not reached are\n" +
		"reported as skipped (exit 1).\n\n" +
		"Examples:\n  cbctl capella janitor --prefix cbctl-ci- --older-than-hours 24 --yes\n" +
		"  cbctl capella janitor --prefix cbctl-ci-123-1- --older-than-hours 0 --max-total-minutes 20 --yes   # one run's leftovers"
	cmd.Flags().StringVar(&prefix, "prefix", janitor.Prefix, "Project name prefix to delete (must end with '-', at least 4 characters)")
	cmd.Flags().Float64Var(&olderThanHours, "older-than-hours", 24, "Only delete projects older than this many hours")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List leftovers without deleting")
	cmd.Flags().BoolVar(&failOnLeftovers, "fail-on-leftovers", false, "Exit 1 when any leftover project was found")
	cmd.Flags().Float64Var(&timeoutMinutes, "timeout-minutes", 60, "Per-project delete timeout in minutes")
	cmd.Flags().Float64Var(&totalMinutes, "max-total-minutes", 0, "Budget for the whole run in minutes (0: no limit); projects not reached are skipped")
	return cmd
}

// janitorOptions are the janitor's flags, validated.
type janitorOptions struct {
	prefix          string
	olderThan       time.Duration
	dryRun          bool
	failOnLeftovers bool
	timeout         time.Duration // per project
	budget          time.Duration // whole run; 0 = none
}

func (rt *runtime) runJanitor(o janitorOptions) error {
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	now := rt.Clock.Now()
	if now.IsZero() {
		now = clockFactory().Now().UTC()
	}
	found := janitor.Stale(projects, o.prefix, o.olderThan, now)
	res := janitorResult{Prefix: o.prefix, Status: "nothing", Projects: []janitorTarget{}}
	for _, p := range janitor.Unreadable(projects, o.prefix) {
		res.Unreadable = append(res.Unreadable, p.Name)
		logging.Warning("kept %s: its creation time cannot be read; delete it by hand if it is a leftover", p.Name)
	}
	for _, p := range found {
		created := "unknown"
		if p.Audit != nil && p.Audit.CreatedAt != "" {
			created = p.Audit.CreatedAt
		}
		res.Projects = append(res.Projects, janitorTarget{Project: p.Name, ID: p.ID, Created: created})
	}
	if len(found) == 0 {
		rt.report(res, "no %s* projects older than %g hours", o.prefix, o.olderThan.Hours())
		return nil
	}
	for _, t := range res.Projects {
		rt.Out.Line("leftover project %s (created %s)", t.Project, t.Created)
	}
	if o.dryRun {
		res.Status = "dry-run"
		rt.report(res, "%d leftover project(s), 0 failed", len(found))
		if o.failOnLeftovers {
			return Errorf(ExitError, "Run the janitor without --dry-run to delete them.", "%d leftover project(s) found", len(found))
		}
		return nil
	}
	ok, err := confirm(rt, fmt.Sprintf("Delete %d leftover project(s) starting with %q and all their clusters and data?", len(found), o.prefix))
	if err != nil {
		return err
	}
	if !ok {
		res.Status = "cancelled"
		rt.report(res, "cancelled; nothing was deleted")
		return nil
	}

	start := rt.Clock.Monotonic()
	failed := 0
	for i, p := range found {
		timeout := o.timeout
		if o.budget > 0 {
			left := o.budget - (rt.Clock.Monotonic() - start)
			if left <= 0 {
				for _, rest := range found[i:] {
					res.Skipped = append(res.Skipped, rest.Name)
					rt.Out.Line("  skipped %s: --max-total-minutes reached", rest.Name)
				}
				break
			}
			timeout = min(timeout, left)
		}
		steps, err := janitor.DeleteProjectTree(rt.Ctx, rt.Client, p, timeout, rt.Clock)
		for _, s := range steps {
			rt.Out.Line("  %s", s)
		}
		if err != nil {
			failed++
			logging.Error("%s", err.Error())
			fmt.Fprintf(rt.errOut(), "  FAILED: %v\n", err)
			if rt.Ctx.Err() != nil {
				break
			}
		}
	}
	res.Failed = failed
	res.Status = "deleted"
	deleted := len(found) - failed - len(res.Skipped)
	rt.report(res, "%d leftover project(s) deleted, %d failed, %d skipped", deleted, failed, len(res.Skipped))
	if failed > 0 || len(res.Skipped) > 0 || o.failOnLeftovers {
		return Errorf(ExitError, "Run the janitor again; the projects listed above as failed or skipped are still there.", "%d leftover project(s) deleted, %d failed, %d skipped", deleted, failed, len(res.Skipped))
	}
	return nil
}
