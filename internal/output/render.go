package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mminichino/cbctl/internal/redact"
)

// Renderer writes command results. The text implementation prints aligned tables and key: value
// lines; the JSON implementation prints exactly one JSON document on stdout. Both pass every
// string through the redactor, with the one-time secrets (Secret, Secrets) as the only
// exception. Renderer output is data: it is never colored.
type Renderer interface {
	// Table prints a header row and aligned rows with a 2-space gutter. It does nothing in JSON mode.
	Table(cols []string, rows [][]string)
	// Object prints one object: key: value lines in struct order (text) or indented JSON.
	Object(v any)
	// List prints v as a JSON array. It does nothing in text mode, where callers use Table.
	List(v any)
	// Line prints a formatted line to stdout. It does nothing in JSON mode.
	Line(format string, a ...any)
	// Progress writes dot to stderr, but only on a terminal, when not quiet and when CI is unset.
	Progress(dot string)
	// Secret prints doc (the visible fields, may be nil) with "<name>: <value>" appended (text) or
	// as the name field of the JSON document. The value skips the redactor: it is a one-time
	// secret such as a generated password, registered with the redactor when it was issued.
	Secret(doc any, name, value string)
	// Secrets is Secret for several values: text prints "credential <name> password: <value>"
	// lines, JSON adds them as the field array of the document.
	Secrets(doc any, field string, items []NamedSecret)
}

// NamedSecret is one one-time secret with the name of what it belongs to.
type NamedSecret struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// RenderOptions are the injected inputs of a Renderer.
type RenderOptions struct {
	// Env reads environment variables (CI, TERM) for the progress dots. Nil means none are set.
	Env func(string) string
	// IsTerm reports whether the progress stream is a terminal. Nil means never.
	IsTerm func(w any) bool
}

// NewRenderer returns the JSON Renderer when jsonMode is set, otherwise the text Renderer.
func NewRenderer(out, errOut io.Writer, jsonMode, quiet bool, o RenderOptions) Renderer {
	base := baseRenderer{out: out, err: errOut, quiet: quiet, opts: o}
	if jsonMode {
		return &jsonRenderer{base}
	}
	return &textRenderer{base}
}

type baseRenderer struct {
	out, err io.Writer
	quiet    bool
	opts     RenderOptions
}

func (b baseRenderer) Progress(dot string) {
	if b.quiet || !ProgressAllowed(b.opts.Env) || b.opts.IsTerm == nil || !b.opts.IsTerm(b.err) {
		return
	}
	_, _ = fmt.Fprint(b.err, dot)
}

// fail reports a rendering failure on stderr as an error line.
func (b baseRenderer) fail(err error) {
	WriteLine(Decider{}, b.err, KindError, fmt.Sprintf("rendering output: %v", err))
}

type textRenderer struct{ baseRenderer }

func (r *textRenderer) Table(cols []string, rows [][]string) {
	w := tabwriter.NewWriter(r.out, 0, 0, 2, ' ', 0)
	writeRow := func(cells []string) {
		clean := make([]string, len(cells))
		for i, c := range cells {
			clean[i] = cell(c)
		}
		_, _ = fmt.Fprintln(w, strings.Join(clean, "\t"))
	}
	writeRow(cols)
	for _, row := range rows {
		writeRow(row)
	}
	_ = w.Flush()
}

