package main

import (
	"fmt"
	"io"
)

func runDocs(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "cbdev docs: not implemented")
	return 2
}
