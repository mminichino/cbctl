package output

import "io"

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

// GlyphPolicy says how a Printer picks its Glyphs.
type GlyphPolicy int

// Glyph policies.
const (
	// GlyphsByColor uses Unicode glyphs where the stream gets color and ASCII elsewhere. This is
	// the 0.7.0 end state (decision 5).
	GlyphsByColor GlyphPolicy = iota
	// GlyphsUnicode always uses Unicode glyphs. cli uses it until every arrow and symbol in the
	// output goes through this package, so one stream never mixes both spellings.
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
