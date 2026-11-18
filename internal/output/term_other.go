//go:build !windows

package output

import "io"

// platformColorOK reports whether escape codes work on w. Only Windows consoles need a check.
func platformColorOK(io.Writer) bool { return true }
