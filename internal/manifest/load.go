package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/yamlio"
)

// APIVersion is the only apiVersion of manifests.
const APIVersion = "cbctl/v1"

// The kinds a manifest document can have.
const (
	KindServer  = "CouchbaseServer"
	KindCapella = "CouchbaseCapella"
)

var kinds = []string{KindServer, KindCapella}

// Source says where a manifest comes from.
type Source struct {
	// Path is the manifest file, or "-" for Stdin.
	Path string
	// Stdin is read when Path is "-"; nil means no input.
	Stdin io.Reader
	// Cwd resolves a relative Path and the relative file paths of a manifest read from stdin.
	// Empty means the process working directory.
	Cwd string
	// Env looks up environment variables for secret references; nil means os.Getenv. Load does
	// not read it (nothing is resolved while loading); Set.Resolve uses it.
	Env func(string) string
}

// Set is a loaded manifest: the validated documents of one file in file order.
type Set struct {
	Docs []Document
	// BaseDir is the directory relative paths in the manifest resolve from: the manifest's
	// directory, or the working directory for stdin.
	BaseDir string
	// Name is the display name of the manifest: the path as given, or "<stdin>".
	Name string

	env func(string) string
}

// Load reads, strictly decodes and validates every document of the manifest offline. Nothing
// is resolved against the network and no secret is resolved. The error is a *ValidationError
// (exit code 3) listing every problem of every document, or an I/O error. When it is not nil
// no document is returned, so nothing can be applied from a file that has a problem.
func Load(ctx context.Context, src Source) (*Set, error) {
	cwd := src.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("finding the working directory: %w", err)
		}
		cwd = wd
	}
	var (
		r       io.Reader
		name    string
		baseDir = cwd
	)
	switch src.Path {
	case "":
		return nil, &ValidationError{Source: "manifest", Issues: []Issue{{Doc: 0, Msg: "no manifest given", Fix: "use -f FILE, or -f - to read stdin"}}}
	case "-":
		if src.Stdin == nil {
			return nil, errors.New("reading the manifest from stdin: no input")
		}
		r, name = src.Stdin, "<stdin>"
	default:
		p := src.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("opening manifest: %w", err)
		}
		defer f.Close()
		r, name, baseDir = f, src.Path, filepath.Dir(p)
	}
	set, err := Parse(ctx, r, name, baseDir)
	if set != nil {
		set.env = src.Env
	}
	return set, err
}

// Parse is Load for a reader. name is the display name used in messages; baseDir resolves the
// relative paths of the manifest.
func Parse(ctx context.Context, r io.Reader, name, baseDir string) (*Set, error) {
	dec := yamlio.NewDecoder(r, yamlio.WithSource(name), yamlio.WithTimestamps(yamlio.RFC3339), yamlio.WithUseNumber())
	set := &Set{BaseDir: baseDir, Name: name}
	verr := &ValidationError{Source: name}
	seen := map[string]int{}
	var prev error
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if err == prev { //nolint:errorlint // the decoder repeats the identical error value once it gives up
				break
			}
			prev = err
			if !addDecodeError(verr, n, err, nil) {
				return nil, err
			}
			continue
		}
		doc, issues := decodeDocument(ctx, name, baseDir, n, raw)
		verr.Issues = append(verr.Issues, issues...)
		if doc == nil {
			continue
		}
		key := doc.Envelope.Kind + "/" + doc.Envelope.Metadata.Name
		if first, dup := seen[key]; dup && doc.Envelope.Metadata.Name != "" {
			verr.Issues = append(verr.Issues, Issue{Doc: n, Ref: key, Pointer: "/metadata/name",
				Line: doc.Lines.Of("/metadata/name").Line, Col: doc.Lines.Of("/metadata/name").Column, source: name,
				Msg: fmt.Sprintf("document %d already declares %s", first, key), Fix: "each kind and name may appear once in a file"})
		} else {
			seen[key] = n
		}
		set.Docs = append(set.Docs, *doc)
	}
	if len(set.Docs) == 0 && len(verr.Issues) == 0 {
		verr.Issues = append(verr.Issues, Issue{Msg: "the manifest holds no documents", Fix: "start with apiVersion: cbctl/v1 and a kind", source: name})
	}
	if len(verr.Issues) > 0 {
		sortIssues(verr.Issues)
		return nil, verr
	}
	return set, nil
}

// Resolve resolves the secret references of every document. The error is a *ValidationError
// listing every reference that cannot be resolved; no message contains a secret.
func (s *Set) Resolve() error {
	verr := &ValidationError{Source: s.Name}
	for i := range s.Docs {
		var ve *ValidationError
		if err := s.Docs[i].Resolve(s.env, s.BaseDir); errors.As(err, &ve) {
			verr.Issues = append(verr.Issues, ve.Issues...)
		}
	}
	if len(verr.Issues) == 0 {
		return nil
	}
	sortIssues(verr.Issues)
	return verr
}

