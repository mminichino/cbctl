package cli

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

var (
	backupDays       = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	backupIncrements = []int{1, 2, 4, 6, 8, 12, 24}
	// retentionWords maps Capella's retention values to text, in increasing order.
	retentionWords = []struct{ value, text string }{
		{"30days", "30 days"}, {"60days", "60 days"}, {"90days", "90 days"}, {"180days", "180 days"},
		{"1year", "1 year"}, {"2years", "2 years"}, {"3years", "3 years"}, {"4years", "4 years"}, {"5years", "5 years"},
	}
)

func retentionValues() []string {
	out := make([]string, len(retentionWords))
	for i, w := range retentionWords {
		out[i] = w.value
	}
	return out
}

func retentionText(v string) string {
	for _, w := range retentionWords {
		if w.value == v {
			return w.text
		}
	}
	return v
}

// bucketScheduleDefaults are the values of a new bucket backup schedule.
func bucketScheduleDefaults() map[string]any {
	return map[string]any{"dayOfWeek": "sunday", "startAt": 0, "incrementalEvery": 24, "retentionTime": "30days", "costOptimizedRetention": false}
}

// bucketSummary is "weekly full on sunday at 02:00 UTC · incremental every 12 h · kept 90 days".
func bucketSummary(weekly map[string]any) string {
	w := bucketScheduleDefaults()
	for k, v := range weekly {
		w[k] = v
	}
	text := fmt.Sprintf("weekly full on %v at %02d:00 UTC · incremental every %d h · kept %s",
		w["dayOfWeek"], numberOf(w["startAt"]), numberOf(w["incrementalEvery"]), retentionText(fmt.Sprint(w["retentionTime"])))
	if c, _ := w["costOptimizedRetention"].(bool); c {
		text += " · cost-optimized"
	}
	return text
}

func newCapellaBucketBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Bucket backups (weekly full + incremental): schedule, cycles, list, get, create, delete, restore, export",
		Long: "Bucket backups are weekly full backups with incrementals. Backups of the whole cluster: 'cbctl capella cluster backup --help'.\n\n" +
			"Examples:\n  cbctl capella bucket backup schedule set prod-01 orders\n  cbctl capella bucket backup list prod-01\n" +
			"  cbctl capella bucket backup create prod-01 orders\n  cbctl capella bucket backup restore prod-01 BACKUP_ID --replace-ttl none --yes",
		Run: func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	sched := &cobra.Command{
		Use:   "schedule",
		Short: "When a bucket is backed up and how long backups are kept",
		Long: "When a bucket is backed up and how long backups are kept. The schedule is recorded in capella.yaml.\n\n" +
			"Examples:\n  cbctl capella bucket backup schedule set prod-01 orders --start-at 2 --incremental-every 12\n" +
			"  cbctl capella bucket backup schedule show prod-01 orders\n  cbctl capella bucket backup schedule delete prod-01 orders",
		Run: func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	sched.AddCommand(newBucketScheduleShowCmd(), newBucketScheduleSetCmd(), newBucketScheduleDeleteCmd())
	cycle := &cobra.Command{
		Use:   "cycle",
		Short: "Backup cycles: a weekly full backup and the incrementals that follow it",
		Long: "Backup cycles: a weekly full backup and the incrementals that follow it.\n\n" +
			"Examples:\n  cbctl capella bucket backup cycle list prod-01 orders\n  cbctl capella bucket backup cycle get prod-01 orders CYCLE_ID",
		Run: func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	cycle.AddCommand(newBucketCycleListCmd(), newBucketCycleGetCmd())
	cmd.AddCommand(sched, cycle, newBucketBackupListCmd(), newBucketBackupGetCmd(), newBucketBackupCreateCmd(),
		newBucketBackupDeleteCmd(), newBucketBackupRestoreCmd(), newBucketBackupExportCmd())
	return cmd
}

// locateBackupBucket resolves a provisioned cluster and one of its buckets.
func (rt *runtime) locateBackupBucket(cluster, bucket, project string) (*bucketTarget, error) {
	t, err := rt.locateBackupCluster(cluster, project)
	if err != nil {
		return nil, err
	}
	b, err := rt.resolveBucket(t.Project.ID, t.Cluster.ID, bucket)
	if err != nil {
		return nil, err
	}
	return &bucketTarget{clusterTarget: t, Bucket: b}, nil
}

type bucketBackupResult struct {
	Cluster  string `json:"cluster"`
	Bucket   string `json:"bucket,omitempty"`
	BackupID string `json:"backupId,omitempty"`
	ExportID string `json:"exportId,omitempty"`
	Status   string `json:"status"`
	Summary  string `json:"summary,omitempty"`
}

// ---- schedule ---------------------------------------------------------------------------------

func newBucketScheduleShowCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("show CLUSTER BUCKET", "Show a bucket's backup schedule", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupBucket(args[0], args[1], project)
			if err != nil {
				return err
			}
			body, err := rt.Client.GetBucketBackupSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID())
			if errors.Is(err, capellaapi.ErrNotFound) {
				body, err = nil, nil
			}
			if err != nil {
				return fmt.Errorf("reading the backup schedule of %s: %w", args[1], err)
			}
			if rt.JSON {
				doc := capellaapi.Object{"cluster": t.Cluster.Name, "bucket": args[1]}
				for k, v := range body {
					doc[k] = v
				}
				rt.Out.Object(doc)
				return nil
			}
			if body == nil {
				rt.Out.Line("bucket %s has no backup schedule; set one with 'cbctl capella bucket backup schedule set %s %s'", args[1], args[0], args[1])
				return nil
			}
			weekly, _ := body["weeklySchedule"].(map[string]any)
			rt.Out.Line("%s", bucketSummary(weekly))
			return nil
		})
	cmd.Long = "Show a bucket's backup schedule.\n\n" +
		"Examples:\n  cbctl capella bucket backup schedule show prod-01 orders\n  cbctl capella -o json bucket backup schedule show prod-01 orders"
	addProjectFlag(cmd, &project)
	return cmd
}

