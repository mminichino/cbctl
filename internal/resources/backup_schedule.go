package resources

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/plan"
)

const (
	kindBackupSchedule       = "backup-schedule"
	kindBucketBackupSchedule = "bucket-backup-schedule"
)

// BucketBackupFields are the keys of a bucket backup schedule, in the order they are compared.
var BucketBackupFields = []string{"dayOfWeek", "startAt", "incrementalEvery", "retentionTime", "costOptimizedRetention"}

// BucketBackupDefaults fill the fields a bucket backup schedule leaves out.
var BucketBackupDefaults = Obj{
	"dayOfWeek": "sunday", "startAt": int64(0), "incrementalEvery": int64(24),
	"retentionTime": "30days", "costOptimizedRetention": false,
}

var retentionWords = map[string]string{
	"30days": "30 days", "60days": "60 days", "90days": "90 days", "180days": "180 days",
	"1year": "1 year", "2years": "2 years", "3years": "3 years", "4years": "4 years", "5years": "5 years",
}

// HoursText renders 720 as "30 days" and 36 as "36 h".
func HoursText(hours any) string {
	h := intOf(hours)
	if h != 0 && h%24 == 0 {
		return plural(h/24, "day")
	}
	return fmt.Sprintf("%d h", h)
}

// StartHM reads "02:00" or an ISO 8601 instant as "HH:MM" in UTC; "" when absent or unreadable.
func StartHM(v any) string {
	s := strings.TrimSpace(pyPlain(v))
	if s == "" {
		return ""
	}
	if len(s) == 5 && s[2] == ':' {
		return s
	}
	t, ok := ParseTime(s)
	if !ok {
		return ""
	}
	t = t.UTC()
	return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
}

// NextInstant is the next time of day hm (UTC) after now, as Capella's startTime.
func NextInstant(hm string, now time.Time) string {
	now = now.UTC()
	h, m, _ := strings.Cut(hm, ":")
	hour, _ := strconv.Atoi(h)
	minute, _ := strconv.Atoi(m)
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at.Format("2006-01-02T15:04:05Z")
}

// SnapshotSummary is `every 24 h · kept 30 days · first at 02:00 UTC · copies: us-west-2`.
func SnapshotSummary(body Obj) string {
	retention := body["retention"]
	if !truthy(retention) {
		retention = int64(0)
	}
	parts := []string{fmt.Sprintf("every %s h", pyStr(body["interval"])), "kept " + HoursText(retention)}
	if hm := StartHM(body["startTime"]); hm != "" {
		parts = append(parts, "first at "+hm+" UTC")
	}
	if regions := stringsOf(body["copyToRegions"]); len(regions) > 0 {
		parts = append(parts, "copies: "+strings.Join(regions, ", "))
	}
	return strings.Join(parts, " · ")
}

// SnapshotBody is the schedule as the API takes it.
func SnapshotBody(desired Obj, now time.Time) Obj {
	body := Obj{"interval": intOf(desired["interval"]), "retention": intOf(desired["retention"])}
	if truthy(desired["startTime"]) {
		body["startTime"] = NextInstant(pyPlain(desired["startTime"]), now)
	}
	copies := stringsOf(desired["copyToRegions"])
	if copies == nil {
		copies = []string{}
	}
	body["copyToRegions"] = copies
	return body
}

// OptionsProblem says why a schedule is outside what the cluster's support plan permits, or "".
// regions is nil when the available regions were not read.
func OptionsProblem(options, desired Obj, regions []string, haveRegions bool) string {
	var intervals []int64
	for _, i := range listOf(options["intervals"]) {
		intervals = append(intervals, intOf(i))
	}
	interval := intOf(desired["interval"])
	if len(intervals) > 0 {
		ok := false
		var shown []string
		for _, i := range intervals {
			ok = ok || i == interval
			shown = append(shown, strconv.FormatInt(i, 10))
		}
		if !ok {
			return fmt.Sprintf("interval %s is not permitted: use one of %s hours for this cluster's support plan",
				pyStr(desired["interval"]), strings.Join(shown, ", "))
		}
	}
	lo, hi := options["minimumRetention"], options["maximumRetention"]
	ret := intOf(desired["retention"])
	if (lo != nil && ret < intOf(lo)) || (hi != nil && ret > intOf(hi)) {
		return fmt.Sprintf("retention %s is not permitted: keep backups %s to %s for this cluster's support plan",
			HoursText(ret), HoursText(orZero(lo)), HoursText(orZero(hi)))
	}
	copies := stringsOf(desired["copyToRegions"])
	if len(copies) > 2 {
		return "copyToRegions takes at most two regions"
	}
	if haveRegions {
		var bad []string
		for _, r := range copies {
			if !contains(regions, r) {
				bad = append(bad, r)
			}
		}
		if len(bad) > 0 {
			shown := strings.Join(regions, ", ")
			if shown == "" {
				shown = "none"
			}
			return fmt.Sprintf("copies cannot go to %s: available regions are %s", strings.Join(bad, ", "), shown)
		}
	}
	return ""
}

