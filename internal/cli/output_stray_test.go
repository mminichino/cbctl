package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// strayWrite matches the ways a command can write to a stream without the output helpers:
// fmt.Print*, fmt.Fprint* to anything but an in-memory sink, and the process streams.
var strayWrite = regexp.MustCompile(`fmt\.Print|fmt\.Fprint[a-z]*\(\s*([A-Za-z_.&()]+)|\bprintln\(|os\.Std(out|err)|fatih/color`)

// strayAllowed are the fmt.Fprint* targets that are not a terminal stream: a tabwriter or a
// builder that is flushed through a helper afterwards.
var strayAllowed = map[string]bool{"tw": true, "&b": true}

// TestNoStrayOutputWrites keeps the P6 migration from regressing: every write to a terminal
// stream in internal/cli goes through the output helpers (writeLabeled, writeProse, the Renderer,
// logging), so redaction, glyph and color rules apply to all of it.
func TestNoStrayOutputWrites(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}
			if f == "deps.go" && strings.Contains(line, "os.Std") {
				continue // the process streams enter here, once, as Deps defaults
			}
			m := strayWrite.FindStringSubmatch(line)
			if m == nil || strayAllowed[m[1]] {
				continue
			}
			t.Errorf("%s:%d writes outside the output helpers: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}
