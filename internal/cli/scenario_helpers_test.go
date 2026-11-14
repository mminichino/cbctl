package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

// scenarioDoc is a minimal valid single-cluster capella.yaml for Scenario tests.
const scenarioDoc = `apiVersion: cbctl/v1
project:
  name: envproj
clusters:
  - name: envclu
    cloudProvider:
      type: aws
      region: us-east-1
      cidr: 10.0.0.0/24
    availability:
      type: multi
    support:
      plan: developer pro
      timezone: PT
    serviceGroups:
      - node:
          compute: {cpu: 4, ram: 16}
          disk: {type: gp3, storage: 50, iops: 3000}
        numOfNodes: 3
        services: [data, index, query]
    deletionProtection: false
`

func newScenario(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := clitest.NewScenario(t)
	s.Env["CBCTL_CONFIG"] = ""
	t.Setenv("CBCTL_CONFIG", "")
	return s
}

func writeCapellaYAML(t *testing.T, s *clitest.Scenario, doc string) {
	t.Helper()
	s.WriteFile("capella.yaml", doc)
}

func mustScenario(t *testing.T, r clitest.Result, args ...string) clitest.Result {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("capella %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	return r
}

func wantContains(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Errorf("output does not contain %q:\n%s", p, s)
		}
	}
}
