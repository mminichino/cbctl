// Package notices keeps THIRD_PARTY_NOTICES.md: the Go modules compiled into the cbctl release
// binaries, their versions and licences (detected from each module's licence files), checked
// against an SPDX licence policy. It is the Go port of capellactl's scripts/third-party-notices.py
// (CAP-044 AC3/AC4).
package notices

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// File is the notices file at the repository root.
const File = "THIRD_PARTY_NOTICES.md"

// PolicyFile is where the policy lives, named in "not allowed" problems.
const PolicyFile = "internal/ci/notices/licence-policy.json"

// Unknown is the licence of a module whose licence files name no licence this package knows.
const Unknown = "UNKNOWN"

// Platforms are the GOOS values cbctl is released for (.goreleaser.yaml).
var Platforms = []string{"darwin", "linux", "windows"}

//go:embed licence-policy.json
var policyJSON []byte

// Dep is one module compiled into the binaries.
type Dep struct {
	Module  string
	Version string
	Licence string // an SPDX expression, or Unknown
	// Needed lists the platforms that compile the module in, empty when every platform does.
	Needed string
}

// Module is one row of `go list -deps`: a dependency's module path, version and source folder.
type Module struct {
	Path, Version, Dir string
}

// Lister lists the non-main modules the binary package depends on when built for goos.
type Lister func(ctx context.Context, goos string) ([]Module, error)

// Policy is the set of allowed SPDX licence ids.
type Policy map[string]bool

// LoadPolicy reads the policy from path, or the built-in licence-policy.json when path is "".
func LoadPolicy(path string) (Policy, error) {
	data := policyJSON
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading licence policy: %w", err)
		}
		data = b
	}
	var doc struct {
		Allowed []string `json:"allowed"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing licence policy: %w", err)
	}
	if len(doc.Allowed) == 0 {
		return nil, errors.New("licence policy allows no licence")
	}
	p := Policy{}
	for _, id := range doc.Allowed {
		p[strings.TrimSpace(id)] = true
	}
	return p, nil
}

// Allowed reports whether an SPDX expression can be satisfied with allowed licences: OR needs
// one side, AND both, and "X WITH exception" counts as X.
func Allowed(expr string, p Policy) bool {
	expr = strings.TrimSpace(expr)
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && balanced(expr[1:len(expr)-1]) {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if parts := splitTop(expr, " OR "); len(parts) > 1 {
		return slices.ContainsFunc(parts, func(s string) bool { return Allowed(s, p) })
	}
	if parts := splitTop(expr, " AND "); len(parts) > 1 {
		for _, s := range parts {
			if !Allowed(s, p) {
				return false
			}
		}
		return true
	}
	id, _, _ := strings.Cut(expr, " WITH ")
	return p[strings.TrimSpace(id)]
}

func balanced(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// splitTop splits expr on op outside parentheses.
func splitTop(expr, op string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 && strings.HasPrefix(expr[i:], op) {
			parts = append(parts, expr[start:i])
			i += len(op) - 1
			start = i + 1
		}
	}
	return append(parts, expr[start:])
}

// signature recognises one licence in a licence file. All of the phrases must appear and none
// of the excluded ones; text is compared with whitespace collapsed and case folded.
type signature struct {
	id      string
	all     []string
	without []string
}

// signatures are tried in order; the first match per licence family wins.
var signatures = []signature{
	{id: "AGPL-3.0-only", all: []string{"gnu affero general public license"}},
	{id: "LGPL-3.0-only", all: []string{"gnu lesser general public license", "version 3"}},
	{id: "LGPL-2.1-only", all: []string{"gnu lesser general public license"}},
	{id: "GPL-3.0-only", all: []string{"gnu general public license", "version 3"}, without: []string{"lesser", "affero"}},
	{id: "GPL-2.0-only", all: []string{"gnu general public license", "version 2"}, without: []string{"lesser", "affero"}},
	{id: "MPL-2.0", all: []string{"mozilla public license", "2.0"}},
	{id: "Apache-2.0", all: []string{"apache license", "version 2.0"}},
	{id: "MIT-0", all: []string{"permission is hereby granted, free of charge", "without restriction"},
		without: []string{"the above copyright notice and this permission notice shall be included"}},
	{id: "MIT", all: []string{"permission is hereby granted, free of charge", "the above copyright notice and this permission notice shall be included"}},
	{id: "ISC", all: []string{"permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted",
		"copyright notice and this permission notice appear in all copies"}},
	{id: "0BSD", all: []string{"permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted"}},
	{id: "BSD-3-Clause", all: []string{"redistribution and use in source and binary forms", "neither the name"}},
	{id: "BSD-3-Clause", all: []string{"redistribution and use in source and binary forms", "names of its contributors may be used to endorse"}},
	{id: "BSD-2-Clause", all: []string{"redistribution and use in source and binary forms"}},
	{id: "Unlicense", all: []string{"this is free and unencumbered software released into the public domain"}},
}

var space = regexp.MustCompile(`\s+`)

// Detect names the licences in one licence file's text, as an SPDX expression ("A AND B" when
// the file carries several), or Unknown.
func Detect(text string) string {
	t := strings.ToLower(space.ReplaceAllString(text, " "))
	var ids []string
	family := map[string]bool{}
	for _, s := range signatures {
		fam := strings.SplitN(s.id, "-", 2)[0]
		if s.id == "0BSD" {
			fam = "ISC" // 0BSD is ISC without the notice condition
		}
		if family[fam] || !containsAll(t, s.all) || containsAny(t, s.without) {
			continue
		}
		family[fam] = true
		ids = append(ids, s.id)
	}
	if len(ids) == 0 {
		return Unknown
	}
	return strings.Join(ids, " AND ")
}

func containsAll(t string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(t, p) {
			return false
		}
	}
	return true
}

func containsAny(t string, parts []string) bool {
	return slices.ContainsFunc(parts, func(p string) bool { return strings.Contains(t, p) })
}

var licenceFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|unlicense)([.-].*)?$`)

