package capellaapi

import "context"

// ListProjects lists the organization's projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	path, err := c.projectsPath()
	if err != nil {
		return nil, err
	}
	return Paginate[Project](ctx, c, path, "data", nil)
}

// GetProject returns a project and its ETag version.
func (c *Client) GetProject(ctx context.Context, p string) (*Project, string, error) {
	path, err := c.projectPath(p)
	if err != nil {
		return nil, "", err
	}
	return getJSON[Project](ctx, c, path)
}

// CreateProject creates a project and returns its ID.
func (c *Client) CreateProject(ctx context.Context, name, description string) (string, error) {
	path, err := c.projectsPath()
	if err != nil {
		return "", err
	}
	return c.postID(ctx, path, CreateProjectRequest{Name: name, Description: description}, "id", true)
}

// UpdateProject replaces a project's name and description. ifMatch is the bare ETag version, or
// "" to skip the precondition.
func (c *Client) UpdateProject(ctx context.Context, p, name, description, ifMatch string) error {
	path, err := c.projectPath(p)
	if err != nil {
		return err
	}
	// description is always sent so that it can be cleared.
	_, err = c.Put(ctx, path, Object{"name": name, "description": description}, ifMatch, nil)
	return err
}

// DeleteProject deletes a project. Capella answers 409 (ErrConflict) when it is not empty.
func (c *Client) DeleteProject(ctx context.Context, p string) error {
	path, err := c.projectPath(p)
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}
