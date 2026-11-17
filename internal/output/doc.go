// Package output renders everything cbctl shows on a terminal: labeled status lines
// ("[ok] - ..."), action hints, data lines, tables, key: value objects, JSON documents and
// progress. It owns the color decision, which is made per output stream.
//
// Color precedence, highest first (see Decider.Color):
//
//  1. --color=never / --color=always
//  2. NO_COLOR set to a non-empty value: off
//  3. TERM=dumb: off
//  4. CLICOLOR_FORCE (or its alias FORCE_COLOR) set to a non-empty value other than "0": on
//  5. FORCE_COLOR=0: off
//  6. CLICOLOR=0: off
//  7. otherwise on only if that stream is a terminal
//
// Only the bracketed label is colored, using plain 16-color SGR escape codes that are always
// closed with ESC[0m. Message text, tables, key: value output and JSON are never colored.
//
// The package depends on the standard library, golang.org/x/term (and golang.org/x/sys on
// Windows) and internal/redact. It imports nothing from cli, so every command package can use it.
package output
