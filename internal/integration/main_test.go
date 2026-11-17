//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/integration"
	"github.com/mminichino/cbctl/internal/settings"
)

// sweepTimeout bounds the after-interruption sweep, so it finishes before the job is killed.
const sweepTimeout = 25 * time.Minute

func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		if _, values, _, err := settings.LoadDotEnv(wd, os.LookupEnv); err != nil {
			fmt.Fprintf(os.Stderr, "ignoring .env: %v\n", err)
		} else if err := settings.ApplyEnv(values); err != nil {
			fmt.Fprintf(os.Stderr, "applying .env: %v\n", err)
		}
	}

	// go test -timeout panics without running Cleanup, and a cancelled job sends SIGTERM/SIGINT.
	// Either way the run's projects would be left behind, so a watchdog shortly before the
	// deadline and a signal handler sweep this run's prefix, write the report and exit 1.
	interrupted := make(chan string, 1)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	go func() {
		if s, ok := <-sigs; ok {
			interrupted <- "signal " + s.String()
		}
	}()
	if d, ok := watchdogDelay(); ok {
		timer := time.AfterFunc(d, func() { interrupted <- "watchdog: the -timeout deadline is near" })
		defer timer.Stop()
	}
	go func() {
		reason := <-interrupted
		fmt.Fprintf(os.Stderr, "integration: %s; sweeping this run's projects\n", reason)
		os.Exit(finish(1, true))
	}()

	os.Exit(finish(m.Run(), false))
}

// finish tears down, writes the report and returns the exit code.
func finish(code int, sweep bool) int {
	integration.FinishShared()
	if sweep {
		ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
		defer cancel()
		if err := integration.SweepRun(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	if path := integration.SuiteReportPath(); path != "" {
		if err := integration.WriteSuiteReport(path); err != nil {
			fmt.Fprintf(os.Stderr, "write report: %v\n", err)
			code = 1
		}
	}
	for _, line := range integration.SuiteSummary() {
		fmt.Fprintln(os.Stderr, line)
	}
	if integration.SuiteTeardownFailed() {
		code = 1
	}
	if integration.SuiteRequiredButUnused() {
		fmt.Fprintln(os.Stderr, "integration: CBCTL_IT_REQUIRE_TARGET=1 but no test ran against a target")
		code = 1
	}
	return code
}

// watchdogDelay is how long until the sweep must start: the -test.timeout deadline less the
// sweep budget (or a fifth of the timeout when that is shorter). No -timeout: no watchdog.
func watchdogDelay() (time.Duration, bool) {
	f := flagLookup("test.timeout")
	if f == "" {
		return 0, false
	}
	total, err := time.ParseDuration(f)
	if err != nil || total <= 0 {
		return 0, false
	}
	margin := min(sweepTimeout+time.Minute, total/5)
	return total - margin, true
}
