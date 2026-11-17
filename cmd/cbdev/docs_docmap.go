package main

import (
	"fmt"
	"io"
	"slices"

	"github.com/mminichino/cbctl/internal/docgen/coverage"
)

// cmdDocsDocMap checks docs/capella/doc-map.yaml: every capellactl document maps to a cbctl
// document that exists, or to a reason it was dropped. With --src it also checks the file's
// source list against a capellactl docs directory, so a new capellactl document is noticed.
func cmdDocsDocMap(args []string, stdout, stderr io.Writer) int {
	src := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--check":
			// The only mode; accepted so the Makefile reads like the other checks.
		case "--src":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "cbdev docs doc-map: --src needs a directory")
				return 2
			}
			i++
			src = args[i]
		case "-h", "--help":
			fmt.Fprint(stdout, "usage: cbdev docs doc-map [--check] [--src <capellactl>/docs]\n")
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs doc-map: unknown argument %q\n", a)
			return 2
		}
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs doc-map: %v\n", err)
		return 1
	}
	sources, mappings, err := coverage.LoadDocMap(root)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs doc-map: %v\n", err)
		return 1
	}
	var problems []string
	if src != "" {
		live, err := coverage.ListSourceDocs(src)
		if err != nil {
			fmt.Fprintf(stderr, "cbdev docs doc-map: %v\n", err)
			return 1
		}
		for _, d := range live {
			if !slices.Contains(sources, d) {
				problems = append(problems, fmt.Sprintf("%s: in %s but not in the sources of %s", d, src, coverage.DocMapPath))
			}
		}
		sources = live
	}
	problems = append(problems, coverage.CheckDocCoverage(sources, mappings, root)...)
	for _, p := range problems {
		fmt.Fprintf(stderr, "%s: %s\n", coverage.DocMapPath, p)
	}
	if len(problems) > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "%s: all %d capellactl documents are mapped\n", coverage.DocMapPath, len(sources))
	return 0
}
