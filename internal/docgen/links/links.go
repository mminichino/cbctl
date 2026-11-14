// Package links checks relative Markdown links and heading anchors.
package links

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	linkRE  = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	fenceRE = regexp.MustCompile("(?m)^(?: {0,3})(`{3,}|~{3,})")
)

// Issue is one broken link or missing anchor.
type Issue struct {
	File string
	Line int
	Msg  string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Msg)
}

// Check walks roots for *.md and verifies relative links and anchors.
// External http(s) links are skipped. Fenced code blocks are ignored.
func Check(roots ...string) []Issue {
	if len(roots) == 0 {
		roots = []string{"docs", "."}
	}
	var issues []Issue
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(path), ".md") {
				return nil
			}
			// Skip walking into other trees when root is "."
			if root == "." {
				base := filepath.Base(path)
				if path != base && !strings.HasPrefix(path, "docs"+string(filepath.Separator)) {
					return nil
				}
			}
			issues = append(issues, CheckFile(path)...)
			return nil
		})
	}
	return issues
}

// CheckFile verifies relative links in one Markdown file.
func CheckFile(path string) []Issue {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []Issue{{File: path, Line: 1, Msg: err.Error()}}
	}
	text := string(raw)
	lines := strings.Split(text, "\n")
	active := nonFenceLines(lines)

	var issues []Issue
	for i, line := range lines {
		if !active[i] {
			continue
		}
		for _, m := range linkRE.FindAllStringSubmatchIndex(line, -1) {
			target := line[m[4]:m[5]]
			target = strings.TrimSpace(target)
			if target == "" || strings.HasPrefix(target, "#") && !strings.Contains(target[1:], "/") {
				// same-file anchor
				if strings.HasPrefix(target, "#") {
					if !hasAnchor(text, target[1:]) {
						issues = append(issues, Issue{path, i + 1, fmt.Sprintf("missing anchor %q", target)})
					}
				}
				continue
			}
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "mailto:") {
				continue
			}
			// strip title "..."
			if idx := strings.IndexAny(target, " \t"); idx >= 0 {
				target = target[:idx]
			}
			frag := ""
			if hash := strings.IndexByte(target, '#'); hash >= 0 {
				frag = target[hash+1:]
				target = target[:hash]
			}
			if target == "" {
				if frag != "" && !hasAnchor(text, frag) {
					issues = append(issues, Issue{path, i + 1, fmt.Sprintf("missing anchor #%s", frag)})
				}
				continue
			}
			dest := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			fi, err := os.Stat(dest)
			if err != nil || fi.IsDir() {
				// allow directory links that resolve to README? keep strict: file must exist
				if err != nil {
					issues = append(issues, Issue{path, i + 1, fmt.Sprintf("broken link %q", target)})
				}
				continue
			}
			if frag != "" && strings.HasSuffix(strings.ToLower(dest), ".md") {
				body, err := os.ReadFile(dest)
				if err != nil {
					issues = append(issues, Issue{path, i + 1, fmt.Sprintf("cannot read %q", dest)})
					continue
				}
				if !hasAnchor(string(body), frag) {
					issues = append(issues, Issue{path, i + 1, fmt.Sprintf("missing anchor %q in %s", frag, target)})
				}
			}
		}
	}
	return issues
}

func nonFenceLines(lines []string) []bool {
	active := make([]bool, len(lines))
	inFence := false
	var fenceMark string
	for i, line := range lines {
		trim := strings.TrimRight(line, "\r")
		if m := fenceRE.FindStringSubmatch(trim); m != nil {
			mark := m[1][:1] // ` or ~
			n := 0
			for _, c := range m[1] {
				if string(c) == mark {
					n++
				} else {
					break
				}
			}
			fence := strings.Repeat(mark, n)
			if !inFence {
				inFence = true
				fenceMark = fence
				active[i] = false
				continue
			}
			if strings.HasPrefix(strings.TrimLeft(trim, " "), fenceMark) {
				inFence = false
				fenceMark = ""
				active[i] = false
				continue
			}
		}
		active[i] = !inFence
	}
	return active
}

// Slug returns a GitHub-style heading slug.
func Slug(heading string) string {
	heading = strings.TrimSpace(heading)
	heading = strings.TrimLeft(heading, "#")
	heading = strings.TrimSpace(heading)
	// strip trailing custom anchors {#...}
	if i := strings.Index(heading, "{#"); i >= 0 {
		heading = strings.TrimSpace(heading[:i])
	}
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteByte('-')
		case r == '_':
			b.WriteByte('_')
		default:
			// strip punctuation including emoji; keep nothing
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func hasAnchor(text, frag string) bool {
	frag = strings.TrimSpace(frag)
	if frag == "" {
		return true
	}
	// explicit HTML id
	if strings.Contains(text, `id="`+frag+`"`) || strings.Contains(text, `id='`+frag+`'`) {
		return true
	}
	lines := strings.Split(text, "\n")
	active := nonFenceLines(lines)
	counts := map[string]int{}
	for i, line := range lines {
		if !active[i] {
			continue
		}
		trim := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if !strings.HasPrefix(trim, "#") {
			continue
		}
		// require space after hashes
		j := 0
		for j < len(trim) && trim[j] == '#' {
			j++
		}
		if j == 0 || j > 6 || (j < len(trim) && trim[j] != ' ') {
			continue
		}
		slug := Slug(trim)
		n := counts[slug]
		counts[slug] = n + 1
		candidate := slug
		if n > 0 {
			candidate = fmt.Sprintf("%s-%d", slug, n)
		}
		if candidate == frag {
			return true
		}
	}
	return false
}
