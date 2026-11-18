package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/spf13/cobra"
)

// colorFlag is the name of the persistent root flag that picks the color mode.
const colorFlag = "color"

// glyphPolicy is how cli picks arrows and symbols: Unicode on a stream that gets color, ASCII
// elsewhere (pipes, logs), so captured output is plain ASCII.
const glyphPolicy = output.GlyphsByColor

// newDecider returns the color decider of an invocation: its environment and terminal check,
// with the mode from --color.
func newDecider(d Deps, mode output.ColorMode) output.Decider {
	return output.Decider{Mode: mode, Env: d.Env, IsTerm: d.IsTerminal}
}

// newPrinter builds the Printer of one invocation from its Deps. The color mode is auto until
// the root command has parsed --color (see applyOutputFlags).
func newPrinter(d Deps) *output.Printer {
	return output.New(d.Stdout, d.Stderr, newDecider(d, output.ColorAuto), output.WithGlyphPolicy(glyphPolicy))
}

// addColorFlag registers the persistent --color flag on the root command.
func addColorFlag(root *cobra.Command) {
	root.PersistentFlags().String(colorFlag, "auto", "Colorize output: auto|always|never (env NO_COLOR, CLICOLOR, CLICOLOR_FORCE, FORCE_COLOR)")
}

// parseColorFlag reads --color as seen by cmd. A bad value is a usage error (exit 3).
func parseColorFlag(cmd *cobra.Command) (output.ColorMode, error) {
	v, err := cmd.Flags().GetString(colorFlag)
	if err != nil {
		return output.ColorAuto, nil // unreachable once applyOutputFlags found the flag
	}
	mode, err := output.ParseColorMode(v)
	if err != nil {
		return output.ColorAuto, Errorf(ExitConfigInvalid, "run 'cbctl --help'", "%v", err)
	}
	return mode, nil
}

// applyOutputFlags applies --color to the invocation's Printer. It runs from the root command's
// PersistentPreRunE, and from the capella group's, which cobra runs instead of the root's.
func applyOutputFlags(cmd *cobra.Command) error {
	if cmd.Flags().Lookup(colorFlag) == nil {
		return nil // a command tree built without the root flag keeps the installed Printer as is
	}
	mode, err := parseColorFlag(cmd)
	if err != nil {
		return err
	}
	p := logging.Current()
	p.SetDecider(newDecider(depsFromCmd(cmd), mode))
	// D1: with -o json stdout holds only the JSON document, so [ok] and [info] lines go to stderr.
	if f := cmd.Flags().Lookup("output"); f != nil {
		p.SetJSON(f.Value.String() == "json")
	}
	return nil
}

// applyParsedColor applies --color as parsed by Execute. Cobra validates arguments before
// PersistentPreRunE runs, so a usage error would otherwise be printed with the default mode.
// An invalid value was already reported by applyOutputFlags and keeps the default here.
func applyParsedColor(root *cobra.Command, d Deps) {
	f := root.PersistentFlags().Lookup(colorFlag)
	if f == nil {
		return
	}
	if mode, err := output.ParseColorMode(f.Value.String()); err == nil {
		logging.Current().SetDecider(newDecider(d, mode))
	}
}

// outputDecider is the color decider of the current invocation.
func outputDecider() output.Decider { return logging.Current().Decider() }

// writeLabeled writes "[kind] - text" to w, colored when the current invocation colors w. The
// text is redacted.
func writeLabeled(w io.Writer, kind, format string, a ...any) {
	text := outGlyphs(w).Localize(fmt.Sprintf(format, a...))
	output.WriteLine(outputDecider(), w, kind, text)
}

// writeAction writes the "  → action" hint line to w. An empty action writes nothing.
func writeAction(w io.Writer, action string) {
	_, _ = io.WriteString(w, output.ActionLine(outputDecider(), glyphPolicy, w, action))
}

// writeNotice writes "[warning] - what" and its hint line to w.
func writeNotice(w io.Writer, what, action string) {
	output.WriteNotice(outputDecider(), glyphPolicy, w, what, action)
}

// writeProse writes one plain line to w: prompt context, menus, confirmation details and other
// text that is neither a labeled message nor command data. It is redacted but never styled, so
// every non-labeled write in cli goes through here and secrets cannot slip past the redactor.
func writeProse(w io.Writer, format string, a ...any) {
	text := outGlyphs(w).Localize(fmt.Sprintf(format, a...))
	_, _ = io.WriteString(w, redact.String(text)+"\n")
}

// writePrompt writes prompt text to w without a newline, so the answer is typed after it.
func writePrompt(w io.Writer, format string, a ...any) {
	text := outGlyphs(w).Localize(fmt.Sprintf(format, a...))
	_, _ = io.WriteString(w, redact.String(text))
}

// writeDocument writes a generated document (a capella.yaml, guide text) to w with exactly one
// trailing newline. It is not redacted: documents only ever carry ${VAR} references, and
// redacting could change the bytes of a file that is meant to be written back unchanged.
func writeDocument(w io.Writer, text string) {
	_, _ = io.WriteString(w, strings.TrimRight(text, "\n")+"\n")
}

// outGlyphs is the glyph set for text written to w: Unicode where w gets color, ASCII elsewhere.
func outGlyphs(w io.Writer) output.Glyphs {
	return logging.Current().GlyphsFor(w)
}

// stdoutGlyphs is the glyph set for the current invocation's stdout.
func stdoutGlyphs() output.Glyphs {
	out, _ := logging.Outputs()
	return outGlyphs(out)
}
