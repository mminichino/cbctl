//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/ci/janitor"
)

// ReportEntry records one project's test outcome and teardown result (CAP-032 AC3).
type ReportEntry struct {
	Project  string `json:"project"`
	Failure  string `json:"failure"`
	Teardown string `json:"teardown"`
}

// Cleanup registers a teardown that deletes every project named project, whatever
// happened (pass/fail/panic). A failed teardown fails a test that had passed; after
// a failure the original error stays the reported failure and teardown is still recorded.
func (tg *Target) Cleanup(tb testing.TB, project string) *ReportEntry {
	tb.Helper()
	entry := &ReportEntry{Project: project}
	tg.appendReport(*entry)

	tb.Cleanup(func() {
		failedBefore := tb.Failed()
		if failedBefore {
			entry.Failure = "test failed"
		}
		steps, err := janitor.DeleteProjectsNamed(context.Background(), tg.Client(), project, tg.Timeout, tg.Clock)
		if err != nil {
			msg := fmt.Sprintf("FAILED: %v", err)
			if len(msg) > 300 {
				msg = msg[:300]
			}
			entry.Teardown = msg
			tg.replaceReport(*entry)
			if !failedBefore {
				tb.Errorf("teardown of %s failed: %v", project, err)
				markTeardownFailed()
			}
			return
		}
		if len(steps) == 0 {
			entry.Teardown = "ok: nothing left to delete"
		} else {
			entry.Teardown = "ok: " + strings.Join(steps, "; ")
		}
		tg.replaceReport(*entry)
	})
	return entry
}

func (tg *Target) replaceReport(entry ReportEntry) {
	tg.mu.Lock()
	for i := len(tg.report) - 1; i >= 0; i-- {
		if tg.report[i].Project == entry.Project {
			tg.report[i] = entry
			break
		}
	}
	tg.mu.Unlock()
	suiteMu.Lock()
	for i := len(suiteReport) - 1; i >= 0; i-- {
		if suiteReport[i].Project == entry.Project {
			suiteReport[i] = entry
			break
		}
	}
	suiteMu.Unlock()
}

// DeleteNamed runs janitor.DeleteProjectsNamed immediately (suite shared teardown).
func (tg *Target) DeleteNamed(project string) ([]string, error) {
	return janitor.DeleteProjectsNamed(context.Background(), tg.Client(), project, tg.Timeout, tg.Clock)
}
