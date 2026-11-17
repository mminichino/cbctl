// Package guide holds the concept pages shown by 'cbctl capella guide' and renders the
// schema description of one capella.yaml field. The pages are markdown files adapted from
// capellactl and compiled into the binary.
package guide

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/config/capellafile"
)

//go:embed topics/*.md
var topicFS embed.FS

// Topic is one guide page.
type Topic struct {
	Name    string
	Summary string
}

// topics lists the pages in display order.
var topics = []Topic{
	{"getting-started", "configure → init or sync → validate → plan → apply"},
	{"config", "capella.yaml layout, secret references, defaults, .yaml/.yml discovery"},
	{"credentials", "database credentials, generated passwords, rotation"},
	{"scaling", "scale out, in and up; limits; scale-in checks; --step"},
	{"backup", "cluster backups and bucket backups: schedules, back up now, restore, export"},
	{"idempotency", "plan symbols, how resources are matched, what is compared"},
	{"hibernation", "power off/on, weekly on/off schedule, pause/resume"},
	{"ci", "pipelines: --auto-approve, --quiet, gating on plan, GitHub Actions and GitLab examples"},
	{"safety", "deletion protection, prune, cascade, confirmations"},
	{"environment", "API key, organization, profiles, environment variables, precedence"},
	{"exit-codes", "what each exit code means"},
	{"migrate", "moving from capellactl: renamed commands, environment, profiles, exit codes"},
}

// Topics returns the available pages in display order.
func Topics() []Topic { return append([]Topic(nil), topics...) }

// Names returns the topic names in display order.
func Names() []string {
	names := make([]string, len(topics))
	for i, t := range topics {
		names[i] = t.Name
	}
	return names
}

