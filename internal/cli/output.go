package cli

import (
	"fmt"
	"io"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/spf13/cobra"
)

// colorFlag is the name of the persistent root flag that picks the color mode.
const colorFlag = "color"

// glyphPolicy is how cli picks arrows. It stays Unicode until every arrow and symbol in the
// output goes through internal/output (0.7.0 phase P6), so one stream never mixes "→" and "->".
// Then it becomes output.GlyphsByColor: Unicode on a color terminal, ASCII elsewhere.
const glyphPolicy = output.GlyphsUnicode

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
	logging.Current().SetDecider(newDecider(depsFromCmd(cmd), mode))
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
	output.WriteLine(outputDecider(), w, kind, fmt.Sprintf(format, a...))
}

// writeAction writes the "  → action" hint line to w. An empty action writes nothing.
func writeAction(w io.Writer, action string) {
	_, _ = io.WriteString(w, output.ActionLine(outputDecider(), glyphPolicy, w, action))
}

// writeNotice writes "[warning] - what" and its hint line to w.
func writeNotice(w io.Writer, what, action string) {
	output.WriteNotice(outputDecider(), glyphPolicy, w, what, action)
}
