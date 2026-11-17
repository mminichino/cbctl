package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgress(t *testing.T) {
	tests := []struct {
		name  string
		tty   bool
		quiet bool
		run   func(p *Progress)
		want  string
	}{
		{"line", true, false, func(p *Progress) { p.Line("state") }, "state\n"},
		{"quiet", true, true, func(p *Progress) { p.Line("state"); p.Inline("x ", true); p.Inline(".", false) }, ""},
		{"inline on terminal draws dots", true, false, func(p *Progress) {
			p.Inline("waiting ", true)
			p.Inline(".", false)
			p.Inline(".", false)
			p.Line("done")
		}, "waiting ..\ndone\n"},
		{"inline off terminal closes at once", false, false, func(p *Progress) {
			p.Inline("waiting ", true)
			p.Inline(".", false)
		}, "waiting \n"},
		{"restart closes the open line", true, false, func(p *Progress) {
			p.Inline("a ", true)
			p.Inline("b ", true)
			p.Close()
		}, "a \nb \n"},
		{"close without open line", true, false, func(p *Progress) { p.Close() }, ""},
		{"dot without start", true, false, func(p *Progress) { p.Inline(".", false) }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.run(NewProgress(&buf, tc.quiet, tc.tty))
			if buf.String() != tc.want {
				t.Errorf("got %q, want %q", buf.String(), tc.want)
			}
		})
	}
}

func TestProgressAllowed(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want bool
	}{
		{nil, true},
		{map[string]string{"CI": "true"}, false},
		{map[string]string{"TERM": "dumb"}, false},
		{map[string]string{"TERM": "xterm"}, true},
	}
	for _, tc := range tests {
		if got := ProgressAllowed(envOf(tc.env)); got != tc.want {
			t.Errorf("ProgressAllowed(%v) = %v, want %v", tc.env, got, tc.want)
		}
	}
	if !ProgressAllowed(nil) {
		t.Error("nil env should allow progress")
	}
}

// TestPrinterClosesInlineProgress: a labeled line on stderr ends an open inline progress line, and
// a Printer returns the same Progress each time.
func TestPrinterClosesInlineProgress(t *testing.T) {
	var out, errW bytes.Buffer
	d := Decider{Mode: ColorNever, Env: envOf(nil), IsTerm: func(any) bool { return true }}
	p := New(&out, &errW, d)
	pw := p.Progress()
	if pw != p.Progress() {
		t.Error("Progress() should be stable")
	}
	pw.Inline("waiting ", true)
	pw.Inline(".", false)
	p.Warn("slow")
	p.OK("stdout lines do not close it") // stdout: progress stays as is
	if got, want := errW.String(), "waiting .\n[warning] - slow\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestGlyphs(t *testing.T) {
	var tty, pipe bytes.Buffer
	d := Decider{Env: envOf(nil), IsTerm: fakeTerm(&tty)}
	if d.Glyphs(&tty) != UnicodeGlyphs {
		t.Error("color terminal should get Unicode glyphs")
	}
	if d.Glyphs(&pipe) != ASCIIGlyphs {
		t.Error("pipe should get ASCII glyphs")
	}
	for _, g := range []string{ASCIIGlyphs.Arrow, ASCIIGlyphs.Dot, ASCIIGlyphs.PlusMinus, ASCIIGlyphs.Ellipsis, ASCIIGlyphs.Mask} {
		for _, r := range g {
			if r > 127 {
				t.Errorf("ASCII glyph %q has a non-ASCII rune", g)
			}
		}
	}

	tests := []struct {
		name   string
		policy GlyphPolicy
		w      *bytes.Buffer
		want   string
	}{
		{"by color, terminal", GlyphsByColor, &tty, "  → hint\n"},
		{"by color, pipe", GlyphsByColor, &pipe, "  -> hint\n"},
		{"unicode, pipe", GlyphsUnicode, &pipe, "  → hint\n"},
		{"ascii, terminal", GlyphsASCII, &tty, "  -> hint\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActionLine(d, tc.policy, tc.w, "hint"); got != tc.want {
				t.Errorf("ActionLine = %q, want %q", got, tc.want)
			}
		})
	}
	if ActionLine(d, GlyphsByColor, &tty, "") != "" {
		t.Error("an empty action writes nothing")
	}
}

func TestPrinterGlyphPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy GlyphPolicy
		want   Glyphs
	}{{GlyphsUnicode, UnicodeGlyphs}, {GlyphsASCII, ASCIIGlyphs}, {GlyphsByColor, ASCIIGlyphs}} {
		p, _, _ := newTestPrinter(ColorNever, WithGlyphPolicy(tc.policy))
		if p.Glyphs() != tc.want {
			t.Errorf("policy %v: Glyphs = %+v", tc.policy, p.Glyphs())
		}
	}
	var out, errW bytes.Buffer
	p := New(&out, &errW, Decider{Mode: ColorNever}, WithGlyphPolicy(GlyphsUnicode))
	p.Notice("careful", "do this")
	if got, want := errW.String(), "[warning] - careful\n  → do this\n"; got != want {
		t.Errorf("Notice = %q, want %q", got, want)
	}
}

func TestWriteNoticeAndWriteLine(t *testing.T) {
	var buf bytes.Buffer
	d := Decider{Mode: ColorAlways}
	WriteNotice(d, GlyphsUnicode, &buf, "what", "")
	WriteLine(d, &buf, KindError, "bad")
	if want := "\x1b[1;33m[warning]\x1b[0m - what\n\x1b[1;31m[error]\x1b[0m - bad\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
	if !strings.HasSuffix(Line(d, &buf, KindInfo, "x"), "\n") {
		t.Error("Line should end with a newline")
	}
}