func orZero(v any) any {
	if !truthy(v) {
		return int64(0)
	}
	return v
}

func clusterIDs(parent *plan.Action) (string, string) {
	if parent == nil {
		return "", ""
	}
	return projectID(parent), parent.ID()
}

// backupScheduleRec reconciles a cluster's cloud snapshot backup schedule.
type backupScheduleRec struct{ plan.Base }

func newBackupSchedule() *backupScheduleRec {
	r := &backupScheduleRec{Base: plan.NewBase(kindBackupSchedule, plan.Flags{Prunable: true, ReportsUnmanaged: true})}
	r.Bind(r)
	return r
}

func (r *backupScheduleRec) DesiredItems(parent any) []any {
	if b := objOf(objOf(parent)["backupSchedule"]); b != nil {
		return []any{b}
	}
	return nil
}

func (r *backupScheduleRec) Key(_ any, parent *plan.Action) string {
	if parent == nil {
		return ""
	}
	return parent.Key
}

func (r *backupScheduleRec) UnmanagedLabel(o plan.Observed, parent *plan.Action) string {
	key := ""
	if parent != nil {
		key = parent.Key
	}
	return fmt.Sprintf("backup-schedule %s  %s", key, SnapshotSummary(o.Body))
}

// Observe reads the schedule. A key that may not read it (403) is reported as a warning and
// leaves the schedule unknown; it is never treated as proof that none exists, and a 409 on create
// re-observes.
func (r *backupScheduleRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	if parent != nil && truthy(parent.Data["free"]) {
		return nil, nil // no backups on the free tier
	}
	pid, cid := clusterIDs(parent)
	if err := needIDs("backup-schedule", pid, cid); err != nil {
		return nil, err
	}
	body, err := rc.Client.GetSnapshotSchedule(ctx, pid, cid)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		if isForbidden(err) {
			rc.Warning(fmt.Sprintf("backup schedules not read: cluster %s (the API key may not read backup schedules)", parent.Key),
				"Use an Organization Owner or Project Owner key to plan backup schedules.")
			return nil, nil
		}
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	return []plan.Observed{{ID: cid, Body: normObj(body)}}, nil
}

func (r *backupScheduleRec) Match(_ any, observed []plan.Observed) ([]plan.Observed, error) {
	if len(observed) > 1 {
		return observed[:1], nil
	}
	return observed, nil
}

