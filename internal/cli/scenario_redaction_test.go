package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

const canaryPassword = "Canary!Passw0rd-xyz"

// credentialDoc adds a bucket + database credential whose password comes from APP_PW.
const credentialDoc = scenarioDoc + `
    buckets:
      - name: orders
        memoryAllocationInMb: 1024
    databaseCredentials:
      - name: app
        password: ${APP_PW}
        access:
          - privileges: [read]
            resources:
              buckets:
                - name: orders
`

// TestRedaction_CAP008_AC1_CanaryNeverPrinted asserts API key and canary password never appear in CLI output.
func TestRedaction_CAP008_AC1_CanaryNeverPrinted(t *testing.T) {
	s := newScenario(t)
	s.Env["APP_PW"] = canaryPassword
	t.Setenv("APP_PW", canaryPassword)
	writeCapellaYAML(t, s, credentialDoc)

	apiKey := s.Fake.APIKey
	var outputs []string
	for _, args := range [][]string{
		{"capella", "validate"},
		{"capella", "plan"},
		{"capella", "-o", "json", "plan"},
		{"capella", "apply", "--yes"},
	} {
		r := s.Run(args...)
		if r.Code != 0 && !strings.Contains(args[len(args)-1], "apply") {
			// validate/plan must succeed; apply may still succeed with credentials.
			t.Fatalf("%v: exit %d\n%s\n%s", args, r.Code, r.Stdout, r.Stderr)
		}
		if r.Code != 0 {
			t.Fatalf("%v: exit %d\n%s\n%s", args, r.Code, r.Stdout, r.Stderr)
		}
		outputs = append(outputs, r.Stdout, r.Stderr)
		clitest.AssertNoSecret(t, r, apiKey, canaryPassword)
	}

	joined := strings.Join(outputs, "\n")
	if strings.Contains(joined, canaryPassword) || strings.Contains(joined, apiKey) {
		t.Fatal("canary or API key leaked into combined output")
	}
}
