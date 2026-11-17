//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/integration"
)

func TestQuietDayIsThreeDaysAhead(t *testing.T) {
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC), "friday"},    // Tuesday
		{time.Date(2026, 11, 20, 23, 59, 0, 0, time.UTC), "monday"},  // Friday
		{time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), "wednesday"}, // Sunday
		// The UTC day counts, not the local one: 20:00 Saturday in US/Pacific is Sunday UTC.
		{time.Date(2026, 11, 21, 20, 0, 0, 0, time.FixedZone("PST", -8*3600)), "wednesday"},
	} {
		if got := integration.QuietDayAt(tc.now); got != tc.want {
			t.Errorf("QuietDayAt(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}
