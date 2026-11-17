package yamlio

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// ParseNode parses y, which must hold at most one document, into its document node. It returns
// nil for empty input. Comments and positions are kept, which makes it the entry point for
// editors; values should be read through Unmarshal or NodeToJSON.
func ParseNode(y []byte, opts ...Option) (*yaml.Node, error) {
	n, err := parseOne(y, newOptions(opts))
	if err != nil || isEmpty(n) {
		return nil, err
	}
	return n, nil
}

// EncodeNode writes n (a document or any node) as YAML with its comments and key order.
func EncodeNode(n *yaml.Node, opts ...Option) ([]byte, error) {
	return encodeNode(n, newOptions(opts))
}

// MappingKeys returns the keys of mapping node n in file order (nil for other nodes). Go maps
// lose that order, so lists that must follow the file read it from the node.
func MappingKeys(n *yaml.Node) []string {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	keys := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys = append(keys, n.Content[i].Value)
	}
	return keys
}

// DeleteMappingKeys removes names from the mapping stored under parentKey at the top of doc and
// reports how many it removed. Everything else, comments included, stays as it is.
func DeleteMappingKeys(doc *yaml.Node, parentKey string, names []string) int {
	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root == nil || root.Kind != yaml.MappingNode {
		return 0
	}
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		drop[n] = true
	}
	removed := 0
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != parentKey {
			continue
		}
		m := root.Content[i+1]
		if m.Kind != yaml.MappingNode {
			return 0
		}
		kept := make([]*yaml.Node, 0, len(m.Content))
		for j := 0; j+1 < len(m.Content); j += 2 {
			if drop[m.Content[j].Value] {
				removed++
				continue
			}
			kept = append(kept, m.Content[j], m.Content[j+1])
		}
		m.Content = kept
		if len(kept) == 0 {
			m.Style = yaml.FlowStyle // an empty mapping must be written as {}
		}
		break
	}
	return removed
}

// KeysAt returns the keys, in file order, of the mapping at the JSON pointer ptr ("" is the
// document root) in y. It returns nil when ptr does not name a mapping. Go maps lose file
// order, so lists that must follow the file (preset names, disk types) read it from here.
func KeysAt(y []byte, ptr string, opts ...Option) ([]string, error) {
	doc, err := ParseNode(y, opts...)
	if err != nil || doc == nil {
		return nil, err
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	if ptr != "" {
		for _, seg := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
			seg = strings.NewReplacer("~1", "/", "~0", "~").Replace(seg)
			next := (*yaml.Node)(nil)
			if n.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(n.Content); i += 2 {
					if n.Content[i].Value == seg {
						next = n.Content[i+1]
					}
				}
			}
			if next == nil {
				return nil, nil
			}
			n = next
		}
	}
	return MappingKeys(n), nil
}
