package capellaapi

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetSnapshotSchedule returns the cluster's cloud-snapshot backup schedule, or (nil, nil) when
// there is none (Capella answers 204 or an empty body).
func (c *Client) GetSnapshotSchedule(ctx context.Context, p, cl string) (Object, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackupschedule")
	if err != nil {
		return nil, err
	}
	var out Object
	if _, err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// UpsertSnapshotSchedule creates or replaces the cloud-snapshot backup schedule.
func (c *Client) UpsertSnapshotSchedule(ctx context.Context, p, cl string, body Object) error {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackupschedule")
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, body, "", nil)
	return err
}

// DeleteSnapshotSchedule deletes the cloud-snapshot backup schedule.
func (c *Client) DeleteSnapshotSchedule(ctx context.Context, p, cl string) error {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackupschedule")
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// GetSnapshotScheduleOptions returns the allowed schedule intervals and retention limits (hours).
func (c *Client) GetSnapshotScheduleOptions(ctx context.Context, p, cl string) (Object, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackupschedule/options")
	if err != nil {
		return nil, err
	}
	return getObject(ctx, c, path)
}

// ListSnapshotRegions lists the regions a snapshot can be copied to (AWS and Azure only).
func (c *Client) ListSnapshotRegions(ctx context.Context, p, cl string) ([]string, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups/regions")
	if err != nil {
		return nil, err
	}
	resp, err := c.Get(ctx, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var regions []string
	if len(resp.Body) == 0 {
		return regions, nil
	}
	if err := json.Unmarshal(resp.Body, &regions); err != nil {
		// Some responses wrap the list in an envelope.
		var env struct {
			Data []string `json:"data"`
		}
		if err2 := json.Unmarshal(resp.Body, &env); err2 != nil {
			return nil, fmt.Errorf("decode GET %s: %w", path, err)
		}
		regions = env.Data
	}
	return regions, nil
}

// ListSnapshots lists the cluster's cloud snapshots. Queued snapshots are not listed.
func (c *Client) ListSnapshots(ctx context.Context, p, cl string) ([]Object, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups")
	if err != nil {
		return nil, err
	}
	return Paginate[Object](ctx, c, path, "data", nil)
}

// ListProjectSnapshots returns one snapshot summary per cluster in the project.
func (c *Client) ListProjectSnapshots(ctx context.Context, p string) ([]Object, error) {
	prj, err := c.projectPath(p)
	if err != nil {
		return nil, err
	}
	return Paginate[Object](ctx, c, prj+"/cloudsnapshotbackups", "data", nil)
}

// CreateSnapshot starts a cloud snapshot (Capella answers 202) and returns its backupId. body may
// carry retention (hours) and regionsToCopy.
func (c *Client) CreateSnapshot(ctx context.Context, p, cl string, body Object) (string, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups")
	if err != nil {
		return "", err
	}
	return c.postID(ctx, path, body, "backupId", false)
}

// UpdateSnapshotRetention changes a snapshot's retention, in hours.
func (c *Client) UpdateSnapshotRetention(ctx context.Context, p, cl, backupID string, retentionHours int) error {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups/"+pathEscape(backupID))
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, Object{"retention": retentionHours}, "", nil)
	return err
}

// DeleteSnapshot deletes a cloud snapshot (Capella answers 202).
func (c *Client) DeleteSnapshot(ctx context.Context, p, cl, backupID string) error {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups/"+pathEscape(backupID))
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// RestoreSnapshot restores a snapshot over the cluster, replacing all of its data, and returns
// the restoreId.
func (c *Client) RestoreSnapshot(ctx context.Context, p, cl, backupID string, body Object) (string, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups/"+pathEscape(backupID)+"/restore")
	if err != nil {
		return "", err
	}
	return c.postID(ctx, path, body, "restoreId", false)
}

// ListSnapshotRestores lists the cluster's snapshot restores.
func (c *Client) ListSnapshotRestores(ctx context.Context, p, cl string) ([]Object, error) {
	path, err := c.clusterSub(p, cl, "/cloudsnapshotbackups/restores")
	if err != nil {
		return nil, err
	}
	return Paginate[Object](ctx, c, path, "data", nil)
}
