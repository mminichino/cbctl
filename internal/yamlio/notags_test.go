package yamlio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// devOnly are the packages the plan leaves on the raw YAML library: they read the repository's
// own docs and backlog files and never touch user data.
var devOnly = []string{
	filepath.Join("internal", "docgen"),
	filepath.Join("cmd", "cbdev"),
	filepath.Join("internal", "yamlio"),
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestNoYAMLTags fails on any `yaml:"…"` struct tag outside internal/yamlio (and the dev-only
// packages): the json tag is the only contract for typed structs, YAML is just a serialisation.
func TestNoYAMLTags(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			for _, skip := range devOnly {
				if rel == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			field, ok := n.(*ast.Field)
			if !ok || field.Tag == nil {
				return true
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				return true
			}
			if _, has := reflect.StructTag(tag).Lookup("yaml"); has {
				t.Errorf("%s: struct tag %s: use a json tag (see internal/yamlio)", fset.Position(field.Pos()), field.Tag.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
