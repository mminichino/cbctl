// Package accoverage checks that story acceptance criteria have tagged Go tests.
package accoverage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	titleRE = regexp.MustCompile(`(?m)^#\s+(CAP-\d{3})\b`)
	acRE    = regexp.MustCompile(`(?m)^### (AC\d+)\b`)
	// Subject is PascalCase (no underscores). Do not use \b after ACn: '_' is a word char.
	testRE = regexp.MustCompile(`func Test[A-Za-z0-9]+_CAP(\d{3})_AC(\d+)(?:_|$|\()`)
)

// Options configures a coverage scan.
type Options struct {
	Docs   []string
	Tests  string
	Strict bool
	All    bool
}

// Result summarizes AC coverage.
type Result struct {
	Covered int
	Total   int
	Stories int
	Failing []string // Implemented (or --strict/--all) gaps
	Pending []string // In progress gaps (non-strict)
	OK      bool
	Message string
}

// Check scans docs and tests. Exit semantics: OK false when Failing is non-empty.
func Check(opts Options) (Result, error) {
	if len(opts.Docs) == 0 {
		opts.Docs = []string{"docs/backlog/release-1.5.0"}
	}
	if opts.Tests == "" {
		opts.Tests = "."
	}
	acs, err := storyACs(opts.Docs, opts.All)
	if err != nil {
		return Result{}, err
	}
	have, err := taggedTests(opts.Tests)
	if err != nil {
		return Result{}, err
	}
	var failing, pending []string
	total := 0
	for sid, info := range acs {
		for _, ac := range info.acs {
			total++
			key := sid + "|" + ac
			if have[key] {
				continue
			}
			label := sid + " " + ac
			if info.status == "Implemented" || opts.Strict || opts.All {
				failing = append(failing, label)
			} else {
				pending = append(pending, label)
			}
		}
	}
	sort.Strings(failing)
	sort.Strings(pending)
	covered := total - len(failing) - len(pending)
	r := Result{
		Covered: covered, Total: total, Stories: len(acs),
		Failing: failing, Pending: pending, OK: len(failing) == 0,
	}
	if len(pending) > 0 {
		r.Message += fmt.Sprintf("In progress: %d acceptance criteria not tested yet:\n", len(pending))
		for _, m := range pending {
			r.Message += "  " + m + "\n"
		}
	}
	if len(failing) > 0 {
		r.Message += fmt.Sprintf("FAIL: %d acceptance criteria of Implemented stories have no tagged test:\n", len(failing))
		for _, m := range failing {
			r.Message += "  " + m + "\n"
		}
	} else {
		r.Message += fmt.Sprintf("AC coverage OK: %d/%d acceptance criteria in %d stories have tagged tests\n",
			covered, total, len(acs))
	}
	return r, nil
}

type storyInfo struct {
	status string
	acs    []string
}

func storyACs(docs []string, includeAll bool) (map[string]storyInfo, error) {
	out := map[string]storyInfo{}
	checked := map[string]bool{"In progress": true, "Implemented": true}
	for _, dir := range docs {
		matches, err := filepath.Glob(filepath.Join(dir, "CAP-*.md"))
		if err != nil {
			return nil, err
		}
		for _, f := range matches {
			raw, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			text := string(raw)
			m := titleRE.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			status := ""
			lines := strings.Split(text, "\n")
			for i, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "| Epic | Priority | Points |") && i+2 < len(lines) {
					cells := strings.Split(strings.Trim(strings.TrimSpace(lines[i+2]), "|"), "|")
					status = strings.TrimSpace(cells[len(cells)-1])
					break
				}
			}
			if !includeAll && !checked[status] {
				continue
			}
			var acs []string
			for _, am := range acRE.FindAllStringSubmatch(text, -1) {
				acs = append(acs, am[1])
			}
			out[m[1]] = storyInfo{status: status, acs: acs}
		}
	}
	return out, nil
}

func taggedTests(root string) (map[string]bool, error) {
	found := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// skip vendor / .git
		if strings.Contains(path, string(filepath.Separator)+"vendor"+string(filepath.Separator)) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range testRE.FindAllStringSubmatch(string(raw), -1) {
			sid := "CAP-" + m[1]
			ac := "AC" + m[2]
			found[sid+"|"+ac] = true
		}
		return nil
	})
	return found, err
}
