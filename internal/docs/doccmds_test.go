package docs_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mminichino/cbctl/internal/cli"
)

// docCmd is one cbctl command line found in a document.
type docCmd struct {
	file  string
	line  int
	text  string
	fence bool // in a code fence: also checked for argument counts
}

var (
	reInlineCbctl = regexp.MustCompile("`(cbctl [^`]+)`")
	reSubshell    = regexp.MustCompile(`\$\([^)]*\)`)
)

// docFiles are the documents whose cbctl commands must parse.
func docFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{filepath.Join(root, "README.md"), filepath.Join(root, ".cursor", "rules", "cbctl.mdc")}
	for _, pattern := range []string{"docs/capella/*.md", "internal/guide/topics/*.md"} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	return files
}

// extractCommands returns the cbctl lines of a Markdown file: lines in code fences (joined across
// trailing backslashes), bare "cbctl ..." lines (the rules' command map) and inline code spans.
// "<!-- doccmds:skip -->" skips the next fence or line.
func extractCommands(t *testing.T, path string) []docCmd {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var out []docCmd
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	inFence, skip, skipFence := false, false, false
	pending, pendingLine := "", 0
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<!-- doccmds:skip") {
			skip = true
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				skipFence, skip = skip, false
			}
			inFence = !inFence
			continue
		}
		if inFence {
			if skipFence {
				continue
			}
			if pending != "" {
				pending += " " + trimmed
			} else {
				cmd := strings.TrimPrefix(trimmed, "$ ")
				if !strings.HasPrefix(cmd, "cbctl ") {
					continue
				}
				pending, pendingLine = cmd, n
			}
			if strings.HasSuffix(pending, "\\") {
				pending = strings.TrimSuffix(pending, "\\")
				continue
			}
			out = append(out, docCmd{path, pendingLine, pending, true})
			pending = ""
			continue
		}
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(trimmed, "cbctl ") {
			out = append(out, docCmd{path, n, trimmed, false})
			continue
		}
		for _, m := range reInlineCbctl.FindAllStringSubmatch(line, -1) {
			out = append(out, docCmd{path, n, m[1], false})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// tokens splits a command line like a shell would for our purposes: quotes group, a comment, a
// pipe, a redirect or a command separator ends the command. Usage syntax ([ ], ( ), a|b, <x>,
// ...) is flattened: optional parts are kept, the first alternative is used.
func tokens(s string) []string {
	s = reSubshell.ReplaceAllString(s, "SUBSHELL") // "$(curl -s URL)/32" is one argument
	s = strings.ReplaceAll(s, `\|`, "|")           // a pipe escaped inside a Markdown table
	var out []string
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	var cleaned []string
	for i, tok := range out {
		if tok == "#" || strings.HasPrefix(tok, "#") && i > 0 || tok == "&&" || tok == "||" || tok == ";" || tok == ">" || tok == "2>&1" ||
			strings.HasPrefix(tok, ">") || strings.HasPrefix(tok, "<<") {
			break
		}
		if end := strings.HasSuffix(tok, ";"); end {
			if tok = strings.Trim(strings.TrimSuffix(tok, ";"), "[]()"); tok != "" {
				cleaned = append(cleaned, tok)
			}
			break
		}
		if tok == "|" {
			if i+1 < len(out) && strings.HasPrefix(strings.Trim(out[i+1], "[]()"), "-") {
				continue // usage alternative: "--on | --off"
			}
			break // a shell pipe
		}
		tok = strings.Trim(tok, "[]()")
		tok = strings.TrimSuffix(tok, "...")
		tok = strings.TrimSuffix(tok, "…")
		tok = strings.TrimSuffix(tok, ",")
		if tok == "" || tok == "…" {
			continue
		}
		cleaned = append(cleaned, tok)
	}
	return cleaned
}

// checkCommand resolves the command path and parses its flags against a fresh command tree.
func checkCommand(c docCmd) string {
	toks := tokens(c.text)
	if len(toks) < 2 || toks[0] != "cbctl" {
		return ""
	}
	deps := cli.DefaultDeps()
	deps.Version = "doc-check" // the released binary has a version, so --version exists
	root := cli.NewRootCmd(deps)
	args := toks[1:]
	// The command path: leading words that name subcommands ("a|b" takes the first).
	// Global flags may come before the subcommand ("cbctl capella -y cluster scale ...").
	cmd := root
	i := 0
	var lead []string
	for ; i < len(args); i++ {
		word := strings.SplitN(args[i], "|", 2)[0]
		if strings.HasPrefix(word, "-") {
			fl := lookupFlag(cmd, word)
			if fl == nil {
				break
			}
			lead = append(lead, args[i])
			if takesValue(fl, word) && i+1 < len(args) {
				i++
				lead = append(lead, args[i])
			}
			continue
		}
		next := findChild(cmd, word)
		if next == nil {
			break
		}
		cmd = next
	}
	rest := append(lead, args[i:]...)
	var flags, positional []string
	help := false
	for j := 0; j < len(rest); j++ {
		tok := rest[j]
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			positional = append(positional, tok)
			continue
		}
		for _, alt := range strings.Split(tok, "|") {
			if !strings.HasPrefix(alt, "-") {
				continue
			}
			if alt == "--help" || alt == "-h" {
				help = true // cobra adds --help to every command when it runs
				continue
			}
			if alt == "--version" && cmd == root && root.Version != "" {
				help = true // cobra adds --version to a root command with a Version
				continue
			}
			fl := lookupFlag(cmd, alt)
			if fl == nil {
				return "unknown flag " + alt + " for '" + cmd.CommandPath() + "'"
			}
			flags = append(flags, alt)
			// A flag that takes a value consumes the next token (unless written --flag=value).
			if takesValue(fl, alt) && j+1 < len(rest) {
				j++
			}
		}
	}
	if len(positional) > 0 && !cmd.Runnable() {
		return "unknown subcommand '" + positional[0] + "' of '" + cmd.CommandPath() + "'"
	}
	_ = flags // parsed above; kept for debugging
	if c.fence && !help && cmd.Runnable() && cmd.Args != nil {
		if err := cmd.Args(cmd, positional); err != nil {
			return "'" + cmd.CommandPath() + "': " + err.Error()
		}
	}
	return ""
}