// DetectDir names the licence of the module in dir from its top-level licence files.
func DetectDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading module %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !licenceFile.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		if id := Detect(string(b)); id != Unknown {
			for _, part := range strings.Split(id, " AND ") {
				if !slices.Contains(ids, part) {
					ids = append(ids, part)
				}
			}
		}
	}
	if len(ids) == 0 {
		return Unknown, nil
	}
	return strings.Join(ids, " AND "), nil
}

// GoList is the Lister that runs `go list -deps` on pkg (for example ./cmd/cbctl) in root.
func GoList(root, pkg string) Lister {
	return func(ctx context.Context, goos string) ([]Module, error) {
		cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-f",
			`{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{if .Replace}}{{.Replace.Dir}}{{else}}{{.Dir}}{{end}}{{end}}{{end}}`, pkg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOFLAGS=-mod=mod")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s: %w: %s", goos, err, strings.TrimSpace(stderr.String()))
		}
		var mods []Module
		seen := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 3 || seen[f[0]] {
				continue
			}
			seen[f[0]] = true
			mods = append(mods, Module{Path: f[0], Version: f[1], Dir: f[2]})
		}
		return mods, nil
	}
}

// Collect lists the modules compiled in on each of Platforms and detects their licences.
func Collect(ctx context.Context, list Lister) ([]Dep, error) {
	type entry struct {
		mod Module
		on  []string
	}
	byPath := map[string]*entry{}
	for _, goos := range Platforms {
		mods, err := list(ctx, goos)
		if err != nil {
			return nil, err
		}
		for _, m := range mods {
			e := byPath[m.Path]
			if e == nil {
				e = &entry{mod: m}
				byPath[m.Path] = e
			}
			e.on = append(e.on, goos)
		}
	}
	deps := make([]Dep, 0, len(byPath))
	for path, e := range byPath {
		lic, err := DetectDir(e.mod.Dir)
		if err != nil {
			return nil, err
		}
		d := Dep{Module: path, Version: e.mod.Version, Licence: lic}
		if len(e.on) < len(Platforms) {
			d.Needed = strings.Join(e.on, ", ")
		}
		deps = append(deps, d)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Module < deps[j].Module })
	return deps, nil
}

const header = `# Third-party notices

cbctl (Apache-2.0, see LICENSE and NOTICE) is built with the open-source Go modules below, which are
distributed under their own licences. Generated by ` + "`cbdev ci notices`" + ` (` + "`make notices`" + `); do not
edit by hand. "Needed" names the platforms that compile a module in when not all of them do.

| Module | Version | Licence | Needed | Project |
|---|---|---|---|---|
`

// Render writes the notices file for deps.
func Render(deps []Dep) string {
	var b strings.Builder
	b.WriteString(header)
	for _, d := range deps {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | https://pkg.go.dev/%s |\n", d.Module, d.Version, d.Licence, d.Needed, d.Module)
	}
	return b.String()
}

// Parse reads the rows of a notices file, by module path.
func Parse(text string) map[string]Dep {
	rows := map[string]Dep{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Module ") {
			continue
		}
		f := strings.Split(strings.Trim(line, "|"), "|")
		if len(f) < 4 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		rows[f[0]] = Dep{Module: f[0], Version: f[1], Licence: f[2], Needed: f[3]}
	}
	return rows
}

// Check returns the problems of text against the modules compiled in: a module missing from the
// file, a version or licence that differs, a row for a module no longer used, and a licence the
// policy does not allow.
func Check(text string, deps []Dep, p Policy) []string {
	rows := Parse(text)
	var problems []string
	used := map[string]bool{}
	for _, d := range deps {
		used[d.Module] = true
		row, ok := rows[d.Module]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s %s is not in %s", d.Module, d.Version, File))
		case row.Version != d.Version:
			problems = append(problems, fmt.Sprintf("%s: %s says version %s, the build uses %s", d.Module, File, row.Version, d.Version))
		case row.Licence != d.Licence:
			problems = append(problems, fmt.Sprintf("%s: %s says %s, the module's licence files say %s", d.Module, File, row.Licence, d.Licence))
		case row.Needed != d.Needed:
			problems = append(problems, fmt.Sprintf("%s: %s says it is needed on %q, the build needs it on %q", d.Module, File, row.Needed, d.Needed))
		}
		if !Allowed(d.Licence, p) {
			problems = append(problems, fmt.Sprintf("%s %s is licensed %s, which is not allowed by %s", d.Module, d.Version, d.Licence, PolicyFile))
		}
	}
	var gone []string
	for m := range rows {
		if !used[m] {
			gone = append(gone, m)
		}
	}
	sort.Strings(gone)
	for _, m := range gone {
		problems = append(problems, fmt.Sprintf("%s is in %s but no longer compiled in", m, File))
	}
	return problems
}
