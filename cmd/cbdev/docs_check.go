package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/accoverage"
	"github.com/mminichino/cbctl/internal/docgen/links"
	"github.com/mminichino/cbctl/internal/docgen/reference"
)

func cmdDocsACCoverage(args []string, stdout, stderr io.Writer) int {
	opts := accoverage.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--docs" && i+1 < len(args):
			i++
			opts.Docs = append(opts.Docs, args[i])
		case strings.HasPrefix(a, "--docs="):
			opts.Docs = append(opts.Docs, strings.TrimPrefix(a, "--docs="))
		case a == "--tests" && i+1 < len(args):
			i++
			opts.Tests = args[i]
		case strings.HasPrefix(a, "--tests="):
			opts.Tests = strings.TrimPrefix(a, "--tests=")
		case a == "--strict":
			opts.Strict = true
		case a == "--all":
			opts.All = true
		case a == "--port-table":
			// optional: requires capellactl checkout; skipped when absent
			fmt.Fprintln(stderr, "cbdev docs ac-coverage: --port-table not implemented yet (skipped)")
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, "usage: cbdev docs ac-coverage [--docs DIR]… [--tests DIR] [--strict] [--all]\n")
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs ac-coverage: unknown argument %q\n", a)
			return 2
		}
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
		return 1
	}
	// Make relative docs paths absolute from repo root when needed.
	for i, d := range opts.Docs {
		if !filepath.IsAbs(d) {
			opts.Docs[i] = filepath.Join(root, d)
		}
	}
	if opts.Tests == "" {
		opts.Tests = root
	} else if !filepath.IsAbs(opts.Tests) {
		opts.Tests = filepath.Join(root, opts.Tests)
	}
	res, err := accoverage.Check(opts)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs ac-coverage: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, res.Message)
	if !res.OK {
		return 1
	}
	return 0
}

func cmdDocsReference(args []string, stdout, stderr io.Writer) int {
	check := false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "-h", "--help":
			fmt.Fprint(stdout, "usage: cbdev docs reference [--check]\n")
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs reference: unknown argument %q\n", a)
			return 2
		}
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs reference: %v\n", err)
		return 1
	}
	if err := reference.Write(root, check); err != nil {
		fmt.Fprintf(stderr, "cbdev docs reference: %v\n", err)
		return 1
	}
	if check {
		fmt.Fprintln(stdout, "docs/capella/reference.md is up to date")
	} else {
		fmt.Fprintln(stdout, "written: docs/capella/reference.md")
	}
	return 0
}

func cmdDocsLinks(args []string, stdout, stderr io.Writer) int {
	check := false
	var roots []string
	for _, a := range args {
		switch {
		case a == "--check":
			check = true
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, "usage: cbdev docs links [--check] [ROOT…]\n")
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "cbdev docs links: unknown argument %q\n", a)
			return 2
		default:
			roots = append(roots, a)
		}
	}
	_ = check // always check; flag kept for Makefile compatibility
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs links: %v\n", err)
		return 1
	}
	if len(roots) == 0 {
		roots = []string{filepath.Join(root, "docs", "backlog"), filepath.Join(root, "docs", "capella"), filepath.Join(root, "README.md")}
	}
	var issues []links.Issue
	for _, r := range roots {
		if strings.HasSuffix(strings.ToLower(r), ".md") {
			issues = append(issues, links.CheckFile(r)...)
			continue
		}
		issues = append(issues, links.Check(r)...)
	}
	if len(issues) > 0 {
		for _, iss := range issues {
			fmt.Fprintln(stderr, iss.String())
		}
		fmt.Fprintf(stderr, "docs links: %d issue(s)\n", len(issues))
		return 1
	}
	fmt.Fprintln(stdout, "docs links OK")
	return 0
}
