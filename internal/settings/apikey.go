package settings

import (
	"fmt"
	"strings"
	"unicode"
)

// pasteMarkers are the bracketed-paste markers some terminals wrap pasted text in (with and
// without the escape byte, which some terminals drop).
var pasteMarkers = []string{"\x1b[200~", "\x1b[201~", "[200~", "[201~"}

// CleanAPIKey undoes common copy/paste damage to an API key typed or piped into configure: bracketed
// paste markers, a byte-order mark, surrounding quotes, a leading "Bearer " and line breaks, spaces
// or control characters inside the key. It returns the key and one note per fix made; the notes
// never contain the key. Leading and trailing whitespace is trimmed silently.
func CleanAPIKey(raw string) (string, []string) {
	var fixes []string
	key := raw
	marked := false
	for _, m := range pasteMarkers {
		if strings.Contains(key, m) {
			key = strings.ReplaceAll(key, m, "")
			marked = true
		}
	}
	if marked {
		fixes = append(fixes, "removed terminal paste markers")
	}
	if strings.Contains(key, "\ufeff") {
		// Windows PowerShell pipes text with a UTF-8 byte-order mark.
		key = strings.ReplaceAll(key, "\ufeff", "")
		fixes = append(fixes, "removed a byte-order mark")
	}
	key = strings.TrimSpace(key)
	if len(key) >= 2 && key[0] == key[len(key)-1] && (key[0] == '"' || key[0] == '\'') {
		key = strings.TrimSpace(key[1 : len(key)-1])
		fixes = append(fixes, "removed surrounding quotes")
	}
	if len(key) >= 7 && strings.EqualFold(key[:7], "Bearer ") {
		key = strings.TrimSpace(key[7:])
		fixes = append(fixes, "removed a leading 'Bearer '")
	}
	inner := 0
	key = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			inner++
			return -1
		}
		return r
	}, key)
	if inner > 0 {
		fixes = append(fixes, fmt.Sprintf("removed %d line break/space/control character(s)", inner))
	}
	return key, fixes
}
