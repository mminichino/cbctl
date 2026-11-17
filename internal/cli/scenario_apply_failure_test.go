package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// scenarioBucketDoc is scenarioDoc plus a bucket, so a failure can land after the cluster.
const scenarioBucketDoc = scenarioDoc + `    buckets:
      - name: orders
        memoryAllocationInMb: 256
`

func wantExit(t *testing.T, r clitest.Result, code cli.ExitCode) {
	t.Helper()
	if r.Code != int(code) {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", r.Code, code, r.Stdout, r.Stderr)
	}
}

// TestApply_CAP006_AC5_FailureSkipsRestAndResumes: a failed write stops the apply, names what
// completed, exits 1, and a second apply finishes without repeating what was done.
func TestApply_CAP006_AC5_FailureSkipsRestAndResumes(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioBucketDoc)
	s.Fake.FailNext("POST", `/buckets$`, 500, 1)

	r := s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "Failed: bucket", "Completed before the failure: project envproj, cluster envclu",
		"completed changes are not repeated")
	if n := s.Fake.Count("POST", `/clusters$`); n != 1 {
		t.Fatalf("cluster POSTs = %d, want 1", n)
	}

	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes (resume)")
	wantContains(t, r.Stderr, "Apply complete")
	clitest.AssertWrites(t, s.Fake, "POST /v4/organizations/*/projects/*/clusters/*/buckets")
}

// TestApply_CAP006_AC8_InterruptExit130: Ctrl-C while waiting for a cluster exits 130 and says
// that Capella carries on; the rerun adopts the cluster instead of creating a second one.
func TestApply_CAP006_AC8_InterruptExit130(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(3)
	writeCapellaYAML(t, s, scenarioDoc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel on the first cluster poll, as a Ctrl-C during the wait would.
	s.Fake.OnRequest("GET", "/v4/organizations/*/projects/*/clusters/*", func(int) { cancel() })

	r := s.RunContext(ctx, "", "capella", "apply", "--yes")
	wantExit(t, r, cli.ExitInterrupted)
	wantContains(t, r.Stderr, "stopped waiting; the operation continues in Capella", "Run the command again to continue")

	s.Fake.SettlePolls(0)
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes (after interrupt)")
	if n := s.Fake.Count("POST", `/clusters$`); n != 0 {
		t.Fatalf("rerun created %d more cluster(s)", n)
	}
}

// TestWaiter_CAP007_AC4_TimeoutExit5: a cluster that never settles exits 5 at --timeout.
func TestWaiter_CAP007_AC4_TimeoutExit5(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(100000)
	writeCapellaYAML(t, s, scenarioDoc)

	r := s.Run("capella", "--timeout", "5m", "apply", "--yes")
	wantExit(t, r, cli.ExitTimeout)
	wantContains(t, r.Stderr, "Failed: cluster envclu", "Completed before the failure: project envproj")
}

// TestDestroy_CAP030_FailureInterruptAndTimeout covers destroy's failure exits.
func TestDestroy_CAP030_FailureInterruptAndTimeout(t *testing.T) {
	setup := func(t *testing.T) *clitest.Scenario {
		s := newScenario(t)
		s.Fake.SettlePolls(0)
		writeCapellaYAML(t, s, scenarioDoc)
		mustScenario(t, s.Run("capella", "apply", "--yes"), "apply")
		return s
	}
	t.Run("failure", func(t *testing.T) {
		s := setup(t)
		s.Fake.FailNext("DELETE", `/clusters/`, 500, 100)
		r := s.Run("capella", "--yes", "destroy")
		wantExit(t, r, cli.ExitError)
		wantContains(t, r.Stderr, "Failed: cluster envclu", "run 'cbctl capella destroy' again")
	})
	t.Run("timeout", func(t *testing.T) {
		s := setup(t)
		s.Fake.SettlePolls(100000)
		r := s.Run("capella", "--yes", "--timeout", "5m", "destroy")
		wantExit(t, r, cli.ExitTimeout)
	})
	t.Run("interrupt", func(t *testing.T) {
		s := setup(t)
		s.Fake.SettlePolls(3)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s.Fake.OnRequest("DELETE", "/v4/organizations/*/projects/*/clusters/*", func(int) { cancel() })
		r := s.RunContext(ctx, "", "capella", "--yes", "destroy")
		wantExit(t, r, cli.ExitInterrupted)
		wantContains(t, r.Stderr, "cluster envclu: stopped waiting; the operation continues in Capella", "destroy' again")
	})
}

// TestApply_FailureJSON: -o json still writes the result document on failure.
func TestApply_FailureJSON(t *testing.T) {
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	writeCapellaYAML(t, s, scenarioBucketDoc)
	s.Fake.FailNext("POST", `/buckets$`, 500, 1)
	r := s.Run("capella", "-o", "json", "apply", "--yes")
	wantExit(t, r, cli.ExitError)
	if !strings.Contains(r.Stdout, `"failed"`) {
		t.Errorf("JSON result lacks the failed action:\n%s", r.Stdout)
	}
}
