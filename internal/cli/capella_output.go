package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Renderer writes command results (tables, key: value objects, JSON documents, one-time
// secrets). It lives in internal/output; the alias keeps the cli code unchanged.
type Renderer = output.Renderer

// namedSecret is one one-time secret with the name of what it belongs to.
type namedSecret = output.NamedSecret

// terminalCheck reports whether v (an *os.File) is a terminal. Tests replace it.
var terminalCheck = func(v any) bool {
	if u, ok := v.(interface{ Underlying() io.Reader }); ok {
		v = u.Underlying()
	}
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// newRenderer returns the JSON or text Renderer. Whether progress dots are drawn is decided by
// terminalCheck, which Run routes through Deps, and the CI/TERM environment of the process.
func newRenderer(out, errOut io.Writer, jsonMode, quiet bool) Renderer {
	return output.NewRenderer(out, errOut, jsonMode, quiet, output.RenderOptions{
		Env:    DefaultDeps().Env,
		IsTerm: terminalCheck,
	})
}

// printSecretOnce shows a one-time secret such as a generated credential password. It warns on
// stderr that the value will not be shown again, then prints doc (the credential's visible
// fields, may be nil) with "<name>: <value>" appended (text) or as the name field of the JSON
// document. This is the only output path that skips the redactor, because the value was
// registered with it when Capella returned it.
func printSecretOnce(rt *runtime, doc any, name, value string) {
	logging.Warning("this %s is shown only once", name)
	rt.Out.Secret(doc, name, value)
}

// printSecretsOnce is printSecretOnce for several secrets (for example the credentials a clone
// creates): JSON gets them as the field array of the document, text as "name: password" lines.
func printSecretsOnce(rt *runtime, doc any, field string, items []namedSecret) {
	if len(items) == 0 {
		return
	}
	logging.Warning("these passwords are shown only once")
	rt.Out.Secrets(doc, field, items)
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
