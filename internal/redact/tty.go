package redact

import "os"

// unicodeMask reports whether MaskTail may print the "••••" bullets: stdout is a terminal and
// NO_COLOR is unset. It is the package's only process-global read, kept here so the hermetic
// gate can allow exactly this file, and a variable so tests can fix the answer.
var unicodeMask = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
