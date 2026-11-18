package output

import (
	"io"
	"strings"
)

// Glyphs are the symbols used in output that has a Unicode and an ASCII spelling.
type Glyphs struct {
	// Arrow introduces an action hint line.
	Arrow string
	// Dot separates parts of a header line.
	Dot string
	// PlusMinus marks a change that adds and removes.
	PlusMinus string
	// Ellipsis marks truncated text.
	Ellipsis string
	// Mask replaces the hidden characters of a masked value.
	Mask string
}

// Glyph sets.
var (
	// UnicodeGlyphs are used on a color-capable terminal.
	UnicodeGlyphs = Glyphs{Arrow: "→", Dot: "·", PlusMinus: "±", Ellipsis: "…", Mask: "••••"}
	// ASCIIGlyphs are used for pipes, logs and anywhere color is off, so captured output is ASCII.
	ASCIIGlyphs = Glyphs{Arrow: "->", Dot: "-", PlusMinus: "+/-", Ellipsis: "...", Mask: "****"}
)

// asciiReplacer rewrites the Unicode glyphs to their ASCII spelling. The en and em dashes of
// ranges and sentences are not glyphs in this sense and stay.
var asciiReplacer = strings.NewReplacer(
	UnicodeGlyphs.Mask, ASCIIGlyphs.Mask,
	UnicodeGlyphs.Arrow, ASCIIGlyphs.Arrow,
	UnicodeGlyphs.PlusMinus, ASCIIGlyphs.PlusMinus,
	UnicodeGlyphs.Ellipsis, ASCIIGlyphs.Ellipsis,
	UnicodeGlyphs.Dot, ASCIIGlyphs.Dot,
)

// Localize returns text written in the spelling of g: the Unicode glyphs in text are rewritten
// for ASCII glyphs, and text is returned as is for Unicode glyphs (the zero Glyphs counts as
// Unicode). It works on whole lines, so a name that itself contains one of the four glyphs is
// rewritten too when output is not a color terminal; that is the price of one rule for all text.
func (g Glyphs) Localize(text string) string {
	if g.Arrow == "" || g.Arrow == UnicodeGlyphs.Arrow {
		return text
	}
	return asciiReplacer.Replace(text)
}

// GlyphPolicy says how a Printer picks its Glyphs.
type GlyphPolicy int

// Glyph policies.
const (
	// GlyphsByColor uses Unicode glyphs where the stream gets color and ASCII elsewhere. This is
	// what cli uses (decision 5).
	GlyphsByColor GlyphPolicy = iota
	// GlyphsUnicode always uses Unicode glyphs.
	GlyphsUnicode
	// GlyphsASCII always uses ASCII glyphs.
	GlyphsASCII
)

// Glyphs returns the glyph set for w under d: Unicode when w gets color, ASCII otherwise.
func (d Decider) Glyphs(w io.Writer) Glyphs {
	if d.Color(w) {
		return UnicodeGlyphs
	}
	return ASCIIGlyphs
}

// glyphsFor applies policy to the stream w.
func (p GlyphPolicy) glyphsFor(d Decider, w io.Writer) Glyphs {
	switch p {
	case GlyphsUnicode:
		return UnicodeGlyphs
	case GlyphsASCII:
		return ASCIIGlyphs
	}
	return d.Glyphs(w)
}
