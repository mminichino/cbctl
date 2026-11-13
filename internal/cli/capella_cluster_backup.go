package cli

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

func newCapellaClusterBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Cluster backups (cloud snapshots of the whole cluster)",
		Long: "Cluster backups are cloud snapshots of the whole cluster: schedule, list, create, retain, delete, restore.\n" +
			"Backups of one bucket: 'cbctl capella bucket backup --help'.\n\n" +
			"Examples:\n  cbctl capella cluster backup schedule set prod-01 --every 24 --retain 30d\n" +
			"  cbctl capella cluster backup list prod-01\n  cbctl capella cluster backup create prod-01\n" +
			"  cbctl capella cluster backup restore prod-01 BACKUP_ID --yes",
		Run: func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newBackupScheduleCmd(), newBackupListCmd(), newBackupCreateCmd(), newBackupRetainCmd(),
		newBackupDeleteCmd(), newBackupRestoreCmd(), newBackupRestoresCmd(), newBackupRegionsCmd())
	return cmd
}

func newBackupScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "When the cluster is backed up and how long backups are kept",
		Run:   func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newBackupScheduleShowCmd(), newBackupScheduleOptionsCmd(), newBackupScheduleSetCmd(), newBackupScheduleDeleteCmd())
	return cmd
}

// ---- schedule helpers (port of capellactl resources/backup_schedule.py) --------------------------

// startHM reads "02:00" or an RFC 3339 instant as "HH:MM" in UTC; "" when absent or unreadable.
func startHM(v any) string {
	s, _ := v.(string)
	if s == "" {
		return ""
	}
	if len(s) == 5 && s[2] == ':' {
		return s
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.UTC().Format("15:04")
}

var hmPattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// nextInstant is the next time of day hm (UTC) after now, as Capella's startTime.
func nextInstant(hm string, now time.Time) string {
	var h, m int
	_, _ = fmt.Sscanf(hm, "%d:%d", &h, &m)
	n := now.UTC()
	at := time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, time.UTC)
	if !at.After(n) {
		at = at.AddDate(0, 0, 1)
	}
	return at.Format("2006-01-02T15:04:05Z")
}

func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// snapshotSummary is "every 24 h · kept 30 days · first at 02:00 UTC · copies: us-west-2".
func snapshotSummary(body capellaapi.Object) string {
	parts := []string{
		fmt.Sprintf("every %d h", numberOf(body["interval"])),
		"kept " + hoursText(numberOf(body["retention"])),
	}
	if hm := startHM(body["startTime"]); hm != "" {
		parts = append(parts, "first at "+hm+" UTC")
	}
	if copies := stringList(body["copyToRegions"]); len(copies) > 0 {
		parts = append(parts, "copies: "+strings.Join(copies, ", "))
	}
	return strings.Join(parts, " · ")
}

// optionsProblem explains why a schedule is outside what the support plan permits, or "".
func optionsProblem(options capellaapi.Object, interval, retention int, copies []string, regions []string, checkRegions bool) string {
	var intervals []int
	if list, ok := options["intervals"].([]any); ok {
		for _, i := range list {
			intervals = append(intervals, numberOf(i))
		}
	}
	if len(intervals) > 0 && interval != 0 && !slices.Contains(intervals, interval) {
		strs := make([]string, len(intervals))
		for i, n := range intervals {
			strs[i] = fmt.Sprint(n)
		}
		return fmt.Sprintf("interval %d is not permitted: use one of %s hours for this cluster's support plan", interval, strings.Join(strs, ", "))
	}
	lo, hi := numberOf(options["minimumRetention"]), numberOf(options["maximumRetention"])
	if retention != 0 && options["minimumRetention"] != nil && options["maximumRetention"] != nil && (retention < lo || retention > hi) {
		return fmt.Sprintf("retention %s is not permitted: keep backups %s to %s for this cluster's support plan",
			hoursText(retention), hoursText(lo), hoursText(hi))
	}
	if len(copies) > 2 {
		return "copy regions take at most two regions"
	}
	if checkRegions {
		var bad []string
		for _, c := range copies {
			if !slices.Contains(regions, c) {
				bad = append(bad, c)
			}
		}
		if len(bad) > 0 {
			avail := strings.Join(regions, ", ")
			if avail == "" {
				avail = "none"
			}
			return fmt.Sprintf("copies cannot go to %s: available regions are %s", strings.Join(bad, ", "), avail)
		}
	}
	return ""
}

