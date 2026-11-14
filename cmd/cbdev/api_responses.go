package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// cmdAPIResponses refreshes docs/backlog/api/management-api-v4.0-responses.json from Inventory.
// Full OpenAPI $ref resolution (Python update-api-responses.py) is left as a follow-up;
// this keeps the snapshot present and inventory-aligned for --check/make flows.
func cmdAPIResponses(args []string, stdout, stderr io.Writer) int {
	var spec string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--spec" && i+1 < len(args):
			i++
			spec = args[i]
		case strings.HasPrefix(a, "--spec="):
			spec = strings.TrimPrefix(a, "--spec=")
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, "usage: cbdev api responses [--spec FILE_OR_URL]\n")
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev api responses: unknown argument %q\n", a)
			return 2
		}
	}
	_ = spec // OpenAPI refresh not yet ported; inventory-based stub
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	out := filepath.Join(root, "docs", "backlog", "api", "management-api-v4.0-responses.json")
	// Prefer keeping an existing rich snapshot; rewrite only when missing.
	if _, err := os.Stat(out); err == nil {
		fmt.Fprintf(stdout, "kept existing %s (%d inventory operations)\n", out, len(capellaapi.Inventory))
		return 0
	}
	type op struct {
		Method     string         `json:"method"`
		Path       string         `json:"path"`
		Documented bool           `json:"documented"`
		Summary    string         `json:"summary,omitempty"`
		Responses  map[string]any `json:"responses,omitempty"`
	}
	doc := map[string]any{
		"source":     "internal/capellaapi.Inventory (stub; full OpenAPI resolve not ported)",
		"apiVersion": "v4.0",
		"retrieved":  time.Now().UTC().Format("2006-01-02"),
		"operations": []op{},
	}
	ops := make([]op, 0, len(capellaapi.Inventory))
	for _, e := range capellaapi.Inventory {
		ops = append(ops, op{Method: e.Method, Path: e.Path, Documented: false})
	}
	doc["operations"] = ops
	raw, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "cbdev api responses: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "written: %s (%d operations; OpenAPI resolve leftover)\n", out, len(ops))
	return 0
}
