package cli

import (
	"testing"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/output"
)

// isolatePrinter gives the test its own process Printer and restores the previous one when the
// test ends. A test that runs the shared rootCmd directly (not through Run or Execute, which
// install a fresh Printer per invocation) would otherwise leave "-o json" or a --color decision
// on the default Printer. The next test in the process, such as the same test under -count=2,
// would then find [ok] and [info] lines on stderr instead of stdout.
func isolatePrinter(t *testing.T) {
	t.Helper()
	prev := logging.SetPrinter(output.New(nil, nil, output.SystemDecider()))
	t.Cleanup(func() { logging.SetPrinter(prev) })
}