// nextBackup is the next scheduled backup after now: startTime plus whole intervals.
func nextBackup(body capellaapi.Object, now time.Time) (time.Time, bool) {
	start, err := time.Parse(time.RFC3339, strOf(body, "startTime"))
	step := time.Duration(numberOf(body["interval"])) * time.Hour
	if err != nil || step <= 0 {
		return time.Time{}, false
	}
	n := now.UTC()
	if start.After(n) {
		return start, true
	}
	return start.Add(step * (n.Sub(start)/step + 1)), true
}

// ---- schedule show / options / set / delete ------------------------------------------------------

func newBackupScheduleShowCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("show CLUSTER", "Show the cluster's backup schedule and the next backup", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			body, err := rt.Client.GetSnapshotSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("reading the backup schedule of %s: %w", t.Cluster.Name, err)
			}
			next, hasNext := time.Time{}, false
			if body != nil {
				next, hasNext = nextBackup(body, rt.Clock.Now())
			}
			if rt.JSON {
				doc := capellaapi.Object{"cluster": t.Cluster.Name}
				for k, v := range body {
					doc[k] = v
				}
				if hasNext {
					doc["nextBackup"] = next.Format("2006-01-02T15:04:05Z")
				}
				rt.Out.Object(doc)
				return nil
			}
			if body == nil {
				rt.Out.Line("cluster %s has no backup schedule; set one with 'cbctl capella cluster backup schedule set %s --every HOURS --retain DAYSd'",
					t.Cluster.Name, t.Cluster.Name)
				return nil
			}
			rt.Out.Line("%s", snapshotSummary(body))
			if hasNext {
				rt.Out.Line("next backup: %s UTC", next.Format("2006-01-02 15:04"))
			}
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

func newBackupScheduleOptionsCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("options CLUSTER", "What the support plan permits: intervals, retention and copy regions", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			opts, err := rt.Client.GetSnapshotScheduleOptions(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("reading the backup options of %s: %w", t.Cluster.Name, err)
			}
			regions, err := rt.Client.ListSnapshotRegions(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("listing the copy regions of %s: %w", t.Cluster.Name, err)
			}
			if rt.JSON {
				doc := capellaapi.Object{"cluster": t.Cluster.Name, "regions": nonNil(regions)}
				for k, v := range opts {
					doc[k] = v
				}
				rt.Out.Object(doc)
				return nil
			}
			plan := "?"
			if t.Cluster.Support != nil {
				plan = t.Cluster.Support.Plan
			}
			var intervals []string
			if list, ok := opts["intervals"].([]any); ok {
				for _, i := range list {
					intervals = append(intervals, fmt.Sprint(numberOf(i)))
				}
			}
			rt.Out.Line("Cluster backup schedule options for %s (support plan: %s)", t.Cluster.Name, plan)
			rt.Out.Line("  every (hours)   %s", strings.Join(intervals, ", "))
			rt.Out.Line("  keep            %s to %s", hoursText(numberOf(opts["minimumRetention"])), hoursText(numberOf(opts["maximumRetention"])))
			if len(regions) > 0 {
				rt.Out.Line("  copy regions    %s (up to two)", strings.Join(regions, ", "))
			} else {
				rt.Out.Line("  copy regions    none (AWS and Azure only)")
			}
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

type backupScheduleResult struct {
	Cluster string `json:"cluster"`
	Status  string `json:"status"` // created | updated | unchanged | deleted | absent | cancelled
	Summary string `json:"summary,omitempty"`
}

func newBackupScheduleSetCmd() *cobra.Command {
	var project, retain, start string
	var every int
	var copyTo []string
	var noCopies bool
	var cmd *cobra.Command
	cmd = capellaLeaf("set CLUSTER", "Create or change the backup schedule", cobra.ExactArgs(1),
		func([]string) error {
			ch := cmd.Flags().Changed
			if !ch("every") && !ch("retain") && !ch("start") && !ch("copy-to") && !noCopies {
				return usageError("add at least one of --every, --retain, --start, --copy-to or --no-copies", "nothing to set: no schedule option was given")
			}
			if ch("copy-to") && noCopies {
				return usageError("use either --copy-to or --no-copies", "--copy-to and --no-copies are mutually exclusive")
			}
			if ch("start") && !hmPattern.MatchString(start) {
				return usageError("give a UTC time of day as HH:MM, for example --start 02:00", "--start takes a UTC time of day HH:MM, e.g. 02:00 (got %q)", start)
			}
			if ch("every") && every <= 0 {
				return usageError("give a number of hours", "--every must be a positive number of hours")
			}
			if ch("retain") {
				if _, err := parseHours(retain, "--retain"); err != nil {
					return err
				}
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			return rt.runBackupScheduleSet(t, cmd, every, retain, start, copyTo, noCopies)
		})
	cmd.Long = "Create or change the cluster's backup schedule. On an existing schedule only the values you give change.\n" +
		"What the support plan permits: 'cbctl capella cluster backup schedule options CLUSTER'.\n\n" +
		"Examples:\n  cbctl capella cluster backup schedule set prod-01 --every 24 --retain 30d\n" +
		"  cbctl capella cluster backup schedule set prod-01 --every 6 --retain 7d --start 02:00 --copy-to us-west-2\n" +
		"  cbctl capella cluster backup schedule set prod-01 --retain 90d"
	addProjectFlag(cmd, &project)
	cmd.Flags().IntVar(&every, "every", 0, "Hours between backups (needed for a new schedule)")
	cmd.Flags().StringVar(&retain, "retain", "", "How long each backup is kept: hours or days, e.g. 720, 720h, 30d (needed for a new schedule)")
	cmd.Flags().StringVar(&start, "start", "", "Time of day (UTC, HH:MM) of the first backup")
	cmd.Flags().StringArrayVar(&copyTo, "copy-to", nil, "Also keep a copy in this region (repeatable; at most two)")
	cmd.Flags().BoolVar(&noCopies, "no-copies", false, "Stop keeping copies in other regions")
	return cmd
}

func (rt *runtime) runBackupScheduleSet(t *clusterTarget, cmd *cobra.Command, every int, retain, start string, copyTo []string, noCopies bool) error {
	ch := cmd.Flags().Changed
	cur, err := rt.Client.GetSnapshotSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID)
	if err != nil {
		return fmt.Errorf("reading the backup schedule of %s: %w", t.Cluster.Name, err)
	}
	isNew := cur == nil
	if isNew && (!ch("every") || !ch("retain")) {
		return usageError(fmt.Sprintf("add both; 'cbctl capella cluster backup schedule options %s' lists the values Capella permits", t.Cluster.Name),
			"a new backup schedule needs --every HOURS and --retain DURATION")
	}
	interval, retention := numberOf(cur["interval"]), numberOf(cur["retention"])
	copies := stringList(cur["copyToRegions"])
	startTime, _ := cur["startTime"].(string)
	if ch("every") {
		interval = every
	}
	if ch("retain") {
		retention, _ = parseHours(retain, "--retain")
	}
	switch {
	case ch("copy-to"):
		copies = copyTo
	case noCopies:
		copies = nil
	}

	opts, err := rt.Client.GetSnapshotScheduleOptions(rt.Ctx, t.Project.ID, t.Cluster.ID)
	if err == nil {
		var regions []string
		if len(copies) > 0 && ch("copy-to") {
			if regions, err = rt.Client.ListSnapshotRegions(rt.Ctx, t.Project.ID, t.Cluster.ID); err != nil {
				return fmt.Errorf("listing the copy regions of %s: %w", t.Cluster.Name, err)
			}
		}
		if problem := optionsProblem(opts, interval, retention, copies, regions, ch("copy-to")); problem != "" {
			return usageError(fmt.Sprintf("choose a value from 'cbctl capella cluster backup schedule options %s'", t.Cluster.Name), "%s", problem)
		}
	} else if !errors.Is(err, capellaapi.ErrNotFound) && !errors.Is(err, capellaapi.ErrForbidden) {
		return fmt.Errorf("reading the backup options of %s: %w", t.Cluster.Name, err)
	}

	body := capellaapi.Object{"interval": interval, "retention": retention}
	if ch("start") {
		startTime = nextInstant(start, rt.Clock.Now())
	}
	if startTime != "" {
		body["startTime"] = startTime
	}
	body["copyToRegions"] = nonNil(copies)

	res := backupScheduleResult{Cluster: t.Cluster.Name, Summary: snapshotSummary(body)}
	if !isNew && snapshotSummary(cur) == res.Summary {
		res.Status = "unchanged"
		rt.report(res, "backup schedule of %s already matches; nothing to do", t.Cluster.Name)
		return nil
	}
	if err := rt.Client.UpsertSnapshotSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, body); err != nil {
		return conflictAsBlocked(err, t.Cluster.Name)
	}
	res.Status = "updated"
	verb := "updated"
	if isNew {
		res.Status, verb = "created", "created"
	}
	rt.report(res, "%s the backup schedule of %s: %s", verb, t.Cluster.Name, res.Summary)
	return nil
}

func newBackupScheduleDeleteCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("delete CLUSTER", "Delete the backup schedule (existing backups are kept until they expire)", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			res := backupScheduleResult{Cluster: t.Cluster.Name}
			body, err := rt.Client.GetSnapshotSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("reading the backup schedule of %s: %w", t.Cluster.Name, err)
			}
			if body == nil {
				res.Status = "absent"
				rt.report(res, "cluster %s has no backup schedule; nothing to do", t.Cluster.Name)
				return nil
			}
			res.Summary = snapshotSummary(body)
			rt.Out.Line("  - backup schedule %s  %s", t.Cluster.Name, res.Summary)
			ok, err := confirm(rt, fmt.Sprintf("Delete the backup schedule of %s? Existing backups are kept until they expire.", t.Cluster.Name))
			if err != nil {
				return err
			}
			if !ok {
				res.Status = "cancelled"
				rt.report(res, "cancelled; the backup schedule is unchanged")
				return nil
			}
			if err := rt.Client.DeleteSnapshotSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "deleted"
			rt.report(res, "deleted the backup schedule of %s", t.Cluster.Name)
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

// ---- backups ----------------------------------------------------------------------------------

func snapshotRows(items []capellaapi.Object) [][]string {
	sorted := slices.Clone(items)
	sort.SliceStable(sorted, func(i, j int) bool { return strOf(sorted[i], "createdAt") > strOf(sorted[j], "createdAt") })
	rows := make([][]string, 0, len(sorted))
	for _, s := range sorted {
		var copies []string
		if list, ok := s["crossRegionCopies"].([]any); ok {
			for _, c := range list {
				if m, ok := c.(map[string]any); ok {
					if r, _ := m["regionCode"].(string); r != "" {
						copies = append(copies, r)
					}
				}
			}
		}
		cp := "-"
		if len(copies) > 0 {
			cp = strings.Join(copies, ", ")
		}
		rows = append(rows, []string{when(s["createdAt"]), strOf(s, "type"), nestedStr(s, "progress", "status"),
			sizeText(s["databaseSize"]), when(s["expiration"]), cp, strOf(s, "id")})
	}
	return rows
}

func newBackupListCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("list [CLUSTER]", "List a cluster's backups, newest first (or the newest and oldest of every cluster)", cobra.MaximumNArgs(1), nil,
		func(rt *runtime, args []string) error {
			if len(args) == 0 {
				return rt.listProjectBackups(project)
			}
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			items, err := rt.Client.ListSnapshots(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("listing the backups of %s: %w", t.Cluster.Name, err)
			}
			rt.Out.List(items)
			if len(items) == 0 {
				rt.Out.Line("no backups")
				return nil
			}
			rt.Out.Table([]string{"CREATED", "TYPE", "STATUS", "SIZE", "EXPIRES", "COPIES", "ID"}, snapshotRows(items))
			return nil
		})
	cmd.Long = "List a cluster's backups, newest first. Without CLUSTER, list the newest and oldest backup of each\n" +
		"cluster of every project (or of --project).\n\n" +
		"Examples:\n  cbctl capella cluster backup list prod-01\n  cbctl capella cluster backup list --project prod"
	cmd.Flags().StringVar(&project, "project", "", "Project NAME (with CLUSTER: disambiguates the name; without: limits the listing)")
	return cmd
}

