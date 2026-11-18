package capellaapply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/capella"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/server"
)

// reachHint is added to a failed connection: Capella only accepts clients from allowed CIDRs and
// apply never adds an entry on its own (C4).
const reachHint = "is this host in spec.allowedCidrs?"

// DataModel is the SDK surface an import uses. *server.Server implements it.
type DataModel interface {
	CollectionIsEmpty(bucket, scope, collection string) (bool, error)
	ImportDocuments(ctx context.Context, src server.DocSource, bucket, scope, collection, keyField string) (int, error)
}

// Connector opens an SDK connection to a Capella cluster with a database credential. cleanup
// releases the connection and must be called once.
type Connector func(ctx context.Context, connectionString, username, password string) (sdk DataModel, cleanup func(), err error)

// sdkConnector is the production Connector: gocb over TLS (couchbases://) with the system roots,
// which Capella's certificates chain to.
func sdkConnector(ctx context.Context, connectionString, username, password string) (DataModel, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	cluster, cleanup, err := capella.ConnectCluster(connectionString, username, password, "", 0, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	s := server.New()
	s.Cluster = cluster
	return s, func() {
		_ = cluster.Close(nil) // closing at the end of the apply; nothing to report
		cleanup()
	}, nil
}

// importer runs the imports of one document. Connections are opened lazily, one per credential,
// and closed by close.
type importer struct {
	a        *applier
	conn     string
	sessions map[string]DataModel
	cleanups []func()
}

func (im *importer) close() {
	for _, c := range im.cleanups {
		c()
	}
}

// session returns the SDK connection of a credential, opening it on first use.
func (im *importer) session(ctx context.Context, cred manifest.Credential) (DataModel, error) {
	if sdk, ok := im.sessions[cred.Name]; ok {
		return sdk, nil
	}
	if cred.Password == nil {
		return nil, fmt.Errorf("credential %q has no password reference; an import needs a password apply can read", cred.Name)
	}
	sdk, cleanup, err := im.a.env.Connect(ctx, im.conn, cred.Name, cred.Password.Value())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("connecting to %s as %s: %w; %s", im.conn, cred.Name, err, reachHint)
	}
	im.sessions[cred.Name] = sdk
	im.cleanups = append(im.cleanups, cleanup)
	return sdk, nil
}

// runImports loads every import entry of the document. It stops at the first failure.
func (a *applier) runImports(ctx context.Context, connectionString string) error {
	im := &importer{a: a, conn: connectionString, sessions: map[string]DataModel{}}
	defer im.close()
	creds := map[string]manifest.Credential{}
	for _, c := range a.spec.Credentials {
		creds[c.Name] = c
	}
	for _, b := range a.spec.Buckets {
		for i, imp := range b.Import {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := a.runImport(ctx, im, creds[imp.Credential], b.Name, i, imp); err != nil {
				return err
			}
		}
	}
	return nil
}

// importSource opens the documents of one import entry: its JSON Lines file or its inline data.
func (a *applier) importSource(bucket string, idx int, imp manifest.Import) (server.DocSource, error) {
	if imp.File != "" {
		return server.FileSource(manifest.ImportPath(a.env.BaseDir, imp.File))
	}
	return server.InlineSource(imp.Data, fmt.Sprintf("buckets[%s].import[%d].data", bucket, idx)), nil
}

// runImport loads one entry unless its collection already holds documents: a second apply must
// not duplicate or overwrite data.
func (a *applier) runImport(ctx context.Context, im *importer, cred manifest.Credential, bucket string, idx int, imp manifest.Import) error {
	scope, coll := imp.ImportTarget()
	target := strings.Join([]string{bucket, scope, coll}, ".")
	return a.timed("import", target, func() (manifest.Step, error) {
		sdk, err := im.session(ctx, cred)
		if err != nil {
			return manifest.Step{}, err
		}
		empty, err := sdk.CollectionIsEmpty(bucket, scope, coll)
		if err != nil {
			return manifest.Step{}, fmt.Errorf("checking whether %s is empty: %w; %s", target, err, reachHint)
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
		if n == 0 {
			a.pr.Info("Imported 0 documents into %s", target)
			return manifest.Step{Status: manifest.StepUnchanged, Detail: "no documents to import"}, nil
		}
		a.pr.OK("Imported %d documents into %s", n, target)
		return manifest.Step{Status: manifest.StepDone, Count: n}, nil
	})
}
