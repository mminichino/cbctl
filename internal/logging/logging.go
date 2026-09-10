// Package logging provides colored CLI log output for cbctl.
package logging

import (
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
)

var (
	infoColor  = color.New(color.FgBlue).SprintFunc()
	warnColor  = color.New(color.FgYellow).SprintFunc()
	errorColor = color.New(color.FgRed).SprintFunc()

	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// SetOutputs overrides the writers used for log messages (tests).
func SetOutputs(out, err io.Writer) {
	if out != nil {
		stdout = out
	}
	if err != nil {
		stderr = err
	}
}

func logMsg(w io.Writer, level, msg string, args ...interface{}) {
	var coloredLevel string
	switch level {
	case "info":
		coloredLevel = infoColor(level)
	case "warning":
		coloredLevel = warnColor(level)
	case "error":
		coloredLevel = errorColor(level)
	default:
		coloredLevel = level
	}
	fmt.Fprintf(w, "[%s] - %s\n", coloredLevel, fmt.Sprintf(msg, args...))
}

// Info writes an info-level message to stdout.
func Info(msg string, args ...interface{}) { logMsg(stdout, "info", msg, args...) }

// Warning writes a warning-level message to stderr.
func Warning(msg string, args ...interface{}) { logMsg(stderr, "warning", msg, args...) }

// Error writes an error-level message to stderr.
func Error(msg string, args ...interface{}) { logMsg(stderr, "error", msg, args...) }

// Println writes a plain line to stdout (machine-readable output).
func Println(msg string, args ...interface{}) {
	fmt.Fprintf(stdout, msg+"\n", args...)
}
