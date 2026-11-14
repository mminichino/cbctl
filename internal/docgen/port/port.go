// Package port rewrites capellactl Markdown into cbctl docs/backlog form.
//
// MVP rules: R1, R3, R5, R6, R13 (plus a minimal bare-word R2 for prose).
package port

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// RulesVersion is recorded in SOURCE.md and ported-from footers.
const RulesVersion = 1

// Options control a Port run.
type Options struct {
	Src     string   // capellactl docs/ directory
	Dst     string   // cbctl docs/backlog directory
	Release []string // e.g. "1.5.0", "1.0.0-architecture"
	SHA     string   // full or short; empty → git rev-parse in src/..
	Force   bool
}

// Result summarizes a Port invocation.
type Result struct {
	Wrote   []string
	Skipped []string
	Reviews []string // "REVIEW path:line rule"
}

// Port applies the rewrite engine for the requested releases.
func Port(opts Options) (Result, error) {
	var res Result
	if opts.Src == "" || opts.Dst == "" {
		return res, fmt.Errorf("port: --src and --dst are required")
	}
	sha := opts.SHA
	if sha == "" {
		var err error
		sha, err = gitSHA(filepath.Join(opts.Src, ".."))
		if err != nil {
			return res, err
		}
	}
	if !opts.Force {
		if dirty, err := gitDirty(filepath.Join(opts.Src, "..")); err != nil {
			return res, err
		} else if dirty {
			return res, fmt.Errorf("port: source tree is dirty; commit or pass --force")
		}
	}
	sha7 := sha
	if len(sha7) > 7 {
		sha7 = sha7[:7]
	}

	releases := opts.Release
	if len(releases) == 0 {
		releases = []string{"1.5.0"}
	}
	for _, rel := range releases {
		switch {
		case rel == "1.0.0-architecture":
			in := filepath.Join(opts.Src, "release-1.0.0", "00-architecture.md")
			out := filepath.Join(opts.Dst, "release-1.0.0", "00-architecture.md")
			wrote, skipped, reviews, err := portFile(in, out, "architecture", "1.0.0", sha7, opts.Force)
			if err != nil {
				return res, err
			}
			if wrote != "" {
				res.Wrote = append(res.Wrote, wrote)
			}
			if skipped != "" {
				res.Skipped = append(res.Skipped, skipped)
			}
			res.Reviews = append(res.Reviews, reviews...)
		case strings.HasPrefix(rel, "1.5.0"):
			dir := filepath.Join(opts.Src, "release-1.5.0")
			entries, err := os.ReadDir(dir)
			if err != nil {
				return res, fmt.Errorf("port: read %s: %w", dir, err)
			}
			outDir := filepath.Join(opts.Dst, "release-1.5.0")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return res, err
			}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasPrefix(name, "CAP-") || !strings.HasSuffix(name, ".md") {
					continue
				}
				in := filepath.Join(dir, name)
				out := filepath.Join(outDir, name)
				wrote, skipped, reviews, err := portFile(in, out, "story", "1.5.0", sha7, opts.Force)
				if err != nil {
					return res, err
				}
				if wrote != "" {
					res.Wrote = append(res.Wrote, wrote)
				}
				if skipped != "" {
					res.Skipped = append(res.Skipped, skipped)
				}
				res.Reviews = append(res.Reviews, reviews...)
			}
		default:
			return res, fmt.Errorf("port: unsupported --release %q", rel)
		}
	}
	return res, nil
}

func portFile(in, out, profile, release, sha7 string, force bool) (wrote, skipped string, reviews []string, err error) {
	raw, err := os.ReadFile(in)
	if err != nil {
		return "", "", nil, err
	}
	if !force {
		if existing, err := os.ReadFile(out); err == nil && strings.Contains(string(existing), "<!-- ported-from:") {
			return "", out, nil, nil
		}
	}
	base := filepath.Base(in)
	srcPath := fmt.Sprintf("docs/release-%s/%s", release, base)
	outBytes, flags := Rewrite(string(raw), RewriteOpts{
		Profile:  profile,
		SHA7:     sha7,
		SrcPath:  srcPath,
		Release:  release,
		FileName: base,
	})
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", "", nil, err
	}
	if err := os.WriteFile(out, []byte(outBytes), 0o644); err != nil {
		return "", "", nil, err
	}
	for _, f := range flags {
		reviews = append(reviews, fmt.Sprintf("REVIEW %s:%d %s", out, f.Line, f.Rule))
	}
	return out, "", reviews, nil
}

