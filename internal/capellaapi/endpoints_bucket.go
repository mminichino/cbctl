package capellaapi

import "context"

// ListBuckets lists the cluster's buckets.
func (c *Client) ListBuckets(ctx context.Context, p, cl string) ([]Bucket, error) {
	path, err := c.clusterSub(p, cl, "/buckets")
	if err != nil {
		return nil, err
	}
	return Paginate[Bucket](ctx, c, path, "data", nil)
}

// GetBucket returns a bucket and its ETag version. b is a bucket ID (see BucketID).
func (c *Client) GetBucket(ctx context.Context, p, cl, b string) (*Bucket, string, error) {
	path, err := c.bucketPath(p, cl, b)
	if err != nil {
		return nil, "", err
	}
	return getJSON[Bucket](ctx, c, path)
}

// CreateBucket creates a bucket and returns its ID.
func (c *Client) CreateBucket(ctx context.Context, p, cl string, req CreateBucketRequest) (string, error) {
	path, err := c.clusterSub(p, cl, "/buckets")
	if err != nil {
		return "", err
	}
	return c.postID(ctx, path, req, "id", true)
}

// UpdateBucket replaces a bucket's mutable settings. body is passed through verbatim because the
// mutable field set differs by bucket type. ifMatch is the bare ETag version, or "".
func (c *Client) UpdateBucket(ctx context.Context, p, cl, b string, body Object, ifMatch string) error {
	path, err := c.bucketPath(p, cl, b)
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, body, ifMatch, nil)
	return err
}

// DeleteBucket deletes a bucket.
func (c *Client) DeleteBucket(ctx context.Context, p, cl, b string) error {
	path, err := c.bucketPath(p, cl, b)
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}
