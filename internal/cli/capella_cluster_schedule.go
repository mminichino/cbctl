package cli

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // schedule timezones must resolve on machines without a tz database

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/spf13/cobra"
)

var (
	scheduleDays  = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	windowPattern = regexp.MustCompile(`^(\d{1,2}):(\d{2})-(\d{1,2}):(\d{2})$`)
)

func shortDay(d string) string { return strings.ToUpper(d[:1]) + d[1:3] }

// ---- schedule document helpers (port of capellactl resources/schedule.py) ----------------------

func numberOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64: // bodies built by resources.ExpandDay
		return int(n)
	}
	return 0
}

func clockParts(v any) (hour, minute int) {
	m, _ := v.(map[string]any)
	return numberOf(m["hour"]), numberOf(m["minute"])
}

// scheduleDayEntries returns the schedule's days keyed by lower-case day name.
func scheduleDayEntries(body capellaapi.Object) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := body["days"].([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			if d, _ := m["day"].(string); d != "" {
				out[strings.ToLower(d)] = m
			}
		}
	}
	return out
}

// dayText renders one API day as "on", "off" or "08:00–18:00".
func dayText(e map[string]any) string { return resources.DayText(e) }

// dayValues maps each day to its friendly text; days the body omits count as "on".
func dayValues(body capellaapi.Object) map[string]string {
	out := map[string]string{}
	for d, e := range scheduleDayEntries(body) {
		out[d] = dayText(e)
	}
	return out
}

// weekSummary is "US/Pacific · Mon–Thu 08:00–18:00 · Fri 08:00–14:30 · Sat–Sun off".
func weekSummary(body capellaapi.Object) string {
	days := dayValues(body)
	val := func(d string) string {
		if v, ok := days[d]; ok {
			return v
		}
		return "?"
	}
	tz, _ := body["timezone"].(string)
	parts := []string{tz}
	for i := 0; i < len(scheduleDays); {
		j := i
		for j+1 < len(scheduleDays) && val(scheduleDays[j+1]) == val(scheduleDays[i]) {
			j++
		}
		span := shortDay(scheduleDays[i])
		if i != j {
			span += "–" + shortDay(scheduleDays[j])
		}
		parts = append(parts, span+" "+val(scheduleDays[i]))
		i = j + 1
	}
	return strings.Join(parts, " · ")
}

// weekdayIndex maps time.Weekday (Sunday=0) to the Monday=0 index of scheduleDays.
func weekdayIndex(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

// stateAt is "on" or "off" at a local time.
func stateAt(body capellaapi.Object, local time.Time) string {
	e, ok := scheduleDayEntries(body)[scheduleDays[weekdayIndex(local)]]
	if !ok {
		return "on"
	}
	switch e["state"] {
	case "on":
		return "on"
	case "off":
		return "off"
	}
	fh, fm := clockParts(e["from"])
	th, tm := clockParts(e["to"])
	start, end := fh*60+fm, th*60+tm
	if end == 0 {
		end = 24 * 60
	}
	minute := local.Hour()*60 + local.Minute()
	if start <= minute && minute < end {
		return "on"
	}
	return "off"
}

func scheduleZone(body capellaapi.Object) *time.Location {
	tz, _ := body["timezone"].(string)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil
	}
	return loc
}

// nextTransition finds the next time the schedule changes between on and off. Boundaries fall on
// the hour or half hour, so 30-minute steps over eight days find it.
func nextTransition(body capellaapi.Object, now time.Time) (state string, at time.Time, ok bool) {
	loc := scheduleZone(body)
	if loc == nil {
		return "", time.Time{}, false
	}
	local := now.In(loc)
	current := stateAt(body, local)
	t := local.Truncate(time.Minute)
	t = t.Add(time.Duration(30-t.Minute()%30) * time.Minute)
	for i := 0; i < 8*48; i++ {
		if s := stateAt(body, t); s != current {
			return s, t, true
		}
		t = t.Add(30 * time.Minute)
	}
	return "", time.Time{}, false
}

func nextText(body capellaapi.Object, now time.Time) string {
	s, at, ok := nextTransition(body, now)
	if !ok {
		return ""
	}
	tz, _ := body["timezone"].(string)
	return fmt.Sprintf("%s at %s %s", s, at.Format("Mon 15:04"), tz)
}

