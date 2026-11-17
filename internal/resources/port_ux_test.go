package resources

import (
	"reflect"
	"testing"
	"time"
)

// TestScheduleExpand_CAP020_AC1_OverrideWins: a per-day value overrides weekdays/weekend, both
// an always-on day and a weekend-day window; untouched days keep the group value.
func TestScheduleExpand_CAP020_AC1_OverrideWins(t *testing.T) {
	body := Expand(Obj{"timezone": "US/Pacific", "weekdays": "08:00-18:00", "weekend": "off",
		"friday": "08:00-14:30", "wednesday": "on", "sunday": "10:00-12:00"})
	days := map[string]Obj{}
	for _, d := range objsOf(body["days"]) {
		days[d["day"].(string)] = d
	}
	want := map[string]Obj{
		"wednesday": {"day": "wednesday", "state": "on"},
		"sunday":    ExpandDay("sunday", "10:00-12:00"),
		"saturday":  {"day": "saturday", "state": "off"},
		"friday":    ExpandDay("friday", "08:00-14:30"),
		"monday":    ExpandDay("monday", "08:00-18:00"),
	}
	for day, w := range want {
		if !reflect.DeepEqual(days[day], w) {
			t.Errorf("%s = %v, want %v", day, days[day], w)
		}
	}
	if got := DayText(days["sunday"]); got != "10:00–12:00" {
		t.Errorf("sunday window = %q", got)
	}
}

// TestScheduleNext_CAP020_AC7_AcrossWeekBoundary: from Sunday the next change is Monday's on, and
// before Friday's early end it is that end.
func TestScheduleNext_CAP020_AC7_AcrossWeekBoundary(t *testing.T) {
	body := Expand(Obj{"timezone": "US/Pacific", "weekdays": "08:00-18:00", "weekend": "off", "friday": "08:00-14:30"})
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"sunday noon", time.Date(2026, 10, 11, 19, 0, 0, 0, time.UTC), "on at Mon 08:00 US/Pacific"},
		{"friday 1pm", time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC), "off at Fri 14:30 US/Pacific"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextText(body, tc.now); got != tc.want {
				t.Errorf("NextText = %q, want %q", got, tc.want)
			}
		})
	}
	if got := NextOnText(body, time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)); got != "Mon 08:00 US/Pacific" {
		t.Errorf("NextOnText from Friday evening = %q", got)
	}
}
