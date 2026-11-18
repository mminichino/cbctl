package manifest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/yamlio"
)

// Issue is one problem found in a manifest.
type Issue struct {
	// Doc is the 1-based document number in the file.
	Doc int `json:"document"`
	// Ref is "Kind/name" of the document, when it could be read.
	Ref string `json:"ref,omitempty"`
	// Pointer is the JSON pointer of the value ("/spec/nodes/2").
	Pointer string `json:"pointer,omitempty"`
	// Line and Col are 1-based positions in the file; 0 when unknown.
	Line int `json:"line,omitempty"`
	Col  int `json:"column,omitempty"`
	// Msg states the problem.
	Msg string `json:"message"`
	// Fix is a one-line next step; optional.
	Fix string `json:"fix,omitempty"`

	source string
}

// String renders "file:14:7 (document 1, CouchbaseServer/airline-ops): spec.nodes[2]: message".
func (i Issue) String() string {
	var b strings.Builder
	b.WriteString(i.source)
	if i.Line > 0 {
		fmt.Fprintf(&b, ":%d", i.Line)
		if i.Col > 0 {
			fmt.Fprintf(&b, ":%d", i.Col)
		}
	}
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	switch {
	case i.Doc == 0 && b.Len() > 0:
		b.WriteString(": ")
	case i.Doc > 0:
		b.WriteString("(document ")
		fmt.Fprint(&b, i.Doc)
		if i.Ref != "" {
			b.WriteString(", " + i.Ref)
		}
		b.WriteString("): ")
	}
	if p := yamlio.DottedPath(i.Pointer); p != "" {
		b.WriteString(p + ": ")
	}
	b.WriteString(i.Msg)
	if i.Fix != "" {
		b.WriteString("\n  action: " + i.Fix)
	}
	return b.String()
}

// ValidationError lists every problem of a manifest. The CLI maps it to exit code 3.
type ValidationError struct {
	// Source is the display name of the manifest ("couchbase.yaml", "<stdin>").
	Source string  `json:"source"`
	Issues []Issue `json:"issues"`
}

// Error renders a heading and one indented block per issue.
func (e *ValidationError) Error() string {
	word := "problems"
	if len(e.Issues) == 1 {
		word = "problem"
	}
	lines := []string{fmt.Sprintf("%s has %d %s:", e.Source, len(e.Issues), word)}
	for _, i := range e.Issues {
		lines = append(lines, "  "+strings.ReplaceAll(i.String(), "\n", "\n  "))
	}
	return strings.Join(lines, "\n")
}

// sorted orders issues by document, line, column, pointer and message.
func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(a, b int) bool {
		x, y := issues[a], issues[b]
		switch {
		case x.Doc != y.Doc:
			return x.Doc < y.Doc
		case x.Line != y.Line:
			return x.Line < y.Line
		case x.Col != y.Col:
			return x.Col < y.Col
		case x.Pointer != y.Pointer:
			return x.Pointer < y.Pointer
		}
		return x.Msg < y.Msg
	})
}

// collector gathers the issues of one document.
type collector struct {
	source string
	doc    int
	ref    string
	lines  yamlio.LineIndex
	issues []Issue
}

// add records a problem at pointer; the position is that of the nearest known ancestor.
func (c *collector) add(pointer, msg string, fix ...string) {
	pos := c.lines.Of(pointer)
	i := Issue{Doc: c.doc, Ref: c.ref, Pointer: pointer, Line: pos.Line, Col: pos.Column, Msg: msg, source: c.source}
	if len(fix) > 0 {
		i.Fix = fix[0]
	}
	c.issues = append(c.issues, i)
}

func (c *collector) addf(pointer, format string, a ...any) {
	c.add(pointer, fmt.Sprintf(format, a...))
}

// ptr joins pointer segments, escaping "~" and "/" like RFC 6901.
func ptr(base string, segs ...any) string {
	for _, s := range segs {
		t := fmt.Sprint(s)
		t = strings.ReplaceAll(t, "~", "~0")
		t = strings.ReplaceAll(t, "/", "~1")
		base += "/" + t
	}
	return base
}
