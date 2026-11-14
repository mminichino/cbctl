package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/port"
)

func cmdDocsPort(args []string, stdout, stderr io.Writer) int {
	opts := port.Options{}
	var releases []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--src" && i+1 < len(args):
			i++
			opts.Src = args[i]
		case strings.HasPrefix(a, "--src="):
			opts.Src = strings.TrimPrefix(a, "--src=")
		case a == "--dst" && i+1 < len(args):
			i++
			opts.Dst = args[i]
		case strings.HasPrefix(a, "--dst="):
			opts.Dst = strings.TrimPrefix(a, "--dst=")
		case a == "--release" && i+1 < len(args):
			i++
			releases = append(releases, args[i])
		case strings.HasPrefix(a, "--release="):
			releases = append(releases, strings.TrimPrefix(a, "--release="))
		case a == "--sha" && i+1 < len(args):
			i++
			opts.SHA = args[i]
		case strings.HasPrefix(a, "--sha="):
			opts.SHA = strings.TrimPrefix(a, "--sha=")
		case a == "--force":
			opts.Force = true
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, `usage: cbdev docs port --src DIR --dst DIR [--release R]... [--sha SHA] [--force]

Port capellactl Markdown into docs/backlog using rewrite rules v1 (R1,R3,R5,R6,R13).
Releases: 1.5.0 (CAP-*.md), 1.0.0-architecture (00-architecture.md).
`)
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs port: unknown argument %q\n", a)
			return 2
		}
	}
	opts.Release = releases
	if opts.Src == "" || opts.Dst == "" {
		fmt.Fprintln(stderr, "cbdev docs port: --src and --dst are required")
		return 2
	}
	res, err := port.Port(opts)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs port: %v\n", err)
		return 1
	}
	for _, r := range res.Reviews {
		fmt.Fprintln(stderr, r)
	}
	fmt.Fprintf(stdout, "ported %d file(s), skipped %d\n", len(res.Wrote), len(res.Skipped))
	return 0
}

// cmdDocsExtractData is implemented in docs_extract.go.
