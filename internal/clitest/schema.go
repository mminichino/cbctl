package clitest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaBase is the URL the output schemas are registered under, so apply.schema.json can
// reference plan.schema.json by its file name.
const schemaBase = "https://cbctl.test/schemas/"

// outputSchemas are the cbctl -o json output schemas kept in internal/plan/testdata.
var outputSchemas = []string{"plan", "apply", "scale"}

var (
	schemaOnce sync.Once
	schemaDocs map[string]any
	schemaErr  error
)

// schemaDir is internal/plan/testdata, found from this source file so that tests in any package
// can use it.
func schemaDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locating the clitest sources")
	}
	return filepath.Join(filepath.Dir(file), "..", "plan", "testdata"), nil
}

func loadOutputSchemas() (map[string]any, error) {
	schemaOnce.Do(func() {
		dir, err := schemaDir()
		if err != nil {
			schemaErr = err
			return
		}
		schemaDocs = map[string]any{}
		for _, n := range outputSchemas {
			raw, err := os.ReadFile(filepath.Join(dir, n+".schema.json"))
			if err != nil {
				schemaErr = fmt.Errorf("reading %s schema: %w", n, err)
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				schemaErr = fmt.Errorf("parsing %s schema: %w", n, err)
				return
			}
			schemaDocs[n] = doc
		}
	})
	return schemaDocs, schemaErr
}

// AssertJSONSchema fails the test unless doc is one JSON document valid against schema. schema is
// either the name of a cbctl output schema in internal/plan/testdata ("plan", "apply" or "scale",
// with or without ".schema.json") or the text of a JSON Schema (draft 2020-12 by default). The
// output schemas are always registered, so an inline schema may reference them by file name.
func AssertJSONSchema(t testing.TB, schema string, doc []byte) {
	t.Helper()
	docs, err := loadOutputSchemas()
	if err != nil {
		t.Fatalf("AssertJSONSchema: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for n, d := range docs {
		if err := c.AddResource(schemaBase+n+".schema.json", d); err != nil {
			t.Fatalf("AssertJSONSchema: registering %s: %v", n, err)
		}
	}
	url := schemaBase + strings.TrimSuffix(strings.TrimSpace(schema), ".schema.json") + ".schema.json"
	if strings.HasPrefix(strings.TrimSpace(schema), "{") {
		inline, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
		if err != nil {
			t.Fatalf("AssertJSONSchema: schema is not JSON: %v", err)
		}
		url = schemaBase + "inline.schema.json"
		if err := c.AddResource(url, inline); err != nil {
			t.Fatalf("AssertJSONSchema: registering the inline schema: %v", err)
		}
	} else if _, ok := docs[strings.TrimSuffix(strings.TrimSpace(schema), ".schema.json")]; !ok {
		t.Fatalf("AssertJSONSchema: unknown schema %q (want one of %s, or a JSON Schema document)", schema, strings.Join(outputSchemas, ", "))
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("AssertJSONSchema: compiling %s: %v", url, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("AssertJSONSchema: output is not one JSON document: %v\n%s", err, doc)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("AssertJSONSchema: output does not match %s: %v\n%s", url, err, doc)
	}
}
