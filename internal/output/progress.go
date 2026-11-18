package output

import (
	"fmt"
	"io"
	"sync"

	"github.com/mminichino/cbctl/internal/redact"
)

// Progress prints engine progress. State lines end with a newline; the dots of a long wait are
// only drawn on a terminal. An open inline line is closed before any other line is written. It is
// safe for concurrent use.
type Progress struct {
	mu    *sync.Mutex
	w     io.Writer
	quiet bool
	tty   bool
	open  bool
}

// NewProgress returns a Progress writing to w. quiet hides everything; tty allows the inline dots.
func NewProgress(w io.Writer, quiet, tty bool) *Progress {
	return newProgress(w, quiet, tty, &sync.Mutex{})
}

// newProgress shares mu with the Printer that owns w, so progress and labeled lines never
// interleave.
func newProgress(w io.Writer, quiet, tty bool, mu *sync.Mutex) *Progress {
	return &Progress{mu: mu, w: w, quiet: quiet, tty: tty}
}

// ProgressAllowed reports whether the environment lets progress be drawn in place: not under CI,
// and not on a terminal that declares itself dumb (TERM=dumb, such as an editor's output pane).
func ProgressAllowed(env func(string) string) bool {
	if env == nil {
		return true
	}
	return env("CI") == "" && env("TERM") != "dumb"
}

// Line prints one redacted state line.
func (p *Progress) Line(s string) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
	_, _ = fmt.Fprintln(p.w, redact.String(s))
}

// Inline starts (start) or continues an inline progress line. Off a terminal a started line is
// closed at once and the continuation dots are dropped.
func (p *Progress) Inline(text string, start bool) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if start {
		p.closeLocked()
		_, _ = fmt.Fprint(p.w, redact.String(text))
		p.open = true
		if !p.tty {
			p.closeLocked()
		}
		return
	}
	if p.tty && p.open {
		_, _ = fmt.Fprint(p.w, text)
	}
}

// Close ends an open inline line.
func (p *Progress) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

func (p *Progress) closeLocked() {
	if p.open {
		_, _ = fmt.Fprintln(p.w)
		p.open = false
	}
}