func newBucketScheduleSetCmd() *cobra.Command {
	var project, day, retention string
	var startAt, every int
	var cost bool
	var cmd *cobra.Command
	cmd = capellaLeaf("set CLUSTER BUCKET", "Create or change a bucket's backup schedule", cobra.ExactArgs(2),
		func([]string) error {
			ch := cmd.Flags().Changed
			if ch("day") {
				day = strings.ToLower(day)
				if !slices.Contains(backupDays, day) {
					return usageError("use one of "+strings.Join(backupDays, ", "), "unknown day %q", day)
				}
			}
			if ch("start-at") && (startAt < 0 || startAt > 23) {
				return usageError("give an hour from 0 to 23 (UTC)", "--start-at must be 0-23, got %d", startAt)
			}
			if ch("incremental-every") && !slices.Contains(backupIncrements, every) {
				return usageError(fmt.Sprintf("use one of %v hours", backupIncrements), "--incremental-every %d is not offered", every)
			}
			if ch("retention") && !slices.Contains(retentionValues(), retention) {
				return usageError("use one of "+strings.Join(retentionValues(), ", "), "unknown retention %q", retention)
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupBucket(args[0], args[1], project)
			if err != nil {
				return err
			}
			ch := cmd.Flags().Changed
			cur, err := rt.Client.GetBucketBackupSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID())
			isNew := errors.Is(err, capellaapi.ErrNotFound)
			if err != nil && !isNew {
				return fmt.Errorf("reading the backup schedule of %s: %w", args[1], err)
			}
			values := bucketScheduleDefaults()
			if !isNew {
				weekly, _ := cur["weeklySchedule"].(map[string]any)
				for k := range values {
					if v, ok := weekly[k]; ok {
						values[k] = v
					}
				}
			}
			before := bucketSummary(values)
			for flag, kv := range map[string]struct {
				key string
				val any
			}{"day": {"dayOfWeek", day}, "start-at": {"startAt", startAt}, "incremental-every": {"incrementalEvery", every},
				"retention": {"retentionTime", retention}, "cost-optimized": {"costOptimizedRetention", cost}} {
				if ch(flag) {
					values[kv.key] = kv.val
				}
			}
			res := bucketBackupResult{Cluster: t.Cluster.Name, Bucket: args[1], Summary: bucketSummary(values)}
			if !isNew && before == res.Summary {
				res.Status = "unchanged"
				rt.report(res, "backup schedule of %s already matches; nothing to do", args[1])
				return nil
			}
			body := capellaapi.Object{"type": "weekly", "weeklySchedule": values}
			if isNew {
				err = rt.Client.CreateBucketBackupSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID(), body)
			} else {
				err = rt.Client.UpdateBucketBackupSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID(), body)
			}
			if err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "updated"
			if isNew {
				res.Status = "created"
			}
			rt.report(res, "%s the backup schedule of %s: %s", res.Status, args[1], res.Summary)
			return nil
		})
	cmd.Long = "Create or change a bucket's weekly backup schedule. A new schedule starts from the defaults\n" +
		"(sunday, 0 UTC, every 24 h, 30days, not cost-optimized); on an existing one only the values you give change.\n\n" +
		"Examples:\n  cbctl capella bucket backup schedule set prod-01 orders\n" +
		"  cbctl capella bucket backup schedule set prod-01 orders --day sunday --start-at 2 --incremental-every 12 --retention 90days\n" +
		"  cbctl capella bucket backup schedule set prod-01 orders --retention 1year"
	addProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&day, "day", "", "Day of the weekly full backup: "+strings.Join(backupDays, ", "))
	cmd.Flags().IntVar(&startAt, "start-at", 0, "Hour (UTC, 0-23) the backups start")
	cmd.Flags().IntVar(&every, "incremental-every", 0, fmt.Sprintf("Hours between incremental backups: %v", backupIncrements))
	cmd.Flags().StringVar(&retention, "retention", "", "How long backups are kept: "+strings.Join(retentionValues(), ", "))
	cmd.Flags().BoolVar(&cost, "cost-optimized", false, "Capella's cost-optimized retention (--cost-optimized=false to turn off)")
	return cmd
}

func newBucketScheduleDeleteCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("delete CLUSTER BUCKET", "Delete a bucket's backup schedule (existing backups are kept until they expire)", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupBucket(args[0], args[1], project)
			if err != nil {
				return err
			}
			res := bucketBackupResult{Cluster: t.Cluster.Name, Bucket: args[1]}
			body, err := rt.Client.GetBucketBackupSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID())
			if errors.Is(err, capellaapi.ErrNotFound) {
				res.Status = "absent"
				rt.report(res, "bucket %s has no backup schedule; nothing to do", args[1])
				return nil
			}
			if err != nil {
				return fmt.Errorf("reading the backup schedule of %s: %w", args[1], err)
			}
			weekly, _ := body["weeklySchedule"].(map[string]any)
			res.Summary = bucketSummary(weekly)
			rt.Out.Line("  - backup schedule %s/%s  %s", t.Cluster.Name, args[1], res.Summary)
			ok, err := confirm(rt, fmt.Sprintf("Delete the backup schedule of %s? Existing backups are kept until they expire.", args[1]))
			if err != nil {
				return err
			}
			if !ok {
				res.Status = "cancelled"
				rt.report(res, "cancelled; the backup schedule is unchanged")
				return nil
			}
			if err := rt.Client.DeleteBucketBackupSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID()); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "deleted"
			rt.report(res, "deleted the backup schedule of %s", args[1])
			return nil
		})
	cmd.Long = "Delete a bucket's backup schedule. Existing backups are kept until they expire.\n\n" +
		"Examples:\n  cbctl capella bucket backup schedule delete prod-01 orders\n  cbctl capella bucket backup schedule delete prod-01 orders --yes"
	addProjectFlag(cmd, &project)
	return cmd
}

// ---- cycles -----------------------------------------------------------------------------------

func backupRows(items []capellaapi.Object, withBucket bool) [][]string {
	sorted := slices.Clone(items)
	sort.SliceStable(sorted, func(i, j int) bool { return strOf(sorted[i], "date") > strOf(sorted[j], "date") })
	rows := make([][]string, 0, len(sorted))
	for _, b := range sorted {
		stats, _ := b["stats"].(map[string]any)
		size := ""
		if mb, ok := stats["sizeInMb"].(float64); ok {
			if mb >= 1024 {
				size = fmt.Sprintf("%.1f GB", mb/1024)
			} else {
				size = fmt.Sprintf("%.0f MB", mb)
			}
		}
		row := []string{when(b["date"]), strOf(b, "method"), strOf(b, "source"), strOf(b, "status"), size,
			fmt.Sprint(numberOf(stats["items"])), when(b["restoreBefore"]), strOf(b, "id")}
		if withBucket {
			row = append([]string{strOf(b, "bucketName")}, row...)
		}
		rows = append(rows, row)
	}
	return rows
}

