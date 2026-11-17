package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

const (
	uxPowerPath = "/v4/organizations/*/projects/*/clusters/*/activationState"
	uxFreePower = "/v4/organizations/*/projects/*/clusters/freeTier/*/activationState"
)

// uxPowerCluster seeds project alpha with cluster c1 in state and returns the scenario and the
// cluster id.
func uxPowerCluster(t *testing.T, state string) (*clitest.Scenario, string) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(1)
	pid := s.Fake.SeedProject("alpha")
	id := s.Fake.SeedCluster(pid, "c1", state)
	s.Fake.ResetCalls()
	return s, id
}

// TestPower_CAP019_AC2_OnBodyFlag: --on sends turnOnLinkedAppService false; --with-app-service
// sends true.
func TestPower_CAP019_AC2_OnBodyFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"plain", nil, false},
		{"with app service", []string{"--with-app-service"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := uxPowerCluster(t, "turnedOff")
			mustScenario(t, s.Run(append([]string{"capella", "cluster", "power", "c1", "--on"}, tc.args...)...))
			calls := s.Fake.Calls("POST", uxPowerPath)
			if len(calls) != 1 {
				t.Fatalf("POST activationState sent %d times", len(calls))
			}
			body, _ := calls[0].Body.(map[string]any)
			if body["turnOnLinkedAppService"] != tc.want {
				t.Fatalf("body = %v", calls[0].Body)
			}
		})
	}
}

// TestPower_CAP019_AC4_SameDirectionWaitsOnly: --off on a cluster already turning off waits for
// it and sends no second request.
func TestPower_CAP019_AC4_SameDirectionWaitsOnly(t *testing.T) {
	s, id := uxPowerCluster(t, "healthy")
	s.Fake.SetTransition(id, []string{"turningOff", "turnedOff"}, 0)
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "power", "c1", "--off"))
	wantContains(t, r.Stdout, "cluster c1 is off")
	if n := len(s.Fake.Writes()); n != 0 {
		t.Fatalf("sent %d writes, want 0: %v", n, s.Fake.Writes())
	}
	if st := s.Fake.ClusterState(id); st != "turnedOff" {
		t.Fatalf("state = %s", st)
	}
}

// TestPower_CAP019_AC4_OppositeDirectionWaitsThenActs: --on on a cluster turning off waits until
// it is off, then turns it on with one request.
func TestPower_CAP019_AC4_OppositeDirectionWaitsThenActs(t *testing.T) {
	s, id := uxPowerCluster(t, "healthy")
	s.Fake.SetTransition(id, []string{"turningOff", "turnedOff"}, 0)
	r := mustScenario(t, s.Run("capella", "cluster", "power", "c1", "--on"))
	wantContains(t, r.Stdout, "cluster c1 is on")
	if n := len(s.Fake.Calls("POST", uxPowerPath)); n != 1 {
		t.Fatalf("POST activationState sent %d times, want 1", n)
	}
	if st := s.Fake.ClusterState(id); st != "healthy" {
		t.Fatalf("state = %s", st)
	}
}

// TestPower_CAP019_AC5_409RereadsState: a 409 on the power request re-reads the cluster and
// retries once; a second 409 exits 1 with an action.
func TestPower_CAP019_AC5_409RereadsState(t *testing.T) {
	t.Run("retry succeeds", func(t *testing.T) {
		s, id := uxPowerCluster(t, "turnedOff")
		s.Fake.FailNextOpts("POST", uxPowerPath, capellafake.FaultOptions{Status: 409, Message: "busy"})
		mustScenario(t, s.Run("capella", "cluster", "power", "c1", "--on"))
		if n := len(s.Fake.Calls("POST", uxPowerPath)); n != 2 {
			t.Fatalf("POST activationState sent %d times, want 2", n)
		}
		if st := s.Fake.ClusterState(id); st != "healthy" {
			t.Fatalf("state = %s", st)
		}
	})
	t.Run("second conflict fails", func(t *testing.T) {
		s, _ := uxPowerCluster(t, "turnedOff")
		s.Fake.FailNextOpts("POST", uxPowerPath, capellafake.FaultOptions{Status: 409, Message: "busy", Times: 2})
		r := s.Run("capella", "cluster", "power", "c1", "--on")
		wantExit(t, r, cli.ExitError)
		wantContains(t, r.Stderr, "Capella refused", "someone else is changing the cluster")
	})
}

// TestPower_CAP019_AC5_TurningOnFailed: a cluster that ends in turningOnFailed exits 1 naming
// the state and pointing at the activity log.
func TestPower_CAP019_AC5_TurningOnFailed(t *testing.T) {
	s, id := uxPowerCluster(t, "turnedOff")
	s.Fake.SetTransition(id, []string{"turningOn", "turningOnFailed"}, 0)
	r := s.Run("capella", "cluster", "power", "c1", "--on")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "turningOnFailed", "activity log")
}

// TestPower_CAP019_AC7_ScheduleNextOnNote: --off on a cluster with an active on/off schedule
// notes when the schedule turns it on again.
func TestPower_CAP019_AC7_ScheduleNextOnNote(t *testing.T) {
	s, _ := uxPowerCluster(t, "healthy")
	mustScenario(t, s.Run("capella", "--yes", "cluster", "schedule", "set", "c1", "--direct",
		"--timezone", "US/Pacific", "--weekdays", "08:00-18:00", "--weekend", "off"))
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "power", "c1", "--off"))
	wantContains(t, r.Stdout, "cluster c1 is off (storage, data and configuration retained)",
		"note: the on/off schedule will turn this cluster on at ")
	t.Run("paused schedule has no note", func(t *testing.T) {
		mustScenario(t, s.Run("capella", "cluster", "power", "c1", "--on"))
		mustScenario(t, s.Run("capella", "cluster", "schedule", "pause", "c1"))
		r := mustScenario(t, s.Run("capella", "--yes", "cluster", "power", "c1", "--off"))
		wantNotContains(t, r.Stdout, "note:")
	})
}

// TestPower_CAP019_AC9_FreeTierEndpoints: a free-tier cluster is turned off and on through the
// freeTier activationState endpoints.
func TestPower_CAP019_AC9_FreeTierEndpoints(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(1)
	pid := s.Fake.SeedProject("alpha")
	id := s.Fake.SeedFreeCluster(pid, "f1", "healthy")
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "--yes", "cluster", "power", "f1", "--off"))
	mustScenario(t, s.Run("capella", "cluster", "power", "f1", "--on"))
	if n := len(s.Fake.Calls("DELETE", uxFreePower)); n != 1 {
		t.Errorf("DELETE freeTier activationState sent %d times", n)
	}
	if n := len(s.Fake.Calls("POST", uxFreePower)); n != 1 {
		t.Errorf("POST freeTier activationState sent %d times", n)
	}
	for _, c := range s.Fake.Writes() {
		if !strings.Contains(c.Path, "/freeTier/") {
			t.Errorf("non-free-tier write: %s %s", c.Method, c.Path)
		}
	}
	if st := s.Fake.ClusterState(id); st != "healthy" {
		t.Fatalf("state = %s", st)
	}
}
