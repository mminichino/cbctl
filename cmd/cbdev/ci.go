package main

import (
	"fmt"
	"io"
)

func runCI(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "cbdev ci: not implemented")
	return 2
}