func backupHeader(withBucket bool) []string {
	h := []string{"DATE", "METHOD", "SOURCE", "STATUS", "SIZE", "ITEMS", "EXPIRES", "ID"}
	if withBucket {
		h = append([]string{"BUCKET"}, h...)
	}
	return h
}

func newBucketCycleListCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("list CLUSTER BUCKET", "List a bucket's backup cycles, newest first", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupBucket(args[0], args[1], project)
			if err != nil {
				return err
			}
			cycles, err := rt.Client.ListBackupCycles(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID())
			if err != nil {
				return fmt.Errorf("listing the backup cycles of %s: %w", args[1], err)
			}
			rt.Out.List(cycles)
			if len(cycles) == 0 {
				rt.Out.Line("no backup cycles")
				return nil
			}
			sort.SliceStable(cycles, func(i, j int) bool { return strOf(cycles[i], "createdAt") > strOf(cycles[j], "createdAt") })
			rows := make([][]string, len(cycles))
			for i, c := range cycles {
				backups, err := rt.Client.ListCycleBackups(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID(), strOf(c, "cycleID"))
				if err != nil {
					return fmt.Errorf("listing the backups of cycle %s: %w", strOf(c, "cycleID"), err)
				}
				latest, latestDate := "-", ""
				for _, b := range backups {
					if d := strOf(b, "date"); d >= latestDate {
						latest, latestDate = strOf(b, "status"), d
					}
				}
				rows[i] = []string{when(c["createdAt"]), fmt.Sprint(len(backups)), latest, strOf(c, "cycleID")}
			}
			rt.Out.Table([]string{"STARTED", "BACKUPS", "LATEST STATUS", "ID"}, rows)
			return nil
		})
	cmd.Long = "List a bucket's backup cycles, newest first, with the number of backups and the status of the latest.\n\n" +
		"Examples:\n  cbctl capella bucket backup cycle list prod-01 orders\n  cbctl capella -o json bucket backup cycle list prod-01 orders"
	addProjectFlag(cmd, &project)
	return cmd
}

func newBucketCycleGetCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("get CLUSTER BUCKET CYCLE_ID", "List the backups of one cycle: full and incremental, scheduled and on-demand", cobra.ExactArgs(3), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupBucket(args[0], args[1], project)
			if err != nil {
				return err
			}
			items, err := rt.Client.ListCycleBackups(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID(), args[2])
			if errors.Is(err, capellaapi.ErrNotFound) {
				return usageError(fmt.Sprintf("list them with 'cbctl capella bucket backup cycle list %s %s'", args[0], args[1]), "cycle %s of %s not found", args[2], args[1])
			}
			if err != nil {
				return fmt.Errorf("listing the backups of cycle %s: %w", args[2], err)
			}
			rt.Out.List(items)
			rt.Out.Table(backupHeader(false), backupRows(items, false))
			return nil
		})
	cmd.Long = "List the backups of one cycle, newest first.\n\n" +
		"Examples:\n  cbctl capella bucket backup cycle get prod-01 orders CYCLE_ID\n  cbctl capella -o json bucket backup cycle get prod-01 orders CYCLE_ID"
	addProjectFlag(cmd, &project)
	return cmd
}

// ---- list, get --------------------------------------------------------------------------------

func newBucketBackupListCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("list CLUSTER [BUCKET]", "List bucket backups: the latest of each bucket, or every backup of BUCKET", cobra.RangeArgs(1, 2), nil,
		func(rt *runtime, args []string) error {
			var items []capellaapi.Object
			withBucket := len(args) == 1
			if withBucket {
				t, err := rt.locateBackupCluster(args[0], project)
				if err != nil {
					return err
				}
				if items, err = rt.Client.ListClusterBackups(rt.Ctx, t.Project.ID, t.Cluster.ID); err != nil {
					return fmt.Errorf("listing the backups of %s: %w", t.Cluster.Name, err)
				}
			} else {
				t, err := rt.locateBackupBucket(args[0], args[1], project)
				if err != nil {
					return err
				}
				cycles, err := rt.Client.ListBackupCycles(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID())
				if err != nil {
					return fmt.Errorf("listing the backup cycles of %s: %w", args[1], err)
				}
				for _, c := range cycles {
					list, err := rt.Client.ListCycleBackups(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID(), strOf(c, "cycleID"))
					if err != nil {
						return fmt.Errorf("listing the backups of cycle %s: %w", strOf(c, "cycleID"), err)
					}
					items = append(items, list...)
				}
			}
			rt.Out.List(items)
			if len(items) == 0 {
				rt.Out.Line("no backups")
				return nil
			}
			rt.Out.Table(backupHeader(withBucket), backupRows(items, withBucket))
			return nil
		})
	cmd.Long = "List bucket backups: the latest of each bucket of the cluster, or every backup of BUCKET.\n\n" +
		"Examples:\n  cbctl capella bucket backup list prod-01\n  cbctl capella bucket backup list prod-01 orders"
	addProjectFlag(cmd, &project)
	return cmd
}

func (rt *runtime) getBackup(t *clusterTarget, id string) (capellaapi.Object, error) {
	b, err := rt.Client.GetBackup(rt.Ctx, t.Project.ID, t.Cluster.ID, id)
	if errors.Is(err, capellaapi.ErrNotFound) {
		return nil, usageError(fmt.Sprintf("list them with 'cbctl capella bucket backup list %s'", t.Cluster.Name), "backup %s not found in cluster %s", id, t.Cluster.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("reading backup %s: %w", id, err)
	}
	return b, nil
}

func newBucketBackupGetCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("get CLUSTER BACKUP_ID", "Show one bucket backup with its statistics", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			b, err := rt.getBackup(t, args[1])
			if err != nil {
				return err
			}
			rt.Out.Object(b)
			return nil
		})
	cmd.Long = "Show one bucket backup with its statistics.\n\n" +
		"Examples:\n  cbctl capella bucket backup get prod-01 BACKUP_ID\n  cbctl capella -o json bucket backup get prod-01 BACKUP_ID"
	addProjectFlag(cmd, &project)
	return cmd
}

// ---- create, delete ---------------------------------------------------------------------------

func newBucketBackupCreateCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("create CLUSTER BUCKET", "Back a bucket up now and wait until the backup is ready", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupBucket(args[0], args[1], project)
			if err != nil {
				return err
			}
			latest := func() (capellaapi.Object, error) {
				items, err := rt.Client.ListClusterBackups(rt.Ctx, t.Project.ID, t.Cluster.ID)
				for _, b := range items {
					if strOf(b, "bucketID") == t.bucketID() || strOf(b, "bucketName") == args[1] {
						return b, err
					}
				}
				return nil, err
			}
			before, err := latest()
			if err != nil {
				return fmt.Errorf("listing the backups of %s: %w", t.Cluster.Name, err)
			}
			logging.Info("backing up bucket %s", args[1])
			if err := rt.Client.CreateBucketBackup(rt.Ctx, t.Project.ID, t.Cluster.ID, t.bucketID()); err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res := bucketBackupResult{Cluster: t.Cluster.Name, Bucket: args[1], Status: "accepted"}
			if rt.NoWait {
				rt.report(res, "backup of %s started (--no-wait); see it with 'cbctl capella bucket backup list %s %s'", args[1], args[0], args[1])
				return nil
			}
			var found capellaapi.Object
			_, err = rt.waitBackupStatus("backup of "+args[1], backupDone, backupFailed, func() (string, error) {
				b, err := latest()
				if err != nil || b == nil || strOf(b, "id") == strOf(before, "id") {
					return "", err // queued: not listed yet
				}
				found = b
				return strOf(b, "status"), nil
			})
			if err != nil {
				return err
			}
			res.Status, res.BackupID = "complete", strOf(found, "id")
			rt.report(res, "backup %s of %s is ready", res.BackupID, args[1])
			return nil
		})
	cmd.Long = "Back a bucket up now and wait until the backup is ready.\n\n" +
		"Examples:\n  cbctl capella bucket backup create prod-01 orders\n  cbctl capella bucket backup create prod-01 orders --no-wait"
	addProjectFlag(cmd, &project)
	return cmd
}

func newBucketBackupDeleteCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("delete CLUSTER BACKUP_ID", "Delete a backup and every other backup of its cycle", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			res := bucketBackupResult{Cluster: t.Cluster.Name, BackupID: args[1]}
			b, err := rt.Client.GetBackup(rt.Ctx, t.Project.ID, t.Cluster.ID, args[1])
			if errors.Is(err, capellaapi.ErrNotFound) {
				res.Status = "absent"
				rt.report(res, "backup %s not found in cluster %s; nothing to do", args[1], t.Cluster.Name)
				return nil
			}
			if err != nil {
				return fmt.Errorf("reading backup %s: %w", args[1], err)
			}
			bucket := strOf(b, "bucketName")
			res.Bucket = bucket
			inCycle, err := rt.Client.ListCycleBackups(rt.Ctx, t.Project.ID, t.Cluster.ID, strOf(b, "bucketID"), strOf(b, "cycleID"))
			if errors.Is(err, capellaapi.ErrNotFound) {
				inCycle, err = []capellaapi.Object{b}, nil
			}
			if err != nil {
				return fmt.Errorf("listing the backups of the cycle: %w", err)
			}
			first := strOf(b, "date")
			for _, x := range inCycle {
				if d := strOf(x, "date"); d != "" && d < first {
					first = d
				}
			}
			plural := "s"
			if len(inCycle) == 1 {
				plural = ""
			}
			rt.Out.Line("  - deletes cycle %s with %d backup%s of %s", when(first), len(inCycle), plural, bucket)
			ok, err := confirmTyped(rt, "Type the bucket name to confirm deletion", bucket)
			if err != nil {
				return err
			}
			if !ok {
				res.Status = "cancelled"
				rt.report(res, "cancelled; nothing was deleted")
				return nil
			}
			if err := rt.Client.DeleteBackup(rt.Ctx, t.Project.ID, t.Cluster.ID, args[1]); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "deleted"
			rt.report(res, "deleted the backup cycle of %s that started %s (%d backups)", bucket, when(first), len(inCycle))
			return nil
		})
	cmd.Long = "Delete a backup and every other backup of its cycle (the weekly full and its incrementals). You confirm by\n" +
		"typing the bucket name (or pass --yes).\n\nExamples:\n  cbctl capella bucket backup delete prod-01 BACKUP_ID\n" +
		"  cbctl capella bucket backup delete prod-01 BACKUP_ID --yes"
	addProjectFlag(cmd, &project)
	return cmd
}

// ---- restore, export --------------------------------------------------------------------------

type restoreOptions struct {
	project, toCluster, services, include, exclude, mapData, filterKeys, filterValues string
	replaceTTL, replaceTTLWith                                                        string
	force, autoRemove                                                                 bool
}

