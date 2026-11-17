//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cleanupTB records what Target.Cleanup does with a test: the teardown it registers, whether the
// test is failed, and the errors teardown reports. The embedded TB serves Helper and TempDir.
type cleanupTB struct {
	testing.TB
	failed   bool
	cleanups []func()
	errs     []string
}

func (c *cleanupTB) Cleanup(f func()) { c.cleanups = append(c.cleanups, f) }
func (c *cleanupTB) Failed() bool     { return c.failed }
func (c *cleanupTB) Errorf(format string, args ...any) {
	c.errs = append(c.errs, fmt.Sprintf(format, args...))
}

// runCleanups runs the registered teardown like the testing package does (last first).
func (c *cleanupTB) runCleanups() {
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}
}

// teardownTarget is a fake target whose suite-wide report and teardown flag are restored when
// the test ends, so the injected failures here do not fail the suite.
func teardownTarget(t *testing.T) *Target {
	t.Helper()
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CBCTL_IT_TARGET")), "fake") {
		t.Skip("the teardown tests inject failures into the fake target; run with CBCTL_IT_TARGET=fake")
	}
	suiteMu.Lock()
	reportLen, bad := len(suiteReport), teardownBad
	kind, run, ref := suiteKind, suiteRun, suiteTargetRef
	suiteMu.Unlock()
	t.Cleanup(func() {
		suiteMu.Lock()
		suiteReport, teardownBad = suiteReport[:reportLen], bad
		if kind != "" {
			suiteKind, suiteRun, suiteTargetRef = kind, run, ref
		}
		suiteMu.Unlock()
	})
	tg := TargetFromEnv(t)
	tg.Timeout = 5 * time.Minute
	return tg
}

// deployProtected applies a single-node project whose cluster has deletion protection on.
func deployProtected(t *testing.T, tg *Target, project string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "capella.yaml")
	body := SingleNodeConfig(project, "ci-01", "10.40.0.0/24", "    deletionProtection: true\n")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tg.MustCLI(t, []CLIOption{WithConfig(cfg)}, "apply")
}

// failProjectDeletes makes every project delete answer 409 for longer than the teardown retries.
func failProjectDeletes(tg *Target) {
	tg.Fake.FailNext("DELETE", `^/v4/organizations/[^/]+/projects/[^/]+$`, 409, 100)
}

// TestCleanup_CAP032_AC3_TeardownOnFailure: a failed test still has its project torn down (with
// deletion protection turned off first); the report keeps the failure and the teardown steps.
func TestCleanup_CAP032_AC3_TeardownOnFailure(t *testing.T) {
	tg := teardownTarget(t)
	project := tg.ProjectName("t")
	tb := &cleanupTB{TB: t}
	entry := tg.Cleanup(tb, project)
	deployProtected(t, tg, project)
	tb.failed = true // the test body failed after deploying
	tb.runCleanups()

	if len(tb.errs) != 0 {
		t.Fatalf("teardown reported errors: %v", tb.errs)
	}
	if entry.Failure != "test failed" {
		t.Errorf("failure = %q, want the test's failure recorded", entry.Failure)
	}
	if !strings.HasPrefix(entry.Teardown, "ok: deletion protection off for ci-01; deleted cluster ci-01") {
		t.Errorf("teardown = %q", entry.Teardown)
	}
	projects, err := tg.Client().ListProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.Name == project {
			t.Fatalf("project %s left after teardown", project)
		}
	}
	found := false
	for _, line := range SuiteSummary() {
		if strings.Contains(line, project+": FAILED: test failed; teardown ok:") {
			found = true
		}
	}
	if !found {
		t.Errorf("suite summary does not report the failure and the teardown: %v", SuiteSummary())
	}
}

// TestCleanup_CAP032_AC3_FailedTeardownFailsAPassingTest: a passing test whose teardown fails is
// failed, and the suite is marked so TestMain exits 1.
func TestCleanup_CAP032_AC3_FailedTeardownFailsAPassingTest(t *testing.T) {
	tg := teardownTarget(t)
	project := tg.ProjectName("t")
	tb := &cleanupTB{TB: t}
	entry := tg.Cleanup(tb, project)
	deployProtected(t, tg, project)
	failProjectDeletes(tg)
	tb.runCleanups()

	if entry.Failure != "" {
		t.Errorf("failure = %q, want none (the test passed)", entry.Failure)
	}
	if !strings.HasPrefix(entry.Teardown, "FAILED:") {
		t.Errorf("teardown = %q, want FAILED: …", entry.Teardown)
	}
	if len(tb.errs) != 1 || !strings.Contains(tb.errs[0], "teardown of "+project+" failed") {
		t.Errorf("the passing test was not failed by its teardown: %v", tb.errs)
	}
	if !SuiteTeardownFailed() {
		t.Error("the suite is not marked as having a failed teardown")
	}
}

// TestCleanup_CAP032_AC3_BothFailuresReported: when the test and its teardown both fail, the
// test's own failure stays the reported one and the teardown failure is recorded beside it.
func TestCleanup_CAP032_AC3_BothFailuresReported(t *testing.T) {
	tg := teardownTarget(t)
	project := tg.ProjectName("t")
	tb := &cleanupTB{TB: t}
	entry := tg.Cleanup(tb, project)
	deployProtected(t, tg, project)
	failProjectDeletes(tg)
	tb.failed = true
	tb.runCleanups()

	if entry.Failure != "test failed" || !strings.HasPrefix(entry.Teardown, "FAILED:") {
		t.Errorf("entry = %+v, want the test failure and a FAILED teardown", *entry)
	}
	if len(tb.errs) != 0 {
		t.Errorf("an already failed test got a second error from teardown: %v", tb.errs)
	}
}
