package capellafile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestYAMLCodecKeepsObjOfValidFixtures compares the Obj that every valid fixture parses to with
// a snapshot taken on main before the YAML codec moved to JSON (testdata/compat, written by the
// old node walker). The snapshots are never regenerated: a diff means the parse changed.
func TestYAMLCodecKeepsObjOfValidFixtures(t *testing.T) {
	var files []string
	for _, pattern := range []string{"testdata/valid/*.yaml", "testdata/capellactl/*.yaml"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 15 {
		t.Fatalf("only %d fixtures found", len(files))
	}
	for _, f := range files {
		dir := strings.Split(filepath.ToSlash(f), "/")[1]
		name := dir + "-" + strings.TrimSuffix(filepath.Base(f), ".yaml")
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			root, issues := parseYAML(string(text), f)
			if len(issues) > 0 {
				t.Fatal(issues)
			}
			snap, err := os.ReadFile(filepath.Join("testdata/compat", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(bytes.NewReader(snap))
			dec.UseNumber()
			var frozen any
			if err := dec.Decode(&frozen); err != nil {
				t.Fatal(err)
			}
			want := fromJSON(frozen)
			if got := toObj(root); !reflect.DeepEqual(got, want) {
				gj, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("Obj differs from the snapshot:\n%s", gj)
			}
		})
	}
}

// TestHibernationOffStaysAString guards decision D2: an unquoted off is the string "off" (YAML
// 1.2), not a YAML 1.1 boolean.
func TestHibernationOffStaysAString(t *testing.T) {
	root, issues := parseYAML("hibernation:\n  weekend: off\n  monday: on\n  tuesday: 08:00-18:00\n", "x")
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	h := toObj(root).(Obj)["hibernation"].(Obj)
	if h["weekend"] != "off" || h["monday"] != "on" || h["tuesday"] != "08:00-18:00" {
		t.Errorf("got %#v", h)
	}
}

func TestParseYAMLIssues(t *testing.T) {
	tests := []struct {
		name, text, msg, fix string
		line                 int
	}{
		{"non-string key", "apiVersion: cbctl/v1\n1: x\n", "invalid YAML: mapping keys must be strings (quote the key)", "", 2},
		{"duplicate", "a: 1\nb: 2\na: 3\n", `invalid YAML: mapping key "a" already defined at line 1`, "remove or rename the duplicate key", 3},
		{"several documents", "a: 1\n---\nb: 2\n", "invalid YAML: the input holds several YAML documents; expected one", "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues := parseYAML(tt.text, "x.yaml")
			if len(issues) != 1 {
				t.Fatalf("issues = %v", issues)
			}
			if !strings.HasPrefix(issues[0].Message, tt.msg) || issues[0].Line != tt.line || issues[0].Fix != tt.fix {
				t.Errorf("got %+v", issues[0])
			}
		})
	}
}