// nextOnText is "Mon 08:00 US/Pacific": when the schedule next turns the cluster on.
func nextOnText(body capellaapi.Object, now time.Time) string {
	loc := scheduleZone(body)
	if loc == nil {
		return ""
	}
	tz, _ := body["timezone"].(string)
	local := now.In(loc)
	was := stateAt(body, local)
	t := local.Truncate(time.Minute)
	t = t.Add(time.Duration(30-t.Minute()%30) * time.Minute)
	for i := 0; i < 8*48; i++ {
		s := stateAt(body, t)
		if s == "on" && was == "off" {
			return t.Format("Mon 15:04") + " " + tz
		}
		was = s
		t = t.Add(30 * time.Minute)
	}
	return ""
}

// scheduleGrid is one line per day: 24 hourly cells (█ on, ░ off) and the day's value.
func scheduleGrid(body capellaapi.Object) []string {
	days := dayValues(body)
	base := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC) // a Monday
	lines := make([]string, 0, len(scheduleDays))
	for i, d := range scheduleDays {
		var cells strings.Builder
		for h := 0; h < 24; h++ {
			if stateAt(body, base.AddDate(0, 0, i).Add(time.Duration(h)*time.Hour)) == "on" {
				cells.WriteString("█")
			} else {
				cells.WriteString("░")
			}
		}
		v, ok := days[d]
		if !ok {
			v = "?"
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s", shortDay(d), cells.String(), v))
	}
	return lines
}

// ---- friendly ⇄ API form ----------------------------------------------------------------------

// parseWindow validates "on", "off" or "HH:MM-HH:MM" (minutes 00 or 30, end up to 24:00) and
// returns the API day entry.
func parseWindow(day, text string) (map[string]any, error) {
	v := strings.ToLower(strings.TrimSpace(text))
	if v == "on" || v == "off" {
		return map[string]any{"day": day, "state": v}, nil
	}
	m := windowPattern.FindStringSubmatch(v)
	bad := func(why string) (map[string]any, error) {
		return nil, usageError("use on, off or HH:MM-HH:MM with minutes 00 or 30, for example 08:00-18:00",
			"%s window %q: %s", day, text, why)
	}
	if m == nil {
		return bad("not on, off or HH:MM-HH:MM")
	}
	n := make([]int, 4)
	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1])
	}
	if n[1]%30 != 0 || n[3]%30 != 0 {
		return bad("minutes must be 00 or 30")
	}
	start, end := n[0]*60+n[1], n[2]*60+n[3]
	if n[0] > 23 || end > 24*60 || end <= start {
		return bad("the window must start before it ends, within one day")
	}
	// One encoding for the CLI and apply (end of day = no "to", whole day = on).
	return resources.ExpandDay(day, fmt.Sprintf("%02d:%02d-%02d:%02d", n[0], n[1], n[2], n[3])), nil
}

// ---- commands ---------------------------------------------------------------------------------

func newCapellaClusterScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "On/off schedule: Capella turns the cluster off outside the on-windows",
		Run:   func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newScheduleShowCmd(), newScheduleSetCmd(), newSchedulePauseCmd(), newScheduleResumeCmd(), newScheduleDeleteCmd(), newScheduleApplyCmd())
	return cmd
}

// requireSchedule reads the cluster's schedule; no schedule is exit 4.
func (rt *runtime) requireSchedule(t *clusterTarget) (capellaapi.Object, string, error) {
	if t.freeTier() {
		return nil, "", blockedError("use a provisioned cluster", "free-tier clusters have no on/off schedule")
	}
	body, etag, err := rt.Client.GetSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID)
	if errors.Is(err, capellaapi.ErrNotFound) {
		return nil, "", blockedError(fmt.Sprintf("create one with 'cbctl capella cluster schedule set %s --timezone TZ --weekdays HH:MM-HH:MM'", t.Cluster.Name),
			"cluster %s has no on/off schedule", t.Cluster.Name)
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading the schedule of %s: %w", t.Cluster.Name, err)
	}
	return body, etag, nil
}

func newScheduleShowCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("show CLUSTER", "Show the weekly on/off grid and the next transition", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			body, _, err := rt.requireSchedule(t)
			if err != nil {
				return err
			}
			paused := body["activationStatus"] == "paused"
			var (
				state string
				at    time.Time
				found bool
			)
			if !paused {
				state, at, found = nextTransition(body, rt.Clock.Now())
			}
			if rt.JSON {
				doc := capellaapi.Object{"cluster": t.Cluster.Name}
				for k, v := range body {
					doc[k] = v
				}
				if found {
					doc["nextTransition"] = capellaapi.Object{"state": state, "at": at.Format(time.RFC3339)}
				}
				rt.Out.Object(doc)
				return nil
			}
			status, _ := body["activationStatus"].(string)
			if status == "" {
				status = "active"
			}
			rt.Out.Line("%s  %v  %s", t.Cluster.Name, body["timezone"], status)
			for _, l := range scheduleGrid(body) {
				rt.Out.Line("%s", l)
			}
			switch {
			case found:
				rt.Out.Line("Next: %s", nextText(body, rt.Clock.Now()))
			case paused:
				rt.Out.Line("Paused: the cluster stays in its current state until 'cbctl capella cluster schedule resume %s'", t.Cluster.Name)
			}
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

type scheduleResult struct {
	Cluster string `json:"cluster"`
	Status  string `json:"status"` // created | updated | unchanged | paused | resumed | deleted | absent | cancelled
	Summary string `json:"summary,omitempty"`
}

func newScheduleSetCmd() *cobra.Command {
	var project, tz, weekdays, weekend string
	days := make([]string, len(scheduleDays))
	cmd := capellaLeaf("set CLUSTER", "Create or change the schedule", cobra.ExactArgs(1),
		func([]string) error {
			given := tz != "" || weekdays != "" || weekend != ""
			for _, d := range days {
				given = given || d != ""
			}
			if !given {
				return usageError("add at least one of --timezone, --weekdays, --weekend or a day option (for example --friday 08:00-14:30)",
					"nothing to set: no schedule option was given")
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			if t.freeTier() {
				return blockedError("use a provisioned cluster", "free-tier clusters have no on/off schedule")
			}
			return rt.runScheduleSet(t, tz, weekdays, weekend, days)
		})
	cmd.Long = "Create or change the on/off schedule. Each day is 'on', 'off' or an on-window HH:MM-HH:MM (minutes 00 or 30).\n" +
		"--weekdays and --weekend set Monday-Friday and Saturday-Sunday; a day option overrides them. On an existing\n" +
		"schedule the days you do not mention keep their value.\n\n" +
		"Examples:\n  cbctl capella cluster schedule set dev-01 --timezone US/Pacific --weekdays 08:00-18:00 --weekend off\n" +
		"  cbctl capella cluster schedule set dev-01 --friday 08:00-14:30"
	addProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&tz, "timezone", "", "Schedule timezone, for example US/Pacific, Europe/London (needed for a new schedule)")
	cmd.Flags().StringVar(&weekdays, "weekdays", "", "Monday-Friday: on, off or HH:MM-HH:MM")
	cmd.Flags().StringVar(&weekend, "weekend", "", "Saturday and Sunday: on, off or HH:MM-HH:MM")
	for i, d := range scheduleDays {
		cmd.Flags().StringVar(&days[i], d, "", strings.ToUpper(d[:1])+d[1:]+": on, off or HH:MM-HH:MM (overrides --weekdays/--weekend)")
	}
	return cmd
}

func (rt *runtime) runScheduleSet(t *clusterTarget, tz, weekdays, weekend string, perDay []string) error {
	existing, etag, err := rt.Client.GetSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID)
	isNew := errors.Is(err, capellaapi.ErrNotFound)
	if err != nil && !isNew {
		return fmt.Errorf("reading the schedule of %s: %w", t.Cluster.Name, err)
	}
	if isNew && tz == "" {
		return usageError("add --timezone, for example --timezone US/Pacific", "a new schedule needs a timezone")
	}
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return usageError("use an IANA name such as US/Pacific, Europe/London or Asia/Calcutta", "unknown timezone %q", tz)
		}
	}

	// Start from the current values (new schedules start with every day on), then layer the
	// group options and the per-day options.
	values := map[string]string{}
	for _, d := range scheduleDays {
		values[d] = "on"
	}
	if !isNew {
		for d, v := range dayValues(existing) {
			values[d] = strings.ReplaceAll(v, "–", "-")
		}
	}
	for _, d := range scheduleDays[:5] {
		if weekdays != "" {
			values[d] = weekdays
		}
	}
	for _, d := range scheduleDays[5:] {
		if weekend != "" {
			values[d] = weekend
		}
	}
	for i, d := range scheduleDays {
		if perDay[i] != "" {
			values[d] = perDay[i]
		}
	}
	entries := make([]any, 0, len(scheduleDays))
	for _, d := range scheduleDays {
		e, err := parseWindow(d, values[d])
		if err != nil {
			return err
		}
		entries = append(entries, e)
	}
	zone := tz
	if zone == "" {
		zone, _ = existing["timezone"].(string)
	}
	body := capellaapi.Object{"timezone": zone, "days": entries}
	res := scheduleResult{Cluster: t.Cluster.Name, Summary: weekSummary(body)}
	if !slices.ContainsFunc(entries, func(e any) bool { return e.(map[string]any)["state"] != "on" }) {
		return usageError(fmt.Sprintf("set at least one day to off or a window, or run 'cbctl capella cluster schedule delete %s' to keep it always on", t.Cluster.Name),
			"the schedule never turns %s off; Capella refuses a week that is on every day", t.Cluster.Name)
	}

	if isNew {
		if err := rt.Client.CreateSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, body); err != nil {
			return conflictAsBlocked(err, t.Cluster.Name)
		}
		res.Status = "created"
		rt.report(res, "created the on/off schedule of %s: %s", t.Cluster.Name, res.Summary)
		return nil
	}
	if weekSummary(existing) == res.Summary {
		res.Status = "unchanged"
		rt.report(res, "schedule of %s already matches; nothing to do", t.Cluster.Name)
		return nil
	}
	err = rt.Client.UpdateSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID, body, etag)
	if isPreconditionFailed(err) { // changed meanwhile: the values were merged from a stale read, so ask for a re-run
		return blockedError("run the command again", "the schedule of %s changed while it was being updated", t.Cluster.Name)
	}
	if err != nil {
		return conflictAsBlocked(err, t.Cluster.Name)
	}
	res.Status = "updated"
	rt.report(res, "updated the on/off schedule of %s: %s", t.Cluster.Name, res.Summary)
	return nil
}

