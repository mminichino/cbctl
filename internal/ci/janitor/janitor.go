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

// DeleteProjectTree deletes one project and everything in it. For each cluster
// it turns deletion protection off when set, deletes (free-tier via
// DeleteFreeCluster; others via DeleteCluster with retainSnapshotBackups=false)
// retrying every RetryEvery until timeout, then WaitForCluster(OpDelete), then
// deletes the project with the same retry. Returns ordered step descriptions.
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
	for i := range clusters {
		cl := &clusters[i]
		if cl.DeletionProtection != nil && *cl.DeletionProtection {
			if err := c.SetDeletionProtection(ctx, p.ID, cl.ID, false); err != nil {
				return done, fmt.Errorf("turning off deletion protection of %s: %w", cl.Name, err)
			}
			done = append(done, "deletion protection off for "+cl.Name)
		}
		del := func() error {
			if isFree(cl) {
				return c.DeleteFreeCluster(ctx, p.ID, cl.ID)
			}
			return c.DeleteCluster(ctx, p.ID, cl.ID, false)
		}
		if err := retry(ctx, clk, timeout, "delete cluster "+cl.Name, del); err != nil {
			return done, err
		}
	}
	for i := range clusters {
		cl := &clusters[i]
		getter := clusterGetter(c, cl)
		if _, err := engine.WaitForCluster(ctx, getter, p.ID, cl.ID, engine.OpDelete, timeout, clk, cl.Name, engine.WaitHooks{}); err != nil {
			return done, fmt.Errorf("waiting for cluster %s delete: %w", cl.Name, err)
		}
		done = append(done, "deleted cluster "+cl.Name)
	}
	if err := retry(ctx, clk, timeout, "delete project "+p.Name, func() error {
		return c.DeleteProject(ctx, p.ID)
	}); err != nil {
		return done, err
	}
	done = append(done, "deleted project "+p.Name)
	return done, nil
}

// DeleteProjectsNamed deletes every project whose name equals name exactly.
func DeleteProjectsNamed(ctx context.Context, c *capellaapi.Client, name string, timeout time.Duration, clk engine.Clock) ([]string, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	var done []string
	for _, p := range projects {
		if p.Name != name {
			continue
		}
		steps, err := DeleteProjectTree(ctx, c, p, timeout, clk)
		done = append(done, steps...)
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

func createdAt(p capellaapi.Project) (time.Time, bool) {
	if p.Audit == nil || p.Audit.CreatedAt == "" {
		return time.Time{}, false
	}
	raw := p.Audit.CreatedAt
	raw = strings.ReplaceAll(raw, "Z", "+00:00")
	raw = fracTrim.ReplaceAllString(raw, "$1")
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return time.Time{}, false
	}
	if t.Location() == time.Local && !strings.ContainsAny(p.Audit.CreatedAt, "Z+-") {
		t = t.UTC()
	}
	return t.UTC(), true
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
