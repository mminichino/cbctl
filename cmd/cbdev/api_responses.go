package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/apisnap"
)

// cmdAPIResponses regenerates docs/backlog/api/management-api-v4.0-responses.json from
// the Capella OpenAPI spec (Go port of capellactl scripts/update-api-responses.py).
// Also copies the result to internal/capellafake/testdata/ (parity single source).
func cmdAPIResponses(args []string, stdout, stderr io.Writer) int {
	var spec string
	keepRetrieved := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--spec" && i+1 < len(args):
			i++
			spec = args[i]
		case strings.HasPrefix(a, "--spec="):
			spec = strings.TrimPrefix(a, "--spec=")
		case a == "--keep-retrieved":
			keepRetrieved = true
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, `usage: cbdev api responses [--spec FILE_OR_URL] [--keep-retrieved]

Regenerate docs/backlog/api/management-api-v4.0-responses.json from the Capella
Management API OpenAPI document (default: published URL). Copies the same bytes
to internal/capellafake/testdata/management-api-v4.0-responses.json.
`)
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev api responses: unknown argument %q\n", a)
			return 2
		}
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	out := filepath.Join(root, "docs", "backlog", "api", "management-api-v4.0-responses.json")
	fakeOut := filepath.Join(root, "internal", "capellafake", "testdata", "management-api-v4.0-responses.json")

	opts := apisnap.Options{SpecSource: spec}
	if keepRetrieved {
		if cur, err := os.ReadFile(out); err == nil {
			var prev apisnap.Document
			if jsonUnmarshalRetrieved(cur, &prev) && prev.Retrieved != "" {
				opts.Retrieved = prev.Retrieved
			}
		}
	}
	doc, err := apisnap.Build(opts)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	if err := apisnap.WriteFile(out, doc); err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: write %s: %v\n", out, err)
		return 1
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(fakeOut), 0o755); err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	if err := os.WriteFile(fakeOut, raw, 0o644); err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: copy fake testdata: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "written: %s (%d operations)\n", relFromRoot(root, out), len(doc.Operations))
	fmt.Fprintf(stdout, "copied:  %s\n", relFromRoot(root, fakeOut))
	if missing := apisnap.Missing(doc); len(missing) > 0 {
		fmt.Fprintf(stderr, "not in the spec:\n  %s\n", strings.Join(missing, "\n  "))
		return 1
	}
	return 0
}

func jsonUnmarshalRetrieved(raw []byte, doc *apisnap.Document) bool {
	type head struct {
		Retrieved string `json:"retrieved"`
	}
	var h head
	if err := json.Unmarshal(raw, &h); err != nil {
		return false
	}
	doc.Retrieved = h.Retrieved
	return true
}

func relFromRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