// Flag is an unresolved rewrite decision.
type Flag struct {
	Rule string
	Line int
	Text string
}

// RewriteOpts configures Rewrite for one file.
type RewriteOpts struct {
	Profile  string // story | architecture | guide
	SHA7     string
	SrcPath  string // e.g. docs/release-1.5.0/CAP-066-….md
	Release  string
	FileName string
}

var (
	reInlineCode = regexp.MustCompile("`[^`]+`")
	reURL        = regexp.MustCompile(`https?://[^\s)]+`)
	reCmdLine    = regexp.MustCompile(`^(\s*(?:\$\s+)?)capellactl(\s+)`)
	reHelpPhrase = regexp.MustCompile("`help` topics")
	reHelpTopics = regexp.MustCompile(`\bhelp topics\b`)
	reHelpConfig = regexp.MustCompile(`\bhelp config\b`)
	reCapHelp    = regexp.MustCompile(`\bcapellactl help\b`)
	reEnvIT      = regexp.MustCompile(`\bCAPELLACTL_(IT_TARGET|IT_LOG|IT_REPORT)\b`)
	reEnvOther   = regexp.MustCompile(`\bCAPELLACTL_(PROFILE|CONFIG|HOME|QUIET)\b`)
	reCIPrefix   = regexp.MustCompile(`capellactl-ci-`)
	reDotDir     = regexp.MustCompile(`\.capellactl/`)
	reConfigPath = regexp.MustCompile(`~/\.config/capellactl`)
	reAPIVersion = regexp.MustCompile(`apiVersion:\s*capellactl/v1`)
	reModuleDot  = regexp.MustCompile(`\bcapellactl\.`)
	reBareWord   = regexp.MustCompile(`\bcapellactl\b`)
	rePortedFrom = regexp.MustCompile(`(?m)^<!-- ported-from:.*?-->\s*$`)
	rePortReview = regexp.MustCompile(`(?m)^<!-- port-review:.*?-->\s*$`)
)

// Rewrite applies ordered rules to one Markdown document. Line endings are normalized to \n.
func Rewrite(src string, opts RewriteOpts) (string, []Flag) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")
	// Drop trailing empty line from Split if file ended with newline — keep structure:
	// strings.Split keeps trailing empty element when src ends with \n.
	var flags []Flag
	inFence := false
	fenceMark := ""

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				inFence = true
				if strings.HasPrefix(trimmed, "```") {
					fenceMark = "```"
				} else {
					fenceMark = "~~~"
				}
				continue
			}
		} else {
			if strings.HasPrefix(trimmed, fenceMark) {
				inFence = false
				fenceMark = ""
			} else {
				// R1: command lines inside fences
				if reCmdLine.MatchString(line) {
					lines[i] = reCmdLine.ReplaceAllString(line, "${1}cbctl capella${2}")
				}
				// R5/R6 still apply inside fences for env/path tokens
				lines[i] = applyR5(lines[i])
				lines[i], flags = applyR6(lines[i], i+1, flags)
			}
			continue
		}

		// Outside fences: R3 first (needs literal `help` topics), then protect
		// URLs / inline code, rewrite spans (R1), then remaining prose rules.
		line = applyR3(line)
		protected, urls := protectURLs(line)
		protected, codeSpans := protectInlineCode(protected)
		for j, span := range codeSpans {
			inner := span[1 : len(span)-1]
			newInner := rewriteInlineCode(inner)
			codeSpans[j] = "`" + newInner + "`"
		}
		protected = applyR5(protected)
		protected, flags = applyR6(protected, i+1, flags)
		protected, flags = applyR2Bare(protected, i+1, flags)
		line = restoreInlineCode(protected, codeSpans)
		line = restoreURLs(line, urls)
		lines[i] = line
	}

	out := strings.Join(lines, "\n")
	// Strip prior footers/flags so re-runs are idempotent when Force regenerates.
	out = rePortReview.ReplaceAllString(out, "")
	out = rePortedFrom.ReplaceAllString(out, "")
	out = strings.TrimRight(out, "\n") + "\n"

	if opts.Profile == "story" || opts.Profile == "architecture" {
		if opts.SHA7 != "" && opts.SrcPath != "" {
			footer := fmt.Sprintf("<!-- ported-from: capellactl@%s %s · rules v%d -->\n", opts.SHA7, opts.SrcPath, RulesVersion)
			out = out + "\n" + footer
		}
	}
	if len(flags) > 0 {
		var b strings.Builder
		for _, f := range flags {
			fmt.Fprintf(&b, "<!-- port-review: %s line %d: %s -->\n", f.Rule, f.Line, f.Text)
		}
		out = out + b.String()
	}
	return out, flags
}

