package reference

import (
	"regexp"
	"strings"
	"testing"
)

func TestSchemaFieldsNonEmpty(t *testing.T) {
	f, err := SchemaFields()
	if err != nil {
		t.Fatal(err)
	}
	if len(f) == 0 {
		t.Fatal("expected schema fields")
	}
}

var reRow = regexp.MustCompile("^\\| `([^`]+)` \\|")

// rendered parses the reference into the full field paths it has a row for.
func rendered(t *testing.T) map[string]bool {
	t.Helper()
	text, err := Render()
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimPrefix(line, "## ")
			if section == "(top level)" {
				section = ""
			}
			continue
		}
		if m := reRow.FindStringSubmatch(line); m != nil {
			path := m[1]
			if section != "" {
				path = section + "." + path
			}
			rows[path] = true
		}
	}
	return rows
}

func TestEverySchemaLeafHasARow(t *testing.T) {
	fields, err := SchemaFields()
	if err != nil {
		t.Fatal(err)
	}
	rows := rendered(t)
	if len(rows) < 103 {
		t.Errorf("reference has %d rows, want at least 103", len(rows))
	}
	for path, node := range fields {
		if singleProjectAlias(path) {
			continue
		}
		if isSection(node) && !strings.Contains(path, ".") {
			if typ, _ := node["type"].(string); typ != "array" {
				continue // a top-level object ("project") is a heading, not a row
			}
		}
		if !rows[path] {
			t.Errorf("schema field %s has no row in reference.md", path)
		}
	}
	for _, want := range []string{
		"projects.clusters.serviceGroups.services",
		"projects.clusters.buckets.scopes.collections.maxTTL",
		"projects.clusters.databaseCredentials.access",
	} {
		if !rows[want] {
			t.Errorf("reference.md has no row for %s", want)
		}
	}
}

func TestDerefItemsDoesNotModifySchema(t *testing.T) {
	root := map[string]any{"$defs": map[string]any{"c": map[string]any{"type": "object", "properties": map[string]any{}}}}
	node := map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/c"}}
	got := derefItems(node, root)
	if items := got["items"].(map[string]any); items["type"] != "object" {
		t.Errorf("items not dereferenced: %v", items)
	}
	if _, still := node["items"].(map[string]any)["$ref"]; !still {
		t.Error("derefItems modified its input")
	}
	if !isSection(got) {
		t.Error("an array of $ref objects must be a section")
	}
}
