package main

import "io"

var apiCommands = map[string]groupFunc{
	"responses": cmdAPIResponses,
}

func runAPI(args []string, stdout, stderr io.Writer) int {
	return dispatch("api", apiCommands, args, stdout, stderr)
}
