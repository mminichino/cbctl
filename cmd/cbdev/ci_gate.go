package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/mminichino/cbctl/internal/ci/releasegate"
)

// fallbackRepo is used when neither GITHUB_REPOSITORY nor the origin remote names the repo.
const fallbackRepo = "mminichino/cbctl"

// gitRemote returns the origin URL; a variable so tests can replace it.
var gitRemote = func(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	return string(out), err
}

// defaultRepo is GITHUB_REPOSITORY, else OWNER/NAME of the origin remote, else fallbackRepo.
func defaultRepo(ctx context.Context) string {
	if v := os.Getenv("GITHUB_REPOSITORY"); v != "" {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if url, err := gitRemote(ctx); err == nil {
		if repo := releasegate.RepoFromRemote(url); repo != "" {
			return repo
		}
	}
	return fallbackRepo
}

func cmdCIReleaseGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci release-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	commit := fs.String("commit", "", "release commit SHA (required)")
	repo := fs.String("repo", "", "OWNER/NAME (default: $GITHUB_REPOSITORY, the origin remote, or "+fallbackRepo+")")
	branch := fs.String("branch", "main", "branch the nightly runs on")
	workflow := fs.String("workflow", "nightly.yml", "nightly workflow file name")
	nights := fs.Int("nights", 3, "consecutive green nights required")
	runsJSON := fs.String("runs-json", "", "path to a workflow_runs JSON fixture with per-run jobs (skip gh api)")
	reportOnly := fs.Bool("report-only", false, "print the verdict and always exit 0 (release.yml uses this: the nightly is a health signal, not a gate)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *commit == "" {
		fmt.Fprintln(stderr, "cbdev ci release-gate: --commit is required")
		return 2
	}
	if *nights < 1 {
		fmt.Fprintln(stderr, "cbdev ci release-gate: --nights must be at least 1")
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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if *repo == "" {
			*repo = defaultRepo(ctx)
		}
		runs, err = releasegate.FetchRuns(ctx, *repo, *workflow, *branch)
	}
	if err != nil {
		// No verdict on missing evidence. With --report-only the error is printed and the run
		// still exits 0, so an API error never blocks a release.
		fmt.Fprintln(stderr, err.Error())
		if *reportOnly {
			fmt.Fprintln(stdout, "UNKNOWN (report only: could not read the nightly runs)")
			return 0
		}
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
