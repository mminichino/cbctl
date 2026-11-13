package capellaapi

import "context"

// CreateFreeCluster creates the organization's free-tier cluster (Capella answers 202; one per
// organization). body is passed through verbatim.
func (c *Client) CreateFreeCluster(ctx context.Context, p string, body Object) (string, error) {
	prj, err := c.projectPath(p)
	if err != nil {
		return "", err
	}
	return c.postID(ctx, prj+"/clusters/freeTier", body, "id", true)
}

// GetFreeCluster returns a free-tier cluster as a raw object, with its ETag version.
func (c *Client) GetFreeCluster(ctx context.Context, p, cl string) (Object, string, error) {
	path, err := c.freeClusterPath(p, cl)
	if err != nil {
		return nil, "", err
	}
	out, etag, err := getJSON[Object](ctx, c, path)
	if err != nil {
		return nil, "", err
	}
	return *out, etag, nil
}

// UpdateFreeCluster updates a free-tier cluster; Capella only accepts name and description.
func (c *Client) UpdateFreeCluster(ctx context.Context, p, cl string, body Object, ifMatch string) error {
	path, err := c.freeClusterPath(p, cl)
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, body, ifMatch, nil)
	return err
}

// DeleteFreeCluster deletes a free-tier cluster (Capella answers 202).
func (c *Client) DeleteFreeCluster(ctx context.Context, p, cl string) error {
	path, err := c.freeClusterPath(p, cl)
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// TurnFreeClusterOn turns a free-tier cluster on; Capella also turns on its linked App Services.
func (c *Client) TurnFreeClusterOn(ctx context.Context, p, cl string) error {
	path, err := c.freeClusterPath(p, cl)
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path+"/activationState", nil, nil)
	return err
}

// TurnFreeClusterOff turns a free-tier cluster off (Capella answers 202).
func (c *Client) TurnFreeClusterOff(ctx context.Context, p, cl string) error {
	path, err := c.freeClusterPath(p, cl)
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path+"/activationState", nil)
	return err
}

// ListFreeBuckets lists a free-tier cluster's buckets together with its capacity summary. The
// endpoint is not paginated.
func (c *Client) ListFreeBuckets(ctx context.Context, p, cl string) ([]Bucket, *ClusterStats, error) {
	path, err := c.clusterSub(p, cl, "/buckets/freeTier")
	if err != nil {
		return nil, nil, err
	}
	var env struct {
		Data         []Bucket     `json:"data"`
		ClusterStats ClusterStats `json:"clusterStats"`
	}
	if _, err := c.Get(ctx, path, nil, &env); err != nil {
		return nil, nil, err
	}
	return env.Data, &env.ClusterStats, nil
}

// GetFreeBucket returns a free-tier bucket (b is a bucket ID, see BucketID) and its ETag version.
func (c *Client) GetFreeBucket(ctx context.Context, p, cl, b string) (*Bucket, string, error) {
	path, err := c.clusterSub(p, cl, "/buckets/freeTier/"+pathEscape(b))
	if err != nil {
		return nil, "", err
	}
	return getJSON[Bucket](ctx, c, path)
}

// CreateFreeBucket creates a free-tier bucket and returns its ID. Capella only accepts name and
// memoryAllocationInMb.
func (c *Client) CreateFreeBucket(ctx context.Context, p, cl string, body Object) (string, error) {
	path, err := c.clusterSub(p, cl, "/buckets/freeTier")
	if err != nil {
		return "", err
	}
	return c.postID(ctx, path, body, "id", true)
}

// UpdateFreeBucket updates a free-tier bucket's memoryAllocationInMb and
// enableCrossClusterVersioning.
func (c *Client) UpdateFreeBucket(ctx context.Context, p, cl, b string, body Object) error {
	path, err := c.clusterSub(p, cl, "/buckets/freeTier/"+pathEscape(b))
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, body, "", nil)
	return err
}

// DeleteFreeBucket deletes a free-tier bucket.
func (c *Client) DeleteFreeBucket(ctx context.Context, p, cl, b string) error {
	path, err := c.clusterSub(p, cl, "/buckets/freeTier/"+pathEscape(b))
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}
