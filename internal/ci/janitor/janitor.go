// Package janitor deletes leftover Capella CI projects and their clusters
// (CAP-032 AC3).
package janitor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
)

// Prefix is the default project-name prefix for nightly leftovers.
const Prefix = "cbctl-ci-"

// RetryEvery is how long DeleteProjectTree waits between delete attempts when
// Capella refuses (for example while a cluster is still deploying or scaling).
const RetryEvery = 60 * time.Second

// fracTrim cuts fractional seconds longer than 9 digits so time.Parse can read
// Capella timestamps that carry nanosecond-or-more precision.
var fracTrim = regexp.MustCompile(`(\.\d{9})\d+`)

// Stale returns projects whose name starts with prefix and whose
// audit.createdAt is older than olderThan relative to now. A project without a
// readable createdAt is kept (it may belong to a run in progress) and a warning
// is logged.
func Stale(projects []capellaapi.Project, prefix string, olderThan time.Duration, now time.Time) []capellaapi.Project {
	var out []capellaapi.Project
	for _, p := range projects {
		if !strings.HasPrefix(p.Name, prefix) {
			continue
		}
		born, ok := createdAt(p)
		if !ok {
			log.Printf("skipping %s: no readable audit.createdAt", p.Name)
			continue
		}
		if now.Sub(born) > olderThan {
			out = append(out, p)
		}
	}
	return out
}

// DeleteProjectTree deletes one project and everything in it. For each cluster it turns
// deletion protection off when set and deletes it (free tier via DeleteFreeCluster, others via
// DeleteCluster without keeping snapshot backups), retrying every RetryEvery until timeout. It
// waits for the deletes, then deletes the project with the same retry.
//
// It is best effort: a cluster that cannot be deleted does not stop the others, and the project
// delete is still tried once. Every failure is returned, joined. A resource that is already
// gone (404) counts as deleted, so a sweep racing a test's own cleanup is harmless. The steps
// are returned in order.
func DeleteProjectTree(ctx context.Context, c *capellaapi.Client, p capellaapi.Project, timeout time.Duration, clk engine.Clock) ([]string, error) {
	if clk == nil {
		clk = engine.RealClock{}
	}
	var done []string
	clusters, err := c.ListClusters(ctx, p.ID)
	if err != nil {
		if errors.Is(err, capellaapi.ErrNotFound) {
			return done, nil
		}
		return done, fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
	}
	var errs []error
	deleting := make([]bool, len(clusters))
	for i := range clusters {
		cl := &clusters[i]
		protOff := false
		del := func() error {
			// Re-checked on every attempt: a test may turn protection back on mid-sweep.
			if cl.DeletionProtection != nil && *cl.DeletionProtection && !protOff {
				if err := c.SetDeletionProtection(ctx, p.ID, cl.ID, false); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
					return fmt.Errorf("turning off deletion protection: %w", err)
				}
				protOff = true
				done = append(done, "deletion protection off for "+cl.Name)
			}
			if isFree(cl) {
				return c.DeleteFreeCluster(ctx, p.ID, cl.ID)
			}
			return c.DeleteCluster(ctx, p.ID, cl.ID, false)
		}
		if err := retry(ctx, clk, timeout, "delete cluster "+cl.Name, del); err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				return done, errors.Join(errs...)
			}
			continue
		}
		deleting[i] = true
	}
	for i := range clusters {
		if !deleting[i] {
			continue
		}
		cl := &clusters[i]
		getter := clusterGetter(c, cl)
		if _, err := engine.WaitForCluster(ctx, getter, p.ID, cl.ID, engine.OpDelete, timeout, clk, cl.Name, engine.WaitHooks{}); err != nil {
			errs = append(errs, fmt.Errorf("waiting for cluster %s delete: %w", cl.Name, err))
			continue
		}
		done = append(done, "deleted cluster "+cl.Name)
	}
	deleteProject := func() error { return c.DeleteProject(ctx, p.ID) }
	if len(errs) > 0 {
		// Clusters are left, so Capella will refuse; try once so the error names the project.
		if err := deleteProject(); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
			errs = append(errs, fmt.Errorf("delete project %s: %w", p.Name, err))
		} else {
			done = append(done, "deleted project "+p.Name)
		}
		return done, errors.Join(errs...)
	}
	if err := retry(ctx, clk, timeout, "delete project "+p.Name, deleteProject); err != nil {
		return done, err
	}
	done = append(done, "deleted project "+p.Name)
	return done, nil
}

