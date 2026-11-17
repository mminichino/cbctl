//go:build integration

package integration_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

func TestResultRequestsParsesDebugLines(t *testing.T) {
	out := "" +
		"[debug] - GET /v4/organizations/org-it/projects -> 200 (12ms)\n" +
		"[debug] - POST /v4/organizations/org-it/projects -> 201 (3ms) body={\"name\":\"x\"}\n" +
		"[info] - not a request\n" +
		"[debug] - DELETE /v4/organizations/org-it/projects/p1 -> 204 (1ms)\n"
	r := integration.Result{Output: out}
	got := r.Requests()
	if len(got) != 3 {
		t.Fatalf("Requests = %#v, want 3", got)
	}
	if got[0].Method != "GET" || got[0].Status != 200 || got[0].Ms != 12 {
		t.Errorf("GET line: %#v", got[0])
	}
	if got[1].Method != "POST" || got[1].Status != 201 {
		t.Errorf("POST line: %#v", got[1])
	}
	writes := r.Writes()
	if len(writes) != 2 || writes[0].Method != "POST" || writes[1].Method != "DELETE" {
		t.Fatalf("Writes = %#v", writes)
	}
}
