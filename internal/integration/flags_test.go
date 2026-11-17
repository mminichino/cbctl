//go:build integration

package integration_test

import "flag"

// flagLookup returns the value of a registered test flag after flag.Parse (m.Run parses lazily,
// so parse here; testing.Init has registered the test.* flags before TestMain runs).
func flagLookup(name string) string {
	if !flag.Parsed() {
		flag.Parse()
	}
	if f := flag.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}
