package guide

import (
	"strings"
	"testing"
)

// TestHelpConfig_CAP034_AC4_EverySchemaFieldDocumented: every leaf of the capella.yaml schema has
// a description and says whether it can change (x-mutability), so 'guide config FIELD' can always
// explain it.
func TestHelpConfig_CAP034_AC4_EverySchemaFieldDocumented(t *testing.T) {
	paths := allFieldPaths()
	if len(paths) < 50 {
		t.Fatalf("only %d schema fields found", len(paths))
	}
	leaves := 0
	for _, p := range paths {
		node, inner, ok := nodeAt(strings.Split(p, "."))
		if !ok {
			t.Errorf("%s: not found", p)
			continue
		}
		if props, _ := inner["properties"].(map[string]any); len(props) > 0 {
			continue // a parent: its children are checked
		}
		leaves++
		if d, _ := node["description"].(string); strings.TrimSpace(d) == "" {
			t.Errorf("%s has no description", p)
		}
		if m, _ := node["x-mutability"].(string); m == "" {
			t.Errorf("%s has no x-mutability", p)
		}
	}
	if leaves == 0 {
		t.Fatal("no leaf fields checked")
	}
}

// TestHelpConfig_CAP034_AC4_FieldDetails: a field shows its type, allowed values, whether it can
// change, the API field it maps to and an example; list indexes in the path are accepted.
func TestHelpConfig_CAP034_AC4_FieldDetails(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"clusters.buckets.storageBackend", []string{"type:       string", "allowed:    couchstore, magma",
			"immutable after creation", "API:        storageBackend (Create Bucket)"}},
		{"clusters[0].serviceGroups[1].numOfNodes", []string{"updatable in place", "example:    numOfNodes: 3"}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got, err := Field(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
		})
	}
}

// TestHelpConfig_CAP034_AC4_Tree: the tree has a heading, lists clusters as a list and marks
// immutable fields.
func TestHelpConfig_CAP034_AC4_Tree(t *testing.T) {
	tree := Fields()
	for _, w := range []string{"capella.yaml fields", "  clusters: list", "storageBackend: string  [immutable]"} {
		if !strings.Contains(tree, w) {
			t.Errorf("missing %q in the tree", w)
		}
	}
}

// TestHelpConfig_CAP034_AC4_UnknownPathSuggestions: a mistyped field (wrong case) names the
// correctly spelled path.
func TestHelpConfig_CAP034_AC4_UnknownPathSuggestions(t *testing.T) {
	_, err := Field("clusters.buckets.storagebackend")
	if err == nil || !strings.Contains(err.Error(), "projects.clusters.buckets.storageBackend") {
		t.Fatalf("err = %v", err)
	}
}
