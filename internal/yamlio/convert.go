package yamlio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// YAMLToJSON converts one YAML document to JSON. Mapping keys must be strings and keep their
// order; aliases are expanded and merge keys (<<) applied; !!binary, non-string keys and
// duplicate keys are errors. Integers keep their digits, so none is rounded.
func YAMLToJSON(y []byte, opts ...Option) ([]byte, error) {
	o := newOptions(opts)
	n, err := parseOne(y, o)
	if err != nil {
		return nil, err
	}
	return nodeToJSON(n, o)
}

// NodeToJSON converts a parsed node (a document or any node below it) to JSON, with the rules
// of YAMLToJSON. A nil node is null.
func NodeToJSON(n *yaml.Node, opts ...Option) ([]byte, error) {
	return nodeToJSON(n, newOptions(opts))
}

func nodeToJSON(n *yaml.Node, o *options) ([]byte, error) {
	c := &converter{o: o}
	if err := c.write(n, 0); err != nil {
		return nil, err
	}
	return c.buf.Bytes(), nil
}

type converter struct {
	o     *options
	buf   bytes.Buffer
	nodes int
}

type entry struct {
	key   string
	value *yaml.Node
}

func (c *converter) fail(n *yaml.Node, format string, args ...any) error {
	e := &SyntaxError{Source: c.o.source, Msg: fmt.Sprintf(format, args...)}
	if n != nil {
		e.Line, e.Column = n.Line, n.Column
	}
	return e
}

func resolve(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && n.Alias != nil && i < maxDepth; i++ {
		n = n.Alias
	}
	return n
}

func (c *converter) write(n *yaml.Node, depth int) error {
	if c.nodes++; c.nodes > maxNodes {
		return c.fail(n, "document expands to more than %d values (alias expansion limit)", maxNodes)
	}
	if depth > maxDepth {
		return c.fail(n, "document is nested more than %d levels deep", maxDepth)
	}
	if n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			c.buf.WriteString("null")
			return nil
		}
		n = n.Content[0]
	}
	orig := n
	n = resolve(n)
	if n == nil {
		c.buf.WriteString("null")
		return nil
	}
	switch n.Kind {
	case yaml.MappingNode:
		entries, err := c.entries(n, 0)
		if err != nil {
			return err
		}
		c.buf.WriteByte('{')
		for i, e := range entries {
			if i > 0 {
				c.buf.WriteByte(',')
			}
			c.writeString(e.key)
			c.buf.WriteByte(':')
			if err := c.write(e.value, depth+1); err != nil {
				return err
			}
		}
		c.buf.WriteByte('}')
	case yaml.SequenceNode:
		c.buf.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				c.buf.WriteByte(',')
			}
			if err := c.write(item, depth+1); err != nil {
				return err
			}
		}
		c.buf.WriteByte(']')
	case yaml.ScalarNode:
		return c.scalar(n)
	default:
		return c.fail(orig, "unsupported YAML node")
	}
	return nil
}

// entries returns the key/value pairs of a mapping in document order, followed by the keys
// that merges add. Explicit keys win over merged ones and earlier merge sources over later ones.
func (c *converter) entries(m *yaml.Node, depth int) ([]entry, error) {
	if depth > maxDepth {
		return nil, c.fail(m, "merge keys are nested more than %d levels deep", maxDepth)
	}
	var out []entry
	seen := map[string]int{}
	var merges []*yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			merges = append(merges, v)
			continue
		}
		key, err := c.key(k)
		if err != nil {
			return nil, err
		}
		if first, dup := seen[key]; dup {
			return nil, c.fail(k, "mapping key %q already defined at line %d", key, first)
		}
		seen[key] = k.Line
		out = append(out, entry{key, v})
	}
	for _, mv := range merges {
		srcs, err := c.mergeSources(mv, depth)
		if err != nil {
			return nil, err
		}
		for _, src := range srcs {
			for _, e := range src {
				if _, exists := seen[e.key]; !exists {
					seen[e.key] = 0
					out = append(out, e)
				}
			}
		}
	}
	return out, nil
}

func (c *converter) mergeSources(n *yaml.Node, depth int) ([][]entry, error) {
	r := resolve(n)
	switch {
	case r == nil:
		return nil, nil
	case r.Kind == yaml.MappingNode:
		es, err := c.entries(r, depth+1)
		return [][]entry{es}, err
	case r.Kind == yaml.SequenceNode:
		var out [][]entry
		for _, item := range r.Content {
			rs := resolve(item)
			if rs == nil || rs.Kind != yaml.MappingNode {
				return nil, c.fail(item, "merge key '<<' needs a mapping or a list of mappings")
			}
			es, err := c.entries(rs, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, es)
		}
		return out, nil
	}
	return nil, c.fail(n, "merge key '<<' needs a mapping or a list of mappings")
}

func (c *converter) key(k *yaml.Node) (string, error) {
	r := resolve(k)
	if r == nil || r.Kind != yaml.ScalarNode || (r.ShortTag() != "!!str" && r.ShortTag() != "!!timestamp") {
		return "", c.fail(k, "mapping keys must be strings (quote the key)")
	}
	return r.Value, nil
}

var integerText = regexp.MustCompile(`^[-+]?[0-9]+$`)

func (c *converter) scalar(n *yaml.Node) error {
	switch n.ShortTag() {
	case "!!null":
		c.buf.WriteString("null")
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			c.writeString(n.Value)
			return nil
		}
		c.buf.WriteString(strconv.FormatBool(b))
	case "!!int":
		var v any
		if err := n.Decode(&v); err != nil {
			c.writeString(n.Value)
			return nil
		}
		switch t := v.(type) {
		case int:
			c.buf.WriteString(strconv.FormatInt(int64(t), 10))
		case int64:
			c.buf.WriteString(strconv.FormatInt(t, 10))
		case uint64:
			c.buf.WriteString(strconv.FormatUint(t, 10))
		default:
			c.writeString(n.Value)
		}
	case "!!float":
		// An integer literal too large for 64 bits is a float to YAML; the digits are kept.
		if integerText.MatchString(n.Value) {
			c.buf.WriteString(strings.TrimPrefix(n.Value, "+"))
			return nil
		}
		var f float64
		if err := n.Decode(&f); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			// JSON cannot carry NaN or Inf: keep the text.
			c.writeString(n.Value)
			return nil
		}
		b, err := json.Marshal(f)
		if err != nil {
			c.writeString(n.Value)
			return nil
		}
		c.buf.Write(b)
	case "!!timestamp":
		if c.o.ts == RFC3339 {
			var t time.Time
			if err := n.Decode(&t); err == nil {
				c.writeString(t.Format(time.RFC3339Nano))
				return nil
			}
		}
		c.writeString(n.Value)
	case "!!binary":
		return c.fail(n, "!!binary values cannot be converted to JSON")
	default:
		c.writeString(n.Value)
	}
	return nil
}

func (c *converter) writeString(s string) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	c.buf.Write(bytes.TrimRight(b.Bytes(), "\n"))
}
