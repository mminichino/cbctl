package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// hib is a weekday 08:00-18:00 schedule with a shorter Friday, off at the weekend.
const hib = "    hibernation:\n      timezone: US/Pacific\n      weekdays: \"08:00-18:00\"\n      weekend: \"off\"\n      friday: \"08:00-14:30\"\n"

const uxSchedulePath = "/v4/organizations/*/projects/*/clusters/*/onOffSchedule"

var uxScheduleSet = []string{"capella", "--yes", "cluster", "schedule", "set", "envclu",
	"--timezone", "US/Pacific", "--weekdays", "08:00-18:00", "--weekend", "off"}

// uxDays returns the day entries of a schedule request body.
func uxDays(t *testing.T, c any) []map[string]any {
	t.Helper()
	body, _ := c.(map[string]any)
	raw, _ := body["days"].([]any)
	var out []map[string]any
	for _, d := range raw {
		out = append(out, d.(map[string]any))
	}
	return out
}

// wantFullWeek checks that a schedule body names all seven days in order and Friday ends 14:30.
func wantFullWeek(t *testing.T, body any) {
	t.Helper()
	days := uxDays(t, body)
	order := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	if len(days) != len(order) {
		t.Fatalf("body has %d days, want 7: %v", len(days), body)
	}
	for i, d := range days {
		if d["day"] != order[i] {
			t.Errorf("day %d = %v, want %s", i, d["day"], order[i])
		}
	}
	to, _ := days[4]["to"].(map[string]any)
	if days[4]["state"] != "custom" || to["hour"] != float64(14) || to["minute"] != float64(30) {
		t.Errorf("friday = %v", days[4])
	}
	if days[5]["state"] != "off" || days[6]["state"] != "off" {
		t.Errorf("weekend = %v %v", days[5], days[6])
	}
}

// TestSchedule_CAP020_AC3_CreateWhenMissing: a declared schedule missing in Capella plans as +
// with its summary and is created with one POST carrying all seven days.
func TestSchedule_CAP020_AC3_CreateWhenMissing(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+hib)
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout, "  + schedule envclu  US/Pacific - Mon–Thu 08:00–18:00 - Fri 08:00–14:30 - Sat–Sun off\n")
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	writes := s.Fake.Writes()
	if len(writes) != 1 || writes[0].Method != "POST" || !strings.HasSuffix(writes[0].Path, "/onOffSchedule") {
		t.Fatalf("writes = %v", writes)
	}
	wantFullWeek(t, writes[0].Body)
	if b, _ := writes[0].Body.(map[string]any); b["timezone"] != "US/Pacific" {
		t.Errorf("timezone = %v", b["timezone"])
	}
}

// TestSchedule_CAP020_AC4_UpdateFullBodyMinimalDiff: changing only Friday plans a one-line diff
// but PUTs the full seven-day body with If-Match.
func TestSchedule_CAP020_AC4_UpdateFullBodyMinimalDiff(t *testing.T) {
	s, base := uxApplied(t, "")
	mustScenario(t, s.Run(append(uxScheduleSet, "--direct")...))
	s.Fake.ResetCalls()
	writeCapellaYAML(t, s, base+hib)
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout, "  ~ schedule envclu\n      friday:  \"08:00–18:00\" -> \"08:00–14:30\"\n\nPlan: 0 to add, 1 to change")
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	writes := s.Fake.Writes()
	if len(writes) != 1 || writes[0].Method != "PUT" {
		t.Fatalf("writes = %v", writes)
	}
	if writes[0].Header.Get("If-Match") == "" {
		t.Error("PUT onOffSchedule sent without If-Match")
	}
	wantFullWeek(t, writes[0].Body)
}

// TestSchedule_CAP020_AC6_EnsureAbsentWhenNoneNoop: ensure: absent with no schedule in Capella
// is not a change and sends nothing.
func TestSchedule_CAP020_AC6_EnsureAbsentWhenNoneNoop(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+hib+"      ensure: absent\n")
	r := s.Run("capella", "plan", "--detailed-exitcode")
	wantExit(t, r, cli.ExitOK)
	wantNotContains(t, r.Stdout, "- schedule", "+ schedule", "~ schedule")
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	clitest.AssertNoWrites(t, s.Fake)
}

// TestSchedule_CAP020_AC6_OmittedUntouched: a schedule set in Capella but not declared in the
// file is left alone by apply.
func TestSchedule_CAP020_AC6_OmittedUntouched(t *testing.T) {
	s, base := uxApplied(t, "")
	mustScenario(t, s.Run(append(uxScheduleSet, "--direct")...))
	s.Fake.ResetCalls()
	writeCapellaYAML(t, s, base)
	r := mustScenario(t, s.Run("capella", "apply", "--yes"))
	wantNotContains(t, r.Stdout, "- schedule")
	clitest.AssertNoWrites(t, s.Fake)
	show := mustScenario(t, s.Run("capella", "cluster", "schedule", "show", "envclu"))
	wantContains(t, show.Stdout, "envclu  US/Pacific  active")
}