func newBucketBackupRestoreCmd() *cobra.Command {
	var o restoreOptions
	cmd := capellaLeaf("restore CLUSTER BACKUP_ID", "Restore a bucket backup into the same or another cluster, all of it or part of it", cobra.ExactArgs(2),
		func([]string) error {
			names := strings.Split(o.services, ",")
			for i := range names {
				names[i] = strings.TrimSpace(names[i])
			}
			names = slices.DeleteFunc(names, func(s string) bool { return s == "" })
			sort.Strings(names)
			names = slices.Compact(names)
			if len(names) == 0 || slices.ContainsFunc(names, func(s string) bool { return s != "data" && s != "query" }) {
				return usageError("give data, query or data,query", "--services takes data, query or data,query (got %q)", o.services)
			}
			o.services = strings.Join(names, ",")
			if o.replaceTTL != "" && !slices.Contains([]string{"none", "all", "expired"}, o.replaceTTL) {
				return usageError("use none, all or expired", "unknown --replace-ttl %q", o.replaceTTL)
			}
			if o.replaceTTLWith != "" && o.replaceTTL != "all" && o.replaceTTL != "expired" {
				return usageError("add --replace-ttl all or --replace-ttl expired", "--replace-ttl-with was given without --replace-ttl all|expired")
			}
			return nil
		},
		func(rt *runtime, args []string) error { return rt.runBucketRestore(args[0], args[1], o) })
	cmd.Long = "Restore a bucket backup into the same or another cluster of the same project. Without --force-updates,\n" +
		"documents that are newer in the target are kept. You confirm by typing the target cluster name (or pass --yes).\n\n" +
		"Examples:\n  cbctl capella bucket backup restore prod-01 BACKUP_ID --replace-ttl none\n" +
		"  cbctl capella bucket backup restore prod-01 BACKUP_ID --to-cluster dr-01 --include-data orders.sales"
	addProjectFlag(cmd, &o.project)
	f := cmd.Flags()
	f.StringVar(&o.toCluster, "to-cluster", "", "Restore into this cluster of the same project (default: the backup's cluster)")
	f.StringVar(&o.services, "services", "data", "What to restore: data, query or data,query")
	f.StringVar(&o.include, "include-data", "", "Restore only these buckets, scopes or collections (B.S.C,...)")
	f.StringVar(&o.exclude, "exclude-data", "", "Skip these buckets, scopes or collections")
	f.StringVar(&o.mapData, "map-data", "", "Restore into other buckets, scopes or collections (SRC=DST,...)")
	f.StringVar(&o.filterKeys, "filter-keys", "", "Only documents whose key matches this regex")
	f.StringVar(&o.filterValues, "filter-values", "", "Only documents whose value matches this regex")
	f.StringVar(&o.replaceTTL, "replace-ttl", "", "Give documents a new expiry: none, all or expired")
	f.StringVar(&o.replaceTTLWith, "replace-ttl-with", "", "The new expiry (with --replace-ttl all or expired)")
	f.BoolVar(&o.force, "force-updates", false, "Overwrite documents even when the target's are newer")
	f.BoolVar(&o.autoRemove, "auto-remove-collections", false, "Delete scopes and collections the backup knows were deleted")
	return cmd
}

func (rt *runtime) runBucketRestore(cluster, backupID string, o restoreOptions) error {
	t, err := rt.locateBackupCluster(cluster, o.project)
	if err != nil {
		return err
	}
	b, err := rt.getBackup(t, backupID)
	if err != nil {
		return err
	}
	target := t
	if o.toCluster != "" && o.toCluster != t.Cluster.Name {
		if target, err = rt.locateBackupCluster(o.toCluster, t.Project.Name); err != nil {
			return err
		}
	}
	if s := target.Cluster.CurrentState; s == "turnedOff" || s == "turningOff" {
		return blockedError(fmt.Sprintf("turn it on with 'cbctl capella cluster power %s --on', then run the command again", target.Cluster.Name),
			"cluster %s is turned off", target.Cluster.Name)
	}
	names := strings.Split(o.services, ",")
	body := capellaapi.Object{"targetClusterID": target.Cluster.ID, "sourceClusterID": t.Cluster.ID, "backupID": backupID, "services": names}
	for k, v := range map[string]string{"includeData": o.include, "excludeData": o.exclude, "mapData": o.mapData,
		"filterKeys": o.filterKeys, "filterValues": o.filterValues, "replaceTTL": o.replaceTTL, "replaceTTLWith": o.replaceTTLWith} {
		if v != "" {
			body[k] = v
		}
	}
	if o.force {
		body["forceUpdates"] = true
	}
	if o.autoRemove {
		body["autoRemoveCollections"] = true
	}
	bucket := strOf(b, "bucketName")
	rt.Out.Line("  ~ restore %s from the backup of %s UTC into %s", bucket, when(b["date"]), target.Cluster.Name)
	newer := fmt.Sprintf("newer documents in %s are kept", target.Cluster.Name)
	if o.force {
		newer = "overwrites newer documents (--force-updates)"
	}
	rt.Out.Line("    services: %s · %s", strings.Join(names, ", "), newer)
	for _, l := range []struct{ label, value string }{{"only", o.include}, {"skipping", o.exclude}, {"mapped", o.mapData}} {
		if l.value != "" {
			rt.Out.Line("    %s: %s", l.label, l.value)
		}
	}
	res := bucketBackupResult{Cluster: target.Cluster.Name, Bucket: bucket, BackupID: backupID, Status: "cancelled"}
	ok, err := confirmTyped(rt, "Type the target cluster name to confirm the restore", target.Cluster.Name)
	if err != nil {
		return err
	}
	if !ok {
		rt.report(res, "cancelled; nothing was restored")
		return nil
	}
	if err := rt.Client.RestoreBackup(rt.Ctx, t.Project.ID, t.Cluster.ID, backupID, body); err != nil {
		return conflictAsBlocked(err, target.Cluster.Name)
	}
	res.Status = "accepted"
	rt.report(res, "restore of %s into %s started; Capella reports its progress in the cluster's activity log (the Management API has no restore status for bucket backups)",
		bucket, target.Cluster.Name)
	return nil
}

