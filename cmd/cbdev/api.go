package main

import (
	"fmt"
	"io"
)

func runAPI(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "cbdev api: not implemented")
	return 2
}
