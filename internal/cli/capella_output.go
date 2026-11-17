package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Renderer writes command results. The text implementation prints aligned tables and key: value
// lines; the JSON implementation prints exactly one JSON document on stdout. Both pass every
// string through the redactor, with the one-time credential password (printSecretOnce) as the
// only exception.
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
}

// terminalCheck reports whether v (an *os.File) is a terminal. Tests replace it.
var terminalCheck = func(v any) bool {
	if u, ok := v.(interface{ Underlying() io.Reader }); ok {
		v = u.Underlying()
	}
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// newRenderer returns the JSON or text Renderer.
func newRenderer(out, errOut io.Writer, jsonMode, quiet bool) Renderer {
	base := baseRenderer{out: out, err: errOut, quiet: quiet}
	if jsonMode {
		return &jsonRenderer{base}
	}
	return &textRenderer{base}
}

type baseRenderer struct {
	out, err io.Writer
	quiet    bool
}

func (b baseRenderer) Progress(dot string) {
	if b.quiet || DefaultDeps().Env("CI") != "" || !terminalCheck(b.err) {
		return
	}
	fmt.Fprint(b.err, dot)
}

type textRenderer struct{ baseRenderer }

func (r *textRenderer) Table(cols []string, rows [][]string) {
	w := tabwriter.NewWriter(r.out, 0, 0, 2, ' ', 0)
	writeRow := func(cells []string) {
		clean := make([]string, len(cells))
		for i, c := range cells {
			clean[i] = cell(c)
		}
		fmt.Fprintln(w, strings.Join(clean, "\t"))
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
		fmt.Fprintf(r.out, "%s\n", redact.String(fmt.Sprint(v)))
		return
	}
	for _, p := range pairs {
		if p.key == "" {
			fmt.Fprintln(r.out, p.val)
			continue
		}
		fmt.Fprintf(r.out, "%s: %s\n", p.key, p.val)
	}
}

func (r *textRenderer) List(any) {}

func (r *textRenderer) Line(format string, a ...any) {
	fmt.Fprintln(r.out, redact.String(fmt.Sprintf(format, a...)))
}

type jsonRenderer struct{ baseRenderer }

func (r *jsonRenderer) Table([]string, [][]string) {}

func (r *jsonRenderer) Object(v any) { r.emit(v, false) }

func (r *jsonRenderer) List(v any) { r.emit(v, true) }

func (r *jsonRenderer) Line(string, ...any) {}

// emit writes v as one indented, redacted JSON document. A nil slice becomes [] when list is set.
func (r *jsonRenderer) emit(v any, list bool) {
	generic, err := toGeneric(v)
	if err != nil {
		logging.Error("rendering output: %v", err)
		return
	}
	if list && generic == nil {
		generic = []any{}
	}
	r.write(redact.Object(generic))
}

func (r *jsonRenderer) write(v any) {
	enc := json.NewEncoder(r.out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		logging.Error("rendering output: %v", err)
	}
}

// secret prints doc and then the secret under name, unredacted.
func (r *jsonRenderer) secret(doc any, name, value string) {
	generic, err := toGeneric(doc)
	if err != nil {
		logging.Error("rendering output: %v", err)
		return
	}
	obj, _ := redact.Object(generic).(map[string]any)
	if obj == nil {
		obj = map[string]any{}
	}
	obj[name] = value
	r.write(obj)
}

func (r *textRenderer) secret(doc any, name, value string) {
	if doc != nil {
		r.Object(doc)
	}
	fmt.Fprintf(r.out, "%s: %s\n", name, value)
}

// secretPrinter is implemented by both renderers.
type secretPrinter interface {
	secret(doc any, name, value string)
}

// printSecretOnce shows a one-time secret such as a generated credential password. It warns on
// stderr that the value will not be shown again, then prints doc (the credential's visible
// fields, may be nil) with "<name>: <value>" appended (text) or as the name field of the JSON
// document. This is the only output path that skips the redactor, because the value was
// registered with it when Capella returned it.
func printSecretOnce(rt *runtime, doc any, name, value string) {
	logging.Warning("this %s is shown only once", name)
	if sp, ok := rt.Out.(secretPrinter); ok {
		sp.secret(doc, name, value)
	}
}

// namedSecret is one one-time secret with the name of what it belongs to.
type namedSecret struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// secretsPrinter is implemented by both renderers.
type secretsPrinter interface {
	secrets(doc any, field string, items []namedSecret)
}

// printSecretsOnce is printSecretOnce for several secrets (for example the credentials a clone
// creates): JSON gets them as the field array of the document, text as "name: password" lines.
func printSecretsOnce(rt *runtime, doc any, field string, items []namedSecret) {
	if len(items) == 0 {
		return
	}
	logging.Warning("these passwords are shown only once")
	if sp, ok := rt.Out.(secretsPrinter); ok {
		sp.secrets(doc, field, items)
	}
}

func (r *jsonRenderer) secrets(doc any, field string, items []namedSecret) {
	generic, err := toGeneric(doc)
	if err != nil {
		logging.Error("rendering output: %v", err)
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

func (r *textRenderer) secrets(doc any, _ string, items []namedSecret) {
	if doc != nil {
		r.Object(doc)
	}
	for _, s := range items {
		fmt.Fprintf(r.out, "credential %s password: %s\n", s.Name, s.Password)
	}
}

// toGeneric converts v to plain JSON values (maps, slices, strings, json.Number, bool, nil).
func toGeneric(v any) (any, error) {
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

// ttyCheck reports whether r is an interactive terminal. Tests replace it.
var ttyCheck = terminalCheck

// prompterFor returns the Prompter injected through Deps, or nil to read the terminal directly.
// It is consulted only after the terminal check, so "no terminal, no --yes" stays ExitBlocked.
func prompterFor(rt *runtime) Prompter {
	if rt == nil || rt.cmd == nil {
		return nil
	}
	return depsFromCmd(rt.cmd).Prompter
}

// noApprovalError is the one refusal for a confirmation with no terminal and no --yes, so
// scripts and CI logs see the same text from every command. Under CI the action names the
// pipeline step.
func noApprovalError(cmd *cobra.Command) *CLIError {
	action := "re-run with --yes"
	if cmd != nil && depsFromCmd(cmd).Env("CI") != "" {
		action = "add --yes to this pipeline step"
	}
	return Errorf(ExitBlocked, action, "confirmation required but no terminal is attached")
}

// confirm asks question on stderr and reads the answer from stdin. --yes answers true without
// asking. With no terminal attached and no --yes it fails with ExitBlocked rather than guessing.
func confirm(rt *runtime, question string) (bool, error) {
	if rt.Yes {
		return true, nil
	}
	in, errOut := stdinFrom(rt.cmd), stderrFrom(rt.cmd)
	if !ttyCheck(in) {
		return false, noApprovalError(rt.cmd)
	}
	if p := prompterFor(rt); p != nil {
		return p.Confirm(question, false)
	}
	fmt.Fprintf(errOut, "%s [y/N]: ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
