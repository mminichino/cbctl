//go:build integration

package integration

import "time"

// scheduleDays are the weekdays in Capella's order, Monday first.
var scheduleDays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// QuietDay is the weekday three days from now (UTC). An on/off schedule must turn the cluster off
// some time (Capella answers 500 for a week that is on every day), so the suite's schedules are on
// except on this day, which no run reaches: the suite takes hours, and three UTC days ahead is at
// least two days ahead in any time zone.
func QuietDay() string {
	return QuietDayAt(time.Now())
}

// QuietDayAt is QuietDay for a given instant.
func QuietDayAt(now time.Time) string {
	return scheduleDays[(int(now.UTC().Weekday())+6+3)%7]
}
