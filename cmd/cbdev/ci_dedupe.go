package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/mminichino/cbctl/internal/ci/dedupe"
)

// fixtureLister serves a workflow_runs JSON file (tests and dry runs).
type fixtureLister struct{ path string }

func (f fixtureLister) ListRuns(context.Context, string, string) ([]dedupe.RunRef, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	return dedupe.ParseRuns(raw)
}

// cmdCIDedupe writes skip=true|false to $GITHUB_OUTPUT (and stdout). It always exits 0 once
// its flags parse: a run list it cannot read is a ::warning:: and skip=false, so an API outage
// never silently cancels a night.
func cmdCIDedupe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci dedupe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", envOr("GITHUB_REPOSITORY", "mminichino/cbctl"), "OWNER/NAME")
	workflow := fs.String("workflow", "nightly.yml", "workflow file name")
	runID := fs.String("run-id", os.Getenv("GITHUB_RUN_ID"), "this run's ID (default $GITHUB_RUN_ID)")
	window := fs.Duration("window", dedupe.Window, "a dispatch within this long of a success on the same commit is a duplicate")
	runsJSON := fs.String("runs-json", "", "path to a workflow_runs JSON fixture (skip gh api)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, err := strconv.ParseInt(*runID, 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintf(stderr, "cbdev ci dedupe: --run-id must be a positive run ID (got %q)\n", *runID)
		return 2
	}

	var l dedupe.Lister = dedupe.GH{}
	if *runsJSON != "" {
		l = fixtureLister{path: *runsJSON}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	skip, reason, err := dedupe.Check(ctx, l, *repo, *workflow, id, *window)
	if err != nil {
		fmt.Fprintf(stdout, "::warning::dedupe: %v; running anyway\n", err)
	}
	fmt.Fprintf(stdout, "dedupe: skip=%v (%s)\n", skip, reason)
	if err := writeOutput("skip", strconv.FormatBool(skip)); err != nil {
		fmt.Fprintf(stderr, "cbdev ci dedupe: %v\n", err)
		return 1
	}
	return 0
}

// writeOutput appends name=value to $GITHUB_OUTPUT when it is set.
func writeOutput(name, value string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening GITHUB_OUTPUT: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%s=%s\n", name, value); err != nil {
		_ = f.Close() // the write error is the one to report
		return fmt.Errorf("writing GITHUB_OUTPUT: %w", err)
	}
	return f.Close()
}
