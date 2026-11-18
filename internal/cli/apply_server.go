package cli

import (
	"context"

	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/serverapply"
)

// serverApplyEnv is the Server apply dependency set; tests replace it to run against fakes.
var serverApplyEnv = func(env *applyEnv) serverapply.Env {
	return serverapply.Env{BaseDir: env.Set.BaseDir, DryRun: env.DryRun}
}

// applyServerDoc applies a CouchbaseServer document through serverapply. The report comes back
// even on failure, so the partial progress is shown; the error is wrapped like the other Server
// commands' (exit 1, Ctrl-C passes through as 130).
func applyServerDoc(ctx context.Context, env *applyEnv, doc *manifest.Document) (*manifest.Report, error) {
	rep, err := serverapply.Apply(ctx, serverApplyEnv(env), doc)
	return rep, failure("", err)
}
