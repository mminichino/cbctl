package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/coverage"
	"github.com/mminichino/cbctl/internal/docgen/stories"
)

func cmdDocsIndex(args []string, stdout, stderr io.Writer) int {
	check := false
	var releases []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--check":
			check = true
		case a == "--release" && i+1 < len(args):
			i++
			releases = append(releases, args[i])
		case strings.HasPrefix(a, "--release="):
			releases = append(releases, strings.TrimPrefix(a, "--release="))
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, "usage: cbdev docs index [--check] [--release R]…\n")
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs index: unknown argument %q\n", a)
			return 2
		}
	}
	if len(releases) == 0 {
		releases = []string{"1.0.0", "1.5.0"}
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
		return 1
	}
	backlog := filepath.Join(root, "docs", "backlog")

	// CAP-ID-MAP from 1.0.0 stories.yaml
	s10, err := stories.LoadRelease(filepath.Join(backlog, "release-1.0.0"))
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
		return 1
	}
	idMap := stories.RenderCAPIDMap(s10)
	idMapPath := filepath.Join(backlog, "CAP-ID-MAP.md")
	if err := writeOrCheck(idMapPath, idMap, check, stderr); err != nil {
		fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
		return 1
	}

	for _, rel := range releases {
		cfg, err := stories.Config(rel)
		if err != nil {
			fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
			return 2
		}
		dir := filepath.Join(backlog, "release-"+rel)
		sts, err := stories.LoadRelease(dir)
		if err != nil {
			fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
			return 1
		}
		if err := stories.ValidateRelease(rel, sts, cfg); err != nil {
			fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
			return 2
		}
		text := stories.RenderIndex(rel, sts, cfg)
		out := filepath.Join(dir, "README.md")
		if err := writeOrCheck(out, text, check, stderr); err != nil {
			fmt.Fprintf(stderr, "cbdev docs index: %v\n", err)
			return 1
		}
		if !check {
			live := 0
			pts := 0
			for _, s := range sts {
				if s.Status != "Descoped" {
					live++
					pts += s.Points
				}
			}
			fmt.Fprintf(stdout, "%s: %d stories, %d points\n", out, live, pts)
		} else {
			fmt.Fprintf(stdout, "%s is up to date\n", out)
		}
	}
	if check {
		fmt.Fprintf(stdout, "%s is up to date\n", idMapPath)
	} else {
		fmt.Fprintf(stdout, "wrote %s\n", idMapPath)
	}
	return 0
}

func cmdDocsCoverage(args []string, stdout, stderr io.Writer) int {
	check := false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "-h", "--help":
			fmt.Fprint(stdout, "usage: cbdev docs coverage [--check]\n")
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs coverage: unknown argument %q\n", a)
			return 2
		}
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs coverage: %v\n", err)
		return 1
	}
	backlog := filepath.Join(root, "docs", "backlog")
	if err := coverage.Generate(backlog, check); err != nil {
		fmt.Fprintf(stderr, "cbdev docs coverage: %v\n", err)
		return 1
	}
	if check {
		fmt.Fprintln(stdout, "API coverage reports are up to date")
	} else {
		fmt.Fprintln(stdout, "API coverage reports written")
	}
	return 0
}

func writeOrCheck(path, text string, check bool, stderr io.Writer) error {
	if check {
		cur, err := os.ReadFile(path)
		if err != nil || string(cur) != text {
			return fmt.Errorf("out of date with the story files: %s; run: cbdev docs index", path)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}