func rewriteInlineCode(inner string) string {
	if inner == "capellactl" {
		return "cbctl"
	}
	if strings.HasPrefix(inner, "capellactl ") {
		return "cbctl capella " + strings.TrimPrefix(inner, "capellactl ")
	}
	// R3 inside inline code
	inner = strings.ReplaceAll(inner, "capellactl help", "cbctl capella guide")
	inner = applyR5(inner)
	inner, _ = applyR6(inner, 0, nil)
	// Remaining bare tokens (e.g. user-agent strings): R2-lite.
	inner = reBareWord.ReplaceAllString(inner, "cbctl")
	return inner
}

func applyR3(s string) string {
	s = reCapHelp.ReplaceAllString(s, "cbctl capella guide")
	s = reHelpPhrase.ReplaceAllString(s, "`guide` topics")
	s = reHelpTopics.ReplaceAllString(s, "guide topics")
	s = reHelpConfig.ReplaceAllString(s, "guide config")
	return s
}

func applyR5(s string) string {
	s = reEnvIT.ReplaceAllString(s, "CBCTL_$1")
	s = reEnvOther.ReplaceAllString(s, "CBCTL_$1")
	return s
}

func applyR6(s string, line int, flags []Flag) (string, []Flag) {
	s = reCIPrefix.ReplaceAllString(s, "cbctl-ci-")
	s = reDotDir.ReplaceAllString(s, ".cbctl/")
	s = reConfigPath.ReplaceAllString(s, "~/.config/cbctl")
	s = reAPIVersion.ReplaceAllString(s, "apiVersion: cbctl/v1")
	if reModuleDot.MatchString(s) {
		flags = append(flags, Flag{Rule: "R6", Line: line, Text: truncate(s, 80)})
	}
	return s, flags
}

func applyR2Bare(s string, line int, flags []Flag) (string, []Flag) {
	// Minimal R2: rewrite remaining bare capellactl (not already handled) to cbctl.
	if !reBareWord.MatchString(s) {
		return s, flags
	}
	// Skip if it is part of longer tokens already handled (capellactl-ci-, paths).
	s2 := reBareWord.ReplaceAllStringFunc(s, func(m string) string {
		return "cbctl"
	})
	return s2, flags
}

func protectURLs(s string) (string, []string) {
	var urls []string
	out := reURL.ReplaceAllStringFunc(s, func(u string) string {
		urls = append(urls, u)
		return fmt.Sprintf("\x00URL%d\x00", len(urls)-1)
	})
	return out, urls
}

func restoreURLs(s string, urls []string) string {
	for i, u := range urls {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00URL%d\x00", i), u)
	}
	return s
}

func protectInlineCode(s string) (string, []string) {
	var spans []string
	out := reInlineCode.ReplaceAllStringFunc(s, func(c string) string {
		spans = append(spans, c)
		return fmt.Sprintf("\x00CODE%d\x00", len(spans)-1)
	})
	return out, spans
}

func restoreInlineCode(s string, spans []string) string {
	for i, c := range spans {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00CODE%d\x00", i), c)
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func gitSHA(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("port: git rev-parse in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitDirty(dir string) (bool, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("port: git status in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
