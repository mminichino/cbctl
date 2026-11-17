package main

import (
	"fmt"
	"io"
)

var docsCommands = map[string]groupFunc{
	"port":         cmdDocsPort,
	"extract-data": cmdDocsExtractData,
	"index":        cmdDocsIndex,
	"coverage":     cmdDocsCoverage,
	"ac-coverage":  cmdDocsACCoverage,
	"reference":    cmdDocsReference,
	"links":        cmdDocsLinks,
	"doc-map":      cmdDocsDocMap,
}

func runDocs(args []string, stdout, stderr io.Writer) int {
	return dispatch("docs", docsCommands, args, stdout, stderr)
}

// dispatch runs the subcommand named by args[0] from cmds.
func dispatch(group string, cmds map[string]groupFunc, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "cbdev %s: missing command\n", group)
		return 2
	}
	c, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "cbdev %s: unknown command %q\n", group, args[0])
		return 2
	}
	return c(args[1:], stdout, stderr)
}
