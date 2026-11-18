// Package apisnap builds Management API response snapshots from an OpenAPI spec
// (port of capellactl scripts/update-api-responses.py).
package apisnap

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"go.yaml.in/yaml/v3"
)

// DefaultSpecURL is the published Capella Management API OpenAPI document.
const DefaultSpecURL = "https://docs.couchbase.com/cloud/management-api-reference/_attachments/openapi.external.generated.yaml"

var keepKeys = map[string]bool{
	"type": true, "properties": true, "required": true, "items": true, "enum": true,
	"allOf": true, "oneOf": true, "anyOf": true, "additionalProperties": true,
	"nullable": true, "format": true,
}

var braceParam = regexp.MustCompile(`\{[^}]+\}`)

// Document is the on-disk responses snapshot shape.
type Document struct {
	Source     string      `json:"source"`
	APIVersion string      `json:"apiVersion"`
	Retrieved  string      `json:"retrieved"`
	Operations []Operation `json:"operations"`
}

// Operation is one inventory row with documented 2xx response schemas.
type Operation struct {
	Method     string         `json:"method"`
	Path       string         `json:"path"`
	Documented bool           `json:"documented"`
	Summary    string         `json:"summary,omitempty"`
	Responses  map[string]any `json:"responses,omitempty"`
}

// Options control Build.
type Options struct {
	SpecSource string // file path or URL; empty → DefaultSpecURL
	Retrieved  string // YYYY-MM-DD; empty → UTC today
	Inventory  []capellaapi.Endpoint
}

// Build loads the OpenAPI spec and produces a responses Document for Inventory.
func Build(opts Options) (Document, error) {
	src := opts.SpecSource
	if src == "" {
		src = DefaultSpecURL
	}
	spec, err := LoadSpec(src)
	if err != nil {
		return Document{}, err
	}
	inv := opts.Inventory
	if inv == nil {
		inv = capellaapi.Inventory
	}
	retrieved := opts.Retrieved
	if retrieved == "" {
		retrieved = time.Now().UTC().Format("2006-01-02")
	}
	sourceLabel := src
	if opts.SpecSource == "" {
		sourceLabel = DefaultSpecURL
	}
	return buildFromSpec(spec, sourceLabel, retrieved, inv), nil
}

// LoadSpec reads an OpenAPI YAML document from a local file or HTTP(S) URL.
func LoadSpec(source string) (map[string]any, error) {
	var raw []byte
	var err error
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		resp, err := http.Get(source) //nolint:gosec // published Capella OpenAPI URL or caller-supplied --spec
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }() // body fully read below
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s", source, resp.Status)
		}
		raw, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
	} else {
		raw, err = os.ReadFile(source)
		if err != nil {
			return nil, err
		}
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("parse OpenAPI YAML: %w", err)
	}
	return spec, nil
}

func buildFromSpec(spec map[string]any, source, retrieved string, inv []capellaapi.Endpoint) Document {
	paths, _ := spec["paths"].(map[string]any)
	byPath := map[string]map[string]any{}
	for p, ops := range paths {
		if m, ok := ops.(map[string]any); ok {
			byPath[normPath(p)] = m
		}
	}
	info, _ := spec["info"].(map[string]any)
	apiVersion, _ := info["version"].(string)

	ops := make([]Operation, 0, len(inv))
	for _, e := range inv {
		entry := Operation{Method: e.Method, Path: e.Path}
		pathOps := byPath[normPath(e.Path)]
		if pathOps == nil {
			ops = append(ops, entry)
			continue
		}
		op, _ := pathOps[strings.ToLower(e.Method)].(map[string]any)
		if op == nil {
			ops = append(ops, entry)
			continue
		}
		entry.Documented = true
		if s, ok := op["summary"].(string); ok {
			entry.Summary = s
		}
		responses := map[string]any{}
		respMap, _ := op["responses"].(map[string]any)
		for code, respAny := range respMap {
			if !strings.HasPrefix(code, "2") {
				continue
			}
			resp, _ := respAny.(map[string]any)
			resp = derefResponse(resp, spec)
			schema := schemaFromResponse(resp)
			if schema != nil {
				responses[fmt.Sprint(code)] = resolve(schema, spec, nil)
			} else {
				responses[fmt.Sprint(code)] = nil
			}
		}
		entry.Responses = responses
		ops = append(ops, entry)
	}
	return Document{
		Source:     source,
		APIVersion: apiVersion,
		Retrieved:  retrieved,
		Operations: ops,
	}
}

func normPath(path string) string {
	return braceParam.ReplaceAllString(path, "{}")
}

func derefResponse(resp map[string]any, spec map[string]any) map[string]any {
	for {
		ref, _ := resp["$ref"].(string)
		if ref == "" {
			return resp
		}
		target := resolveRef(ref, spec)
		next, ok := target.(map[string]any)
		if !ok {
			return resp
		}
		resp = next
	}
}

func schemaFromResponse(resp map[string]any) any {
	content, _ := resp["content"].(map[string]any)
	if content == nil {
		return nil
	}
	app, _ := content["application/json"].(map[string]any)
	if app == nil {
		return nil
	}
	return app["schema"]
}

func resolve(node any, spec map[string]any, seen []string) any {
	switch n := node.(type) {
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			out[i] = resolve(v, spec, seen)
		}
		return out
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok && ref != "" {
			for _, s := range seen {
				if s == ref {
					return map[string]any{}
				}
			}
			target := resolveRef(ref, spec)
			return resolve(target, spec, append(seen, ref))
		}
		out := map[string]any{}
		for k, v := range n {
			if k == "properties" {
				props, _ := v.(map[string]any)
				po := map[string]any{}
				for name, s := range props {
					po[name] = resolve(s, spec, seen)
				}
				out[k] = po
				continue
			}
			if keepKeys[k] {
				out[k] = resolve(v, spec, seen)
			}
		}
		return out
	default:
		return n
	}
}

func resolveRef(ref string, spec map[string]any) any {
	ref = strings.TrimPrefix(ref, "#/")
	var cur any = spec
	for _, part := range strings.Split(ref, "/") {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// Encode writes the document as indented JSON with a trailing newline (Python indent=1).
// Nested schema object keys are sorted alphabetically by encoding/json (deterministic;
// may differ from Python's YAML insertion order while remaining schema-equivalent).
func Encode(doc Document) ([]byte, error) {
	raw, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Missing returns "METHOD path" for undocumented inventory operations.
func Missing(doc Document) []string {
	var out []string
	for _, o := range doc.Operations {
		if !o.Documented {
			out = append(out, o.Method+" "+o.Path)
		}
	}
	sort.Strings(out)
	return out
}

// WriteFile encodes and writes path.
func WriteFile(path string, doc Document) error {
	raw, err := Encode(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