func (rt *runtime) listProjectBackups(project string) error {
	var projects []capellaapi.Project
	if project != "" {
		p, err := rt.resolveProject(project)
		if err != nil {
			return err
		}
		projects = []capellaapi.Project{*p}
	} else {
		var err error
		if projects, err = rt.Client.ListProjects(rt.Ctx); err != nil {
			return fmt.Errorf("listing projects: %w", err)
		}
	}
	var items []capellaapi.Object
	for _, p := range projects {
		snaps, err := rt.Client.ListProjectSnapshots(rt.Ctx, p.ID)
		if err != nil {
			return fmt.Errorf("listing the backups of project %s: %w", p.Name, err)
		}
		for _, s := range snaps {
			s["projectName"] = p.Name
			items = append(items, s)
		}
	}
	rt.Out.List(items)
	if len(items) == 0 {
		rt.Out.Line("no backups")
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if a, b := strOf(items[i], "clusterName"), strOf(items[j], "clusterName"); a != b {
			return a < b
		}
		return strOf(items[i], "projectName") < strOf(items[j], "projectName")
	})
	rows := make([][]string, len(items))
	for i, c := range items {
		newest, _ := c["mostRecentSnapshot"].(map[string]any)
		oldest, _ := c["oldestSnapshot"].(map[string]any)
		rows[i] = []string{strOf(c, "clusterName"), strOf(c, "projectName"), strOf(c, "region"), when(newest["createdAt"]), when(oldest["createdAt"]), strOf(c, "clusterId")}
	}
	rt.Out.Table([]string{"CLUSTER", "PROJECT", "REGION", "NEWEST", "OLDEST", "CLUSTER ID"}, rows)
	return nil
}

// findSnapshot returns the cluster's backup with the given ID, or nil.
func (rt *runtime) findSnapshot(t *clusterTarget, id string) (capellaapi.Object, error) {
	items, err := rt.Client.ListSnapshots(rt.Ctx, t.Project.ID, t.Cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("listing the backups of %s: %w", t.Cluster.Name, err)
	}
	for _, s := range items {
		if strOf(s, "id") == id {
			return s, nil
		}
	}
	return nil, nil
}

func (rt *runtime) requireSnapshot(t *clusterTarget, id string) (capellaapi.Object, error) {
	s, err := rt.findSnapshot(t, id)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, usageError(fmt.Sprintf("list them with 'cbctl capella cluster backup list %s'", t.Cluster.Name), "backup %s of %s not found", id, t.Cluster.Name)
	}
	return s, nil
}

// backupResult is the JSON document of backup create, retain, delete and restore.
type backupResult struct {
	Cluster   string `json:"cluster"`
	BackupID  string `json:"backupId,omitempty"`
	RestoreID string `json:"restoreId,omitempty"`
	Status    string `json:"status"` // complete | accepted | changed | unchanged | deleted | absent | cancelled
}

func newBackupCreateCmd() *cobra.Command {
	var project, retain string
	var copyTo []string
	cmd := capellaLeaf("create CLUSTER", "Back the cluster up now and wait until the backup is complete", cobra.ExactArgs(1),
		func([]string) error {
			if retain != "" {
				_, err := parseHours(retain, "--retain")
				return err
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			body := capellaapi.Object{}
			if retain != "" {
				body["retention"], _ = parseHours(retain, "--retain")
			}
			if len(copyTo) > 0 {
				regions, err := rt.Client.ListSnapshotRegions(rt.Ctx, t.Project.ID, t.Cluster.ID)
				if err != nil {
					return fmt.Errorf("listing the copy regions of %s: %w", t.Cluster.Name, err)
				}
				if problem := optionsProblem(nil, 0, 0, copyTo, regions, true); problem != "" {
					return usageError(fmt.Sprintf("choose a region from 'cbctl capella cluster backup regions %s'", t.Cluster.Name), "%s", problem)
				}
				body["regionsToCopy"] = copyTo
			}
			logging.Info("backing up cluster %s", t.Cluster.Name)
			id, err := rt.Client.CreateSnapshot(rt.Ctx, t.Project.ID, t.Cluster.ID, body)
			if err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res := backupResult{Cluster: t.Cluster.Name, BackupID: id, Status: "accepted"}
			if rt.NoWait {
				rt.report(res, "backup %s of %s started (--no-wait); see it with 'cbctl capella cluster backup list %s'", id, t.Cluster.Name, t.Cluster.Name)
				return nil
			}
			label := fmt.Sprintf("backup %s of %s", id, t.Cluster.Name)
			if _, err := rt.waitBackupStatus(label, backupDone, backupFailed, func() (string, error) {
				s, err := rt.findSnapshot(t, id)
				if s == nil {
					return "", err
				}
				return nestedStr(s, "progress", "status"), err
			}); err != nil {
				return err
			}
			res.Status = "complete"
			rt.report(res, "backup %s of %s is complete", id, t.Cluster.Name)
			return nil
		})
	cmd.Long = "Back the cluster up now and wait until the backup is complete.\n\n" +
		"Examples:\n  cbctl capella cluster backup create prod-01\n  cbctl capella cluster backup create prod-01 --retain 7d --copy-to us-west-2\n" +
		"  cbctl capella cluster backup create prod-01 --no-wait"
	addProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&retain, "retain", "", "How long to keep it: hours or days, e.g. 168 or 7d (default: Capella's)")
	cmd.Flags().StringArrayVar(&copyTo, "copy-to", nil, "Also keep a copy in this region (repeatable; at most two)")
	return cmd
}

