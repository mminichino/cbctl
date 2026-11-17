package capellafile

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// lineIndex maps FormatPath(path) to a 1-based line.
type lineIndex map[string]int

// of returns the line of p or of its nearest ancestor that has one; 0 when none.
func (l lineIndex) of(p Path) int {
	for {
		if n, ok := l[FormatPath(p)]; ok {
			return n
		}
		if len(p) == 0 {
			return 0
		}
		p = p[:len(p)-1]
	}
}

var (
	yamlLineRe    = regexp.MustCompile(`(?:^|\s)line (\d+)`)
	yamlProblemRe = regexp.MustCompile(`^yaml: (?:line \d+: )?`)
)

// parseYAML parses text into a document node. A syntax error becomes a single issue with the
// parser's line. Duplicate keys and other decode errors are reported the same way.
func parseYAML(text, source string) (*yaml.Node, []Issue) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(text, "\ufeff")), &doc); err != nil {
		return nil, []Issue{yamlIssue(err, source)}
	}
	if doc.Kind == 0 || (doc.Kind == yaml.DocumentNode && len(doc.Content) == 0) {
		return nil, []Issue{{Path: "(root)", Where: source, Message: source + " is empty", Line: 1,
			Fix: "add 'apiVersion: cbctl/v1' and your projects, or run 'cbctl capella sync' to write the file from Capella"}}
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		root = root.Content[0]
	}
	root = resolveAlias(root)
	if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		return nil, []Issue{{Path: "(root)", Where: source, Message: source + " is empty", Line: 1,
			Fix: "add 'apiVersion: cbctl/v1' and your projects, or run 'cbctl capella sync' to write the file from Capella"}}
	}
	if root.Kind != yaml.MappingNode {
		return nil, []Issue{{Path: "(root)", Where: source, Message: source + " must be a mapping of settings (key: value)",
			Line: root.Line, Fix: "start the file with 'apiVersion: cbctl/v1'"}}
	}
	// Decoding into a throw-away value surfaces duplicate mapping keys, which the node parser
	// accepts silently.
	var discard any
	if err := doc.Decode(&discard); err != nil {
		return nil, []Issue{yamlIssue(err, source)}
	}
	return root, nil
}

func yamlIssue(err error, source string) Issue {
	var typeErr *yaml.TypeError
	msg := err.Error()
	if errors.As(err, &typeErr) && len(typeErr.Errors) > 0 {
		msg = typeErr.Errors[0]
	}
	line := 0
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		line, _ = strconv.Atoi(m[1])
	}
	problem := yamlProblemRe.ReplaceAllString(msg, "")
	problem = strings.TrimPrefix(problem, "line "+strconv.Itoa(line)+": ")
	issue := Issue{Path: "(root)", Where: source, Line: line, Message: "invalid YAML: " + problem}
	if strings.Contains(msg, "tab character") || strings.Contains(msg, "cannot start any token") {
		issue.Fix = "indent with spaces, not tabs"
	}
	if strings.Contains(msg, "already defined") {
		issue.Fix = "remove or rename the duplicate key"
	}
	return issue
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// toObj converts a parsed node into a JSON-compatible value: mappings become Obj, sequences
// []any, integral numbers int64 and other numbers float64. Aliases are expanded and merge keys
// applied. Timestamps stay strings (RFC 3339 values such as expiresAt are written unquoted).
func toObj(n *yaml.Node) any {
	n = resolveAlias(n)
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.MappingNode:
		out := make(Obj, len(n.Content)/2)
		var merges []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.ShortTag() == "!!merge" {
				merges = append(merges, v)
				continue
			}
			out[k.Value] = toObj(v)
		}
		for _, m := range merges {
			for _, src := range mergeSources(m) {
				for key, val := range src {
					if _, exists := out[key]; !exists {
						out[key] = val
					}
				}
			}
		}
		return out
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, toObj(c))
		}
		return out
	case yaml.ScalarNode:
		return scalarValue(n)
	}
	return nil
}

func mergeSources(n *yaml.Node) []Obj {
	n = resolveAlias(n)
	var out []Obj
	switch n.Kind {
	case yaml.MappingNode:
		if m, ok := toObj(n).(Obj); ok {
			out = append(out, m)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			out = append(out, mergeSources(c)...)
		}
	}
	return out
}

func scalarValue(n *yaml.Node) any {
	switch n.ShortTag() {
	case "!!null":
		return nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err == nil {
			return b
		}
	case "!!int", "!!float":
		var v any
		if err := n.Decode(&v); err == nil {
			if num, ok := NormalizeNumbers(v).(float64); ok && (math.IsNaN(num) || math.IsInf(num, 0)) {
				return n.Value
			}
			return NormalizeNumbers(v)
		}
	}
	return n.Value
}

// NormalizeNumbers returns v with every integral number converted to int64 and every other
// number to float64, recursively through Obj and []any (maps and slices are copied).
func NormalizeNumbers(v any) any {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint:
		return normUint(uint64(t))
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		return normUint(t)
	case float32:
		return NormalizeNumbers(float64(t))
	case float64:
		if !math.IsNaN(t) && !math.IsInf(t, 0) && t == math.Trunc(t) && math.Abs(t) < 1<<53 {
			return int64(t)
		}
		return t
	case Obj:
		out := make(Obj, len(t))
		for k, x := range t {
			out[k] = NormalizeNumbers(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = NormalizeNumbers(x)
		}
		return out
	}
	return v
}

func normUint(u uint64) any {
	if u <= math.MaxInt64 {
		return int64(u)
	}
	return float64(u)
}

// indexLines records the line of every mapping key and sequence item of root, keyed by
// FormatPath. A mapping value is recorded at its key's line.
func indexLines(root *yaml.Node) lineIndex {
	lines := lineIndex{}
	var walk func(n *yaml.Node, p Path)
	walk = func(n *yaml.Node, p Path) {
		if n == nil {
			return
		}
		lines[FormatPath(p)] = n.Line
		if n.Kind == yaml.AliasNode {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.ShortTag() == "!!merge" {
					continue
				}
				child := append(append(Path{}, p...), k.Value)
				walk(n.Content[i+1], child)
				lines[FormatPath(child)] = k.Line
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, append(append(Path{}, p...), i))
			}
		}
	}
	walk(root, nil)
	return lines
}

// Node helpers shared by the editor.

// mapGet returns the value node of key in a mapping node, and the position of the key node.
func mapGet(m *yaml.Node, key string) (*yaml.Node, int) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

// scalarString returns the string value of a scalar node ("" for anything else).
func scalarString(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// hasAnchorsOrAliases reports whether any node in the tree is an anchor or an alias.
func hasAnchorsOrAliases(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return true
	}
	for _, c := range n.Content {
		if hasAnchorsOrAliases(c) {
			return true
		}
	}
	return false
}
