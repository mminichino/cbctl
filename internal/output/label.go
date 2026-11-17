package output

import (
	"fmt"
	"io"
	"strings"
)

// Level is a log verbosity level. A message is shown when its level is at most the printer's.
type Level int

// Levels, from quietest to most verbose.
const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

// ParseLevel parses error|warn|warning|info|debug (case-insensitive).
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

// Label kinds. The kind is the word inside the brackets.
const (
	KindOK      = "ok"
	KindInfo    = "info"
	KindWarning = "warning"
	KindError   = "error"
	KindDebug   = "debug"
)

// sgr maps a kind to its SGR parameters. An empty string means plain text. Only the 16-color
// palette and bold/dim are used, so the user's terminal theme applies.
var sgr = map[string]string{
	KindOK:      "1;32",
	KindInfo:    "",
	KindWarning: "1;33",
	KindError:   "1;31",
	KindDebug:   "2",
}

const sgrReset = "\x1b[0m"

// Label returns the bracketed tag for kind, for example "[ok]". When d says w gets color, the
// tag is wrapped in the kind's escape code and always closed with ESC[0m. Kinds without a style
// (info, and any unknown kind) stay plain.
func Label(d Decider, w io.Writer, kind string) string {
	tag := "[" + kind + "]"
	if code := sgr[kind]; code != "" && d.Color(w) {
		return "\x1b[" + code + "m" + tag + sgrReset
	}
	return tag
}
