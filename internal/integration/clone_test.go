//go:build integration

package integration_test

import (
	"testing"

	"github.com/mminichino/cbctl/internal/integration"
)

// TestClone_CAP049_AC9: clone to a second region, assert plan clean, delete clone.
func TestClone_CAP049_AC9(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	if tg.Kind == "fake" {
		t.Skip("cluster clone needs broader fake/API surface for cross-region clone + plan clean")
	}
	t.Skip("sandbox clone: enable once Capella clone + cleanup path is wired in nightly")
}
