package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mminichino/cbctl/internal/ci/notices"
)

// cmdCINotices writes THIRD_PARTY_NOTICES.md from the modules compiled into ./cmd/cbctl, or
// with --check fails (exit 1) when the file is stale or a licence is not allowed.
func cmdCINotices(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci notices", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "fail when "+notices.File+" is stale or a licence is not allowed")
	root := fs.String("root", ".", "repository root")
	pkg := fs.String("pkg", "./cmd/cbctl", "the released binary's package")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	policy, err := notices.LoadPolicy("")
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	deps, err := notices.Collect(ctx, notices.GoList(*root, *pkg))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	path := filepath.Join(*root, notices.File)
	if *check {
		text, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v; run 'make notices'\n", err)
			return 1
		}
		problems := notices.Check(string(text), deps, policy)
		for _, p := range problems {
			fmt.Fprintln(stderr, "error:", p)
		}
		if len(problems) > 0 {
			fmt.Fprintln(stderr, "run 'make notices' after a dependency change; a licence that is not allowed needs a different module or the code owner's approval")
			return 1
		}
		fmt.Fprintf(stdout, "%s is up to date; every dependency licence is allowed\n", notices.File)
		return 0
	}
	bad := 0
	for _, d := range deps {
		if !notices.Allowed(d.Licence, policy) {
			fmt.Fprintf(stderr, "error: %s %s is licensed %s, which is not allowed by %s\n", d.Module, d.Version, d.Licence, notices.PolicyFile)
			bad++
		}
	}
	if err := os.WriteFile(path, []byte(notices.Render(deps)), 0o644); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%d modules)\n", notices.File, len(deps))
	if bad > 0 {
		return 1
	}
	return 0
}
