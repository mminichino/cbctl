package capellaapi

import "context"

// GetSchedule returns the cluster's on/off schedule and its ETag version. It returns an error
// matching ErrNotFound when the cluster has no schedule.
func (c *Client) GetSchedule(ctx context.Context, p, cl string) (Object, string, error) {
	path, err := c.clusterSub(p, cl, "/onOffSchedule")
	if err != nil {
		return nil, "", err
	}
	out, etag, err := getJSON[Object](ctx, c, path)
	if err != nil {
		return nil, "", err
	}
	return *out, etag, nil
}

// CreateSchedule creates the cluster's on/off schedule. body is passed through verbatim.
func (c *Client) CreateSchedule(ctx context.Context, p, cl string, body Object) error {
	path, err := c.clusterSub(p, cl, "/onOffSchedule")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, body, nil)
	return err
}

// UpdateSchedule replaces the cluster's on/off schedule. ifMatch is the bare ETag version, or ""
// to skip the precondition.
func (c *Client) UpdateSchedule(ctx context.Context, p, cl string, body Object, ifMatch string) error {
	path, err := c.clusterSub(p, cl, "/onOffSchedule")
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, body, ifMatch, nil)
	return err
}

// DeleteSchedule deletes the cluster's on/off schedule.
func (c *Client) DeleteSchedule(ctx context.Context, p, cl string) error {
	path, err := c.clusterSub(p, cl, "/onOffSchedule")
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// PauseSchedule suspends the on/off schedule without deleting it.
func (c *Client) PauseSchedule(ctx context.Context, p, cl string) error {
	path, err := c.clusterSub(p, cl, "/onOffSchedule/activationState")
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// ResumeSchedule resumes a paused on/off schedule.
func (c *Client) ResumeSchedule(ctx context.Context, p, cl string) error {
	path, err := c.clusterSub(p, cl, "/onOffSchedule/activationState")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, Object{}, nil)
	return err
}
