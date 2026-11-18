package server

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeJSONLLine(t *testing.T) {
	t.Parallel()

	doc, err := DecodeJSONLLine(`{"a":1}`, 1, "sample.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", doc)
	}
	if m["a"].(float64) != 1 {
		t.Fatalf("expected a=1, got %#v", m["a"])
	}

	doc, err = DecodeJSONLLine("   \n", 2, "sample.jsonl")
	if err != nil {
		t.Fatalf("blank line error: %v", err)
	}
	if doc != nil {
		t.Fatalf("expected nil for blank line, got %#v", doc)
	}

	_, err = DecodeJSONLLine("{not-json", 3, "data.jsonl")
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
	want := "invalid JSON on line 3 of data.jsonl"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestForEachJSONLDocument(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("{\"n\":1}\n\n{\"n\":2}\n")
	var values []float64
	err := forEachJSONLDocument(input, "in.jsonl", func(doc any) error {
		m := doc.(map[string]any)
		values = append(values, m["n"].(float64))
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 2 || values[0] != 1 || values[1] != 2 {
		t.Fatalf("values = %#v", values)
	}
}

func TestForEachJSONLDocumentInvalid(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("{\"ok\":true}\n[1,2,\n")
	err := forEachJSONLDocument(input, "bad.jsonl", func(doc any) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	want := "invalid JSON on line 2 of bad.jsonl"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestQuoteKeyspace(t *testing.T) {
	t.Parallel()

	got := quoteKeyspace("bucket", "scope", "coll")
	want := "`bucket`.`scope`.`coll`"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = quoteKeyspace("a`b")
	want = "`a``b`"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIsRetryableIndexError(t *testing.T) {
	t.Parallel()

	if !isRetryableIndexError(errString("rebalance in progress")) {
		t.Fatal("expected retryable")
	}
	if !isRetryableIndexError(errString("service not available | CREATE PRIMARY INDEX")) {
		t.Fatal("expected Capella/Server 'service not available' to be retryable")
	}
	if isRetryableIndexError(errString("index already exists")) {
		t.Fatal("expected non-retryable")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestFatalError(t *testing.T) {
	t.Parallel()

	err := &FatalError{Msg: "nope"}
	if err.Error() != "nope" {
		t.Fatalf("got %q", err.Error())
	}
}

func TestDocumentKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		doc      any
		keyField string
		want     string
		wantErr  string
	}{
		{"string key", map[string]any{"id": "a-1"}, "id", "a-1", ""},
		{"float integer key", map[string]any{"id": float64(42)}, "id", "42", ""},
		{"json number integer", map[string]any{"id": json.Number("9007199254740993")}, "id", "9007199254740993", ""},
		{"json number integral float", map[string]any{"id": json.Number("1e3")}, "id", "1000", ""},
		{"negative zero", map[string]any{"id": json.Number("-0")}, "id", "0", ""},
		{"fractional", map[string]any{"id": json.Number("1.5")}, "id", "", "string or an integer"},
		{"float fractional", map[string]any{"id": 1.5}, "id", "", "string or an integer"},
		{"missing", map[string]any{"x": 1}, "id", "", "is missing"},
		{"empty string", map[string]any{"id": ""}, "id", "", "is empty"},
		{"wrong type", map[string]any{"id": true}, "id", "", "string or an integer"},
		{"not an object", []any{1}, "id", "", "JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := documentKey(tt.doc, tt.keyField, "here")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "here") {
					t.Fatalf("error = %v, want it to contain %q and the position", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("key = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	if key, err := documentKey(map[string]any{}, "", "here"); err != nil || len(key) != 36 {
		t.Fatalf("no keyField: key %q, %v; want a UUID", key, err)
	}
}

func TestFileSource(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "d.jsonl")
	if err := os.WriteFile(path, []byte("{\"n\":1}\n\n{\"n\":2}\n{bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := FileSource(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	for i, wantWhere := range []string{path + ":1", path + ":3"} {
		doc, where, err := src.Next()
		if err != nil || where != wantWhere || doc.(map[string]any)["n"].(float64) != float64(i+1) {
			t.Fatalf("doc %d = %v at %q, %v; want position %q", i, doc, where, err, wantWhere)
		}
	}
	if _, _, err := src.Next(); err == nil || !strings.Contains(err.Error(), "invalid JSON on line 4") {
		t.Fatalf("error = %v, want invalid JSON on line 4", err)
	}
	if _, err := FileSource(path + ".missing"); err == nil {
		t.Fatal("FileSource of a missing file succeeded")
	}
}

func TestInlineSource(t *testing.T) {
	t.Parallel()
	src := InlineSource([]json.RawMessage{json.RawMessage(`{"id":12345678901234567890}`), json.RawMessage(`{`)}, "data")
	doc, where, err := src.Next()
	if err != nil || where != "data[0]" {
		t.Fatalf("first = %v at %q, %v", doc, where, err)
	}
	if got := doc.(map[string]any)["id"]; got != json.Number("12345678901234567890") {
		t.Fatalf("id = %#v, want the exact number text", got)
	}
	if _, _, err := src.Next(); err == nil || !strings.Contains(err.Error(), "data[1]") {
		t.Fatalf("second error = %v, want position data[1]", err)
	}
	empty := InlineSource(nil, "data")
	if _, _, err := empty.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty source error = %v, want io.EOF", err)
	}
}
