package cli

import (
	"context"

	"github.com/mminichino/cbctl/internal/manifest"
)

// applyServerDoc applies a CouchbaseServer document.
//
// SKELETON: the Server execution (internal/manifest/serverapply) lands in the next commits of
// this branch; until then the handler reports that it is not implemented.
func applyServerDoc(_ context.Context, _ *applyEnv, doc *manifest.Document) (*manifest.Report, error) {
	return nil, Errorf(ExitConfigInvalid, "this build of cbctl cannot apply it yet", "applying %s documents is not implemented yet", doc.Envelope.Kind)
}
