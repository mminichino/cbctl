package capellaapi

import (
	"context"
	"net/url"
	"strconv"

	"github.com/mminichino/cbctl/internal/engine"
)

// ListClusters lists the clusters in a project.
func (c *Client) ListClusters(ctx context.Context, p string) ([]Cluster, error) {
	prj, err := c.projectPath(p)
	if err != nil {
		return nil, err
	}
	return Paginate[Cluster](ctx, c, prj+"/clusters", "data", nil)
}

// GetCluster returns a cluster with Cluster.ETag set from the response header.
func (c *Client) GetCluster(ctx context.Context, p, cl string) (*Cluster, error) {
	path, err := c.clusterPath(p, cl)
	if err != nil {
		return nil, err
	}
	out, etag, err := getJSON[Cluster](ctx, c, path)
	if err != nil {
		return nil, err
	}
	out.ETag = etag
	return out, nil
}

// CreateCluster starts creating a cluster (Capella answers 202) and returns its ID.
func (c *Client) CreateCluster(ctx context.Context, p string, req CreateClusterRequest) (string, error) {
	prj, err := c.projectPath(p)
	if err != nil {
		return "", err
	}
	return c.postID(ctx, prj+"/clusters", req, "id", true)
}

// UpdateCluster replaces a cluster's name, description, support plan and service groups. ifMatch
// is the bare ETag version, or "" to skip the precondition.
func (c *Client) UpdateCluster(ctx context.Context, p, cl string, req UpdateClusterRequest, ifMatch string) error {
	path, err := c.clusterPath(p, cl)
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, req, ifMatch, nil)
	return err
}

// DeleteCluster starts deleting a cluster. retainSnapshotBackups is sent as the
// retainsnapshotbackups=true|false query parameter.
func (c *Client) DeleteCluster(ctx context.Context, p, cl string, retainSnapshotBackups bool) error {
	path, err := c.clusterPath(p, cl)
	if err != nil {
		return err
	}
	q := url.Values{"retainsnapshotbackups": {strconv.FormatBool(retainSnapshotBackups)}}
	_, err = c.Delete(ctx, path, q)
	return err
}

// SetDeletionProtection turns cluster deletion protection on or off.
func (c *Client) SetDeletionProtection(ctx context.Context, p, cl string, enabled bool) error {
	path, err := c.clusterSub(p, cl, "/deletionProtection")
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, Object{"deletionProtection": enabled}, "", nil)
	return err
}

// TurnClusterOn starts a turned-off cluster (Capella answers 202), optionally also turning on its
// linked App Service.
func (c *Client) TurnClusterOn(ctx context.Context, p, cl string, withAppService bool) error {
	path, err := c.clusterSub(p, cl, "/activationState")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, Object{"turnOnLinkedAppService": withAppService}, nil)
	return err
}

// TurnClusterOff turns a cluster off (Capella answers 202).
func (c *Client) TurnClusterOff(ctx context.Context, p, cl string) error {
	path, err := c.clusterSub(p, cl, "/activationState")
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// GetClusterStats returns the cluster's memory and replica capacity.
func (c *Client) GetClusterStats(ctx context.Context, p, cl string) (*ClusterStats, error) {
	path, err := c.clusterSub(p, cl, "/stats")
	if err != nil {
		return nil, err
	}
	out, _, err := getJSON[ClusterStats](ctx, c, path)
	return out, err
}

// AsClusterGetter adapts the client to engine.ClusterGetter so engine.WaitForCluster can poll it.
func (c *Client) AsClusterGetter() engine.ClusterGetter { return clusterGetter{c: c} }

// clusterGetter implements engine.ClusterGetter on top of Client.GetCluster.
type clusterGetter struct{ c *Client }

// GetCluster reads the cluster and reports its name and state. A 404 satisfies
// errors.Is(err, ErrNotFound) (and engine.ErrNotFound, which is the same value).
func (g clusterGetter) GetCluster(ctx context.Context, projectID, clusterID string) (*engine.ClusterWithState, error) {
	cl, err := g.c.GetCluster(ctx, projectID, clusterID)
	if err != nil {
		return nil, err
	}
	return &engine.ClusterWithState{Name: cl.Name, CurrentState: cl.CurrentState}, nil
}