func newSchedulePauseCmd() *cobra.Command {
	var project string
	var powerOn bool
	cmd := capellaLeaf("pause CLUSTER", "Pause the schedule: the cluster stays as it is until resumed", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			body, _, err := rt.requireSchedule(t)
			if err != nil {
				return err
			}
			res := scheduleResult{Cluster: t.Cluster.Name, Status: "paused"}
			if body["activationStatus"] == "paused" {
				res.Status = "unchanged"
				rt.report(res, "schedule of %s is already paused; nothing to do", t.Cluster.Name)
			} else {
				if err := rt.Client.PauseSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID); err != nil {
					return conflictAsBlocked(err, t.Cluster.Name)
				}
				now := "on"
				if s := t.Cluster.CurrentState; s == "turnedOff" || s == "turningOff" {
					now = "off"
				}
				rt.report(res, "schedule paused; the cluster stays in its current state (%s) until resumed", now)
			}
			if s := t.Cluster.CurrentState; powerOn && (s == "turnedOff" || s == "turningOff") {
				return rt.runPower(t.Project, t.Cluster, true, false, false)
			}
			return nil
		})
	addProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&powerOn, "power-on", false, "Also turn the cluster on if the schedule has turned it off")
	return cmd
}

func newScheduleResumeCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("resume CLUSTER", "Resume a paused schedule and show its next transition", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			body, _, err := rt.requireSchedule(t)
			if err != nil {
				return err
			}
			res := scheduleResult{Cluster: t.Cluster.Name, Status: "resumed"}
			if body["activationStatus"] != "paused" {
				res.Status = "unchanged"
				rt.report(res, "schedule of %s is already active; nothing to do", t.Cluster.Name)
				return nil
			}
			if err := rt.Client.ResumeSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID); err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			body["activationStatus"] = "active"
			next := nextText(body, rt.Clock.Now())
			res.Summary = next
			if next != "" {
				rt.report(res, "schedule resumed; next: %s", next)
			} else {
				rt.report(res, "schedule resumed")
			}
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

func newScheduleDeleteCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("delete CLUSTER", "Delete the schedule (the cluster stays as it is)", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			if t.freeTier() {
				return blockedError("use a provisioned cluster", "free-tier clusters have no on/off schedule")
			}
			res := scheduleResult{Cluster: t.Cluster.Name}
			body, _, err := rt.Client.GetSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID)
			if errors.Is(err, capellaapi.ErrNotFound) {
				res.Status = "absent"
				rt.report(res, "cluster %s has no on/off schedule; nothing to do", t.Cluster.Name)
				return nil
			}
			if err != nil {
				return fmt.Errorf("reading the schedule of %s: %w", t.Cluster.Name, err)
			}
			res.Summary = weekSummary(body)
			rt.Out.Line("  - schedule %s  %s", t.Cluster.Name, res.Summary)
			ok, err := confirm(rt, fmt.Sprintf("Delete the on/off schedule of %s? The cluster stays in its current state.", t.Cluster.Name))
			if err != nil {
				return err
			}
			if !ok {
				res.Status = "cancelled"
				rt.report(res, "cancelled; the schedule is unchanged")
				return nil
			}
			if err := rt.Client.DeleteSchedule(rt.Ctx, t.Project.ID, t.Cluster.ID); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "deleted"
			rt.report(res, "deleted the on/off schedule of %s", t.Cluster.Name)
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}
