package server

import (
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
	want := "Invalid JSON on line 3 of data.jsonl"
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
	want := "Invalid JSON on line 2 of bad.jsonl"
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
