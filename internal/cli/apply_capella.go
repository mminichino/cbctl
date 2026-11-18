package cli

import (
	"context"

	"github.com/mminichino/cbctl/internal/manifest"
)

// applyCapellaDoc applies a CouchbaseCapella document.
//
// STUB (P5 hook): the Capella apply is built by phase P5 (mm--apply-capella), which replaces
// this file with the real handler. The dispatch table in apply.go already points here.
func applyCapellaDoc(_ context.Context, _ *applyEnv, doc *manifest.Document) (*manifest.Report, error) {
	return nil, Errorf(ExitConfigInvalid, "use 'cbctl capella apply -c capella.yaml' for Capella until manifest apply supports it",
		"applying %s documents is not implemented yet", doc.Envelope.Kind)
}
