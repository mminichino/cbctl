package capellaapply_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/capellaapply"
	"github.com/mminichino/cbctl/internal/server"
)

// lines records the progress lines of an apply, in the form "[kind] text".
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) add(kind, format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, "["+kind+"] "+fmt.Sprintf(format, a...))
}

// OK implements capellaapply.Printer.
func (l *lines) OK(format string, a ...any) { l.add("ok", format, a...) }

// Info implements capellaapply.Printer.
func (l *lines) Info(format string, a ...any) { l.add("info", format, a...) }

// Warn implements capellaapply.Printer.
func (l *lines) Warn(format string, a ...any) { l.add("warning", format, a...) }

func (l *lines) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

// harness is one fake Capella plus a manifest directory.
type harness struct {
	t   *testing.T
	f   *capellafake.Fake
	dir string
	env map[string]string
	out *lines
	// connect, when set, replaces the SDK connector of imports.
	connect capellaapply.Connector
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{t: t, f: capellafake.New(t), dir: t.TempDir(), env: map[string]string{"APP_PW": "Passw0rd!Passw0rd!"}}
}

// doc parses and resolves the single document in yaml.
func (h *harness) doc(yaml string) *manifest.Document {
	h.t.Helper()
	path := filepath.Join(h.dir, "m.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		h.t.Fatal(err)
	}
	set, err := manifest.Load(context.Background(), manifest.Source{Path: path, Cwd: h.dir, Env: func(k string) string { return h.env[k] }})
	if err != nil {
		h.t.Fatalf("load: %v", err)
	}
	if err := set.Resolve(); err != nil {
		h.t.Fatalf("resolve: %v", err)
	}
	return &set.Docs[0]
}

// result is the outcome of one Apply call.
type result struct {
	rep *manifest.Report
	err error
	out *lines
}

// run applies yaml. opts adjust the Env before the call.
func (h *harness) run(yaml string, opts ...func(*capellaapply.Env)) result {
	h.t.Helper()
	return h.runCtx(context.Background(), yaml, opts...)
}

func (h *harness) runCtx(ctx context.Context, yaml string, opts ...func(*capellaapply.Env)) result {
	h.t.Helper()
	doc := h.doc(yaml)
	h.out = &lines{}
	env := capellaapply.Env{
		Client:  h.f.Client(),
		BaseDir: h.dir,
		Printer: h.out,
		Clock:   h.f.Clock(),
		Getenv:  func(k string) string { return h.env[k] },
		Connect: h.connect,
	}
	for _, o := range opts {
		o(&env)
	}
	rep, err := capellaapply.Apply(ctx, env, doc)
	return result{rep: rep, err: err, out: h.out}
}

func (h *harness) writes() int { return len(h.f.Writes()) }

// manifestYAML is a cluster manifest with the given spec body (indented two spaces).
func manifestYAML(name, body string) string {
	return "apiVersion: cbctl/v1\nkind: CouchbaseCapella\nmetadata:\n  name: " + name + "\nspec:\n  project: ci-project\n" + body
}

// fakeSDK is a DataModel that records the imports it is asked to do.
type fakeSDK struct {
	mu       sync.Mutex
	empty    bool
	imported []string
	docs     map[string]int
	conns    []string
	users    []string
}

// CollectionIsEmpty implements capellaapply.DataModel.
func (s *fakeSDK) CollectionIsEmpty(string, string, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.empty, nil
}

// ImportDocuments implements capellaapply.DataModel.
func (s *fakeSDK) ImportDocuments(_ context.Context, src server.DocSource, bucket, scope, collection, keyField string) (int, error) {
	n := 0
	for {
		_, _, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, err
		}
		n++
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := bucket + "." + scope + "." + collection + "#" + keyField
	s.imported = append(s.imported, key)
	if s.docs == nil {
		s.docs = map[string]int{}
	}
	s.docs[key] += n
	return n, nil
}

func (s *fakeSDK) connector() capellaapply.Connector {
	return func(_ context.Context, conn, user, _ string) (capellaapply.DataModel, func(), error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.conns = append(s.conns, conn)
		s.users = append(s.users, user)
		return s, func() {}, nil
	}
}
