// Package logging provides colored CLI log output for cbctl.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/mminichino/cbctl/internal/redact"
)

// Level is a log verbosity level.
type Level int

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

var (
	infoColor  = color.New(color.FgBlue).SprintFunc()
	warnColor  = color.New(color.FgYellow).SprintFunc()
	errorColor = color.New(color.FgRed).SprintFunc()
	debugColor = color.New(color.FgCyan).SprintFunc()

	outputMu sync.Mutex
	stdout   io.Writer = os.Stdout
	stderr   io.Writer = os.Stderr
	level              = LevelInfo
)

// SetOutputs overrides the writers used for log messages (tests).
func SetOutputs(out, err io.Writer) {
	outputMu.Lock()
	defer outputMu.Unlock()
	if out != nil {
		stdout = out
	}
	if err != nil {
		stderr = err
	}
}

// WithOutputs temporarily redirects log writers for fn and restores them afterwards.
// Legacy-command tests that rely on the package-level logger must not use t.Parallel().
func WithOutputs(out, err io.Writer, fn func()) {
	outputMu.Lock()
	prevOut, prevErr := stdout, stderr
	if out != nil {
		stdout = out
	}
	if err != nil {
		stderr = err
	}
	outputMu.Unlock()
	defer SetOutputs(prevOut, prevErr)
	fn()
}

// Outputs returns the writers currently used for log messages, so a caller that redirects them
// with SetOutputs can restore them afterwards.
func Outputs() (out, err io.Writer) {
	outputMu.Lock()
	defer outputMu.Unlock()
	return stdout, stderr
}

// SetLevel sets the minimum log level.
func SetLevel(l Level) { level = l }

// ParseLevel parses error|warn|warning|info|debug.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return LevelError, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "info":
		return LevelInfo, nil
	case "debug":
		return LevelDebug, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", s)
	}
}

func logMsg(w io.Writer, lvl, msg string, args ...interface{}) {
	var coloredLevel string
	switch lvl {
	case "info":
		coloredLevel = infoColor(lvl)
	case "warning":
		coloredLevel = warnColor(lvl)
	case "error":
		coloredLevel = errorColor(lvl)
	case "debug":
		coloredLevel = debugColor(lvl)
	default:
		coloredLevel = lvl
	}
	text := redact.String(fmt.Sprintf(msg, args...))
	fmt.Fprintf(w, "[%s] - %s\n", coloredLevel, text)
}

// Info writes an info-level message to stdout.
func Info(msg string, args ...interface{}) {
	if level < LevelInfo {
		return
	}
	logMsg(stdout, "info", msg, args...)
}

// Warning writes a warning-level message to stderr.
func Warning(msg string, args ...interface{}) {
	if level < LevelWarn {
		return
	}
	logMsg(stderr, "warning", msg, args...)
}

// Error writes an error-level message to stderr.
func Error(msg string, args ...interface{}) {
	logMsg(stderr, "error", msg, args...)
}

// Debug writes a debug-level message to stderr.
func Debug(msg string, args ...interface{}) {
	if level < LevelDebug {
		return
	}
	logMsg(stderr, "debug", msg, args...)
}

// Println writes a plain line to stdout (machine-readable output). Never suppressed.
func Println(msg string, args ...interface{}) {
	// Redact the formatted text, not the format: a secret usually arrives as an argument.
	fmt.Fprintln(stdout, redact.String(fmt.Sprintf(msg, args...)))
}