func newBackupRetainCmd() *cobra.Command {
	var project, retain string
	cmd := capellaLeaf("retain CLUSTER BACKUP_ID", "Keep one backup longer (or shorter)", cobra.ExactArgs(2),
		func([]string) error {
			if retain == "" {
				return usageError("add --retain, for example --retain 90d", "--retain is required")
			}
			_, err := parseHours(retain, "--retain")
			return err
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			hours, _ := parseHours(retain, "--retain")
			s, err := rt.requireSnapshot(t, args[1])
			if err != nil {
				return err
			}
			res := backupResult{Cluster: t.Cluster.Name, BackupID: args[1], Status: "unchanged"}
			was := numberOf(s["retention"])
			if was == hours {
				rt.report(res, "no changes: backup %s is already kept %s", args[1], hoursText(hours))
				return nil
			}
			if err := rt.Client.UpdateSnapshotRetention(rt.Ctx, t.Project.ID, t.Cluster.ID, args[1], hours); err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "changed"
			rt.report(res, "backup %s of %s is now kept %s (was %s)", args[1], t.Cluster.Name, hoursText(hours), hoursText(was))
			return nil
		})
	addProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&retain, "retain", "", "How long to keep it, counted from when it was taken: hours or days, e.g. 2160 or 90d (required)")
	return cmd
}

func newBackupDeleteCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("delete CLUSTER BACKUP_ID", "Delete one backup of the cluster", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			res := backupResult{Cluster: t.Cluster.Name, BackupID: args[1]}
			s, err := rt.findSnapshot(t, args[1])
			if err != nil {
				return err
			}
			if s == nil {
				res.Status = "absent"
				rt.report(res, "backup %s of %s not found; nothing to do", args[1], t.Cluster.Name)
				return nil
			}
			rt.Out.Line("  - backup %s of %s  taken %s UTC · %s", args[1], t.Cluster.Name, when(s["createdAt"]), sizeText(s["databaseSize"]))
			ok, err := confirmTyped(rt, "Type the cluster name to confirm deletion", t.Cluster.Name)
			if err != nil {
				return err
			}
			if !ok {
				res.Status = "cancelled"
				rt.report(res, "cancelled; nothing was deleted")
				return nil
			}
			if err := rt.Client.DeleteSnapshot(rt.Ctx, t.Project.ID, t.Cluster.ID, args[1]); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "deleted"
			rt.report(res, "deleted backup %s of %s", args[1], t.Cluster.Name)
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

func newBackupRestoreCmd() *cobra.Command {
	var project string
	var fromRegion []string
	cmd := capellaLeaf("restore CLUSTER BACKUP_ID", "Replace all data of the cluster with a backup and wait until the cluster is healthy", cobra.ExactArgs(2), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			if s := t.Cluster.CurrentState; s == "turnedOff" || s == "turningOff" {
				return blockedError(fmt.Sprintf("turn it on with 'cbctl capella cluster power %s --on', then run the command again", t.Cluster.Name),
					"cluster %s is turned off", t.Cluster.Name)
			}
			s, err := rt.requireSnapshot(t, args[1])
			if err != nil {
				return err
			}
			rt.Out.Line("  ± cluster %s  all data is replaced with the backup of %s UTC", t.Cluster.Name, when(s["createdAt"]))
			ok, err := confirmTyped(rt, "Type the cluster name to confirm the restore", t.Cluster.Name)
			if err != nil {
				return err
			}
			res := backupResult{Cluster: t.Cluster.Name, BackupID: args[1], Status: "cancelled"}
			if !ok {
				rt.report(res, "cancelled; nothing was restored")
				return nil
			}
			body := capellaapi.Object{}
			if len(fromRegion) > 0 {
				body["crossRegionRestorePreference"] = fromRegion
			}
			logging.Info("restoring cluster %s", t.Cluster.Name)
			restoreID, err := rt.Client.RestoreSnapshot(rt.Ctx, t.Project.ID, t.Cluster.ID, args[1], body)
			if err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.RestoreID, res.Status = restoreID, "accepted"
			if rt.NoWait {
				rt.report(res, "restore %s of %s started (--no-wait); follow it with 'cbctl capella cluster backup restores %s'", restoreID, t.Cluster.Name, t.Cluster.Name)
				return nil
			}
			read := func() (string, error) {
				items, err := rt.Client.ListSnapshotRestores(rt.Ctx, t.Project.ID, t.Cluster.ID)
				for _, r := range items {
					if strOf(r, "id") == restoreID {
						return strOf(r, "status"), err
					}
				}
				return "", err
			}
			if _, err := rt.waitBackupStatus("restore of "+t.Cluster.Name, backupDone, backupFailed, read); err != nil {
				return err
			}
			if _, err := rt.waitCluster(t.Project.ID, t.Cluster.ID, t.Cluster.Name, engine.OpReady, false); err != nil {
				return err
			}
			res.Status = "complete"
			rt.report(res, "restored cluster %s from the backup of %s UTC", t.Cluster.Name, when(s["createdAt"]))
			return nil
		})
	cmd.Long = "Replace all data of the cluster with a backup, and wait until the cluster is healthy. You confirm by typing\n" +
		"the cluster name (or pass --yes).\n\nExamples:\n  cbctl capella cluster backup restore prod-01 BACKUP_ID\n" +
		"  cbctl capella cluster backup restore prod-01 BACKUP_ID --from-region us-west-2 --yes"
	addProjectFlag(cmd, &project)
	cmd.Flags().StringArrayVar(&fromRegion, "from-region", nil, "Restore from the copy in this region if the original is not restorable (repeatable, in order)")
	return cmd
}

func newBackupRestoresCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("restores CLUSTER", "List the restores of the cluster", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			items, err := rt.Client.ListSnapshotRestores(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("listing the restores of %s: %w", t.Cluster.Name, err)
			}
			rt.Out.List(items)
			if len(items) == 0 {
				rt.Out.Line("no restores")
				return nil
			}
			sort.SliceStable(items, func(i, j int) bool { return strOf(items[i], "createdAt") > strOf(items[j], "createdAt") })
			rows := make([][]string, len(items))
			for i, r := range items {
				fin := when(r["restoreEnd"])
				if fin == "" {
					fin = "-"
				}
				rows[i] = []string{when(r["createdAt"]), fin, when(r["snapshot"]), strOf(r, "status"), strOf(r, "id")}
			}
			rt.Out.Table([]string{"STARTED", "FINISHED", "SNAPSHOT", "STATUS", "ID"}, rows)
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

func newBackupRegionsCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("regions CLUSTER", "List the regions where copies of the backups can be kept (AWS and Azure)", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateBackupCluster(args[0], project)
			if err != nil {
				return err
			}
			regions, err := rt.Client.ListSnapshotRegions(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if err != nil {
				return fmt.Errorf("listing the copy regions of %s: %w", t.Cluster.Name, err)
			}
			rt.Out.List(nonNil(regions))
			if len(regions) == 0 {
				rt.Out.Line("no copy regions (AWS and Azure only)")
				return nil
			}
			rows := make([][]string, len(regions))
			for i, r := range regions {
				rows[i] = []string{r}
			}
			rt.Out.Table([]string{"REGION"}, rows)
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}