func (r *backupScheduleRec) Diff(ctx context.Context, rc *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if desired["ensure"] == "absent" {
		if observed == nil {
			a := newAction(r, kindBackupSchedule, key, plan.Noop, desired, nil, parent)
			a.Hidden = true
			return a, nil
		}
		a := newAction(r, kindBackupSchedule, key, plan.Delete, desired, observed, parent)
		a.Summary = SnapshotSummary(observed.Body)
		return a, nil
	}
	var a *plan.Action
	if observed == nil {
		a = newAction(r, kindBackupSchedule, key, plan.Create, desired, nil, parent)
		a.Summary = SnapshotSummary(desired)
	} else {
		have := observed.Body
		var changes []plan.Change
		for _, f := range []string{"interval", "retention"} {
			if intOf(have[f]) != intOf(desired[f]) {
				changes = append(changes, plan.Change{Path: f, Before: have[f], After: desired[f]})
			}
		}
		haveRegions, wantRegions := stringsOf(have["copyToRegions"]), stringsOf(desired["copyToRegions"])
		if !sameSorted(haveRegions, wantRegions) {
			changes = append(changes, plan.Change{Path: "copyToRegions", Before: anyStrings(haveRegions), After: anyStrings(wantRegions)})
		}
		if truthy(desired["startTime"]) && StartHM(have["startTime"]) != pyPlain(desired["startTime"]) {
			var before any
			if hm := StartHM(have["startTime"]); hm != "" {
				before = hm
			}
			changes = append(changes, plan.Change{Path: "startTime", Before: before, After: desired["startTime"]})
		}
		t := plan.Noop
		if len(changes) > 0 {
			t = plan.Update
		}
		a = newAction(r, kindBackupSchedule, key, t, desired, observed, parent)
		a.Changes = changes
	}
	if a.Type != plan.Noop && parent != nil && parent.ID() != "" {
		if err := r.checkOptions(ctx, rc, a, parent); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func anyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func sameSorted(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// checkOptions blocks values the support plan does not permit, before any write.
func (r *backupScheduleRec) checkOptions(ctx context.Context, rc *plan.RunContext, a *plan.Action, parent *plan.Action) error {
	pid, cid := clusterIDs(parent)
	options, err := rc.Client.GetSnapshotScheduleOptions(ctx, pid, cid)
	if err != nil {
		if isNotFound(err) || isForbidden(err) {
			return nil // Capella checks it on the write
		}
		return err
	}
	desired := objOf(a.Desired)
	var regions []string
	haveRegions := false
	if len(stringsOf(desired["copyToRegions"])) > 0 {
		if regions, err = rc.Client.ListSnapshotRegions(ctx, pid, cid); err != nil {
			if isNotFound(err) || isForbidden(err) {
				return nil
			}
			return err
		}
		haveRegions = true
	}
	if problem := OptionsProblem(normObj(options), desired, regions, haveRegions); problem != "" {
		a.Type, a.Reason, a.Topic = plan.Blocked, problem, "backup"
	}
	return nil
}

func (r *backupScheduleRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	obs := o
	a := newAction(r, kindBackupSchedule, r.Key(nil, parent), plan.Delete, Obj{"ensure": "absent"}, &obs, parent)
	a.Summary = SnapshotSummary(o.Body)
	return a, nil
}

func (r *backupScheduleRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid := clusterIDs(a.Parent)
	if err := needIDs("backup-schedule "+a.Key, pid, cid); err != nil {
		return err
	}
	if a.Type == plan.Delete {
		return ignoreNotFound(rc.Client.DeleteSnapshotSchedule(ctx, pid, cid))
	}
	return rc.Client.UpsertSnapshotSchedule(ctx, pid, cid, SnapshotBody(objOf(a.Desired), rc.Clock.Now()))
}

// BucketSchedule is the file's flat form with defaults: the API's weeklySchedule.
func BucketSchedule(desired Obj) Obj {
	out := Obj{}
	for _, f := range BucketBackupFields {
		if v, ok := desired[f]; ok {
			out[f] = v
		} else {
			out[f] = BucketBackupDefaults[f]
		}
	}
	return out
}

// BucketSummary is `weekly full on sunday at 02:00 UTC · incremental every 12 h · kept 90 days`.
func BucketSummary(weekly Obj) string {
	w := mergeObj(BucketBackupDefaults, weekly)
	retention := pyPlain(w["retentionTime"])
	if words, ok := retentionWords[retention]; ok {
		retention = words
	}
	text := fmt.Sprintf("weekly full on %s at %02d:00 UTC · incremental every %s h · kept %s",
		pyPlain(w["dayOfWeek"]), intOf(w["startAt"]), pyPlain(w["incrementalEvery"]), retention)
	if truthy(w["costOptimizedRetention"]) {
		text += " · cost-optimized"
	}
	return text
}

func bucketIDs(bucket *plan.Action) (pid, cid, bid string) {
	if bucket == nil || bucket.Parent == nil {
		return "", "", ""
	}
	return projectID(bucket), bucket.Parent.ID(), bucket.ID()
}

// bucketBackupRec reconciles a bucket's weekly backup schedule.
type bucketBackupRec struct{ plan.Base }

func newBucketBackupSchedule() *bucketBackupRec {
	r := &bucketBackupRec{Base: plan.NewBase(kindBucketBackupSchedule, plan.Flags{Prunable: true, ReportsUnmanaged: true})}
	r.Bind(r)
	return r
}

func (r *bucketBackupRec) DesiredItems(parent any) []any {
	if b := objOf(objOf(parent)["backupSchedule"]); b != nil {
		return []any{b}
	}
	return nil
}

func (r *bucketBackupRec) Key(_ any, parent *plan.Action) string {
	if parent == nil {
		return ""
	}
	return parent.Key
}

func (r *bucketBackupRec) UnmanagedLabel(o plan.Observed, parent *plan.Action) string {
	key := ""
	if parent != nil {
		key = parent.Key
	}
	return fmt.Sprintf("bucket-backup-schedule %s  %s", key, BucketSummary(o.Body))
}

func (r *bucketBackupRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	if parent != nil && parent.Parent != nil && truthy(parent.Parent.Data["free"]) {
		return nil, nil
	}
	pid, cid, bid := bucketIDs(parent)
	if err := needIDs("bucket-backup-schedule", pid, cid, bid); err != nil {
		return nil, err
	}
	body, err := rc.Client.GetBucketBackupSchedule(ctx, pid, cid, bid)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		if isForbidden(err) {
			rc.Warning(fmt.Sprintf("backup schedules not read: bucket %s (the API key may not read backup schedules)", parent.Key),
				"Use an Organization Owner or Project Owner key to plan backup schedules.")
			return nil, nil
		}
		return nil, err
	}
	if weekly := objOf(body["weeklySchedule"]); weekly != nil {
		return []plan.Observed{{ID: bid, Body: normObj(copyObj(weekly))}}, nil
	}
	return nil, nil
}

func (r *bucketBackupRec) Match(_ any, observed []plan.Observed) ([]plan.Observed, error) {
	if len(observed) > 1 {
		return observed[:1], nil
	}
	return observed, nil
}

func (r *bucketBackupRec) Diff(_ context.Context, _ *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if desired["ensure"] == "absent" {
		if observed == nil {
			a := newAction(r, kindBucketBackupSchedule, key, plan.Noop, desired, nil, parent)
			a.Hidden = true
			return a, nil
		}
		a := newAction(r, kindBucketBackupSchedule, key, plan.Delete, desired, observed, parent)
		a.Summary = BucketSummary(observed.Body)
		return a, nil
	}
	want := BucketSchedule(desired)
	if observed == nil {
		a := newAction(r, kindBucketBackupSchedule, key, plan.Create, desired, nil, parent)
		a.Summary = BucketSummary(want)
		return a, nil
	}
	var changes []plan.Change
	for _, f := range BucketBackupFields {
		if !plan.ValuesEqual(observed.Body[f], want[f]) {
			changes = append(changes, plan.Change{Path: f, Before: observed.Body[f], After: want[f]})
		}
	}
	t := plan.Noop
	if len(changes) > 0 {
		t = plan.Update
	}
	a := newAction(r, kindBucketBackupSchedule, key, t, desired, observed, parent)
	a.Changes = changes
	return a, nil
}

func (r *bucketBackupRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	obs := o
	a := newAction(r, kindBucketBackupSchedule, r.Key(nil, parent), plan.Delete, Obj{"ensure": "absent"}, &obs, parent)
	a.Summary = BucketSummary(o.Body)
	return a, nil
}

func (r *bucketBackupRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid, bid := bucketIDs(a.Parent)
	if err := needIDs("bucket-backup-schedule "+a.Key, pid, cid, bid); err != nil {
		return err
	}
	if a.Type == plan.Delete {
		return ignoreNotFound(rc.Client.DeleteBucketBackupSchedule(ctx, pid, cid, bid))
	}
	body := Obj{"type": "weekly", "weeklySchedule": BucketSchedule(objOf(a.Desired))}
	if a.Type == plan.Create {
		return rc.Client.CreateBucketBackupSchedule(ctx, pid, cid, bid, body)
	}
	return rc.Client.UpdateBucketBackupSchedule(ctx, pid, cid, bid, body)
}
