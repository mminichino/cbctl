package yamlio

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldenInputs lists testdata/golden/NAME.yaml, not the generated NAME.roundtrip.yaml.
func goldenInputs(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("testdata/golden/*.yaml")
	if err != nil || len(all) == 0 {
		t.Fatalf("no golden inputs: %v", err)
	}
	var out []string
	for _, f := range all {
		if !strings.HasSuffix(f, ".roundtrip.yaml") {
			out = append(out, f)
		}
	}
	return out
}

func indentJSON(t *testing.T, j []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := json.Indent(&b, j, "", "  "); err != nil {
		t.Fatalf("invalid JSON %s: %v", j, err)
	}
	b.WriteByte('\n')
	return b.Bytes()
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/yamlio -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// convertGolden converts one golden input the way its name asks: *-rfc3339 uses RFC3339
// timestamps, multi-* is read as a stream and gives a JSON array.
func convertGolden(t *testing.T, name string, y []byte) []byte {
	t.Helper()
	var opts []Option
	if strings.Contains(name, "rfc3339") {
		opts = append(opts, WithTimestamps(RFC3339))
	}
	if strings.HasPrefix(name, "multi") {
		dec := NewDecoder(bytes.NewReader(y), opts...)
		var docs []json.RawMessage
		for {
			d, err := dec.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			docs = append(docs, d.JSON)
		}
		j, err := json.Marshal(docs)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	j, err := YAMLToJSON(y, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// TestGoldenRoundTrip pins NAME.yaml → NAME.json → NAME.roundtrip.yaml. Regenerate with -update.
func TestGoldenRoundTrip(t *testing.T) {
	for _, f := range goldenInputs(t) {
		base := strings.TrimSuffix(f, ".yaml")
		name := filepath.Base(base)
		t.Run(name, func(t *testing.T) {
			y, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			j := convertGolden(t, name, y)
			compareGolden(t, base+".json", indentJSON(t, j))
			if strings.HasPrefix(name, "multi") {
				return // a stream has no single YAML form
			}
			rt, err := JSONToYAML(j)
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, base+".roundtrip.yaml", rt)
			// The round-trip text means the same thing as the input.
			j2, err := YAMLToJSON(rt, goldenOpts(name)...)
			if err != nil {
				t.Fatalf("round-trip YAML does not parse: %v", err)
			}
			if !bytes.Equal(j, j2) {
				t.Errorf("YAML→JSON→YAML→JSON changed the document:\n%s\n%s", j, j2)
			}
		})
	}
}

func goldenOpts(name string) []Option {
	if strings.Contains(name, "rfc3339") {
		return []Option{WithTimestamps(RFC3339)}
	}
	return nil
}
