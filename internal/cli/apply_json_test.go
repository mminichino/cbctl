package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

const jsonServerYAML = `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: demo
spec:
  credentials:
    password: ${APPLY_JSON_PW}
  nodes:
    - ipAddress: 10.0.0.1
    - ipAddress: 10.0.0.2
  buckets:
    - name: orders
      scopes:
        - name: sales
          collections: [items]
      import:
        - scope: sales
          collection: items
          data:
            - id: 1
`

// TestApplyServerJSON checks the -o json document against its schema, and that stdout holds
// only that document.
func TestApplyServerJSON(t *testing.T) {
	env := map[string]string{"APPLY_JSON_PW": "json-pw-canary"}
	tests := []struct {
		name       string
		args       []string
		failJoin   string
		code       int
		wantStatus string
	}{
		{"applied", []string{"apply", "-f", "-", "-o", "json"}, "", 0, `"status":"applied"`},
		{"dry run", []string{"apply", "-f", "-", "-o", "json", "--dry-run"}, "", 0, `"status":"planned"`},
		{"failed", []string{"apply", "-f", "-", "-o", "json"}, "10.0.0.2", 1, `"status":"failed"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cli.UseApplyFakes(t, tc.failJoin)
			r := runApplyCmd(t, t.TempDir(), jsonServerYAML, env, tc.args...)
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, tc.code, r.stdout, r.stderr)
			}
			clitest.AssertJSONSchema(t, "apply.manifest", []byte(r.stdout))
			if !strings.Contains(strings.ReplaceAll(r.stdout, " ", ""), tc.wantStatus) {
				t.Errorf("stdout lacks %s:\n%s", tc.wantStatus, r.stdout)
			}
			if strings.Contains(r.stdout+r.stderr, "json-pw-canary") {
				t.Errorf("output leaks the password")
			}
		})
	}
}
