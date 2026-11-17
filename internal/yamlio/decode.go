package yamlio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Unmarshal converts YAML to JSON and decodes it with encoding/json into v, so json tags apply.
// Unknown fields are ignored. An empty document leaves v untouched. A document of several
// '---' parts is an error; use a Decoder for streams.
func Unmarshal(y []byte, v any, opts ...Option) error {
	return unmarshal(y, v, false, newOptions(opts))
}

// UnmarshalStrict is Unmarshal with unknown fields rejected. Errors are *SyntaxError for bad
// YAML (duplicate keys included) and *FieldError for fields and values, carrying the YAML line
// and column. Several field errors are returned joined (errors.Join), sorted by position.
func UnmarshalStrict(y []byte, v any, opts ...Option) error {
	return unmarshal(y, v, true, newOptions(opts))
}

func unmarshal(y []byte, v any, strict bool, o *options) error {
	n, err := parseOne(y, o)
	if err != nil {
		return err
	}
	if isEmpty(n) {
		return nil
	}
	js, err := nodeToJSON(n, o)
	if err != nil {
		return err
	}
	return decodeInto(js, BuildLineIndex(n), v, o, strict)
}

// UnknownFields reports the fields of the YAML document that v has no json tag for, without
// failing the decode. It is for files that a newer program version may have extended: the
// caller decodes leniently and tells the user about the extras.
func UnknownFields(y []byte, v any, opts ...Option) ([]*FieldError, error) {
	o := newOptions(opts)
	n, err := parseOne(y, o)
	if err != nil {
		return nil, err
	}
	if isEmpty(n) {
		return nil, nil
	}
	js, err := nodeToJSON(n, o)
	if err != nil {
		return nil, err
	}
	errs, err := checkShape(js, BuildLineIndex(n), v, o, true)
	return errs, err
}

func decodeInto(js []byte, lines LineIndex, v any, o *options, strict bool) error {
	if strict {
		errs, err := checkShape(js, lines, v, o, false)
		if err != nil {
			return err
		}
		if len(errs) > 0 {
			return joinFieldErrors(errs)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(js))
	if o.useNumber {
		dec.UseNumber()
	}
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		return jsonError(err, lines, o)
	}
	return nil
}

func jsonError(err error, lines LineIndex, o *options) error {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		ptr := ""
		if te.Field != "" {
			ptr = "/" + strings.ReplaceAll(te.Field, ".", "/")
		}
		pos := lines.Of(ptr)
		return &FieldError{Source: o.source, Pointer: ptr, Line: pos.Line, Column: pos.Column,
			Msg: fmt.Sprintf("cannot use a %s as %s", te.Value, te.Type)}
	}
	return fmt.Errorf("decoding %s: %w", sourceName(o), err)
}

func sourceName(o *options) string {
	if o.source != "" {
		return o.source
	}
	return "YAML"
}

func joinFieldErrors(errs []*FieldError) error {
	sort.SliceStable(errs, func(a, b int) bool {
		if errs[a].Line != errs[b].Line {
			return errs[a].Line < errs[b].Line
		}
		return errs[a].Column < errs[b].Column
	})
	if len(errs) == 1 {
		return errs[0]
	}
	list := make([]error, len(errs))
	for i, e := range errs {
		list[i] = e
	}
	return errors.Join(list...)
}

var yamlLineRe = regexp.MustCompile(`^(?:yaml: )?(?:line (\d+): )?`)

// syntaxError converts an error of the yaml parser into a *SyntaxError.
func syntaxError(err error, o *options) error {
	msg := err.Error()
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		msg = te.Errors[0]
	}
	e := &SyntaxError{Source: o.source}
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		if m[1] != "" {
			e.Line, _ = strconv.Atoi(m[1])
		}
		msg = msg[len(m[0]):]
	}
	e.Msg = msg
	return e
}

// isEmpty reports whether n is no document, or a document without content ('---' only).
func isEmpty(n *yaml.Node) bool {
	if n == nil || n.Kind == 0 {
		return true
	}
	if n.Kind == yaml.DocumentNode {
		return len(n.Content) == 0 || isEmpty(n.Content[0])
	}
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" && n.Value == "" && n.Style == 0
}

func checkInput(y []byte, o *options) ([]byte, error) {
	if len(y) > MaxInputBytes {
		return nil, &SyntaxError{Source: o.source, Msg: fmt.Sprintf("input is larger than %d MiB", MaxInputBytes>>20)}
	}
	return bytes.TrimPrefix(y, []byte("\ufeff")), nil
}

// parseOne parses y, which must hold at most one non-empty document, and returns its document
// node (nil when there is none).
func parseOne(y []byte, o *options) (*yaml.Node, error) {
	y, err := checkInput(y, o)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(y))
	var first *yaml.Node
	for i := 0; ; i++ {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return first, nil
			}
			return nil, syntaxError(err, o)
		}
		if isEmpty(&doc) {
			continue
		}
		if first != nil {
			return nil, &SyntaxError{Source: o.source, Line: doc.Line, Column: doc.Column,
				Msg: "the input holds several YAML documents; expected one"}
		}
		first = &doc
	}
}

// Document is one parsed YAML document of a stream.
type Document struct {
	// JSON is the document as JSON, mapping keys in document order.
	JSON []byte
	// Lines maps JSON pointers to YAML positions.
	Lines LineIndex
	// Node is the parsed document node, set with WithKeepNode (editors).
	Node *yaml.Node
	// Index is the 0-based position of the document in the stream; empty documents count.
	Index int

	o *options
}

// Decode decodes the document into v, like Unmarshal (strict: UnmarshalStrict).
func (d *Document) Decode(v any, strict bool) error {
	return decodeInto(d.JSON, d.Lines, v, d.o, strict)
}

// Decoder reads a stream of '---'-separated documents.
type Decoder struct {
	r    io.Reader
	o    *options
	dec  *yaml.Decoder
	read int
	err  error
}

// NewDecoder returns a Decoder over r. The whole stream may not exceed MaxInputBytes.
func NewDecoder(r io.Reader, opts ...Option) *Decoder {
	return &Decoder{r: r, o: newOptions(opts)}
}

// Next returns the next non-empty document, or io.EOF at the end. A stream with more than
// the allowed number of documents is a *SyntaxError.
func (d *Decoder) Next() (*Document, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.dec == nil {
		raw, err := io.ReadAll(io.LimitReader(d.r, MaxInputBytes+1))
		if err != nil {
			d.err = fmt.Errorf("reading YAML: %w", err)
			return nil, d.err
		}
		raw, err = checkInput(raw, d.o)
		if err != nil {
			d.err = err
			return nil, err
		}
		d.dec = yaml.NewDecoder(bytes.NewReader(raw))
	}
	for {
		var doc yaml.Node
		if err := d.dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				d.err = io.EOF
			} else {
				d.err = syntaxError(err, d.o)
			}
			return nil, d.err
		}
		index := d.read
		if d.read++; d.read > d.o.maxDocs {
			d.err = &SyntaxError{Source: d.o.source, Line: doc.Line,
				Msg: fmt.Sprintf("the stream holds more than %d documents", d.o.maxDocs)}
			return nil, d.err
		}
		if isEmpty(&doc) {
			continue
		}
		js, err := nodeToJSON(&doc, d.o)
		if err != nil {
			return nil, err // the stream can go on with the next document
		}
		out := &Document{JSON: js, Lines: BuildLineIndex(&doc), Index: index, o: d.o}
		if d.o.keepNode {
			out.Node = &doc
		}
		return out, nil
	}
}