// lookupFlag finds the flag a token like "--name", "--name=v" or "-n" names on cmd.
func lookupFlag(cmd *cobra.Command, tok string) *pflag.Flag {
	name := strings.TrimLeft(strings.SplitN(tok, "=", 2)[0], "-")
	short := !strings.HasPrefix(tok, "--") && len(name) == 1
	for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags()} {
		if fl := fs.Lookup(name); fl != nil {
			return fl
		}
		if short {
			if fl := fs.ShorthandLookup(name); fl != nil {
				return fl
			}
		}
	}
	return nil
}

func takesValue(fl *pflag.Flag, tok string) bool {
	return fl.Value.Type() != "bool" && fl.NoOptDefVal == "" && !strings.Contains(tok, "=")
}

func findChild(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name || sub.HasAlias(name) {
			return sub
		}
	}
	return nil
}

// TestDocCommands checks that every cbctl command shown in the README, the agent rules, the
// Capella docs and the guide topics names a real command and only real flags.
func TestDocCommands(t *testing.T) {
	root := repoRoot(t)
	total := 0
	for _, path := range docFiles(t, root) {
		rel, _ := filepath.Rel(root, path)
		for _, c := range extractCommands(t, path) {
			total++
			if problem := checkCommand(c); problem != "" {
				t.Errorf("%s:%d: %s\n    %s", rel, c.line, problem, c.text)
			}
		}
	}
	if total < 100 {
		t.Fatalf("found only %d cbctl commands in the docs; the extractor is broken", total)
	}
}