type exportOptions struct{ project, exportID string }

func newBucketBackupExportCmd() *cobra.Command {
	var o exportOptions
	cmd := capellaLeaf("export CLUSTER BACKUP_ID", "Package a backup cycle as a zip and print its download link", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], o.project)
			if err != nil {
				return err
			}
			b, err := rt.getBackup(t, args[1])
			if err != nil {
				return err
			}
			bid, bucket := strOf(b, "bucketID"), strOf(b, "bucketName")
			exportID := o.exportID
			res := bucketBackupResult{Cluster: t.Cluster.Name, Bucket: bucket, BackupID: args[1]}
			if exportID == "" {
				exportID, err = rt.Client.ExportBackup(rt.Ctx, t.Project.ID, t.Cluster.ID, bid, args[1])
				switch {
				case errors.Is(err, capellaapi.ErrConflict):
					res.Status = "exists"
					rt.report(res, "the backup cycle of %s already has an export (Capella allows one per cycle); show it with --export-id ID (printed when it was started)", bucket)
					return nil
				case errors.Is(err, capellaapi.ErrValidation):
					return blockedError(fmt.Sprintf("choose another backup ('cbctl capella bucket backup list %s'), or take a new one with 'cbctl capella bucket backup create'", args[0]),
						"Capella cannot export this backup: %v", err)
				case err != nil:
					return err
				}
				logging.Info("export %s of %s started", exportID, bucket)
			}
			var found capellaapi.Object
			read := func() (string, error) {
				var err error
				found, err = rt.Client.GetBackupExport(rt.Ctx, t.Project.ID, t.Cluster.ID, bid, args[1], exportID)
				return strOf(found, "status"), err
			}
			if rt.NoWait {
				if _, err := read(); err != nil {
					return fmt.Errorf("reading export %s: %w", exportID, err)
				}
			} else if _, err := rt.waitBackupStatus("export of "+bucket, map[string]bool{"complete": true}, map[string]bool{"failed": true, "expired": true}, read); err != nil {
				return err
			}
			res.ExportID, res.Status = exportID, strOf(found, "status")
			if rt.JSON {
				found["exportId"] = exportID
				rt.Out.Object(found)
				return nil
			}
			if res.Status != "complete" {
				rt.Out.Line("export %s: %s; run this command with --export-id %s to check again", exportID, res.Status, exportID)
				return nil
			}
			rt.Out.Line("export %s is complete: %.1f GB, available until %s UTC", exportID, float64(numberOf(found["sizeInBytes"]))/(1<<30), when(found["expiration"]))
			rt.Out.Line("download (valid for one hour; treat it like a password):")
			rt.Out.Line("  %s", strOf(found, "backupDownloadURL"))
			return nil
		})
	cmd.Long = "Package a backup cycle as a zip and print its download link. The link is valid for one hour; run the\n" +
		"command again with --export-id for a new one.\n\nExamples:\n  cbctl capella bucket backup export prod-01 BACKUP_ID\n" +
		"  cbctl capella bucket backup export prod-01 BACKUP_ID --export-id EXPORT_ID"
	addProjectFlag(cmd, &o.project)
	cmd.Flags().StringVar(&o.exportID, "export-id", "", "Show an export started earlier (its status and a new download link)")
	return cmd
}