// TestSchedule_CAP020_AC8_UpdateWhileTurnedOff: the schedule of a turned-off cluster is planned
// and applied normally (not blocked like its buckets).
func TestSchedule_CAP020_AC8_UpdateWhileTurnedOff(t *testing.T) {
	s, base := uxApplied(t, "")
	s.Fake.SetClusterState(uxClusterID(t, s), "turnedOff")
	writeCapellaYAML(t, s, base+hib)
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout, "  + schedule envclu", "0 blocked.")
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	if n := len(s.Fake.Calls("POST", uxSchedulePath)); n != 1 {
		t.Fatalf("POST onOffSchedule sent %d times", n)
	}
}

// uxSynced is a healthy envclu in Capella imported into capella.yaml by sync.
func uxSynced(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	pid := s.Fake.SeedProject("envproj")
	s.Fake.SeedCluster(pid, "envclu", "healthy")
	mustScenario(t, s.Run("capella", "sync"))
	s.Fake.ResetCalls()
	return s
}

// TestSchedule_CAP020_AC3_SetRecordsAndCreates: a managed schedule set records hibernation in
// the file (Friday override included), creates it with one POST, and sync is then in sync.
func TestSchedule_CAP020_AC3_SetRecordsAndCreates(t *testing.T) {
	s := uxSynced(t)
	r := mustScenario(t, s.Run(append(uxScheduleSet, "--friday", "08:00-14:30")...))
	wantContains(t, r.Stdout, "  + schedule envclu  US/Pacific - Mon–Thu 08:00–18:00 - Fri 08:00–14:30 - Sat–Sun off")
	wantContains(t, r.Stderr, "capella.yaml updated")
	wantContains(t, s.ReadFile("capella.yaml"), "        hibernation:\n          timezone: US/Pacific\n          weekdays: 08:00-18:00\n          weekend: \"off\"\n          friday: 08:00-14:30\n")
	if n := len(s.Fake.Writes()); n != 1 {
		t.Fatalf("writes = %v", s.Fake.Writes())
	}
	wantContains(t, mustScenario(t, s.Run("capella", "sync")).Stdout, "capella.yaml is already in sync with Capella")
}

// TestSchedule_CAP020_AC6_DeleteRemovesFromCapellaAndFile: a managed delete removes the Capella
// schedule (marking it ensure: absent until the next sync drops it); a second sync is in sync.
func TestSchedule_CAP020_AC6_DeleteRemovesFromCapellaAndFile(t *testing.T) {
	s := uxSynced(t)
	mustScenario(t, s.Run(uxScheduleSet...))
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "schedule", "delete", "envclu"))
	wantContains(t, r.Stdout, "  - schedule envclu")
	if n := len(s.Fake.Calls("DELETE", uxSchedulePath)); n != 1 {
		t.Fatalf("DELETE onOffSchedule sent %d times", n)
	}
	wantContains(t, s.ReadFile("capella.yaml"), "ensure: absent")
	mustScenario(t, s.Run("capella", "sync"))
	wantNotContains(t, s.ReadFile("capella.yaml"), "hibernation:")
	wantContains(t, mustScenario(t, s.Run("capella", "sync")).Stdout, "capella.yaml is already in sync with Capella")
}

// TestSchedule_CAP020_AC7_ShowGridAndNext: show prints the week grid and the clock-driven next
// transition; -o json carries nextTransition.
func TestSchedule_CAP020_AC7_ShowGridAndNext(t *testing.T) {
	s, _ := uxApplied(t, "")
	mustScenario(t, s.Run(append(uxScheduleSet, "--direct", "--friday", "08:00-14:30")...))
	// The scenario clock starts on Monday 2026-10-05 10:00 US/Pacific.
	if now := s.Clock.Now(); !now.Equal(time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("scenario clock = %v", now)
	}
	r := mustScenario(t, s.Run("capella", "cluster", "schedule", "show", "envclu"))
	wantContains(t, r.Stdout,
		"envclu  US/Pacific  active\n",
		"Mon ░░░░░░░░██████████░░░░░░  08:00–18:00\n",
		"Fri ░░░░░░░░███████░░░░░░░░░  08:00–14:30\n",
		"Sat ░░░░░░░░░░░░░░░░░░░░░░░░  off\n",
		"Next: off at Mon 18:00 US/Pacific")
	doc, _ := mustScenario(t, s.Run("capella", "-o", "json", "cluster", "schedule", "show", "envclu")).JSON(t).(map[string]any)
	next, _ := doc["nextTransition"].(map[string]any)
	if next["state"] != "off" || next["at"] != "2026-10-05T18:00:00-07:00" {
		t.Fatalf("nextTransition = %v", doc["nextTransition"])
	}
}

// TestSchedulePause_CAP021_AC6_PausedAfterCreate: a declared paused: true schedule is created
// and then paused in the same apply.
func TestSchedulePause_CAP021_AC6_PausedAfterCreate(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+hib+"      paused: true\n")
	r := mustScenario(t, s.Run("capella", "plan"))
	wantContains(t, r.Stdout, "Sat–Sun off - paused")
	mustScenario(t, s.Run("capella", "apply", "--yes"))
	var got []string
	for _, c := range s.Fake.Writes() {
		got = append(got, c.Method+" "+c.Path[strings.LastIndex(c.Path, "/")+1:])
	}
	if strings.Join(got, ",") != "POST onOffSchedule,DELETE activationState" {
		t.Fatalf("writes = %v", got)
	}
	if !s.Fake.SchedulePaused(uxClusterID(t, s)) {
		t.Fatal("schedule not paused after apply")
	}
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), cli.ExitOK)
}
