package output

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mminichino/cbctl/internal/redact"
)

// Option configures a Printer.
type Option func(*Printer)

// WithLevel sets the minimum level shown (default LevelInfo).
func WithLevel(l Level) Option { return func(p *Printer) { p.level = l } }

// WithJSON marks JSON mode: stdout carries only the JSON document, so [ok] and [info] lines move
// to stderr.
func WithJSON(on bool) Option { return func(p *Printer) { p.json = on } }

// WithQuiet hides [ok], [info] and [debug] lines and progress. Warnings, errors, data and
// one-time secrets are never hidden.
func WithQuiet(on bool) Option { return func(p *Printer) { p.quiet = on } }

// WithGlyphPolicy sets how action-line glyphs are chosen (default GlyphsByColor).
func WithGlyphPolicy(g GlyphPolicy) Option { return func(p *Printer) { p.glyphs = g } }

// Printer writes labeled lines, action hints and data lines. Every line is redacted, then
// labeled, then written with a single Write, under one mutex, so concurrent writers never
// interleave inside a line. It is safe for concurrent use.
//
//	[ok] - ...       stdout (stderr in JSON mode)
//	[info] - ...     stdout (stderr in JSON mode)
//	[warning] - ...  stderr
//	[error] - ...    stderr
//	[debug] - ...    stderr
//	data lines       stdout, plain
type Printer struct {
	mu       sync.Mutex
	out, err io.Writer
	d        Decider
	level    Level
	json     bool
	quiet    bool
	glyphs   GlyphPolicy
	progress *Progress
}

// New returns a Printer writing to out and err. Nil writers default to os.Stdout and os.Stderr.
func New(out, err io.Writer, d Decider, opts ...Option) *Printer {
	if out == nil {
		out = os.Stdout
	}
	if err == nil {
		err = os.Stderr
	}
	p := &Printer{out: out, err: err, d: d, level: LevelInfo}
	for _, o := range opts {
		o(p)
	}
	return p
}

// SetOutputs replaces the writers; a nil writer keeps the current one.
func (p *Printer) SetOutputs(out, err io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if out != nil {
		p.out = out
	}
	if err != nil {
		p.err = err
		p.progress = nil // it wrote to the old stderr
	}
}

// Outputs returns the stdout and stderr writers.
func (p *Printer) Outputs() (out, err io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out, p.err
}

// SetLevel sets the minimum level shown.
func (p *Printer) SetLevel(l Level) {
	p.mu.Lock()
	p.level = l
	p.mu.Unlock()
}

// Level returns the minimum level shown.
func (p *Printer) Level() Level {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.level
}

// SetDecider replaces the color decider, for example once --color has been parsed.
func (p *Printer) SetDecider(d Decider) {
	p.mu.Lock()
	p.d = d
	p.mu.Unlock()
}

// Decider returns the color decider.
func (p *Printer) Decider() Decider {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.d
}

// SetJSON switches JSON mode on or off.
func (p *Printer) SetJSON(on bool) {
	p.mu.Lock()
	p.json = on
	p.mu.Unlock()
}

// SetQuiet switches quiet mode on or off.
func (p *Printer) SetQuiet(on bool) {
	p.mu.Lock()
	p.quiet = on
	p.mu.Unlock()
}

// Glyphs returns the glyph set the printer uses on stderr, where action hints go.
func (p *Printer) Glyphs() Glyphs {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.glyphs.glyphsFor(p.d, p.err)
}

// Line formats one labeled line, "LABEL - text\n". text is redacted before the label is added, so
// a secret can never be split by escape codes.
func Line(d Decider, w io.Writer, kind, text string) string {
	return Label(d, w, kind) + " - " + redact.String(text) + "\n"
}

// ActionLine formats a hint line, "  -> text\n" (glyph per policy), or "" when text is empty.
func ActionLine(d Decider, policy GlyphPolicy, w io.Writer, text string) string {
	if text == "" {
		return ""
	}
	return "  " + policy.glyphsFor(d, w).Arrow + " " + redact.String(text) + "\n"
}

