package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mminichino/cbctl/internal/ci/releasegate"
)

func cmdCIReleaseGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci release-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	commit := fs.String("commit", "", "release commit SHA (required)")
	repo := fs.String("repo", envOr("GITHUB_REPOSITORY", "couchbaselabs/cbctl"), "OWNER/NAME")
	branch := fs.String("branch", "main", "branch the nightly runs on")
	workflow := fs.String("workflow", "nightly.yml", "nightly workflow file name")
	nights := fs.Int("nights", 3, "consecutive green nights required")
	runsJSON := fs.String("runs-json", "", "path to a workflow_runs JSON fixture (skip gh api)")
	reportOnly := fs.Bool("report-only", false, "print the verdict and always exit 0")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *commit == "" {
		fmt.Fprintln(stderr, "cbdev ci release-gate: --commit is required")
		return 2
	}

	var (
		runs []releasegate.Run
		err  error
	)
	if *runsJSON != "" {
		raw, readErr := os.ReadFile(*runsJSON)
		if readErr != nil {
			fmt.Fprintf(stderr, "release-gate: read %s: %v\n", *runsJSON, readErr)
			return 1
		}
		runs, err = releasegate.ParseRunsJSON(raw)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		runs, err = releasegate.FetchRuns(ctx, *repo, *workflow, *branch)
	}
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	ok, lines := releasegate.Evaluate(runs, *commit, *nights)
	fmt.Fprintf(stdout, "release gate: last %d nightly runs of %s on %s\n", *nights, *workflow, *branch)
	for _, line := range lines {
		fmt.Fprintf(stdout, "  %s\n", line)
	}
	if ok {
		fmt.Fprintln(stdout, "PASSED")
		return 0
	}
	msg := "FAILED"
	if *reportOnly {
		msg += " (report only: not blocking this run)"
	}
	fmt.Fprintln(stdout, msg)
	if *reportOnly {
		return 0
	}
	return 1
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
