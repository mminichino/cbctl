//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/integration"
)

// TestScheduleProbe asks live Capella which on/off schedule bodies it takes, so the end-of-day
// and always-on encodings are chosen from evidence (nightly 44121812933 refused custom 0:00-0:00
// with 422; 44132049306 got 500 for a week on every day). It only reports: each candidate is a
// PROBE line with the status and the body Capella stored. It runs only with
// CBCTL_IT_PROBE=schedule (nightly.yml's probe input) and uses the shared cluster.
func TestScheduleProbe(t *testing.T) {
	if os.Getenv("CBCTL_IT_PROBE") != "schedule" {
		t.Skip("set CBCTL_IT_PROBE=schedule to probe the on/off schedule encodings")
	}
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	shared := integration.SharedProject(t, tg)
	ctx := context.Background()
	c := tg.Client()
	pid, cid := sharedIDs(ctx, t, c, shared)

	hm := func(h, m int) map[string]any { return map[string]any{"hour": h, "minute": m} }
	custom := func(from, to map[string]any) map[string]any {
		d := map[string]any{"state": "custom", "from": from}
		if to != nil {
			d["to"] = to
		}
		return d
	}
	on := map[string]any{"state": "on"}
	off := map[string]any{"state": "off"}
	// The probed day is three days ahead in the schedule's zone and every other day is on, so no
	// candidate can turn the shared cluster off while the probe runs.
	const tz = "US/Pacific"
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("time zone %s: %v", tz, err)
	}
	dayNames := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	probed := dayNames[(int(time.Now().In(loc).Weekday())+6+3)%7]
	t.Logf("PROBE day %s (%s); every other day is on", probed, tz)
	week := func(value map[string]any) capellaapi.Object {
		days := []any{}
		for _, d := range dayNames {
			v := on
			if d == probed {
				v = value
			}
			e := map[string]any{"day": d}
			for k, x := range v {
				e[k] = x
			}
			days = append(days, e)
		}
		return capellaapi.Object{"timezone": tz, "days": days}
	}
	candidates := []struct {
		name string
		body capellaapi.Object
	}{
		{"all on", week(on)},
		{"all on (again: was the 500 transient?)", week(on)},
		{"day off", week(off)},
		{"day 08:00-18:00", week(custom(hm(8, 0), hm(18, 0)))},
		{"day 00:00 to 0:00 (whole day, capellactl form)", week(custom(hm(0, 0), hm(0, 0)))},
		{"day 08:00 to 0:00 (end of day, capellactl form)", week(custom(hm(8, 0), hm(0, 0)))},
		{"day 08:00 to 24:00", week(custom(hm(8, 0), hm(24, 0)))},
		{"day 00:00 to 24:00", week(custom(hm(0, 0), hm(24, 0)))},
		{"day 08:00 to 23:59", week(custom(hm(8, 0), hm(23, 59)))},
		{"day 08:00 to 23:30", week(custom(hm(8, 0), hm(23, 30)))},
		{"day 00:00 to 23:30", week(custom(hm(0, 0), hm(23, 30)))},
		{"day from 08:00, no to", week(custom(hm(8, 0), nil))},
		{"day from 00:00, no to", week(custom(hm(0, 0), nil))},
	}
	// Start clean: the suite's own schedule test may have left nothing, but a probe must not
	// fail on a leftover.
	if _, _, err := c.GetSchedule(ctx, pid, cid); err == nil {
		if err := c.DeleteSchedule(ctx, pid, cid); err != nil {
			t.Fatalf("delete leftover schedule: %v", err)
		}
	}
	for _, cand := range candidates {
		err := c.CreateSchedule(ctx, pid, cid, cand.body)
		if err != nil {
			t.Logf("PROBE %-48s REFUSED %v", cand.name, err)
			continue
		}
		stored, _, gerr := c.GetSchedule(ctx, pid, cid)
		text, _ := json.Marshal(stored["days"])
		if gerr != nil {
			text = []byte("GET failed: " + gerr.Error())
		}
		t.Logf("PROBE %-48s ACCEPTED stored=%s", cand.name, text)
		if err := c.DeleteSchedule(ctx, pid, cid); err != nil {
			t.Fatalf("delete after %q: %v", cand.name, err)
		}
	}
}

// sharedIDs are the IDs of the shared project and cluster.
func sharedIDs(ctx context.Context, t *testing.T, c *capellaapi.Client, s *integration.Shared) (string, string) {
	t.Helper()
	projects, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	for _, p := range projects {
		if p.Name != s.Project {
			continue
		}
		clusters, err := c.ListClusters(ctx, p.ID)
		if err != nil {
			t.Fatalf("list clusters: %v", err)
		}
		for _, cl := range clusters {
			if cl.Name == s.Cluster {
				return p.ID, cl.ID
			}
		}
	}
	t.Fatalf("shared cluster %s/%s not found", s.Project, s.Cluster)
	return "", ""
}
