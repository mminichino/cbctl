package capellaapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// BucketID returns the Capella bucket ID for a bucket name: the unpadded URL-safe base64 of the
// name's UTF-8 bytes. Endpoint functions take this ID, never the raw name.
func BucketID(name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name))
}

// pathEscape escapes a scope, collection or ID for use as one URL path segment.
func pathEscape(s string) string { return url.PathEscape(s) }

// projectPath returns "<org>/projects/<p>".
func (c *Client) projectPath(p string) (string, error) {
	org, err := c.OrgPath()
	if err != nil {
		return "", err
	}
	return org + "/projects/" + pathEscape(p), nil
}

// projectsPath returns "<org>/projects".
func (c *Client) projectsPath() (string, error) {
	org, err := c.OrgPath()
	if err != nil {
		return "", err
	}
	return org + "/projects", nil
}

// clusterPath returns "<org>/projects/<p>/clusters/<cl>".
func (c *Client) clusterPath(p, cl string) (string, error) {
	prj, err := c.projectPath(p)
	if err != nil {
		return "", err
	}
	return prj + "/clusters/" + pathEscape(cl), nil
}

// freeClusterPath returns "<org>/projects/<p>/clusters/freeTier/<cl>".
func (c *Client) freeClusterPath(p, cl string) (string, error) {
	prj, err := c.projectPath(p)
	if err != nil {
		return "", err
	}
	return prj + "/clusters/freeTier/" + pathEscape(cl), nil
}

// bucketPath returns "<cluster>/buckets/<b>" where b is already a bucket ID.
func (c *Client) bucketPath(p, cl, b string) (string, error) {
	clu, err := c.clusterPath(p, cl)
	if err != nil {
		return "", err
	}
	return clu + "/buckets/" + pathEscape(b), nil
}

// collectionsPath returns "<bucket>/scopes/<scope>/collections".
func (c *Client) collectionsPath(p, cl, b, scope string) (string, error) {
	bkt, err := c.bucketPath(p, cl, b)
	if err != nil {
		return "", err
	}
	return bkt + "/scopes/" + pathEscape(scope) + "/collections", nil
}

// clusterSub returns the cluster path plus suffix (which must start with "/" and be pre-escaped).
func (c *Client) clusterSub(p, cl, suffix string) (string, error) {
	clu, err := c.clusterPath(p, cl)
	if err != nil {
		return "", err
	}
	return clu + suffix, nil
}

// bucketSub returns the bucket path plus suffix (which must start with "/" and be pre-escaped).
func (c *Client) bucketSub(p, cl, b, suffix string) (string, error) {
	bkt, err := c.bucketPath(p, cl, b)
	if err != nil {
		return "", err
	}
	return bkt + suffix, nil
}

// parseID extracts the string field key from a JSON object body. It returns an error when the
// body is not an object or the field is missing or empty.
func parseID(body []byte, key string) (string, error) {
	v, err := optionalID(body, key)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("response has no %q field", key)
	}
	return v, nil
}

// optionalID is like parseID but returns "" when the field or the body is absent.
func optionalID(body []byte, key string) (string, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	s, _ := m[key].(string)
	return s, nil
}

// postID POSTs body to path and returns the string field key of the response.
func (c *Client) postID(ctx context.Context, path string, body any, key string, required bool) (string, error) {
	resp, err := c.Post(ctx, path, body, nil)
	if err != nil {
		return "", err
	}
	var id string
	if required {
		id, err = parseID(resp.Body, key)
	} else {
		id, err = optionalID(resp.Body, key)
	}
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", path, err)
	}
	return id, nil
}

// getJSON GETs path and decodes the body into a new T, returning the response ETag.
func getJSON[T any](ctx context.Context, c *Client, path string) (*T, string, error) {
	out := new(T)
	resp, err := c.Get(ctx, path, nil, out)
	if err != nil {
		return nil, "", err
	}
	return out, resp.ETag(), nil
}

// getData GETs path and returns the items of its {"data": [...]} envelope.
func getData(ctx context.Context, c *Client, path string) ([]Object, error) {
	var env struct {
		Data []Object `json:"data"`
	}
	if _, err := c.Get(ctx, path, nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// getObject GETs path and returns the JSON object body (empty, non-nil map when there is none).
func getObject(ctx context.Context, c *Client, path string) (Object, error) {
	out := Object{}
	if _, err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
