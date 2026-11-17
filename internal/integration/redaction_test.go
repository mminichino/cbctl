//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/ci/secretscan"
	"github.com/mminichino/cbctl/internal/integration"
)

const redactionCanaryPassword = "Cbctl!It-Passw0rd"

// TestRedaction_CAP032_AC4: the run log holds no API key or test passwords.
func TestRedaction_CAP032_AC4(t *testing.T) {
	tg := integration.TargetFromEnv(t)

	// Produce debug Capella traffic so the log is non-empty.
	r := tg.CLIOpts(t, []integration.CLIOption{integration.NoApprove()}, "project", "list")
	if r.Code != 0 {
		t.Fatalf("project list: exit %d\n%s", r.Code, r.Output)
	}
	if !strings.Contains(r.Output, "[debug] - GET /v4/") {
		t.Fatalf("expected debug request lines in CLI output:\n%s", r.Output)
	}

	logBytes, err := os.ReadFile(tg.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "[debug] - GET /v4/") {
		t.Fatalf("integration log missing debug lines:\n%s", logText)
	}

	secrets := map[string]string{
		"API key":       tg.APIKey,
		"test password": redactionCanaryPassword,
	}
	for k, v := range secretscan.SecretsInDir(tg.SecretsDir) {
		secrets[k] = v
	}

	hits := secretscan.Scan(map[string]string{"integration log": logText}, secrets)
	if len(hits) > 0 {
		var b strings.Builder
		for _, h := range hits {
			fmt.Fprintf(&b, "%s:%d: contains %s\n", h.File, h.Line, h.Source)
		}
		t.Fatalf("secrets found in log:\n%s", b.String())
	}
}
