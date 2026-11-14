// Command cbdev is the cbctl developer tool: documentation generators and checkers, API snapshot
// refresh and CI utilities. It is not shipped with releases.
package main

import (
	"fmt"
	"io"
	"os"
)

// groupFunc runs one command group (docs, api, ci) and returns the process exit code.
type groupFunc func(args []string, stdout, stderr io.Writer) int

var groups = map[string]groupFunc{
	"docs": runDocs,
	"api":  runAPI,
	"ci":   runCI,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(stdout)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	g, ok := groups[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "cbdev: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	return g(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: cbdev <group> <command> [flags]

groups:
  docs   port | extract-data | index | coverage | ac-coverage | reference | links
  api    responses
  ci     release-gate | scan-secrets | cover-gate
`)
}
