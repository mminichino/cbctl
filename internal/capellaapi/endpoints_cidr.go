package capellaapi

import "context"

// ListAllowedCIDRs lists the cluster's allowed CIDRs.
func (c *Client) ListAllowedCIDRs(ctx context.Context, p, cl string) ([]AllowedCIDR, error) {
	path, err := c.clusterSub(p, cl, "/allowedcidrs")
	if err != nil {
		return nil, err
	}
	return Paginate[AllowedCIDR](ctx, c, path, "data", nil)
}

// GetAllowedCIDR returns one allowed CIDR and its ETag version.
func (c *Client) GetAllowedCIDR(ctx context.Context, p, cl, id string) (*AllowedCIDR, string, error) {
	path, err := c.clusterSub(p, cl, "/allowedcidrs/"+pathEscape(id))
	if err != nil {
		return nil, "", err
	}
	return getJSON[AllowedCIDR](ctx, c, path)
}

// CreateAllowedCIDR adds an allowed CIDR and returns its ID. comment and expiresAt (RFC 3339) are
// optional.
func (c *Client) CreateAllowedCIDR(ctx context.Context, p, cl, cidr, comment, expiresAt string) (string, error) {
	path, err := c.clusterSub(p, cl, "/allowedcidrs")
	if err != nil {
		return "", err
	}
	req := CreateAllowedCIDRRequest{CIDR: cidr, Comment: comment, ExpiresAt: expiresAt}
	return c.postID(ctx, path, req, "id", true)
}

// DeleteAllowedCIDR removes an allowed CIDR.
func (c *Client) DeleteAllowedCIDR(ctx context.Context, p, cl, id string) error {
	path, err := c.clusterSub(p, cl, "/allowedcidrs/"+pathEscape(id))
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}
