package yamlio

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Position is a 1-based line and column in the YAML text.
type Position struct{ Line, Column int }

// LineIndex maps JSON pointers ("/spec/nodes/0/ipAddress") to the position of the mapping key
// or sequence item that holds the value. The root document is the empty pointer.
type LineIndex map[string]Position

// Of returns the position of pointer, or of its nearest ancestor that has one. The zero
// Position means nothing is known.
func (l LineIndex) Of(pointer string) Position {
	for {
		if p, ok := l[pointer]; ok {
			return p
		}
		i := strings.LastIndexByte(pointer, '/')
		if i < 0 {
			return Position{}
		}
		pointer = pointer[:i]
	}
}

// BuildLineIndex records the position of every mapping key and sequence item below n. Merge
// keys and the targets of aliases are not followed; a value that comes from a merge reports the
// position of its nearest ancestor.
func BuildLineIndex(n *yaml.Node) LineIndex {
	idx := LineIndex{}
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	indexNode(idx, n, "")
	return idx
}

func indexNode(idx LineIndex, n *yaml.Node, ptr string) {
	if n == nil {
		return
	}
	idx[ptr] = Position{n.Line, n.Column}
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
			child := ptr + "/" + escapePointer(k.Value)
			indexNode(idx, n.Content[i+1], child)
			idx[child] = Position{k.Line, k.Column}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			indexNode(idx, c, ptr+"/"+strconv.Itoa(i))
		}
	}
}
