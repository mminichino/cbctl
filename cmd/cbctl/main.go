package main

import (
	"os"

	"github.com/mminichino/cbctl/internal/cli"
)

// Set by GoReleaser via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.SetVersion(version, commit, date)
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
