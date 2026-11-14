package clitest

import (
	"encoding/json"
	"strings"
	"testing"
)

// Result is the outcome of one Scenario.Run / RunWith invocation.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// JSON unmarshals Stdout as JSON and fails the test when decoding fails.
func (r Result) JSON(t testing.TB) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.Stdout)
	}
	return v
}

// Lines returns Stdout split on newlines with a trailing empty line from a final
// newline removed (strings.Split behaviour).
func (r Result) Lines() []string {
	if r.Stdout == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(r.Stdout, "\n"), "\n")
}