// cell redacts s and keeps it on one line so tabwriter columns stay aligned.
func cell(s string) string {
	s = redact.String(s)
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

func (r *textRenderer) Object(v any) {
	pairs, err := flatten(v)
	if err != nil {
		_, _ = fmt.Fprintf(r.out, "%s\n", redact.String(fmt.Sprint(v)))
		return
	}
	for _, p := range pairs {
		if p.key == "" {
			_, _ = fmt.Fprintln(r.out, p.val)
			continue
		}
		_, _ = fmt.Fprintf(r.out, "%s: %s\n", p.key, p.val)
	}
}

func (r *textRenderer) List(any) {}

func (r *textRenderer) Line(format string, a ...any) {
	_, _ = fmt.Fprintln(r.out, redact.String(fmt.Sprintf(format, a...)))
}

func (r *textRenderer) Secret(doc any, name, value string) {
	if doc != nil {
		r.Object(doc)
	}
	_, _ = fmt.Fprintf(r.out, "%s: %s\n", name, value)
}

func (r *textRenderer) Secrets(doc any, _ string, items []NamedSecret) {
	if doc != nil {
		r.Object(doc)
	}
	for _, s := range items {
		_, _ = fmt.Fprintf(r.out, "credential %s password: %s\n", s.Name, s.Password)
	}
}

type jsonRenderer struct{ baseRenderer }

func (r *jsonRenderer) Table([]string, [][]string) {}

func (r *jsonRenderer) Object(v any) { r.emit(v, false) }

func (r *jsonRenderer) List(v any) { r.emit(v, true) }

func (r *jsonRenderer) Line(string, ...any) {}

// emit writes v as one indented, redacted JSON document. A nil slice becomes [] when list is set.
func (r *jsonRenderer) emit(v any, list bool) {
	generic, err := ToGeneric(v)
	if err != nil {
		r.fail(err)
		return
	}
	if list && generic == nil {
		generic = []any{}
	}
	r.write(redact.Object(generic))
}

func (r *jsonRenderer) write(v any) {
	if err := WriteJSON(r.out, v); err != nil {
		r.fail(err)
	}
}

func (r *jsonRenderer) Secret(doc any, name, value string) {
	generic, err := ToGeneric(doc)
	if err != nil {
		r.fail(err)
		return
	}
	obj, _ := redact.Object(generic).(map[string]any)
	if obj == nil {
		obj = map[string]any{}
	}
	obj[name] = value
	r.write(obj)
}

func (r *jsonRenderer) Secrets(doc any, field string, items []NamedSecret) {
	generic, err := ToGeneric(doc)
	if err != nil {
		r.fail(err)
		return
	}
	obj, _ := redact.Object(generic).(map[string]any)
	if obj == nil {
		obj = map[string]any{}
	}
	list := make([]any, len(items))
	for i, s := range items {
		list[i] = map[string]any{"name": s.Name, "password": s.Password}
	}
	obj[field] = list
	r.write(obj)
}

// WriteJSON writes v to w as one document: two-space indent, no HTML escaping, trailing
// newline. The document never contains escape codes. v is written as given; the Renderer
// redacts before it calls this.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// ToGeneric converts v to plain JSON values (maps, slices, strings, json.Number, bool, nil), so
// it can be redacted by key and value. A nil v is nil.
func ToGeneric(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

type pair struct{ key, val string }

// flatten renders v as ordered leaf pairs: nested objects become dotted keys (a.b) and array
// elements indexed keys (a[0]). Order follows the JSON encoding, so struct field order.
func flatten(v any) ([]pair, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out []pair
	if err := walk(dec, "", "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walk(dec *json.Decoder, path, name string, out *[]pair) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	leaf := func(s string) {
		// Reuse the redactor's key rules (password, token, ...) for the leaf's own key.
		masked := redact.Object(map[string]any{name: s}).(map[string]any)[name].(string)
		*out = append(*out, pair{key: path, val: masked})
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n := 0
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				key := kt.(string)
				child := key
				if path != "" {
					child = path + "." + key
				}
				if err := walk(dec, child, key, out); err != nil {
					return err
				}
				n++
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			if n == 0 && path != "" {
				leaf("{}")
			}
		case '[':
			n := 0
			for dec.More() {
				if err := walk(dec, fmt.Sprintf("%s[%d]", path, n), name, out); err != nil {
					return err
				}
				n++
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			if n == 0 {
				leaf("[]")
			}
		}
	case string:
		leaf(t)
	case json.Number:
		leaf(t.String())
	case bool:
		leaf(strconv.FormatBool(t))
	case nil:
		leaf("null")
	}
	return nil
}
