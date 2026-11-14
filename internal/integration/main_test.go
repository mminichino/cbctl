//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
	"github.com/mminichino/cbctl/internal/settings"
)

func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		if _, _, err := settings.LoadDotEnv(wd); err != nil {
			fmt.Fprintf(os.Stderr, "ignoring .env: %v\n", err)
		}
	}

	code := m.Run()

	integration.FinishShared()

	if path := os.Getenv("CBCTL_IT_REPORT"); path != "" {
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
	os.Exit(code)
}