// WriteLine writes one labeled line to w. A write error is ignored: there is nowhere to report
// it, and a closed pipe must not turn into a second failure.
func WriteLine(d Decider, w io.Writer, kind, text string) {
	_, _ = io.WriteString(w, Line(d, w, kind, text))
}

// WriteNotice writes "[warning] - what" and, when action is not empty, its hint line to w.
func WriteNotice(d Decider, policy GlyphPolicy, w io.Writer, what, action string) {
	_, _ = io.WriteString(w, Line(d, w, KindWarning, what)+ActionLine(d, policy, w, action))
}

// write sends s to the out (stdout) or err stream. Callers hold no lock.
func (p *Printer) write(toErr bool, kind, text, action string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	toErr = toErr || p.json
	w := p.out
	if toErr {
		w = p.err
	}
	if kind != "" {
		s := Line(p.d, w, kind, text)
		if action != "" {
			s += ActionLine(p.d, p.glyphs, w, action)
		}
		p.emit(toErr, w, s)
		return
	}
	p.emit(toErr, w, ActionLine(p.d, p.glyphs, w, action))
}

// emit writes s, closing an open inline progress line first when s goes to stderr.
func (p *Printer) emit(toErr bool, w io.Writer, s string) {
	if s == "" {
		return
	}
	if toErr && p.progress != nil {
		p.progress.closeLocked()
	}
	_, _ = io.WriteString(w, s)
}

func (p *Printer) shows(l Level) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l >= LevelInfo && p.quiet {
		return false
	}
	return p.level >= l
}

// OK writes an [ok] line: a requested change was made or a check passed. It is hidden below
// LevelInfo and when quiet.
func (p *Printer) OK(format string, a ...any) {
	if p.shows(LevelInfo) {
		p.write(false, KindOK, fmt.Sprintf(format, a...), "")
	}
}

// Info writes an [info] line (status, progress notes, no-ops). Same stream and filtering as OK.
func (p *Printer) Info(format string, a ...any) {
	if p.shows(LevelInfo) {
		p.write(false, KindInfo, fmt.Sprintf(format, a...), "")
	}
}

// Warn writes a [warning] line to stderr. It is hidden below LevelWarn.
func (p *Printer) Warn(format string, a ...any) {
	if p.shows(LevelWarn) {
		p.write(true, KindWarning, fmt.Sprintf(format, a...), "")
	}
}

// Error writes an [error] line to stderr. It is never hidden.
func (p *Printer) Error(format string, a ...any) {
	p.write(true, KindError, fmt.Sprintf(format, a...), "")
}

// Debug writes a [debug] line to stderr. It is hidden below LevelDebug and when quiet.
func (p *Printer) Debug(format string, a ...any) {
	if p.shows(LevelDebug) {
		p.write(true, KindDebug, fmt.Sprintf(format, a...), "")
	}
}

// Action writes a hint line ("  -> text") to stderr. It is never hidden.
func (p *Printer) Action(text string) {
	p.write(true, "", "", text)
}

// Notice writes a [warning] line and its hint as one write, so nothing lands between them.
func (p *Printer) Notice(what, action string) {
	if p.shows(LevelWarn) {
		p.write(true, KindWarning, what, action)
	}
}

// Data writes a plain, redacted line to stdout, for machine-readable output such as true and
// false. It is never colored, never hidden and never moved to stderr.
func (p *Printer) Data(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Redact the formatted text, not the format: a secret usually arrives as an argument.
	p.emit(false, p.out, redact.String(fmt.Sprintf(format, a...))+"\n")
}

// Renderer returns a Renderer that writes to the printer's streams.
func (p *Printer) Renderer() Renderer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return NewRenderer(p.out, p.err, p.json, p.quiet, RenderOptions{Env: p.d.Env, IsTerm: p.d.IsTerm})
}

// Progress returns the printer's progress writer for stderr. The same Progress is returned on
// every call; labeled lines close its open inline line before they are written.
func (p *Printer) Progress() *Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.progress == nil {
		tty := p.d.IsTerm != nil && p.d.IsTerm(p.err)
		p.progress = newProgress(p.err, p.quiet, tty && ProgressAllowed(p.d.Env), &p.mu)
	}
	return p.progress
}
