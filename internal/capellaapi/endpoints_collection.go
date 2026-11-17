package capellaapi

import "context"

// Collection is a collection in a scope.
type Collection struct {
	Name   string `json:"name,omitempty"`
	MaxTTL int    `json:"maxTTL,omitempty"`
}

// ListCollections lists a scope's collections.
func (c *Client) ListCollections(ctx context.Context, p, cl, b, scope string) ([]Collection, error) {
	path, err := c.collectionsPath(p, cl, b, scope)
	if err != nil {
		return nil, err
	}
	return Paginate[Collection](ctx, c, path, "data", nil)
}

// GetCollection returns a collection and its ETag version.
func (c *Client) GetCollection(ctx context.Context, p, cl, b, scope, name string) (*Collection, string, error) {
	path, err := c.collectionsPath(p, cl, b, scope)
	if err != nil {
		return nil, "", err
	}
	return getJSON[Collection](ctx, c, path+"/"+pathEscape(name))
}

// CreateCollection creates a collection. maxTTL (seconds) is omitted from the request when nil.
func (c *Client) CreateCollection(ctx context.Context, p, cl, b, scope, name string, maxTTL *int) error {
	path, err := c.collectionsPath(p, cl, b, scope)
	if err != nil {
		return err
	}
	body := Object{"name": name}
	if maxTTL != nil {
		body["maxTTL"] = *maxTTL
	}
	_, err = c.Post(ctx, path, body, nil)
	return err
}

// UpdateCollection sets a collection's maxTTL (seconds). It needs Couchbase Server 7.6.0 or later.
// ifMatch is the bare ETag version, or "".
func (c *Client) UpdateCollection(ctx context.Context, p, cl, b, scope, name string, maxTTL int, ifMatch string) error {
	path, err := c.collectionsPath(p, cl, b, scope)
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path+"/"+pathEscape(name), Object{"maxTTL": maxTTL}, ifMatch, nil)
	return err
}

// DeleteCollection deletes a collection.
func (c *Client) DeleteCollection(ctx context.Context, p, cl, b, scope, name string) error {
	path, err := c.collectionsPath(p, cl, b, scope)
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path+"/"+pathEscape(name), nil)
	return err
}
