// Package capellafile loads, validates, edits and discovers the declarative environment file
// (capella.yaml) that "cbctl capella plan|apply|destroy|drift|sync" works from.
//
// The desired state is held as generic maps rather than typed structs on purpose: the engine only
// compares fields that are present in the file, which needs presence semantics. It is a port of
// capellactl's config/loader.py, crossref.py and editor.py (Apache-2.0, Couchbase, Inc.).
//
// The package never touches the network and must not import the plan, resources or capellaapi
// packages.
package capellafile

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Obj is a JSON-compatible mapping: its values are string, bool, int64, float64, []any, Obj or nil.
// Numbers decoded from YAML are normalized so that integral values are int64 and all other
// numbers are float64.
type Obj = map[string]any

// Path addresses a value inside the document. Elements are string (mapping key) or int (list
// index).
type Path []any

// DefaultCredentialsOutput is the secret-file directory used when the file does not set
// credentialsOutput.
const DefaultCredentialsOutput = "./.cbctl/credentials"

// ProjectEntry is one project of the file together with its clusters, for either file form.
type ProjectEntry struct {
	// Data is the project mapping itself. In the projects: form it still contains the clusters
	// key; the clusters are also available as Clusters.
	Data Obj
	// Clusters are the project's cluster mappings in file order.
	Clusters []Obj
	// Base is ("projects", i) or ("project").
	Base Path
	// ClustersBase is ("projects", i, "clusters") or ("clusters").
	ClustersBase Path
}

// Name returns the project's name ("" when unset).
func (p ProjectEntry) Name() string {
	return asString(p.Data["name"])
}

// Config is a loaded and validated environment file.
type Config struct {
	// Path is the absolute path of the file ("" when loaded from text).
	Path string
	// Source is the display name of the file, for example "capella.yaml".
	Source string
	// Raw is the validated document with defaults applied and secret references resolved.
	Raw Obj
	// Text is the original file text.
	Text string
	// OrgID is organizationId from the file after ${VAR} resolution ("" if absent or unresolved).
	OrgID string
	// Hints are one-line tips collected while loading, shown by Warnings.
	Hints []string

	baseDir string
	lines   map[string]int
}

// Projects returns the projects of both file forms: projects: [...] or the single-project form
// project: + clusters:.
func (c *Config) Projects() []ProjectEntry {
	return projectEntries(c.Raw)
}

func projectEntries(raw Obj) []ProjectEntry {
	if list, ok := raw["projects"].([]any); ok {
		out := make([]ProjectEntry, 0, len(list))
		for i, item := range list {
			p, ok := item.(Obj)
			if !ok {
				continue
			}
			out = append(out, ProjectEntry{
				Data:         p,
				Clusters:     objList(p["clusters"]),
				Base:         Path{"projects", i},
				ClustersBase: Path{"projects", i, "clusters"},
			})
		}
		return out
	}
	if p, ok := raw["project"].(Obj); ok {
		return []ProjectEntry{{
			Data:         p,
			Clusters:     objList(raw["clusters"]),
			Base:         Path{"project"},
			ClustersBase: Path{"clusters"},
		}}
	}
	return nil
}

// ProjectNames returns the project names in file order.
func (c *Config) ProjectNames() []string {
	entries := c.Projects()
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// CredentialsOutput returns the directory for generated credential files; a relative setting is
// resolved against the directory of the config file.
func (c *Config) CredentialsOutput() string {
	dir := DefaultCredentialsOutput
	if s, ok := c.Raw["credentialsOutput"].(string); ok && strings.TrimSpace(s) != "" {
		dir = s
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(c.baseDir, dir)
}

// LineOf returns the 1-based line of p in the file, falling back to the nearest enclosing path
// that has one. It returns 0 when nothing is known.
func (c *Config) LineOf(p Path) int {
	return lineIndex(c.lines).of(p)
}

// Counts holds how many resources of each kind the file declares.
type Counts struct {
	Projects    int `json:"projects"`
	Clusters    int `json:"clusters"`
	Buckets     int `json:"buckets"`
	Scopes      int `json:"scopes"`
	Collections int `json:"collections"`
	Credentials int `json:"credentials"`
	CIDRs       int `json:"cidrs"`
}

// Counts tallies the declared resources.
func (c *Config) Counts() Counts {
	var n Counts
	for _, e := range c.Projects() {
		n.Projects++
		for _, cl := range e.Clusters {
			n.Clusters++
			n.CIDRs += len(asList(cl["allowedCidrs"]))
			n.Credentials += len(asList(cl["databaseCredentials"]))
			for _, b := range objList(cl["buckets"]) {
				n.Buckets++
				for _, s := range objList(b["scopes"]) {
					n.Scopes++
					n.Collections += len(asList(s["collections"]))
				}
			}
		}
	}
	return n
}

// Summary returns the one-line result of a successful validation, for example
// "capella.yaml is valid: 1 project, 2 clusters, 1 bucket, ...".
func (c *Config) Summary() string {
	n := c.Counts()
	plural := func(count int, one, many string) string {
		if count == 1 {
			return fmt.Sprintf("%d %s", count, one)
		}
		return fmt.Sprintf("%d %s", count, many)
	}
	parts := []string{
		plural(n.Projects, "project", "projects"),
		plural(n.Clusters, "cluster", "clusters"),
		plural(n.Buckets, "bucket", "buckets"),
		plural(n.Scopes, "scope", "scopes"),
		plural(n.Collections, "collection", "collections"),
		plural(n.Credentials, "credential", "credentials"),
		plural(n.CIDRs, "allowed CIDR", "allowed CIDRs"),
	}
	source := c.Source
	if source == "" {
		source = "capella.yaml"
	}
	return fmt.Sprintf("%s is valid: %s", source, strings.Join(parts, ", "))
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asObj(v any) Obj {
	o, _ := v.(Obj)
	return o
}

func objList(v any) []Obj {
	var out []Obj
	for _, item := range asList(v) {
		if o, ok := item.(Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

// asInt returns v as an int64 when it is an integral number.
func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	}
	return 0, false
}
