package cli

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mminichino/cbctl/internal/guide"
)

// withTerminal makes progress output believe stderr is (or is not) a terminal.
func withTerminal(t *testing.T, tty bool) {
	t.Helper()
	old := terminalCheck
	terminalCheck = func(any) bool { return tty }
	t.Cleanup(func() { terminalCheck = old })
}

// TestConsoleSpinner_CAP007_AC9_NoSpinnerInCIOrDumbTerminals: CI set or TERM=dumb suppresses
// progress decoration even on a terminal.
func TestConsoleSpinner_CAP007_AC9_NoSpinnerInCIOrDumbTerminals(t *testing.T) {
	withTerminal(t, true)
	for _, tc := range []struct {
		name, ci, term, want string
	}{
		{"interactive", "", "xterm-256color", "."},
		{"CI", "true", "xterm-256color", ""},
		{"dumb terminal", "", "dumb", ""},
		{"CI and dumb", "1", "dumb", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			t.Setenv("TERM", tc.term)
			var out, errW bytes.Buffer
			newRenderer(&out, &errW, false, false).Progress(".")
			if errW.String() != tc.want || out.Len() != 0 {
				t.Fatalf("stdout %q stderr %q, want stderr %q", out.String(), errW.String(), tc.want)
			}
		})
	}
}

// TestConsoleSpinner_CAP007_AC9_NoSpinnerWhenStderrIsNotATerminal: off a terminal there is no
// progress decoration at all, so piped stderr gets only whole log lines.
func TestConsoleSpinner_CAP007_AC9_NoSpinnerWhenStderrIsNotATerminal(t *testing.T) {
	withTerminal(t, false)
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var out, errW bytes.Buffer
	r := newRenderer(&out, &errW, false, false)
	for range 5 {
		r.Progress(".")
	}
	if errW.Len() != 0 || out.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", out.String(), errW.String())
	}
}

// TestMessages_CAP063_AC2_TransportAndUnexpected: an error no typed case explains is printed with
// its message and the generic next step, exit 1; a panic gets the same action.
func TestMessages_CAP063_AC2_TransportAndUnexpected(t *testing.T) {
	var buf bytes.Buffer
	prev := errWriter
	SetErrorWriter(&buf)
	t.Cleanup(func() { SetErrorWriter(prev) })
	if code := HandleError(errors.New("something odd happened")); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	want := "[error] - something odd happened\n  → " + unexpectedErrorAction + "\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

// sourceFiles parses the non-test Go files of this package.
func sourceFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[n] = f
	}
	return fset, files
}

// emptyString reports whether e is the string literal "".
func emptyString(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(lit.Value)
	return err == nil && strings.TrimSpace(s) == ""
}

// TestMessages_CAP063_AC1_EveryCLIExitHasAction: every error the CLI builds carries a next step:
// no Errorf/usageError/blockedError call with an empty action and no CLIError literal with a
// Message but no Action. A literal with neither is a bare exit code (plan --detailed-exitcode,
// a blocked plan, doctor findings) whose output was already printed.
func TestMessages_CAP063_AC1_EveryCLIExitHasAction(t *testing.T) {
	actionArg := map[string]int{"Errorf": 1, "usageError": 0, "blockedError": 0}
	fset, files := sourceFiles(t)
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				id, ok := x.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if i, ok := actionArg[id.Name]; ok && len(x.Args) > i && emptyString(x.Args[i]) {
					t.Errorf("%s: %s with an empty action", fset.Position(x.Pos()), id.Name)
				}
			case *ast.CompositeLit:
				id, ok := x.Type.(*ast.Ident)
				if !ok || id.Name != "CLIError" || len(x.Elts) == 0 {
					return true
				}
				has, message := false, false
				for _, el := range x.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						k, _ := kv.Key.(*ast.Ident)
						switch {
						case k == nil:
						case k.Name == "Action" && !emptyString(kv.Value):
							has = true
						case k.Name == "Message" && !emptyString(kv.Value):
							message = true
						}
					}
				}
				if message && !has {
					t.Errorf("%s: CLIError without an Action", fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
}

var storyID = regexp.MustCompile(`\bCAP-\d{3}\b|\bP-[A-Z]+-\d+\b`)

// TestMessages_CAP063_AC7_NoStoryIDsShown: no help text, guide topic or user-facing string
// literal names a backlog story id (CAP-NNN) or plan item.
func TestMessages_CAP063_AC7_NoStoryIDsShown(t *testing.T) {
	root := NewRootCmd(Deps{})
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpCmd()
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		text := c.Use + c.Short + c.Long + c.Example
		c.Flags().VisitAll(func(f *pflag.Flag) { text += f.Usage })
		if m := storyID.FindString(text); m != "" {
			t.Errorf("%s help shows %s", c.CommandPath(), m)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, name := range guide.Names() {
		body, err := guide.Render(name)
		if err != nil {
			t.Fatal(err)
		}
		if m := storyID.FindString(body); m != "" {
			t.Errorf("guide %s shows %s", name, m)
		}
	}
	if m := storyID.FindString(guide.Fields()); m != "" {
		t.Errorf("guide config shows %s", m)
	}
	fset, files := sourceFiles(t)
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if m := storyID.FindString(lit.Value); m != "" {
					t.Errorf("%s: string literal shows %s", fset.Position(lit.Pos()), m)
				}
			}
			return true
		})
	}
	if _, err := os.Stat("errors.go"); err != nil {
		t.Fatalf("source scan did not run in the package directory: %v", err)
	}
}
