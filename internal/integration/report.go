//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mminichino/cbctl/internal/ci/janitor"
)

// WriteReport writes {"target","run","projects":[...]} to path.
func (tg *Target) WriteReport(path string) error {
	tg.mu.Lock()
	projects := append([]ReportEntry(nil), tg.report...)
	tg.mu.Unlock()
	return writeReportFile(path, tg.Kind, tg.Run, projects)
}

// Summary returns human-readable lines for the teardown section.
func (tg *Target) Summary() []string {
	tg.mu.Lock()
	projects := append([]ReportEntry(nil), tg.report...)
	tg.mu.Unlock()
	return summaryLines(tg.Kind, tg.Run, projects)
}

// WriteSuiteReport writes the aggregated suite report (every Cleanup entry).
func WriteSuiteReport(path string) error {
	kind, run := SuiteKindRun()
	return writeReportFile(path, kind, run, SuiteReportEntries())
}

// SuiteSummary returns aggregated teardown lines for TestMain.
func SuiteSummary() []string {
	kind, run := SuiteKindRun()
	return summaryLines(kind, run, SuiteReportEntries())
}

func writeReportFile(path, kind, run string, projects []ReportEntry) error {
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(struct {
		Target   string        `json:"target"`
		Run      string        `json:"run"`
		Projects []ReportEntry `json:"projects"`
	}{Target: kind, Run: run, Projects: projects}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o600)
}

func summaryLines(kind, run string, projects []ReportEntry) []string {
	if kind == "" {
		return nil
	}
	lines := []string{fmt.Sprintf("integration target: %s, run %s", kind, run)}
	for _, e := range projects {
		status := "passed"
		if e.Failure != "" {
			status = "FAILED: " + e.Failure
		}
		teardown := e.Teardown
		if teardown == "" {
			teardown = "pending"
		}
		lines = append(lines, fmt.Sprintf("  %s: %s; teardown %s", e.Project, status, teardown))
	}
	return lines
}

// FinishShared tears down the sandbox shared project. It runs when the apply that creates it
// failed too: a half-applied project still has to go.
func FinishShared() {
	suiteMu.Lock()
	name, tg := sharedProjectName, sharedSandboxTG
	suiteMu.Unlock()
	if name == "" || tg == nil {
		return
	}
	steps, err := tg.DeleteNamed(name)
	entry := ReportEntry{Project: name}
	if sharedSandboxErr != "" {
		entry.Failure = truncate(sharedSandboxErr, 300)
	}
	if err != nil {
		entry.Teardown = truncate(fmt.Sprintf("FAILED: %v", err), 300)
		markTeardownFailed()
	} else if len(steps) == 0 {
		entry.Teardown = "ok: nothing left to delete"
	} else {
		entry.Teardown = "ok: " + strings.Join(steps, "; ")
	}
	suiteMu.Lock()
	suiteReport = append(suiteReport, entry)
	suiteMu.Unlock()
}

// SweepRun deletes every sandbox project this run created (by its run prefix), whatever its
// state. TestMain calls it after a watchdog timeout or a signal, when per-test cleanups may
// not have run. It does nothing on the fake or when no sandbox target was used.
func SweepRun(ctx context.Context) error {
	suiteMu.Lock()
	tg := suiteTargetRef
	suiteMu.Unlock()
	if tg == nil || tg.Kind != "sandbox" {
		return nil
	}
	sum, err := janitor.DeleteRun(ctx, tg.Client(), tg.RunPrefix(), tg.Timeout, tg.Clock)
	for _, name := range sum.Deleted {
		suiteMu.Lock()
		suiteReport = append(suiteReport, ReportEntry{Project: name, Teardown: "ok: swept after interruption"})
		suiteMu.Unlock()
	}
	if err != nil {
		markTeardownFailed()
		return fmt.Errorf("sweeping run %s: %w", tg.RunPrefix(), err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
