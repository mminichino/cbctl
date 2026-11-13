package capellaapi

import "context"

// Scope is a scope in a bucket, with its collections when Capella includes them.
type Scope struct {
	Name        string       `json:"name,omitempty"`
	Collections []Collection `json:"collections,omitempty"`
}

// ListScopes lists a bucket's scopes (the items are under the "scopes" key).
func (c *Client) ListScopes(ctx context.Context, p, cl, b string) ([]Scope, error) {
	path, err := c.bucketSub(p, cl, b, "/scopes")
	if err != nil {
		return nil, err
	}
	return Paginate[Scope](ctx, c, path, "scopes", nil)
}

// GetScope returns a scope and its ETag version.
func (c *Client) GetScope(ctx context.Context, p, cl, b, scope string) (*Scope, string, error) {
	path, err := c.bucketSub(p, cl, b, "/scopes/"+pathEscape(scope))
	if err != nil {
		return nil, "", err
	}
	return getJSON[Scope](ctx, c, path)
}

// CreateScope creates a scope in a bucket.
func (c *Client) CreateScope(ctx context.Context, p, cl, b, name string) error {
	path, err := c.bucketSub(p, cl, b, "/scopes")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, Object{"name": name}, nil)
	return err
}

// DeleteScope deletes a scope.
func (c *Client) DeleteScope(ctx context.Context, p, cl, b, scope string) error {
	path, err := c.bucketSub(p, cl, b, "/scopes/"+pathEscape(scope))
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}