// DeleteProjectsNamed deletes every project whose name equals name exactly. It goes on after a
// failed project and returns every failure, joined.
func DeleteProjectsNamed(ctx context.Context, c *capellaapi.Client, name string, timeout time.Duration, clk engine.Clock) ([]string, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	var done []string
	var errs []error
	for _, p := range projects {
		if p.Name != name {
			continue
		}
		steps, err := DeleteProjectTree(ctx, c, p, timeout, clk)
		done = append(done, steps...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return done, errors.Join(errs...)
}

// ErrBarePrefix is returned by DeleteRun for a prefix that could reach other runs' projects.
var ErrBarePrefix = errors.New("a run sweep needs a run-specific prefix, longer than " + Prefix)

// Summary is the outcome of DeleteRun: exact project names, in order, and the step log.
type Summary struct {
	Deleted []string `json:"deleted"`
	Failed  []string `json:"failed"`
	Steps   []string `json:"steps"`
}

// DeleteRun deletes every project whose name starts with runPrefix, whatever its age. It is the
// sweep a test run makes of its own leftovers (after a timeout, a signal or a failed cleanup),
// so it refuses a prefix that is not longer than Prefix: it can never be organization-wide.
func DeleteRun(ctx context.Context, c *capellaapi.Client, runPrefix string, timeout time.Duration, clk engine.Clock) (Summary, error) {
	var sum Summary
	if !strings.HasPrefix(runPrefix, Prefix) || len(runPrefix) <= len(Prefix) || !strings.HasSuffix(runPrefix, "-") {
		return sum, fmt.Errorf("%w (got %q)", ErrBarePrefix, runPrefix)
	}
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return sum, fmt.Errorf("listing projects: %w", err)
	}
	var errs []error
	for _, p := range projects {
		if !strings.HasPrefix(p.Name, runPrefix) {
			continue
		}
		steps, err := DeleteProjectTree(ctx, c, p, timeout, clk)
		sum.Steps = append(sum.Steps, steps...)
		if err != nil {
			sum.Failed = append(sum.Failed, p.Name)
			errs = append(errs, err)
			continue
		}
		sum.Deleted = append(sum.Deleted, p.Name)
	}
	return sum, errors.Join(errs...)
}

// Unreadable returns the projects starting with prefix whose audit.createdAt cannot be read.
// Stale keeps them (they may belong to a run in progress); the janitor lists them so they are
// not silently left forever.
func Unreadable(projects []capellaapi.Project, prefix string) []capellaapi.Project {
	var out []capellaapi.Project
	for _, p := range projects {
		if !strings.HasPrefix(p.Name, prefix) {
			continue
		}
		if _, ok := createdAt(p); !ok {
			out = append(out, p)
		}
	}
	return out
}

// noZoneLayouts are the timestamp forms without a zone; Capella means UTC by them.
var noZoneLayouts = []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

func createdAt(p capellaapi.Project) (time.Time, bool) {
	if p.Audit == nil || strings.TrimSpace(p.Audit.CreatedAt) == "" {
		return time.Time{}, false
	}
	raw := fracTrim.ReplaceAllString(strings.TrimSpace(p.Audit.CreatedAt), "$1")
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), true
	}
	for _, layout := range noZoneLayouts {
		if t, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func isFree(c *capellaapi.Cluster) bool {
	return c != nil && c.Support != nil && strings.EqualFold(c.Support.Plan, "free")
}

func clusterGetter(c *capellaapi.Client, cl *capellaapi.Cluster) engine.ClusterGetter {
	if isFree(cl) {
		return freeGetter{c: c}
	}
	return c.AsClusterGetter()
}

type freeGetter struct{ c *capellaapi.Client }

func (g freeGetter) GetCluster(ctx context.Context, p, id string) (*engine.ClusterWithState, error) {
	obj, _, err := g.c.GetFreeCluster(ctx, p, id)
	if err != nil {
		return nil, err
	}
	name, _ := obj["name"].(string)
	state, _ := obj["currentState"].(string)
	return &engine.ClusterWithState{Name: name, CurrentState: state}, nil
}

func retry(ctx context.Context, clk engine.Clock, timeout time.Duration, what string, call func() error) error {
	start := clk.Monotonic()
	for {
		err := call()
		if err == nil || errors.Is(err, capellaapi.ErrNotFound) {
			return nil
		}
		elapsed := clk.Monotonic() - start
		if elapsed+RetryEvery > timeout {
			return fmt.Errorf("%s: %w", what, err)
		}
		log.Printf("  %s: refused for now, trying again in %s", what, RetryEvery)
		if err := clk.Sleep(ctx, RetryEvery); err != nil {
			return err
		}
	}
}
