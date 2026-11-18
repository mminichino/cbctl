package serverapply_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/serverapply"
	"github.com/mminichino/cbctl/internal/rest"
	"github.com/mminichino/cbctl/internal/server"
)

// fakeProv is a Provisioner that records its calls.
type fakeProv struct {
	calls       []string
	initialized bool
	joined      map[string]bool
	members     []rest.NodeInfo
	pending     []string
	failJoin    string
	clusterName string
}

func (f *fakeProv) ClusterName(_ rest.Endpoint, _, _ string) (string, bool, error) {
	f.calls = append(f.calls, "clustername")
	return f.clusterName, f.initialized, nil
}

func (f *fakeProv) BootstrapPrimary(o rest.ProvisionOptions) (bool, error) {
	f.calls = append(f.calls, "bootstrap "+o.IPAddress)
	created := !f.initialized
	f.initialized = true
	return created, nil
}

func (f *fakeProv) JoinNode(o rest.ProvisionOptions) (bool, error) {
	f.calls = append(f.calls, "join "+o.IPAddress)
	if o.IPAddress == f.failJoin {
		return false, errors.New("boom")
	}
	if f.joined[o.IPAddress] {
		return false, nil
	}
	if f.joined == nil {
		f.joined = map[string]bool{}
	}
	f.joined[o.IPAddress] = true
	return true, nil
}

func (f *fakeProv) ClusterNodes(rest.Endpoint, string, string) ([]rest.NodeInfo, error) {
	f.calls = append(f.calls, "nodes")
	return f.members, nil
}

func (f *fakeProv) PendingNodes(rest.Endpoint, string, string) ([]string, error) {
	f.calls = append(f.calls, "pending")
	return f.pending, nil
}

func (f *fakeProv) RebalanceCluster(rest.ProvisionOptions) error {
	f.calls = append(f.calls, "rebalance")
	f.pending = nil
	return nil
}

// fakeData is a DataModel over in-memory maps. It shares state between instances.
type fakeData struct {
	buckets  map[string]int
	scopes   map[string]bool
	colls    map[string][]string // keyspace -> imported keys
	creates  int
	imported map[string]int
	failConn bool
}

func newFakeData() *fakeData {
	return &fakeData{buckets: map[string]int{}, scopes: map[string]bool{}, colls: map[string][]string{}, imported: map[string]int{}}
}

func (f *fakeData) Connect(context.Context, *config.Config) error {
	if f.failConn {
		return errors.New("no route")
	}
	return nil
}
func (f *fakeData) Disconnect() {}
func (f *fakeData) BucketQuotaMiB(n string) (int, bool, error) {
	q, ok := f.buckets[n]
	return q, ok, nil
}
func (f *fakeData) CreateBucket(n string, q, _ int) error {
	f.creates++
	f.buckets[n] = q
	return nil
}
func (f *fakeData) ScopeExists(b, s string) (bool, error) { return f.scopes[b+"."+s], nil }
func (f *fakeData) CreateScope(b, s string) error {
	f.creates++
	f.scopes[b+"."+s] = true
	return nil
}
func (f *fakeData) CollectionExists(b, s, c string) (bool, error) {
	_, ok := f.colls[b+"."+s+"."+c]
	return ok, nil
}
func (f *fakeData) CreateCollection(b, s, c string) error {
	f.creates++
	f.colls[b+"."+s+"."+c] = nil
	return nil
}
func (f *fakeData) CollectionIsEmpty(b, s, c string) (bool, error) {
	return len(f.colls[b+"."+s+"."+c]) == 0, nil
}
func (f *fakeData) ImportDocuments(_ context.Context, src server.DocSource, b, s, c, keyField string) (int, error) {
	n := 0
	for {
		doc, _, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, err
		}
		key := "uuid"
		if v, ok := doc.(map[string]any); ok && keyField != "" {
			key = strings.TrimSpace(toString(v[keyField]))
		}
		ks := b + "." + s + "." + c
		f.colls[ks] = append(f.colls[ks], key)
		n++
	}
	f.imported[b+"."+s+"."+c] += n
	return n, nil
}

func toString(v any) string {
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

// printer records the lines written.
type printer struct{ lines []string }

func (p *printer) OK(f string, a ...any)   { p.lines = append(p.lines, "ok:"+sprintf(f, a...)) }
func (p *printer) Info(f string, a ...any) { p.lines = append(p.lines, "info:"+sprintf(f, a...)) }
func (p *printer) Warn(f string, a ...any) { p.lines = append(p.lines, "warn:"+sprintf(f, a...)) }

func (p *printer) has(sub string) bool {
	for _, l := range p.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// doc parses and resolves a one-document manifest.
func doc(t *testing.T, dir, yaml string) *manifest.Document {
	t.Helper()
	t.Setenv("SA_PW", "pw-canary-1")
	set, err := manifest.Parse(context.Background(), strings.NewReader(yaml), "m.yaml", dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := set.Resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return &set.Docs[0]
}

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

// run applies d with the fakes and returns the report.
func run(t *testing.T, ctx context.Context, dir string, d *manifest.Document, p *fakeProv, data *fakeData, dry bool) (*manifest.Report, *printer, error) {
	t.Helper()
	pr := &printer{}
	rep, err := serverapply.Apply(ctx, serverapply.Env{
		Provisioner:  p,
		NewDataModel: func() serverapply.DataModel { return data },
		Printer:      pr,
		BaseDir:      dir,
		DryRun:       dry,
	}, d)
	return rep, pr, err
}
