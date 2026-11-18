// Package logging is the process-wide entry point to leveled, labeled CLI messages. It is a thin
// shim over internal/output: the functions here write through the process Printer, so packages
// that cannot receive a Printer (rest, capellaapi, capella, secrets) need no change. cli installs
// the invocation's Printer with SetPrinter.
package logging

import (
	"io"
	"sync"

	"github.com/mminichino/cbctl/internal/output"
)

// Level is a log verbosity level.
type Level = output.Level

// Log levels, from quietest to most verbose.
const (
	LevelError = output.LevelError
	LevelWarn  = output.LevelWarn
	LevelInfo  = output.LevelInfo
	LevelDebug = output.LevelDebug
)

var (
	mu  sync.RWMutex
	cur = output.New(nil, nil, output.SystemDecider())
)

// Current returns the process Printer.
func Current() *output.Printer {
	mu.RLock()
	defer mu.RUnlock()
	return cur
}

// SetPrinter installs p as the process Printer and returns the previous one, so a caller can
// restore it. A nil p is ignored.
func SetPrinter(p *output.Printer) *output.Printer {
	mu.Lock()
	defer mu.Unlock()
	prev := cur
	if p != nil {
		cur = p
	}
	return prev
}

// SetOutputs overrides the writers used for log messages (tests). A nil writer is kept.
func SetOutputs(out, err io.Writer) { Current().SetOutputs(out, err) }

// WithOutputs temporarily redirects log writers for fn and restores them afterwards.
// Legacy-command tests that rely on the package-level logger must not use t.Parallel().
func WithOutputs(out, err io.Writer, fn func()) {
	p := Current()
	prevOut, prevErr := p.Outputs()
	p.SetOutputs(out, err)
	defer p.SetOutputs(prevOut, prevErr)
	fn()
}

// Outputs returns the writers currently used for log messages, so a caller that redirects them
// with SetOutputs can restore them afterwards.
func Outputs() (out, err io.Writer) { return Current().Outputs() }

// SetLevel sets the minimum log level.
func SetLevel(l Level) { Current().SetLevel(l) }

// ParseLevel parses error|warn|warning|info|debug.
func ParseLevel(s string) (Level, error) { return output.ParseLevel(s) }

// Success writes an [ok] message: a requested change was made or a check passed.
func Success(msg string, args ...any) { Current().OK(msg, args...) }

// Info writes an info-level message to stdout.
func Info(msg string, args ...any) { Current().Info(msg, args...) }

// Warning writes a warning-level message to stderr.
func Warning(msg string, args ...any) { Current().Warn(msg, args...) }

// Error writes an error-level message to stderr.
func Error(msg string, args ...any) { Current().Error(msg, args...) }

// Debug writes a debug-level message to stderr.
func Debug(msg string, args ...any) { Current().Debug(msg, args...) }

// Println writes a plain line to stdout (machine-readable output). Never suppressed.
func Println(msg string, args ...any) { Current().Data(msg, args...) }
