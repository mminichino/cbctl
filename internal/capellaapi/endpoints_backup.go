package capellaapi

import "context"

// GetBucketBackupSchedule returns a bucket's backup schedule. It returns an error matching
// ErrNotFound when the bucket has none. b is a bucket ID (see BucketID).
func (c *Client) GetBucketBackupSchedule(ctx context.Context, p, cl, b string) (Object, error) {
	path, err := c.bucketSub(p, cl, b, "/backup/schedules")
	if err != nil {
		return nil, err
	}
	return getObject(ctx, c, path)
}

// CreateBucketBackupSchedule creates a bucket's weekly backup schedule (Capella answers 202).
func (c *Client) CreateBucketBackupSchedule(ctx context.Context, p, cl, b string, body Object) error {
	path, err := c.bucketSub(p, cl, b, "/backup/schedules")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, body, nil)
	return err
}

// UpdateBucketBackupSchedule replaces a bucket's backup schedule.
func (c *Client) UpdateBucketBackupSchedule(ctx context.Context, p, cl, b string, body Object) error {
	path, err := c.bucketSub(p, cl, b, "/backup/schedules")
	if err != nil {
		return err
	}
	_, err = c.Put(ctx, path, body, "", nil)
	return err
}

// DeleteBucketBackupSchedule deletes a bucket's backup schedule (Capella answers 202).
func (c *Client) DeleteBucketBackupSchedule(ctx context.Context, p, cl, b string) error {
	path, err := c.bucketSub(p, cl, b, "/backup/schedules")
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// ListBackupCycles lists a bucket's backup cycles.
func (c *Client) ListBackupCycles(ctx context.Context, p, cl, b string) ([]Object, error) {
	path, err := c.bucketSub(p, cl, b, "/backup/cycles")
	if err != nil {
		return nil, err
	}
	return getData(ctx, c, path)
}

// ListCycleBackups lists the backups in one backup cycle.
func (c *Client) ListCycleBackups(ctx context.Context, p, cl, b, cycleID string) ([]Object, error) {
	path, err := c.bucketSub(p, cl, b, "/backup/cycles/"+pathEscape(cycleID))
	if err != nil {
		return nil, err
	}
	return getData(ctx, c, path)
}

// CreateBucketBackup starts a bucket backup (Capella answers 202 with no body; find the new
// backup by listing).
func (c *Client) CreateBucketBackup(ctx context.Context, p, cl, b string) error {
	path, err := c.bucketSub(p, cl, b, "/backups")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, Object{}, nil)
	return err
}

// ListClusterBackups lists the latest backup of each bucket in the cluster. Queued backups are
// not listed.
func (c *Client) ListClusterBackups(ctx context.Context, p, cl string) ([]Object, error) {
	path, err := c.clusterSub(p, cl, "/backups")
	if err != nil {
		return nil, err
	}
	return getData(ctx, c, path)
}

// GetBackup returns one bucket backup.
func (c *Client) GetBackup(ctx context.Context, p, cl, backupID string) (Object, error) {
	path, err := c.clusterSub(p, cl, "/backups/"+pathEscape(backupID))
	if err != nil {
		return nil, err
	}
	return getObject(ctx, c, path)
}

// DeleteBackup deletes every backup of the backup's cycle (Capella answers 202).
func (c *Client) DeleteBackup(ctx context.Context, p, cl, backupID string) error {
	path, err := c.clusterSub(p, cl, "/backups/"+pathEscape(backupID))
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, path, nil)
	return err
}

// RestoreBackup restores a bucket backup (Capella answers 202). body must carry targetClusterID,
// sourceClusterID, backupID and services.
func (c *Client) RestoreBackup(ctx context.Context, p, cl, backupID string, body Object) error {
	path, err := c.clusterSub(p, cl, "/backups/"+pathEscape(backupID)+"/restore")
	if err != nil {
		return err
	}
	_, err = c.Post(ctx, path, body, nil)
	return err
}

// ExportBackup starts exporting a backup and returns the exportId. Capella answers 409
// (ErrConflict) when the backup's cycle already has an export.
func (c *Client) ExportBackup(ctx context.Context, p, cl, b, backupID string) (string, error) {
	path, err := c.bucketSub(p, cl, b, "/backups/"+pathEscape(backupID)+"/export")
	if err != nil {
		return "", err
	}
	return c.postID(ctx, path, Object{}, "exportId", false)
}

// GetBackupExport returns an export's status; backupDownloadURL is present once it is complete
// and is valid for one hour.
func (c *Client) GetBackupExport(ctx context.Context, p, cl, b, backupID, exportID string) (Object, error) {
	path, err := c.bucketSub(p, cl, b, "/backups/"+pathEscape(backupID)+"/exports/"+pathEscape(exportID))
	if err != nil {
		return nil, err
	}
	return getObject(ctx, c, path)
}
