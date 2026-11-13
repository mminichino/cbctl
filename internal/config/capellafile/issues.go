package capellafile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// FixProblems is the action printed after a list of configuration problems.
const FixProblems = "Fix each problem above (an 'action:' line says how), then run the command again."

// Issue is one problem found in the configuration file.
type Issue struct {
	// Path is the location as FormatPath renders it, for example "projects[0].clusters[1].name".
	Path string `json:"path"`
	// Where names the location in words: "project nova › cluster nova-dev-01 › cloudProvider.region".
	Where string `json:"where"`
	// Message states the problem.
	Message string `json:"message"`
	// Line is the 1-based line in the file; 0 means unknown.
	Line int `json:"line"`
	// Topic is the guide topic that explains the rule; it defaults to "config".
	Topic string `json:"topic,omitempty"`
	// Fix is a one-line next step; optional.
	Fix string `json:"fix,omitempty"`
}

// String renders "<where> (line N)", the message and the optional action, one per line.
func (i Issue) String() string {
	where := i.Where
	if where == "" {
		where = i.Path
	}
	if where == "" {
		where = "(root)"
	}
	var sb strings.Builder
	sb.WriteString(where)
	if i.Line > 0 {
		fmt.Fprintf(&sb, " (line %d)", i.Line)
	}
	sb.WriteString("\n  ")
	sb.WriteString(i.Message)
	if i.Fix != "" {
		sb.WriteString("\n  action: ")
		sb.WriteString(i.Fix)
	}
	return sb.String()
}

// ConfigError is the failure to load a configuration file: one or more Issues, sorted by line.
// The CLI maps it to exit code 3.
type ConfigError struct {
	Issues []Issue
	Source string
}

// NewConfigError returns a ConfigError whose issues are sorted by (Line, Path).
func NewConfigError(source string, issues []Issue) *ConfigError {
	sorted := append([]Issue(nil), issues...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Line != sorted[b].Line {
			return sorted[a].Line < sorted[b].Line
		}
		return sorted[a].Path < sorted[b].Path
	})
	return &ConfigError{Issues: sorted, Source: source}
}

// Heading returns "<source> has N problem(s):".
func (e *ConfigError) Heading() string {
	source := e.Source
	if source == "" {
		source = "capella.yaml"
	}
	n := len(e.Issues)
	word := "problems"
	if n == 1 {
		word = "problem"
	}
	return fmt.Sprintf("%s has %d %s:", source, n, word)
}

// Error implements error: the heading followed by one indented block per issue.
func (e *ConfigError) Error() string {
	return RenderIssues(e.Heading(), e.Issues)
}

// RenderIssues renders heading and then one indented block per issue.
func RenderIssues(heading string, issues []Issue) string {
	lines := []string{heading}
	for _, i := range issues {
		lines = append(lines, "  "+strings.ReplaceAll(i.String(), "\n", "\n  "))
	}
	return strings.Join(lines, "\n")
}

// FormatPath renders p as "a.b[2].c"; an empty path is "(root)".
func FormatPath(p Path) string {
	var sb strings.Builder
	for _, el := range p {
		switch v := el.(type) {
		case int:
			fmt.Fprintf(&sb, "[%d]", v)
		default:
			if sb.Len() > 0 {
				sb.WriteByte('.')
			}
			fmt.Fprint(&sb, v)
		}
	}
	if sb.Len() == 0 {
		return "(root)"
	}
	return sb.String()
}

// ParsePath is the inverse of FormatPath: "a[1].b" becomes Path{"a", 1, "b"}.
func ParsePath(s string) Path {
	if s == "" || s == "(root)" {
		return nil
	}
	var out Path
	for i := 0; i < len(s); {
		switch s[i] {
		case '.':
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				out = append(out, s[i+1:])
				return out
			}
			if n, err := strconv.Atoi(s[i+1 : i+end]); err == nil {
				out = append(out, n)
			} else {
				out = append(out, s[i+1:i+end])
			}
			i += end + 1
		default:
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

// namedList describes a list whose entries are named resources.
type namedList struct{ label, key string }

var namedLists = map[string]namedList{
	"projects":            {"project", "name"},
	"clusters":            {"cluster", "name"},
	"serviceGroups":       {"service group", "services"},
	"buckets":             {"bucket", "name"},
	"scopes":              {"scope", "name"},
	"collections":         {"collection", "name"},
	"databaseCredentials": {"credential", "name"},
	"allowedCidrs":        {"allowed IP range", "cidr"},
}

const maxDescribeName = 40

// Describe says where p is, by name: "project shop › cluster web-01 › service group data ›
// numOfNodes". Named resources are followed while each one sits directly in the one before it;
// the rest is the field path.
func Describe(root Obj, p Path) string {
	var parts []string
	if len(p) > 0 && p[0] == "clusters" {
		if proj, ok := root["project"].(Obj); ok {
			parts = append(parts, fmt.Sprintf("project %v", proj["name"]))
		}
	}
	var node any = root
	i := 0
	for i+1 < len(p) {
		key, ok := p[i].(string)
		if !ok {
			break
		}
		nl, named := namedLists[key]
		idx, isIdx := p[i+1].(int)
		if !named || !isIdx {
			break
		}
		var item any
		if m, ok := node.(Obj); ok {
			if items, ok := m[key].([]any); ok && idx >= 0 && idx < len(items) {
				item = items[idx]
			}
		}
		var name any
		if m, ok := item.(Obj); ok {
			name = m[nl.key]
		}
		if list, ok := name.([]any); ok {
			strs := make([]string, 0, len(list))
			for _, s := range list {
				strs = append(strs, fmt.Sprint(s))
			}
			sort.Strings(strs)
			name = strings.Join(strs, ",")
		}
		if s, ok := name.(string); ok {
			if r := []rune(s); len(r) > maxDescribeName {
				name = string(r[:maxDescribeName]) + "…"
			}
		}
		if name == nil || name == "" {
			parts = append(parts, fmt.Sprintf("%s #%d", nl.label, idx+1))
		} else {
			parts = append(parts, fmt.Sprintf("%s %v", nl.label, name))
		}
		node = item
		i += 2
	}
	rest := p[i:]
	if len(parts) > 0 && i >= 2 {
		if key, ok := p[i-2].(string); ok && len(rest) == 1 && rest[0] == namedLists[key].key {
			rest = nil // the field that names the entry is the entry itself
		}
	}
	if len(rest) > 0 || len(parts) == 0 {
		parts = append(parts, FormatPath(rest))
	}
	return strings.Join(parts, " › ")
}

// fillWhere sets Where on every issue that has none.
func fillWhere(issues []Issue, root Obj) {
	for i := range issues {
		if issues[i].Where == "" {
			issues[i].Where = Describe(root, ParsePath(issues[i].Path))
		}
		if issues[i].Topic == "" {
			issues[i].Topic = "config"
		}
	}
}
