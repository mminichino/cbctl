package resources

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
)

const kindSchedule = "schedule"

// Days are the weekdays in the order Capella lists them.
var Days = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

func shortDay(d string) string { return strings.ToUpper(d[:1]) + d[1:3] }

func hm(text string) Obj {
	h, m, _ := strings.Cut(text, ":")
	hour, _ := strconv.Atoi(h)
	minute, _ := strconv.Atoi(m)
	return Obj{"hour": int64(hour % 24), "minute": int64(minute)} // 24:00 (end of day) is sent as 0:00
}

// DayValue is the friendly value of one day of a hibernation mapping: the per-day key, else
// weekdays or weekend, else "on".
func DayValue(hib Obj, day string) string { return capellafile.DayValue(hib, day) }

// ExpandDay is one day in the API form: on, off or custom with from and to.
func ExpandDay(day, value string) Obj {
	if value == "on" || value == "off" {
		return Obj{"day": day, "state": value}
	}
	start, end, _ := strings.Cut(value, "-")
	return Obj{"day": day, "state": "custom", "from": hm(start), "to": hm(end)}
}

// Expand turns the file's friendly form into the API body: timezone and seven days, Monday to
// Sunday.
func Expand(hib Obj) Obj {
	days := make([]any, 0, len(Days))
	for _, d := range Days {
		days = append(days, ExpandDay(d, DayValue(hib, d)))
	}
	return Obj{"timezone": hib["timezone"], "days": days}
}

func fmtHM(b Obj, end bool) string {
	h, m := intOf(b["hour"]), intOf(b["minute"])
	if end && h == 0 && m == 0 {
		return "24:00"
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

// DayText is one API day as text: on, off or 08:00–18:00.
func DayText(entry Obj) string {
	if s := entry["state"]; s == "on" || s == "off" {
		return fmt.Sprint(s)
	}
	return fmtHM(objOf(entry["from"]), false) + "–" + fmtHM(objOf(entry["to"]), true)
}

// ByDay maps each day of an API body to its text.
func ByDay(body Obj) map[string]string {
	out := map[string]string{}
	for _, e := range objsOf(body["days"]) {
		out[pyPlain(e["day"])] = DayText(e)
	}
	return out
}

// WeekSummary is `US/Pacific · Mon–Thu 08:00–18:00 · Fri 08:00–14:30 · Sat–Sun off`: runs of
// equal days are joined.
func WeekSummary(body Obj) string {
	days := ByDay(body)
	parts := []string{pyStr(body["timezone"])}
	for i := 0; i < len(Days); {
		j := i
		for j+1 < len(Days) && days[Days[j+1]] == days[Days[i]] && has2(days, Days[j+1]) == has2(days, Days[i]) {
			j++
		}
		span := shortDay(Days[i])
		if i != j {
			span = shortDay(Days[i]) + "–" + shortDay(Days[j])
		}
		text, ok := days[Days[i]]
		if !ok {
			text = "?"
		}
		parts = append(parts, span+" "+text)
		i = j + 1
	}
	return strings.Join(parts, " · ")
}

func has2(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func zone(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil // no tz database on this machine: no next-transition line
	}
	return loc
}

// weekdayIndex is Monday = 0.
func weekdayIndex(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

// StateAt is "on" or "off" at a local time.
func StateAt(body Obj, local time.Time) string {
	var entry Obj
	for _, e := range objsOf(body["days"]) {
		if e["day"] == Days[weekdayIndex(local)] {
			entry = e
			break
		}
	}
	if entry == nil || entry["state"] == "on" {
		return "on"
	}
	if entry["state"] == "off" {
		return "off"
	}
	from := objOf(entry["from"])
	start := int(intOf(from["hour"])*60 + intOf(from["minute"]))
	to := objOf(entry["to"])
	end := int(intOf(to["hour"])*60 + intOf(to["minute"]))
	if end == 0 {
		end = 24 * 60
	}
	minute := local.Hour()*60 + local.Minute()
	if start <= minute && minute < end {
		return "on"
	}
	return "off"
}

// addMinutes moves a wall-clock time forward, ignoring daylight-saving jumps like a naive datetime.
func addMinutes(t time.Time, n int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()+n, 0, 0, t.Location())
}

// NextTransition is the next time the schedule turns the cluster on or off, as (state, local
// time); ok is false when the zone is unknown or nothing changes within 8 days. Boundaries are on
// the hour or half hour, so 30-minute steps find it.
func NextTransition(body Obj, now time.Time) (state string, at time.Time, ok bool) {
	loc := zone(pyPlain(body["timezone"]))
	if loc == nil {
		return "", time.Time{}, false
	}
	local := now.In(loc)
	current := StateAt(body, local)
	t := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, loc)
	t = addMinutes(t, 30-t.Minute()%30)
	for range 8 * 48 {
		if s := StateAt(body, t); s != current {
			return s, t, true
		}
		t = addMinutes(t, 30)
	}
	return "", time.Time{}, false
}

// NextText is "on at Mon 08:00 US/Pacific": the next transition, or "".
func NextText(body Obj, now time.Time) string {
	s, at, ok := NextTransition(body, now)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s at %s %s", s, at.Format("Mon 15:04"), pyStr(body["timezone"]))
}

// NextOnText is "Mon 08:00 US/Pacific": when the schedule next turns the cluster on, or "".
func NextOnText(body Obj, now time.Time) string {
	loc := zone(pyPlain(body["timezone"]))
	if loc == nil {
		return ""
	}
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, loc)
	t = addMinutes(t, 30-t.Minute()%30)
	was := StateAt(body, local)
	for range 8 * 48 {
		s := StateAt(body, t)
		if s == "on" && was == "off" {
			return fmt.Sprintf("%s %s", t.Format("Mon 15:04"), pyStr(body["timezone"]))
		}
		was = s
		t = addMinutes(t, 30)
	}
	return ""
}

