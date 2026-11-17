package importer

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/engine"
	"gopkg.in/yaml.v3"
)

// Templates written under an empty projects: or clusters: list. The editor strips them when it
// next opens the file.
const (
	NewClusterTemplate = capellafile.NewClusterTemplate
	NoProjectsTemplate = capellafile.NoProjectsTemplate
)

// Version is shown in the header comment of a written file; main sets it through SetVersion.
var version = "dev"

// SetVersion records the cbctl version for the header comment.
func SetVersion(v string) {
	if v != "" {
		version = v
	}
}

// Now is the clock of the header comment; tests replace it.
var Now = func() time.Time { return engine.RealClock{}.Now().UTC() }

func projectNames(data Obj) []string {
	var names []string
	if ps, ok := data["projects"].([]any); ok {
		for _, p := range ps {
			if m, ok := p.(Obj); ok {
				names = append(names, str(m, "name"))
			}
		}
		return names
	}
	return []string{str(sub(data, "project"), "name")}
}

// Header is the comment block at the top of a file written by sync.
func Header(orgID, orgName string, names []string) string {
	org := orgID
	if orgName != "" {
		org = fmt.Sprintf("%s (%s)", orgName, orgID)
	}
	label := "no projects"
	switch len(names) {
	case 0:
	case 1:
		label = "project " + names[0]
	default:
		label = "projects " + strings.Join(names, ", ")
	}
	return strings.Join([]string{
		fmt.Sprintf("# Imported by cbctl %s on %s", version, Now().Format("2006-01-02T15:04:05Z")),
		fmt.Sprintf("# Organization %s, %s.", org, label),
		"# Maintained by cbctl: change it with `cbctl capella cluster create|update|scale` and refresh it",
		"# with `cbctl capella sync`. `cbctl capella guide config <field>` explains every field.",
		"",
	}, "\n")
}

// Render returns the YAML text of the result with a header. Comment markers become comments or
// commented-out list entries.
func Render(res *Result, orgName string) (string, error) {
	node := capellafile.ToNode(res.Data, "")
	applyMarkers(node, res.Comments)
	body, err := capellafile.EncodeNode(node)
	if err != nil {
		return "", err
	}
	orgID, _ := res.Data["organizationId"].(string)
	text := Header(orgID, orgName, projectNames(res.Data)) + body
	text = strings.Replace(text, "projects: []\n", "projects: []\n"+NoProjectsTemplate, 1)
	text = strings.Replace(text, "clusters: []\n", "clusters: []\n"+NewClusterTemplate, 1)
	return text, nil
}

func commentText(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "# " + l
	}
	return strings.Join(lines, "\n")
}

// applyMarkers rewrites marker keys of a node tree into YAML comments.
func applyMarkers(n *yaml.Node, comments map[string]string) {
	if trailing := markNode(n, comments); len(trailing) > 0 {
		footLast(n, trailing)
	}
}

// footLast attaches trailing comments of a list to its last item, for a list that is not the
// value of a mapping key (yaml.v3 then writes them at the end of the document).
func footLast(n *yaml.Node, trailing []string) {
	if len(n.Content) == 0 {
		n.HeadComment = joinComments(append([]string{n.HeadComment}, trailing...))
		return
	}
	last := n.Content[len(n.Content)-1]
	last.FootComment = joinComments(append([]string{last.FootComment}, trailing...))
}

// markNode is applyMarkers for one node. For a non-empty list whose last entries are commented
// out it returns those comments, so the caller can put them under the list's key: as the foot
// comment of the last item yaml.v3 would write them at the end of the document.
func markNode(n *yaml.Node, comments map[string]string) []string {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			applyMarkers(c, comments)
		}
	case yaml.MappingNode:
		var content []*yaml.Node
		var pending []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if IsMarker(k.Value) {
				pending = append(pending, comments[k.Value])
				continue
			}
			if trailing := markNode(v, comments); len(trailing) > 0 {
				k.FootComment = joinComments(append([]string{k.FootComment}, trailing...))
			}
			if len(pending) > 0 {
				k.HeadComment = joinComments(append(pending, k.HeadComment))
				pending = nil
			}
			content = append(content, k, v)
		}
		n.Content = content
		if len(pending) > 0 {
			if len(content) >= 2 {
				last := content[len(content)-2]
				last.FootComment = joinComments(append([]string{last.FootComment}, pending...))
			} else {
				n.HeadComment = joinComments(pending)
			}
		}
	case yaml.SequenceNode:
		var content []*yaml.Node
		var pending []string
		for _, item := range n.Content {
			if item.Kind == yaml.MappingNode && onlyMarkers(item) {
				for i := 0; i < len(item.Content); i += 2 {
					pending = append(pending, comments[item.Content[i].Value])
				}
				continue
			}
			if item.Kind == yaml.MappingNode && leadingMarkers(item) {
				// markers ahead of data in a list item: comment above the dash
				var own []string
				var rest []*yaml.Node
				for i := 0; i+1 < len(item.Content); i += 2 {
					if IsMarker(item.Content[i].Value) {
						own = append(own, comments[item.Content[i].Value])
					} else {
						rest = append(rest, item.Content[i], item.Content[i+1])
					}
				}
				item.Content = rest
				pending = append(pending, own...)
			}
			applyMarkers(item, comments)
			if len(pending) > 0 {
				item.HeadComment = joinComments(append(pending, item.HeadComment))
				pending = nil
			}
			content = append(content, item)
		}
		n.Content = content
		if len(pending) > 0 {
			if len(content) > 0 {
				return pending
			}
			n.Kind, n.Tag, n.Style = yaml.SequenceNode, "!!seq", yaml.FlowStyle
			n.HeadComment = joinComments(pending)
		}
	}
	return nil
}

func onlyMarkers(m *yaml.Node) bool {
	if len(m.Content) == 0 {
		return false
	}
	for i := 0; i < len(m.Content); i += 2 {
		if !IsMarker(m.Content[i].Value) {
			return false
		}
	}
	return true
}

func leadingMarkers(m *yaml.Node) bool {
	for i := 0; i < len(m.Content); i += 2 {
		if IsMarker(m.Content[i].Value) {
			return true
		}
	}
	return false
}

func joinComments(parts []string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if strings.HasPrefix(p, "#") {
			out = append(out, p)
			continue
		}
		out = append(out, commentText(p))
	}
	return strings.Join(out, "\n")
}

// Summary is the one-line count of what was imported.
func Summary(res *Result) string {
	c := res.Counts
	plural := func(n int, w string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, w)
		}
		return fmt.Sprintf("%d %ss", n, w)
	}
	projects := c["project"]
	if projects == 0 {
		projects = 1
	}
	parts := []string{plural(projects, "project")}
	for _, k := range []string{"cluster", "bucket", "scope", "collection", "credential", "allowed CIDR", "hibernation schedule"} {
		parts = append(parts, plural(c[k], k))
	}
	return "Imported " + strings.Join(parts, ", ")
}

// SortedUnread returns the unread entries ordered by cluster name.
func SortedUnread(res *Result) []*UnreadInfo {
	var out []*UnreadInfo
	for _, u := range res.Unread {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
