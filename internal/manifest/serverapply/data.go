package serverapply

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/rest"
	"github.com/mminichino/cbctl/internal/server"
)

const defaultName = "_default"

// dataModel connects the SDK to the primary, creates the buckets, scopes and collections, and
// runs the imports (steps 5 and 6). Nothing connects when the manifest has no buckets.
func (a *applier) dataModel(ctx context.Context) error {
	if len(a.spec.Buckets) == 0 {
		a.rep.Add(manifest.Step{Name: "data-model", Status: manifest.StepSkipped, Detail: "the manifest has no buckets"})
		return nil
	}
	sdk := a.env.NewDataModel()
	if err := a.timed("connect", a.primary.IPAddress, func() (manifest.Step, error) {
		if err := sdk.Connect(ctx, a.sdkConfig()); err != nil {
			return manifest.Step{}, fmt.Errorf("connecting to %s: %w", a.primary.IPAddress, err)
		}
		return manifest.Step{Status: manifest.StepUnchanged}, nil
	}); err != nil {
		return err
	}
	defer sdk.Disconnect()

	for _, b := range a.spec.Buckets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.ensureBucket(sdk, b); err != nil {
			return err
		}
		for _, s := range b.Scopes {
			if err := a.ensureScope(ctx, sdk, b.Name, s); err != nil {
				return err
			}
		}
	}
	for _, b := range a.spec.Buckets {
		for i, imp := range b.Import {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := a.runImport(ctx, sdk, b.Name, i, imp); err != nil {
				return err
			}
		}
	}
	return nil
}

// sdkConfig is the SDK connection to the primary node.
func (a *applier) sdkConfig() *config.Config {
	cfg := config.New()
	host, _ := rest.ParseHostPort(a.primary.IPAddress, 8091)
	cfg.Hostname = host
	cfg.Username = a.spec.Credentials.Username
	cfg.Password = a.pw
	if t := a.spec.TLS; t != nil && t.Enabled {
		cfg.SSL = true
		if t.CAFile != "" {
			cfg.CACertPath = a.path(t.CAFile)
		}
	}
	return cfg
}

// path resolves a manifest path against the manifest's directory.
func (a *applier) path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(a.env.BaseDir, p)
}

func (a *applier) ensureBucket(sdk DataModel, b manifest.ServerBucket) error {
	return a.timed("bucket", b.Name, func() (manifest.Step, error) {
		quota, replicas := 0, 0
		if b.QuotaMiB != nil {
			quota = *b.QuotaMiB
		}
		if b.Replicas != nil {
			replicas = *b.Replicas
		}
		have, found, err := sdk.BucketQuotaMiB(b.Name)
		if err != nil {
			return manifest.Step{}, fmt.Errorf("looking up bucket %q: %w", b.Name, err)
		}
		a.counts.buckets++
		if found {
			a.pr.Info("Bucket %q exists", b.Name)
			if quota > 0 && have != quota {
				a.warn("bucket %q exists with a %d MiB quota; the manifest says %d MiB and the bucket is left as it is", b.Name, have, quota)
			}
			return manifest.Step{Status: manifest.StepUnchanged}, nil
		}
		if err := sdk.CreateBucket(b.Name, quota, replicas); err != nil {
			return manifest.Step{}, fmt.Errorf("creating bucket %q: %w", b.Name, err)
		}
		a.pr.OK("Bucket %q created", b.Name)
		return manifest.Step{Status: manifest.StepDone}, nil
	})
}

func (a *applier) ensureScope(ctx context.Context, sdk DataModel, bucket string, s manifest.ServerScope) error {
	if s.Name != defaultName {
		target := bucket + "." + s.Name
		err := a.timed("scope", target, func() (manifest.Step, error) {
			exists, err := sdk.ScopeExists(bucket, s.Name)
			if err != nil {
				return manifest.Step{}, fmt.Errorf("looking up scope %q: %w", target, err)
			}
			a.counts.scopes++
			if exists {
				a.pr.Info("Scope %q exists", target)
				return manifest.Step{Status: manifest.StepUnchanged}, nil
			}
			if err := sdk.CreateScope(bucket, s.Name); err != nil {
				return manifest.Step{}, fmt.Errorf("creating scope %q: %w", target, err)
			}
			a.pr.OK("Scope %q created", target)
			return manifest.Step{Status: manifest.StepDone}, nil
		})
		if err != nil {
			return err
		}
	}
	for _, c := range s.Collections {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c == defaultName {
			continue
		}
		target := bucket + "." + s.Name + "." + c
		err := a.timed("collection", target, func() (manifest.Step, error) {
			exists, err := sdk.CollectionExists(bucket, s.Name, c)
			if err != nil {
				return manifest.Step{}, fmt.Errorf("looking up collection %q: %w", target, err)
			}
			a.counts.collections++
			if exists {
				a.pr.Info("Collection %q exists", target)
				return manifest.Step{Status: manifest.StepUnchanged}, nil
			}
			if err := sdk.CreateCollection(bucket, s.Name, c); err != nil {
				return manifest.Step{}, fmt.Errorf("creating collection %q: %w", target, err)
			}
			a.pr.OK("Collection %q created", target)
			return manifest.Step{Status: manifest.StepDone}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// importSource opens the documents of one import entry: its JSON Lines file or its inline data.
func (a *applier) importSource(bucket string, idx int, imp manifest.Import) (server.DocSource, error) {
	if imp.File != "" {
		return server.FileSource(a.path(imp.File))
	}
	where := fmt.Sprintf("buckets[%s].import[%d].data", bucket, idx)
	return server.InlineSource(imp.Data, where), nil
}

// runImport loads one import entry into its collection unless the collection already holds
// documents: a second apply must not duplicate or overwrite data.
func (a *applier) runImport(ctx context.Context, sdk DataModel, bucket string, idx int, imp manifest.Import) error {
	scope, coll := imp.Scope, imp.Collection
	if scope == "" {
		scope = defaultName
	}
	if coll == "" {
		coll = defaultName
	}
	target := bucket + "." + scope + "." + coll
	return a.timed("import", target, func() (manifest.Step, error) {
		empty, err := sdk.CollectionIsEmpty(bucket, scope, coll)
		if err != nil {
			return manifest.Step{}, fmt.Errorf("checking whether %s is empty: %w", target, err)
		}
		if !empty {
			a.warn("Collection %s not empty; import skipped", target)
			return manifest.Step{Status: manifest.StepSkipped, Detail: "collection not empty"}, nil
		}
		src, err := a.importSource(bucket, idx, imp)
		if err != nil {
			return manifest.Step{}, fmt.Errorf("opening the documents for %s: %w", target, err)
		}
		defer func() { _ = src.Close() }() // read-only source
		n, err := sdk.ImportDocuments(ctx, src, bucket, scope, coll, imp.KeyField)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return manifest.Step{Count: n}, err
			}
			return manifest.Step{Count: n}, fmt.Errorf("import into %s failed after %d documents: %w; the next apply skips a collection that is not empty, so empty it to reload", target, n, err)
		}
		a.counts.imports++
		if n == 0 {
			a.pr.Info("Imported 0 documents into %s", target)
			return manifest.Step{Status: manifest.StepUnchanged, Detail: "no documents to import"}, nil
		}
		a.pr.OK("Imported %d documents into %s", n, target)
		return manifest.Step{Status: manifest.StepDone, Count: n}, nil
	})
}
