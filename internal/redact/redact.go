// Package redact masks registered secrets in CLI and API output.
package redact

import (
	"sort"
	"strings"
	"sync"
	"unicode"
)

const mask = "***"

var secretKeys = map[string]struct{}{
	"password":      {},
	"authorization": {},
	"apikey":        {},
	"api_key":       {},
	"secret":        {},
	"secretkey":     {},
	"token":         {},
}

var (
	mu     sync.Mutex
	values = map[string]struct{}{}
)

// Register adds a secret for later redaction. Values shorter than 4 characters are ignored.
func Register(secret string) {
	if len(secret) < 4 {
		return
	}
	mu.Lock()
	values[secret] = struct{}{}
	mu.Unlock()
}

// Reset clears registered secrets. Intended for tests only.
func Reset() {
	mu.Lock()
	values = map[string]struct{}{}
	mu.Unlock()
}

// String replaces every registered secret (longest first) with "***".
func String(s string) string {
	mu.Lock()
	list := make([]string, 0, len(values))
	for v := range values {
		list = append(list, v)
	}
	mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, v := range list {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, mask)
		}
	}
	return s
}

// Object returns a deep copy of maps/slices/strings with secrets masked.
// Non-empty values of known secret keys are replaced regardless of registration.
func Object(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if _, ok := secretKeys[strings.ToLower(k)]; ok {
				if s, isStr := val.(string); isStr && s != "" {
					out[k] = mask
					continue
				}
				if val != nil && val != "" {
					out[k] = mask
					continue
				}
			}
			out[k] = Object(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = Object(val)
		}
		return out
	case string:
		return String(x)
	default:
		return v
	}
}

// MaskTail returns "••••" + last 4 chars, or ASCII "****" when stdout is not a TTY / NO_COLOR.
// Empty input returns "(not set)".
func MaskTail(s string) string {
	if s == "" {
		return "(not set)"
	}
	tail := s
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	prefix := "••••"
	if !unicodeMask() {
		prefix = "****"
	}
	return prefix + tail
}

// HasPrintableSecret reports whether s contains non-space printable runes (tests/helpers).
func HasPrintableSecret(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && unicode.IsPrint(r) {
			return true
		}
	}
	return false
}
