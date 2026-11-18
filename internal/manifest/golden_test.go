package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file (run go test -update): %v", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (run go test -update and review the diff)\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// TestInvalidGolden checks the error text and JSON of every invalid fixture.
func TestInvalidGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/invalid/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".yaml")
		t.Run(name, func(t *testing.T) {
			set, err := Load(context.Background(), Source{Path: filepath.Base(f), Cwd: filepath.Dir(f)})
			if set != nil {
				t.Fatalf("an invalid manifest returned documents: %+v", set)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}
			for _, is := range ve.Issues {
				if is.Doc > 0 && is.Line == 0 && name != "no-kind" && !strings.HasPrefix(is.Msg, "spec is required") {
					t.Errorf("issue without a line: %s", is)
				}
			}
			compareGolden(t, strings.TrimSuffix(f, ".yaml")+".golden", err.Error()+"\n")
			js, err := json.MarshalIndent(ve, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, strings.TrimSuffix(f, ".yaml")+".json.golden", string(js)+"\n")
		})
	}
}

// TestValidFixtures loads every valid fixture.
func TestValidFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/valid/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			set, err := Load(context.Background(), Source{Path: filepath.Base(f), Cwd: filepath.Dir(f)})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(set.Docs) == 0 {
				t.Fatal("no documents")
			}
		})
	}
}
