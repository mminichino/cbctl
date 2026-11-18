// Package serverapply applies a CouchbaseServer manifest document: it initializes the primary
// node, joins the other nodes, rebalances, creates buckets, scopes and collections, and imports
// documents. Every step is safe to repeat, so a failed or interrupted apply is finished by
// running it again.
package serverapply

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/rest"
	"github.com/mminichino/cbctl/internal/server"
)

// Provisioner is the management REST API surface the apply uses. *rest.Client implements it.
type Provisioner interface {
	BootstrapPrimary(opts rest.ProvisionOptions) (bool, error)
	JoinNode(opts rest.ProvisionOptions) (bool, error)
	ClusterName(endpoint rest.Endpoint, username, password string) (name string, initialized bool, err error)
	ClusterNodes(endpoint rest.Endpoint, username, password string) ([]rest.NodeInfo, error)
	PendingNodes(endpoint rest.Endpoint, username, password string) ([]string, error)
	RebalanceCluster(opts rest.ProvisionOptions) error
}

// DataModel is the SDK surface the apply uses for buckets, scopes, collections and imports.
// *server.Server implements it.
type DataModel interface {
	Connect(ctx context.Context, cfg *config.Config) error
	Disconnect()
	BucketQuotaMiB(name string) (quota int, found bool, err error)
	CreateBucket(name string, quota int, replicas int) error
	ScopeExists(bucket, scope string) (bool, error)
	CreateScope(bucket, scope string) error
	CollectionExists(bucket, scope, collection string) (bool, error)
	CreateCollection(bucket, scope, collection string) error
	CollectionIsEmpty(bucket, scope, collection string) (bool, error)
	ImportDocuments(ctx context.Context, src server.DocSource, bucket, scope, collection, keyField string) (int, error)
}

// Env carries what Apply needs besides the document. The zero value of each field has a
// working default except BaseDir.
type Env struct {
	// Provisioner talks to the management REST API (default rest.NewClient()).
	Provisioner Provisioner
	// NewDataModel returns an unconnected SDK handle (default server.New).
	NewDataModel func() DataModel
	// Printer receives the progress lines (default logging.Current()).
	Printer Printer
	// Now supplies the time for step durations (default time.Now).
	Now func() time.Time
	// BaseDir resolves the relative file paths of the manifest.
	BaseDir string
	// DryRun plans the steps without contacting any node.
	DryRun bool
}

// Printer is the part of the output printer the apply writes progress to.
type Printer interface {
	OK(format string, a ...any)
	Info(format string, a ...any)
	Warn(format string, a ...any)
}

// applier is the state of one Apply call.
type applier struct {
	env  Env
	spec *manifest.ServerSpec
	name string
	rep  *manifest.Report
	pr   Printer

	primary manifest.ServerNode
	// pw is the resolved admin password, kept out of every message.
	pw string
	// created counts what the apply changed, for the summary line.
	counts summaryCounts
}

type summaryCounts struct{ buckets, scopes, collections, imports int }

// Apply runs the Server pipeline for doc and returns what it did. The report is returned even
// when err is not nil: it lists the steps that completed and ends with the one that failed.
// ctx is checked between steps; when it is done Apply returns ctx's error and the completed
// steps stay in place.
func Apply(ctx context.Context, env Env, doc *manifest.Document) (*manifest.Report, error) {
	if doc == nil || doc.Server == nil {
		return nil, errors.New("serverapply: the document is not a CouchbaseServer")
	}
	if env.Provisioner == nil {
		env.Provisioner = rest.NewClient()
	}
	if env.NewDataModel == nil {
		env.NewDataModel = func() DataModel { return server.New() }
	}
	if env.Printer == nil {
		env.Printer = logging.Current()
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	spec := doc.Server
	idx := spec.PrimaryIndex()
	if idx < 0 {
		return nil, errors.New("serverapply: the document has no single primary node")
	}
	a := &applier{
		env:     env,
		spec:    spec,
		name:    doc.Envelope.Metadata.Name,
		rep:     &manifest.Report{Kind: doc.Envelope.Kind, Name: doc.Envelope.Metadata.Name, Steps: []manifest.Step{}},
		pr:      env.Printer,
		primary: spec.Nodes[idx],
		pw:      spec.Credentials.Password.Value(),
	}
	if env.DryRun {
		a.plan()
		a.rep.Finish()
		return a.rep, nil
	}
	err := a.run(ctx)
	a.rep.Summary = a.summary()
	if err != nil {
		a.rep.Status = manifest.ReportFailed
		a.rep.Error = err.Error()
	}
	a.rep.Finish()
	return a.rep, err
}

// run executes the steps in order and stops at the first failure.
func (a *applier) run(ctx context.Context) error {
	for _, step := range []func(context.Context) error{
		a.bootstrap, a.joins, a.membership, a.rebalance, a.dataModel,
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// timed runs fn as one step and records it. fn returns the finished step (Name and Target are
// filled in here); a non-nil error records the step as failed and is returned wrapped with the
// step's name.
func (a *applier) timed(name, target string, fn func() (manifest.Step, error)) error {
	start := a.env.Now()
	s, err := fn()
	s.Name, s.Target = name, target
	s.Duration = a.env.Now().Sub(start)
	if err != nil {
		s.Status = manifest.StepFailed
		s.Error = err.Error()
		a.rep.Add(s)
		return err
	}
	a.rep.Add(s)
	return nil
}

func (a *applier) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.rep.Warn(msg)
	a.pr.Warn("%s", msg)
}

// summary describes what the document holds, for the closing line.
func (a *applier) summary() string {
	parts := []string{plural(len(a.spec.Nodes), "node")}
	if a.counts.buckets > 0 {
		parts = append(parts, plural(a.counts.buckets, "bucket"))
	}
	if a.counts.collections > 0 {
		parts = append(parts, plural(a.counts.collections, "collection"))
	}
	if a.counts.imports > 0 {
		parts = append(parts, plural(a.counts.imports, "import"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
