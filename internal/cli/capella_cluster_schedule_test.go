package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// weekdayHours is a UTC schedule: Mon-Fri 08:00-18:00, Sat-Sun off.
func weekdayHours(t *testing.T) capellaapi.Object {
	t.Helper()
	body := capellaapi.Object{"timezone": "UTC"}
	var days []any
	for _, d := range scheduleDays {
		switch d {
		case "saturday", "sunday":
			days = append(days, map[string]any{"day": d, "state": "off"})
		default:
			e, err := parseWindow(d, "08:00-18:00")
			if err != nil {
				t.Fatal(err)
			}
			days = append(days, e)
		}
	}
	body["days"] = days
	return body
}

func TestParseWindow(t *testing.T) {
	tests := []struct {
		in      string
		state   string
		wantErr string
	}{
		{"on", "on", ""},
		{"OFF", "off", ""},
		{"08:00-18:30", "custom", ""},
		{"00:00-24:00", "custom", ""},
		{"8:00-18:00", "custom", ""},
		{"08:15-18:00", "", "minutes must be 00 or 30"},
		{"18:00-08:00", "", "must start before it ends"},
		{"08:00-08:00", "", "must start before it ends"},
		{"24:00-24:30", "", "must start before it ends"},
		{"soon", "", "not on, off or HH:MM-HH:MM"},
		{"", "", "not on, off or HH:MM-HH:MM"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			e, err := parseWindow("monday", tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e["state"] != tc.state || e["day"] != "monday" {
				t.Fatalf("entry = %v", e)
			}
		})
	}
	e, _ := parseWindow("friday", "00:00-24:00")
	if to := e["to"].(map[string]any); to["hour"] != 0 || to["minute"] != 0 {
		t.Fatalf("24:00 should be sent as 00:00, got %v", to)
	}
}

func TestScheduleHelpers(t *testing.T) {
	body := weekdayHours(t)
	if got, want := weekSummary(body), "UTC · Mon–Fri 08:00–18:00 · Sat–Sun off"; got != want {
		t.Fatalf("weekSummary = %q, want %q", got, want)
	}
	friday := time.Date(2026, time.November, 13, 20, 0, 0, 0, time.UTC)
	if got := nextOnText(body, friday); got != "Mon 08:00 UTC" {
		t.Fatalf("nextOnText = %q", got)
	}
	if got := nextText(body, time.Date(2026, time.November, 16, 9, 0, 0, 0, time.UTC)); got != "off at Mon 18:00 UTC" {
		t.Fatalf("nextText = %q", got)
	}
	if s := stateAt(body, time.Date(2026, time.November, 16, 9, 0, 0, 0, time.UTC)); s != "on" {
		t.Fatalf("stateAt Monday 09:00 = %s", s)
	}
	if s := stateAt(body, time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)); s != "off" {
		t.Fatalf("stateAt Saturday = %s", s)
	}
	if got := nextOnText(capellaapi.Object{"timezone": "Nowhere/Land"}, friday); got != "" {
		t.Fatalf("nextOnText with a bad zone = %q", got)
	}
	grid := scheduleGrid(body)
	if len(grid) != 7 || !strings.HasPrefix(grid[0], "Mon ░░░░░░░░██████████░░░░░░") || strings.Contains(grid[6], "█") {
		t.Fatalf("grid = %q", grid)
	}
}

