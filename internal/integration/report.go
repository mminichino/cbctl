//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

// FinishShared tears down the sandbox shared project if it was created.
func FinishShared() {
	if sharedSandbox == nil || sharedSandboxTG == nil {
		return
	}
	steps, err := sharedSandboxTG.DeleteNamed(sharedSandbox.Project)
	entry := ReportEntry{Project: sharedSandbox.Project}
	if err != nil {
		entry.Teardown = fmt.Sprintf("FAILED: %v", err)
		if len(entry.Teardown) > 300 {
			entry.Teardown = entry.Teardown[:300]
		}
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