// Exists reports whether name is a topic.
func Exists(name string) bool {
	for _, t := range topics {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Render returns the markdown of topic name.
func Render(name string) (string, error) {
	if !Exists(name) {
		return "", fmt.Errorf("unknown guide topic %q (topics: %s)", name, strings.Join(Names(), ", "))
	}
	raw, err := topicFS.ReadFile("topics/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("reading guide topic %s: %w", name, err)
	}
	return string(raw), nil
}

// TopicList is the "name  summary" listing printed by 'guide topics'.
func TopicList() string {
	var b strings.Builder
	for _, t := range topics {
		fmt.Fprintf(&b, "  %-16s %s\n", t.Name, t.Summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

var mutability = map[string]string{
	"updatable":   "updatable in place",
	"immutable":   "immutable after creation (changing it is BLOCKED; requires a new resource)",
	"key":         "identifies the resource (natural key)",
	"replace":     "changing it re-creates the entry (the API has no update)",
	"rotate-only": "set on create; changed only with --rotate-password",
	"tool":        "cbctl setting (not sent to Capella)",
}

// FieldPath normalizes a user-supplied dotted field path: list indexes are dropped and the
// single-project form (project.*, clusters.*) maps to projects.*.
func FieldPath(p string) []string {
	var parts []string
	for _, s := range strings.Split(strings.Trim(p, "."), ".") {
		if i := strings.Index(s, "["); i >= 0 {
			s = s[:i]
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	switch {
	case len(parts) > 0 && parts[0] == "clusters":
		parts = append([]string{"projects"}, parts...)
	case len(parts) > 0 && parts[0] == "project":
		parts[0] = "projects"
	}
	return parts
}

// nodeAt follows keys through the schema, stepping into list items between keys. node is the
// schema at the last key (the array node for a list); inner is what the list holds, or node itself.
func nodeAt(keys []string) (node, inner map[string]any, ok bool) {
	var path capellafile.Path
	for _, k := range keys {
		path = append(path, k)
		n, found := capellafile.SchemaNode(path)
		if !found {
			return nil, nil, false
		}
		node, inner = n, n
		if n["type"] == "array" {
			if _, has := n["items"].(map[string]any); has {
				path = append(path, 0)
				if it, found := capellafile.SchemaNode(path); found {
					inner = it
				}
			}
		}
	}
	return node, inner, node != nil
}

// Field describes the schema of one dotted field path such as "clusters.cloudProvider.region".
// A path that does not start at the document root is looked up under a cluster, then under a
// project, so "cloudProvider.region" works too. An unknown field returns an error that lists
// close matches.
func Field(p string) (string, error) {
	parts := FieldPath(p)
	if len(parts) > 0 {
		candidates := [][]string{
			parts,
			append([]string{"projects", "clusters"}, parts...),
			append([]string{"projects"}, parts...),
		}
		for _, c := range candidates {
			if node, _, ok := nodeAt(c); ok {
				return fieldDetail(strings.Join(c, "."), node), nil
			}
		}
	}
	msg := fmt.Sprintf("unknown config field %q", p)
	if close := closeFields(strings.Join(parts, ".")); len(close) > 0 {
		msg += ". Did you mean: " + strings.Join(close, ", ") + "?"
	}
	return "", fmt.Errorf("%s", msg)
}

func fieldDetail(path string, node map[string]any) string {
	lines := []string{path, "", fmt.Sprintf("  %v", orEmpty(node["description"])), ""}
	t := fmt.Sprint(orEmpty(node["type"]))
	if t == "" {
		if one, ok := node["oneOf"].([]any); ok {
			set := map[string]bool{}
			for _, o := range one {
				if m, ok := o.(map[string]any); ok {
					if s, ok := m["type"].(string); ok {
						set[s] = true
					}
				}
			}
			var names []string
			for s := range set {
				names = append(names, s)
			}
			sort.Strings(names)
			t = strings.Join(names, " | ")
		}
	}
	lines = append(lines, "  type:       "+t)
	enum, _ := node["enum"].([]any)
	if items, ok := node["items"].(map[string]any); ok && enum == nil {
		enum, _ = items["enum"].([]any)
	}
	if len(enum) > 0 {
		strs := make([]string, len(enum))
		for i, e := range enum {
			strs[i] = fmt.Sprint(e)
		}
		lines = append(lines, "  allowed:    "+strings.Join(strs, ", "))
	}
	_, hasMin := node["minimum"]
	_, hasMax := node["maximum"]
	if hasMin || hasMax {
		lo, hi := "…", "…"
		if hasMin {
			lo = fmt.Sprint(node["minimum"])
		}
		if hasMax {
			hi = fmt.Sprint(node["maximum"])
		}
		lines = append(lines, fmt.Sprintf("  range:      %s–%s", lo, hi))
	}
	if d, ok := node["default"]; ok {
		lines = append(lines, fmt.Sprintf("  default:    %v", d))
	}
	if m, ok := node["x-mutability"].(string); ok && m != "" {
		desc := mutability[m]
		if desc == "" {
			desc = m
		}
		lines = append(lines, "  changes:    "+desc)
	}
	if api, ok := node["x-api"].(string); ok && api != "" {
		lines = append(lines, "  API:        "+api)
	}
	if ex, ok := node["examples"].([]any); ok && len(ex) > 0 {
		name := path
		if i := strings.LastIndex(path, "."); i >= 0 {
			name = path[i+1:]
		}
		lines = append(lines, fmt.Sprintf("  example:    %s: %v", name, ex[0]))
	}
	return strings.Join(lines, "\n")
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// Fields lists every documented field in schema order, indented by depth.
func Fields() string {
	lines := []string{"capella.yaml fields (cbctl capella guide config <field> for details):", ""}
	var walk func(keys []string)
	walk = func(keys []string) {
		_, inner, ok := nodeAt(keys)
		if !ok && len(keys) > 0 {
			return
		}
		if len(keys) == 0 {
			inner, _ = capellafile.SchemaNode(nil)
		}
		props, _ := inner["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for k := range props {
			if len(keys) == 0 && (k == "project" || k == "clusters") {
				continue
			}
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			child := append(append([]string(nil), keys...), k)
			n, in, ok := nodeAt(child)
			if !ok {
				continue
			}
			t := fmt.Sprint(orEmpty(n["type"]))
			if t == "" {
				t = "value"
			}
			if t == "array" && in["type"] == "object" {
				t = "list"
			}
			mark := map[string]string{"immutable": "  [immutable]", "key": "  [key]", "replace": "  [re-create]"}[fmt.Sprint(n["x-mutability"])]
			lines = append(lines, fmt.Sprintf("%s%s: %s%s", strings.Repeat("  ", len(keys)), k, t, mark))
			walk(child)
		}
	}
	walk(nil)
	return strings.Join(lines, "\n")
}

func allFieldPaths() []string {
	var out []string
	var walk func(keys []string)
	walk = func(keys []string) {
		var inner map[string]any
		if len(keys) == 0 {
			inner, _ = capellafile.SchemaNode(nil)
		} else {
			_, in, ok := nodeAt(keys)
			if !ok {
				return
			}
			inner = in
		}
		props, _ := inner["properties"].(map[string]any)
		for k := range props {
			if len(keys) == 0 && (k == "project" || k == "clusters") {
				continue
			}
			child := append(append([]string(nil), keys...), k)
			out = append(out, strings.Join(child, "."))
			walk(child)
		}
	}
	walk(nil)
	sort.Strings(out)
	return out
}

func closeFields(want string) []string {
	all := allFieldPaths()
	last := want
	if i := strings.LastIndex(want, "."); i >= 0 {
		last = want[i+1:]
	}
	var out []string
	for _, f := range all {
		if f == last || strings.HasSuffix(f, "."+last) {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		for _, f := range all {
			if strings.HasPrefix(f, want) || (len(want) > 3 && strings.Contains(f, want)) {
				out = append(out, f)
			}
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
