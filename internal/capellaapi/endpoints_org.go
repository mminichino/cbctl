package capellaapi

import "context"

// ListOrganizations lists the organizations visible to the API key (GET /v4/organizations).
func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	return Paginate[Organization](ctx, c, "/v4/organizations", "data", nil)
}

// GetOrganization returns the client's organization (GET /v4/organizations/{organizationId}).
func (c *Client) GetOrganization(ctx context.Context) (*Organization, error) {
	path, err := c.OrgPath()
	if err != nil {
		return nil, err
	}
	org, _, err := getJSON[Organization](ctx, c, path)
	return org, err
}
