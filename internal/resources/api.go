package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/plan"
)

// rawAPI reads Capella resources as generic mappings. The typed models in capellaapi drop zero
// values and fields they do not know (replicas: 0, userRoles, zones), and the engine's rule is to
// compare exactly what the file and Capella say, so reconcilers read mappings and use the typed
// endpoint methods only for writes whose request shape is fixed.
type rawAPI struct {
	c   *capellaapi.Client
	org string
}

func newRawAPI(rc *plan.RunContext) (*rawAPI, error) {
	if rc.Client == nil {
		return nil, errors.New("no Capella client configured")
	}
	org, err := rc.Client.OrgPath()
	if err != nil {
		return nil, err
	}
	return &rawAPI{c: rc.Client, org: org}, nil
}

func esc(s string) string { return url.PathEscape(s) }

func (a *rawAPI) projects() string         { return a.org + "/projects" }
func (a *rawAPI) project(p string) string  { return a.projects() + "/" + esc(p) }
func (a *rawAPI) clusters(p string) string { return a.project(p) + "/clusters" }

func (a *rawAPI) cluster(p, cl string) string { return a.clusters(p) + "/" + esc(cl) }

func (a *rawAPI) freeCluster(p, cl string) string { return a.clusters(p) + "/freeTier/" + esc(cl) }

func (a *rawAPI) buckets(p, cl string) string { return a.cluster(p, cl) + "/buckets" }

func (a *rawAPI) freeBuckets(p, cl string) string { return a.buckets(p, cl) + "/freeTier" }

func (a *rawAPI) bucket(p, cl, b string) string { return a.buckets(p, cl) + "/" + esc(b) }

func (a *rawAPI) freeBucket(p, cl, b string) string { return a.freeBuckets(p, cl) + "/" + esc(b) }

func (a *rawAPI) scopes(p, cl, b string) string { return a.bucket(p, cl, b) + "/scopes" }

func (a *rawAPI) collections(p, cl, b, scope string) string {
	return a.scopes(p, cl, b) + "/" + esc(scope) + "/collections"
}

func (a *rawAPI) users(p, cl string) string { return a.cluster(p, cl) + "/users" }

func (a *rawAPI) user(p, cl, id string) string { return a.users(p, cl) + "/" + esc(id) }

// list returns every item under key of a paginated listing, numbers normalized.
func (a *rawAPI) list(ctx context.Context, path, key string) ([]Obj, error) {
	items, err := capellaapi.Paginate[capellaapi.Object](ctx, a.c, path, key, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Obj, len(items))
	for i, it := range items {
		out[i] = normObj(it)
	}
	return out, nil
}

// get returns one resource and its ETag version.
func (a *rawAPI) get(ctx context.Context, path string) (Obj, string, error) {
	var out Obj
	resp, err := a.c.Get(ctx, path, nil, &out)
	if err != nil {
		return nil, "", err
	}
	return normObj(out), resp.ETag(), nil
}

// postID POSTs body and returns the id of the created resource.
func (a *rawAPI) postID(ctx context.Context, path string, body Obj) (string, error) {
	resp, err := a.c.Post(ctx, path, body, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("POST %s: response has no %q field", path, "id")
	}
	return out.ID, nil
}

// put PUTs body with an optional If-Match version.
func (a *rawAPI) put(ctx context.Context, path string, body Obj, ifMatch string) error {
	_, err := a.c.Put(ctx, path, body, ifMatch, nil)
	return err
}

// freeBucketList returns a free-tier cluster's buckets and its capacity summary (not paginated).
func (a *rawAPI) freeBucketList(ctx context.Context, p, cl string) ([]Obj, Obj, error) {
	var env struct {
		Data         []Obj `json:"data"`
		ClusterStats Obj   `json:"clusterStats"`
	}
	if _, err := a.c.Get(ctx, a.freeBuckets(p, cl), nil, &env); err != nil {
		return nil, nil, err
	}
	for _, b := range env.Data {
		normObj(b)
	}
	if env.ClusterStats != nil {
		normObj(env.ClusterStats)
	}
	return env.Data, env.ClusterStats, nil
}

func isNotFound(err error) bool  { return errors.Is(err, capellaapi.ErrNotFound) }
func isForbidden(err error) bool { return errors.Is(err, capellaapi.ErrForbidden) }
func isConflict(err error) bool  { return errors.Is(err, capellaapi.ErrConflict) }

// ignoreNotFound treats a 404 as success: the resource is already gone, which is the goal.
func ignoreNotFound(err error) error {
	if isNotFound(err) {
		return nil
	}
	return err
}

// freeGetter adapts a free-tier cluster read to engine.ClusterGetter so the cluster waiter can
// poll it.
type freeGetter struct{ a *rawAPI }

func (g freeGetter) GetCluster(ctx context.Context, projectID, clusterID string) (*engine.ClusterWithState, error) {
	body, _, err := g.a.get(ctx, g.a.freeCluster(projectID, clusterID))
	if err != nil {
		return nil, err
	}
	return &engine.ClusterWithState{Name: strOf(body, "name"), CurrentState: strOf(body, "currentState")}, nil
}
