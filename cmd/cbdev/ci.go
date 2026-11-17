package main

import "io"

var ciCommands = map[string]groupFunc{
	"release-gate": cmdCIReleaseGate,
	"scan-secrets": cmdCIScanSecrets,
	"cover-gate":   cmdCICoverGate,
	"dedupe":       cmdCIDedupe,
}

func runCI(args []string, stdout, stderr io.Writer) int {
	return dispatch("ci", ciCommands, args, stdout, stderr)
}
