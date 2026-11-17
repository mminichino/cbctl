package yamlio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Marshal encodes v with encoding/json and converts the result to block-style YAML. Struct
// fields keep their declaration order and map keys are sorted (encoding/json rules), so the
// output is stable. Strings that YAML would read as another type (on, 123, null) are quoted.
func Marshal(v any, opts ...Option) ([]byte, error) {
	js, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}
	return JSONToYAML(js, opts...)
}

// MarshalOrdered is Marshal with the named keys moved to the front of every mapping, in the
// order given; the remaining keys keep their order. It is for hand-built values, whose map
// keys would otherwise come out alphabetical.
func MarshalOrdered(v any, first []string, opts ...Option) ([]byte, error) {
	js, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}
	n, err := jsonToNode(js)
	if err != nil {
		return nil, err
	}
	hoistKeys(n, first)
	return encodeNode(n, newOptions(opts))
}

// JSONToYAML converts JSON to block-style YAML, keeping the order of the object keys. Numbers
// keep their digits.
func JSONToYAML(j []byte, opts ...Option) ([]byte, error) {
	n, err := jsonToNode(j)
	if err != nil {
		return nil, err
	}
	return encodeNode(n, newOptions(opts))
}

// JSONToNode converts JSON to a YAML node tree, keeping key order. Editors use it to insert
// values into a tree that keeps its comments.
func JSONToNode(j []byte) (*yaml.Node, error) { return jsonToNode(j) }

func encodeNode(n *yaml.Node, o *options) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(o.indent)
	if err := enc.Encode(n); err != nil {
		return nil, fmt.Errorf("encoding YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding YAML: %w", err)
	}
	return buf.Bytes(), nil
}

func jsonToNode(j []byte) (*yaml.Node, error) {
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.UseNumber()
	n, err := readValue(dec)
	if err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("parsing JSON: unexpected data after the first value")
	}
	return n, nil
}

func readValue(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				m.Content = append(m.Content, stringNode(key), v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return m, nil
		case '[':
			s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for dec.More() {
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				s.Content = append(s.Content, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, fmt.Errorf("unexpected %q", t)
	case string:
		return stringNode(t), nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(t)}, nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") || !fitsInt64(t.String()) {
			// An integer that does not fit 64 bits reads back as a float; tagging it so keeps
			// the encoder from quoting it.
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: t.String()}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

func fitsInt64(s string) bool {
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// stringNode is a string scalar; the encoder quotes it when plain style would read back as
// another type, and picks a block style for multi-line text.
func stringNode(s string) *yaml.Node {
	n := &yaml.Node{}
	if err := n.Encode(s); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	}
	return n
}

func hoistKeys(n *yaml.Node, first []string) {
	switch n.Kind {
	case yaml.MappingNode:
		var head, rest []*yaml.Node
		for _, want := range first {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == want {
					head = append(head, n.Content[i], n.Content[i+1])
					break
				}
			}
		}
		taken := map[*yaml.Node]bool{}
		for i := 0; i < len(head); i += 2 {
			taken[head[i]] = true
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !taken[n.Content[i]] {
				rest = append(rest, n.Content[i], n.Content[i+1])
			}
		}
		n.Content = append(head, rest...)
		for i := 1; i < len(n.Content); i += 2 {
			hoistKeys(n.Content[i], first)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			hoistKeys(c, first)
		}
	}
}