// addDecodeError turns a YAML error into issues. It returns false when err is not one it knows.
func addDecodeError(verr *ValidationError, doc int, err error, lines yamlio.LineIndex) bool {
	var multi interface{ Unwrap() []error }
	if errors.As(err, &multi) {
		ok := true
		for _, e := range multi.Unwrap() {
			ok = addDecodeError(verr, doc, e, lines) && ok
		}
		return ok
	}
	var fe *yamlio.FieldError
	var se *yamlio.SyntaxError
	switch {
	case errors.As(err, &fe):
		msg := fe.Msg
		if fe.Suggest != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", fe.Suggest)
		}
		verr.Issues = append(verr.Issues, Issue{Doc: doc, Pointer: fe.Pointer, Line: fe.Line, Col: fe.Column, Msg: msg, source: verr.Source})
	case errors.As(err, &se):
		verr.Issues = append(verr.Issues, Issue{Doc: doc, Line: se.Line, Col: se.Column, Msg: se.Msg, source: verr.Source})
	default:
		return false
	}
	return true
}

// header is the envelope read leniently, to find the kind before the strict decode.
type header struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   Metadata        `json:"metadata"`
	Spec       json.RawMessage `json:"spec"`
}

type serverDoc struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   Metadata   `json:"metadata"`
	Spec       ServerSpec `json:"spec"`
}

type capellaDoc struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   Metadata    `json:"metadata"`
	Spec       CapellaSpec `json:"spec"`
}

// decodeDocument decodes and validates one document. The document is nil when it has problems.
func decodeDocument(ctx context.Context, name, baseDir string, n int, raw *yamlio.Document) (*Document, []Issue) {
	c := &collector{source: name, doc: n, lines: raw.Lines}
	fail := func() (*Document, []Issue) { sortIssues(c.issues); return nil, c.issues }

	var h header
	if err := raw.Decode(&h, false); err != nil {
		v := &ValidationError{Source: name}
		if !addDecodeError(v, n, err, raw.Lines) {
			c.add("", err.Error())
		}
		c.issues = append(c.issues, v.Issues...)
		return fail()
	}
	c.ref = h.Kind + "/" + h.Metadata.Name
	if h.Kind == "" || h.Metadata.Name == "" {
		c.ref = strings.Trim(c.ref, "/")
	}
	if !c.header(h) {
		return fail()
	}

	doc := &Document{Index: n - 1, Lines: raw.Lines, source: name}
	doc.Envelope = Envelope{APIVersion: h.APIVersion, Kind: h.Kind, Metadata: h.Metadata}
	var err error
	switch h.Kind {
	case KindServer:
		var d serverDoc
		if err = raw.Decode(&d, true); err == nil {
			doc.Server = &d.Spec
		}
	case KindCapella:
		var d capellaDoc
		if err = raw.Decode(&d, true); err == nil {
			doc.Capella = &d.Spec
		}
	}
	if err != nil {
		v := &ValidationError{Source: name}
		if !addDecodeError(v, n, err, raw.Lines) {
			c.add("", err.Error())
		}
		for _, i := range v.Issues {
			i.Ref = c.ref
			c.issues = append(c.issues, i)
		}
		return fail()
	}
	c.checkClusterName(h.Metadata.Name)
	doc.validate(ctx, c, baseDir)
	if len(c.issues) > 0 {
		return fail()
	}
	return doc, nil
}

// header checks apiVersion and kind. It returns false when the kind is unusable.
func (c *collector) header(h header) bool {
	switch {
	case h.Kind == "" && h.APIVersion == APIVersion:
		c.add("/kind", "kind is missing; this looks like a capella.yaml, not a manifest",
			"use 'cbctl capella apply -c FILE' for capella.yaml, or add kind: CouchbaseServer or CouchbaseCapella")
		return false
	case h.APIVersion == "":
		c.add("/apiVersion", "apiVersion is required", "use apiVersion: "+APIVersion)
		return false
	case h.APIVersion != APIVersion:
		c.addf("/apiVersion", "apiVersion %q is not supported; use %s", clip(h.APIVersion), APIVersion)
		return false
	case h.Kind == "":
		c.add("/kind", "kind is required", "use one of: "+strings.Join(kinds, ", "))
		return false
	case !oneOf(h.Kind, kinds):
		msg := fmt.Sprintf("unknown kind %q", clip(h.Kind))
		if g := yamlio.ClosestKey(h.Kind, kinds); g != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", g)
		}
		c.add("/kind", msg, "kind is case-sensitive; use one of: "+strings.Join(kinds, ", "))
		return false
	}
	if t := strings.TrimSpace(string(h.Spec)); t == "" || t == "null" {
		c.add("", "spec is required", "add a spec: section for the "+h.Kind)
		return false
	}
	return true
}

// validate runs the offline rules of the document's kind, then applies defaults when it passes.
func (d *Document) validate(ctx context.Context, c *collector, baseDir string) {
	switch {
	case d.Server != nil:
		c.validateServer(ctx, baseDir, d.Server)
		if len(c.issues) == 0 {
			applyServerDefaults(d.Server)
		}
	case d.Capella != nil:
		c.validateCapellaCluster(d.Capella)
		c.validateCapellaData(ctx, baseDir, d.Capella)
	}
}
