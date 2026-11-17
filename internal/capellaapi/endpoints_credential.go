package capellaapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mminichino/cbctl/internal/redact"
)

// CredentialTypeBasic is the credentialType of a credential with bucket/scope access (not
// custom database roles).
const CredentialTypeBasic = "basic"

// ListCredentials lists the cluster's database credentials.
func (c *Client) ListCredentials(ctx context.Context, p, cl string) ([]Credential, error) {
	path, err := c.clusterSub(p, cl, "/users")
	if err != nil {
		return nil, err
	}
	return Paginate[Credential](ctx, c, path, "data", nil)
}

// GetCredential returns a database credential and its ETag version. Capella never returns the
// password.
func (c *Client) GetCredential(ctx context.Context, p, cl, id string) (*Credential, string, error) {
	path, err := c.clusterSub(p, cl, "/users/"+pathEscape(id))
	if err != nil {
		return nil, "", err
	}
	return getJSON[Credential](ctx, c, path)
}

// CreateCredential creates a database credential and returns its ID and the password from the
// response. Capella only returns a password when it generated one (req.Password empty); the
// password is registered with the redactor immediately.
func (c *Client) CreateCredential(ctx context.Context, p, cl string, req CreateCredentialRequest) (id, password string, err error) {
	path, err := c.clusterSub(p, cl, "/users")
	if err != nil {
		return "", "", err
	}
	// A supplied password is a secret before Capella answers.
	redact.Register(req.Password)
	if req.CredentialType == "" {
		req.CredentialType = CredentialTypeBasic
	}
	resp, err := c.Post(ctx, path, req, nil)
	if err != nil {
		return "", "", err
	}
	var out CreateCredentialResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", "", fmt.Errorf("decode POST %s: %w", path, err)
	}
	redact.Register(out.Password)
	if out.ID == "" {
		return "", out.Password, fmt.Errorf("POST %s: response has no %q field", path, "id")
	}
	return out.ID, out.Password, nil
}

// UpdateCredential replaces a credential's access and/or password. ifMatch is the bare ETag
// version, or "".
func (c *Client) UpdateCredential(ctx context.Context, p, cl, id string, req UpdateCredentialRequest, ifMatch string) error {
	path, err := c.clusterSub(p, cl, "/users/"+pathEscape(id))
	if err != nil {
		return err
	}
	redact.Register(req.Password)
	_, err = c.Put(ctx, path, req, ifMatch, nil)
	return err
}

// DeleteCredential deletes a database credential.
func (c *Client) DeleteCredential(ctx context.Context, p, cl, id string) error {
	path, err := c.clusterSub(p, cl, "/users/"+pathEscape(id))
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}