func TestClusterSchedule(t *testing.T) {
	f := newFake(t)
	_, id := seedCluster(f, "alpha", "c1")

	runCap(t, "cluster", "schedule", "show", "c1").mustFail(t, ExitBlocked, "no on/off schedule")
	runCap(t, "cluster", "schedule", "delete", "c1").mustOK(t).wantStdout(t, "has no on/off schedule")
	runCap(t, "cluster", "schedule", "set", "c1", "--weekdays", "08:00-18:00").mustFail(t, ExitConfigInvalid, "needs a timezone")
	runCap(t, "cluster", "schedule", "set", "c1", "--timezone", "Mars/Base", "--weekdays", "on").mustFail(t, ExitConfigInvalid, "unknown timezone")
	runCap(t, "cluster", "schedule", "set", "c1", "--timezone", "UTC", "--weekdays", "08:10-18:00").mustFail(t, ExitConfigInvalid, "minutes must be 00 or 30")
	runCap(t, "cluster", "schedule", "set", "c1").mustFail(t, ExitConfigInvalid, "add at least one of")

	var r scheduleResult
	runCap(t, "-o", "json", "cluster", "schedule", "set", "c1", "--timezone", "UTC", "--weekdays", "08:00-18:00", "--weekend", "off").mustOK(t).jsonOut(t, &r)
	if r.Status != "created" || r.Summary != "UTC · Mon–Fri 08:00–18:00 · Sat–Sun off" {
		t.Fatalf("result = %+v", r)
	}
	runCap(t, "cluster", "schedule", "set", "c1", "--timezone", "UTC", "--weekdays", "08:00-18:00", "--weekend", "off").mustOK(t).wantStdout(t, "already matches")

	// A day option overrides the weekday range, and an unchanged timezone is kept.
	runCap(t, "-o", "json", "cluster", "schedule", "set", "c1", "--friday", "08:00-14:30").mustOK(t).jsonOut(t, &r)
	if r.Status != "updated" || !strings.Contains(r.Summary, "Mon–Thu 08:00–18:00 · Fri 08:00–14:30 · Sat–Sun off") {
		t.Fatalf("result = %+v", r)
	}

	show := runCap(t, "cluster", "schedule", "show", "c1").mustOK(t)
	show.wantStdout(t, "c1  UTC  active", "Fri", "Next:")
	var doc map[string]any
	runCap(t, "-o", "json", "cluster", "schedule", "show", "c1").mustOK(t).jsonOut(t, &doc)
	if doc["cluster"] != "c1" || doc["timezone"] != "UTC" || doc["days"] == nil {
		t.Fatalf("doc = %v", doc)
	}

	runCap(t, "cluster", "schedule", "pause", "c1").mustOK(t).wantStdout(t, "schedule paused")
	if !f.SchedulePaused(id) {
		t.Fatal("schedule not paused")
	}
	runCap(t, "cluster", "schedule", "pause", "c1").mustOK(t).wantStdout(t, "already paused")
	runCap(t, "cluster", "schedule", "show", "c1").mustOK(t).wantStdout(t, "Paused:")
	runCap(t, "cluster", "schedule", "resume", "c1").mustOK(t).wantStdout(t, "schedule resumed")
	if f.SchedulePaused(id) {
		t.Fatal("schedule still paused")
	}
	runCap(t, "cluster", "schedule", "resume", "c1").mustOK(t).wantStdout(t, "already active")

	runCap(t, "cluster", "schedule", "delete", "c1").mustFail(t, ExitBlocked, "no terminal")
	runCap(t, "--yes", "cluster", "schedule", "delete", "c1").mustOK(t).wantStdout(t, "deleted the on/off schedule of c1")
	runCap(t, "cluster", "schedule", "show", "c1").mustFail(t, ExitBlocked, "no on/off schedule")
}

func TestClusterSchedulePausePowerOn(t *testing.T) {
	f := newFake(t)
	_, id := seedCluster(f, "alpha", "c1")
	runCap(t, "cluster", "schedule", "set", "c1", "--timezone", "UTC", "--weekdays", "on", "--weekend", "on").mustOK(t)
	f.SetClusterState(id, "turnedOff")
	runCap(t, "cluster", "schedule", "pause", "c1", "--power-on").mustOK(t)
	if s := f.ClusterState(id); s != "healthy" {
		t.Fatalf("state = %s, want the cluster turned on", s)
	}
}

func TestClusterScheduleFreeTier(t *testing.T) {
	f := newFake(t)
	seedFree(t, f, "freebie")
	runCap(t, "cluster", "schedule", "show", "freebie").mustFail(t, ExitBlocked, "free-tier clusters have no on/off schedule")
	runCap(t, "cluster", "schedule", "set", "freebie", "--timezone", "UTC", "--weekdays", "on").mustFail(t, ExitBlocked, "free-tier clusters have no on/off schedule")
}
