package yamlio

import (
	"fmt"
	"strconv"
	"strings"
)

// SyntaxError is a document that is not valid YAML, or YAML that cannot be represented as
// JSON (non-string keys, duplicate keys, !!binary, runaway alias expansion, size limits).
type SyntaxError struct {
	// Source is the display name of the input (a file name); it may be empty.
	Source string
	// Line and Column are 1-based; Line is 0 when unknown.
	Line, Column int
	// Msg is the problem without position.
	Msg string
}

// Error formats the position, when known, followed by the message.
func (e *SyntaxError) Error() string {
	return position(e.Source, e.Line, e.Column) + e.Msg
}

// FieldError is a problem with one value of a document: an unknown field in strict mode or a
// value of the wrong type.
type FieldError struct {
	// Source is the display name of the input; it may be empty.
	Source string
	// Pointer is the JSON pointer of the value ("/spec/nodes/0/ipAddress").
	Pointer string
	// Line and Column are the YAML position of the value's key (or item); 0 when unknown.
	Line, Column int
	// Msg is the problem, such as "unknown field".
	Msg string
	// Suggest is the closest valid field name, when there is one.
	Suggest string
}

// Error reads "capella.yaml:12:5: spec.nodes[0].ipadress: unknown field (did you mean ipAddress?)".
func (e *FieldError) Error() string {
	var b strings.Builder
	b.WriteString(position(e.Source, e.Line, e.Column))
	if p := DottedPath(e.Pointer); p != "" {
		b.WriteString(p)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	if e.Suggest != "" {
		fmt.Fprintf(&b, " (did you mean %s?)", e.Suggest)
	}
	return b.String()
}

func position(source string, line, col int) string {
	switch {
	case line <= 0 && source == "":
		return ""
	case line <= 0:
		return source + ": "
	}
	var b strings.Builder
	if source != "" {
		b.WriteString(source)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(line))
		if col > 0 {
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(col))
		}
	} else {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(line))
		if col > 0 {
			b.WriteString(", column ")
			b.WriteString(strconv.Itoa(col))
		}
	}
	b.WriteString(": ")
	return b.String()
}

// DottedPath turns a JSON pointer into a readable path: "/spec/nodes/0/ip" becomes
// "spec.nodes[0].ip". The root pointer yields "".
func DottedPath(pointer string) string {
	if pointer == "" {
		return ""
	}
	var b strings.Builder
	for _, seg := range strings.Split(pointer[1:], "/") {
		seg = unescapePointer(seg)
		if _, err := strconv.Atoi(seg); err == nil && b.Len() > 0 {
			b.WriteString("[" + seg + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}

func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescapePointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}
