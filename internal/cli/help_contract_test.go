package cli

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/guide"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// helpContractDeps runs a help invocation hermetically: no terminal, no API key, a throwaway home.
func helpContractDeps(t *testing.T, out *bytes.Buffer) Deps {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{"CBCTL_HOME": dir, "CBCTL_CONFIG": "", "CAPELLA_API_KEY": "", "CAPELLA_ORG_ID": "", "CI": ""}
	return Deps{
		Env:            func(k string) string { return env[k] },
		LookupEnv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Stdin:          strings.NewReader(""),
		Stdout:         out,
		Stderr:         out,
		IsTTY:          func(io.Reader) bool { return false },
		Cwd:            func() (string, error) { return dir, nil },
		Home:           dir,
		ResetRedaction: true,
	}
}

// helpTree is a fully built command tree, including cobra's lazily added completion command.
func helpTree() *cobra.Command {
	root := NewRootCmd(Deps{})
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpCmd()
	return root
}

// commandPaths lists every command of the tree as its argument path (root excluded).
func commandPaths(root *cobra.Command) [][]string {
	var out [][]string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			p := append(append([]string(nil), path...), sub.Name())
			out = append(out, p)
			walk(sub, p)
		}
	}
	walk(root, nil)
	return out
}

// TestHelpContract_EveryCommandHelpRenders: "--help" on every command (hidden ones too) exits 0
// and prints a usage block, offline and without an API key.
func TestHelpContract_EveryCommandHelpRenders(t *testing.T) {
	root := helpTree()
	paths := append([][]string{{}}, commandPaths(root)...)
	if len(paths) < 100 {
		t.Fatalf("only %d commands found; the walk is broken", len(paths))
	}
	for _, p := range paths {
		var out bytes.Buffer
		args := append(append([]string(nil), p...), "--help")
		code := Run(context.Background(), args, helpContractDeps(t, &out))
		if code != 0 {
			t.Errorf("cbctl %s: exit %d\n%s", strings.Join(args, " "), code, out.String())
			continue
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Errorf("cbctl %s: no Usage block\n%s", strings.Join(args, " "), out.String())
		}
	}
}

// exampleCommands extracts the cbctl invocations of an Example block: one per line that starts
// with "cbctl" (after "$ " or indentation), with trailing comments and "\" continuations handled.
func exampleCommands(example string) []string {
	var out []string
	var pending string
	for _, raw := range strings.Split(example, "\n") {
		line := strings.TrimSpace(raw)
		if pending != "" {
			line = pending + " " + line
			pending = ""
		}
		line = strings.TrimPrefix(line, "$ ")
		if strings.HasSuffix(line, "\\") {
			pending = strings.TrimSuffix(line, "\\")
			continue
		}
		if !strings.HasPrefix(line, "cbctl ") && line != "cbctl" {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		// A pipeline or a command list: only the cbctl part is checked.
		for _, sep := range []string{" | ", " && ", " || ", "; "} {
			if i := strings.Index(line, sep); i >= 0 {
				line = line[:i]
			}
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

var quotedArg = regexp.MustCompile(`'[^']*'|"[^"]*"|\S+`)

// lookupFlag finds a long (--name) or short (-n) flag of c, inherited ones included.
func lookupFlag(c *cobra.Command, tok string) *pflag.Flag {
	inherited := c.InheritedFlags()
	if strings.HasPrefix(tok, "--") {
		name := strings.TrimPrefix(tok, "--")
		if f := c.Flags().Lookup(name); f != nil {
			return f
		}
		return inherited.Lookup(name)
	}
	short := strings.TrimPrefix(tok, "-")
	if len(short) != 1 {
		return nil
	}
	if f := c.Flags().ShorthandLookup(short); f != nil {
		return f
	}
	return inherited.ShorthandLookup(short)
}

// checkExample reports what is wrong with one example invocation: an unknown subcommand or a flag
// the command does not have. Positional placeholders (NAME, <cluster>, …) are not checked.
func checkExample(root *cobra.Command, line string) string {
	toks := quotedArg.FindAllString(line, -1)[1:] // drop "cbctl"
	cur := root
	positional := 0
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if tok == "--" {
			break
		}
		if strings.HasPrefix(tok, "-") && len(tok) > 1 && !isNumber(tok) {
			name := tok
			if j := strings.Index(tok, "="); j >= 0 {
				name = tok[:j]
			}
			f := lookupFlag(cur, name)
			if f == nil {
				return "command '" + cur.CommandPath() + "' has no flag " + name
			}
			if !strings.Contains(tok, "=") && f.NoOptDefVal == "" {
				i++ // the flag's value
			}
			continue
		}
		if positional == 0 {
			if sub := findSub(cur, tok); sub != nil {
				cur = sub
				continue
			}
			if cur.HasSubCommands() && len(strings.Fields(cur.Use)) == 1 {
				return "command '" + cur.CommandPath() + "' has no subcommand " + tok
			}
		}
		positional++
	}
	if cur == root {
		return "names no command"
	}
	return ""
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// findSub is c's subcommand named (or aliased) name, without prefix matching.
func findSub(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name || sub.HasAlias(name) {
			return sub
		}
	}
	return nil
}

// TestHelpContract_ExamplesNameRealCommandsAndFlags: every Example line of every command is a
// real command path with flags that command accepts, so copying an example never fails on usage.
func TestHelpContract_ExamplesNameRealCommandsAndFlags(t *testing.T) {
	root := helpTree()
	checked := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, line := range exampleCommands(c.Example) {
			checked++
			if problem := checkExample(root, line); problem != "" {
				t.Errorf("%s: example %q: %s", c.CommandPath(), line, problem)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	if checked < 60 {
		t.Fatalf("only %d example lines checked; the extraction is broken", checked)
	}
}

// TestHelpContract_ExampleCheckerCatchesMistakes keeps the checker honest.
func TestHelpContract_ExampleCheckerCatchesMistakes(t *testing.T) {
	root := helpTree()
	tests := []struct {
		line string
		want string
	}{
		{"cbctl capella cluster get nova-dev-01", ""},
		{"cbctl capella -o json cluster get nova-dev-01", ""},
		{"cbctl capella cluster scale nova --step -1", ""},
		{"cbctl capella cluster power nova --off -y", ""},
		{"cbctl capella clustr get nova", "has no subcommand clustr"},
		{"cbctl capella cluster get nova --bogus", "has no flag --bogus"},
		{"cbctl capella cluster scale nova -Z", "has no flag -Z"},
	}
	for _, tc := range tests {
		got := checkExample(root, tc.line)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("checkExample(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

var guideRef = regexp.MustCompile(`(?:cbctl capella (?:guide|help)|capella guide) ([a-z][a-z-]*)`)

// TestHelpContract_ReferencedGuideTopicsExist: every "cbctl capella guide TOPIC" named in any help
// text (short, long, example or rendered usage) is a topic the guide has.
func TestHelpContract_ReferencedGuideTopicsExist(t *testing.T) {
	root := helpTree()
	found := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		_ = c.Help()
		text := strings.Join([]string{c.Short, c.Long, c.Example, buf.String()}, "\n")
		for _, m := range guideRef.FindAllStringSubmatch(text, -1) {
			found++
			if name := m[1]; name != "topics" && !guide.Exists(name) {
				t.Errorf("%s: help refers to guide topic %q, which does not exist", c.CommandPath(), name)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	if found == 0 {
		t.Fatal("no guide references found; the pattern is stale")
	}
}