// scheduleRec reconciles a cluster's on/off (hibernation) schedule, with the declarative paused
// flag. The file's friendly form and Capella's seven-day form are compared in the expanded form.
type scheduleRec struct{ plan.Base }

func newSchedule() *scheduleRec {
	// An existing schedule with no hibernation key is left alone and listed as unmanaged;
	// apply --prune deletes it.
	r := &scheduleRec{Base: plan.NewBase(kindSchedule, plan.Flags{Prunable: true, ReportsUnmanaged: true})}
	r.Bind(r)
	return r
}

func (r *scheduleRec) DesiredItems(parent any) []any {
	if h := objOf(objOf(parent)["hibernation"]); h != nil {
		return []any{h}
	}
	return nil
}

func (r *scheduleRec) Key(_ any, parent *plan.Action) string {
	if parent == nil {
		return ""
	}
	return parent.Key
}

func (r *scheduleRec) UnmanagedLabel(o plan.Observed, parent *plan.Action) string {
	key := ""
	if parent != nil {
		key = parent.Key
	}
	return fmt.Sprintf("schedule %s  %s", key, WeekSummary(o.Body))
}

func (r *scheduleRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	pid, cid := projectID(parent), ""
	if parent != nil {
		cid = parent.ID()
	}
	if err := needIDs("schedule", pid, cid); err != nil {
		return nil, err
	}
	body, etag, err := rc.Client.GetSchedule(ctx, pid, cid)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return []plan.Observed{{ID: cid, Body: normObj(body), ETag: etag}}, nil
}

func (r *scheduleRec) Match(_ any, observed []plan.Observed) ([]plan.Observed, error) {
	if len(observed) > 1 {
		return observed[:1], nil
	}
	return observed, nil
}

func (r *scheduleRec) Diff(_ context.Context, _ *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if desired["ensure"] == "absent" {
		if observed == nil {
			a := newAction(r, kindSchedule, key, plan.Noop, desired, nil, parent)
			a.Hidden = true
			return a, nil
		}
		a := newAction(r, kindSchedule, key, plan.Delete, desired, observed, parent)
		a.Summary = WeekSummary(observed.Body)
		return a, nil
	}
	want := Expand(desired)
	if observed == nil {
		a := newAction(r, kindSchedule, key, plan.Create, desired, nil, parent)
		a.Summary = WeekSummary(want)
		if truthy(desired["paused"]) {
			a.Summary += " · paused"
		}
		a.Data["body"] = want
		return a, nil
	}
	have := observed.Body
	var changes []plan.Change
	if !plan.ValuesEqual(have["timezone"], want["timezone"]) {
		changes = append(changes, plan.Change{Path: "timezone", Before: have["timezone"], After: want["timezone"]})
	}
	old, updated := ByDay(have), ByDay(want)
	for _, d := range Days {
		if old[d] != updated[d] {
			var before any
			if v, ok := old[d]; ok {
				before = v
			}
			changes = append(changes, plan.Change{Path: d, Before: before, After: updated[d]})
		}
	}
	a := newAction(r, kindSchedule, key, plan.Noop, desired, observed, parent)
	a.Data["body"] = want
	a.Data["put"] = len(changes) > 0
	status := "active"
	if s, ok := have["activationStatus"]; ok && s != nil {
		status = fmt.Sprint(s)
	}
	if v, ok := desired["paused"]; ok && (status == "paused") != truthy(v) {
		target := "active"
		if truthy(v) {
			target = "paused"
		}
		changes = append(changes, plan.Change{Path: "activationStatus", Before: status, After: target})
		a.Data["status"] = target
	}
	if len(changes) > 0 {
		a.Type, a.Changes = plan.Update, changes
	}
	return a, nil
}

func (r *scheduleRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	obs := o
	a := newAction(r, kindSchedule, r.Key(nil, parent), plan.Delete, Obj{"ensure": "absent"}, &obs, parent)
	a.Summary = WeekSummary(o.Body)
	return a, nil
}

func (r *scheduleRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid := projectID(a), ""
	if a.Parent != nil {
		cid = a.Parent.ID()
	}
	if err := needIDs("schedule "+a.Key, pid, cid); err != nil {
		return err
	}
	switch a.Type {
	case plan.Delete:
		return ignoreNotFound(rc.Client.DeleteSchedule(ctx, pid, cid))
	case plan.Create:
		body, _ := a.Data["body"].(Obj)
		if err := rc.Client.CreateSchedule(ctx, pid, cid, body); err != nil {
			return err
		}
		if truthy(objOf(a.Desired)["paused"]) { // created active; paused after the POST
			return rc.Client.PauseSchedule(ctx, pid, cid)
		}
		return nil
	}
	if truthy(a.Data["put"]) {
		body, _ := a.Data["body"].(Obj)
		etag := ""
		if a.Observed != nil {
			etag = a.Observed.ETag
		}
		if err := rc.Client.UpdateSchedule(ctx, pid, cid, body, etag); err != nil {
			return err
		}
	}
	switch a.Data["status"] {
	case "paused":
		return rc.Client.PauseSchedule(ctx, pid, cid)
	case "active":
		return rc.Client.ResumeSchedule(ctx, pid, cid)
	}
	return nil
}
