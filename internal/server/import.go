package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// PopulateCollection ensures the keyspace exists, imports JSONL documents with UUID keys,
// and returns the number of documents upserted. It stops before the next document once ctx is done
// and returns ctx.Err() along with the count imported so far.
func (s *Server) PopulateCollection(ctx context.Context, path, bucketName, scopeName, collectionName string) (int, error) {
	if _, err := s.EnsureCollection(bucketName, scopeName, collectionName); err != nil {
		return 0, err
	}
	src, err := FileSource(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }() // read-only handle
	return s.ImportDocuments(ctx, src, bucketName, scopeName, collectionName, "")
}

// DocSource yields the documents of an import in order. Next returns io.EOF after the last one.
// where names the position of the document ("data/a.jsonl:12", "buckets[0].import[1].data[3]")
// for error messages.
type DocSource interface {
	Next() (doc any, where string, err error)
	Close() error
}

// FileSource reads the documents of a JSON Lines file. Blank lines are skipped and a line may be
// up to 10 MiB.
func FileSource(path string) (DocSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening import file: %w", err)
	}
	scanner := bufio.NewScanner(f)
	// Allow large JSON lines (default 64KiB is often too small).
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	return &fileSource{f: f, scanner: scanner, path: path}, nil
}

type fileSource struct {
	f       *os.File
	scanner *bufio.Scanner
	path    string
	line    int
}

// Next implements DocSource.
func (s *fileSource) Next() (any, string, error) {
	for s.scanner.Scan() {
		s.line++
		doc, err := DecodeJSONLLine(s.scanner.Text(), s.line, s.path)
		if err != nil {
			return nil, "", err
		}
		if doc == nil {
			continue
		}
		return doc, fmt.Sprintf("%s:%d", s.path, s.line), nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, "", err
	}
	return nil, "", io.EOF
}

// Close implements DocSource.
func (s *fileSource) Close() error { return s.f.Close() }

// InlineSource yields documents held in memory, one JSON value each. Numbers keep their exact
// text (json.Number), so a large integer survives. where prefixes the position of each document.
func InlineSource(docs []json.RawMessage, where string) DocSource {
	return &inlineSource{docs: docs, where: where}
}

type inlineSource struct {
	docs  []json.RawMessage
	where string
	next  int
}

// Next implements DocSource.
func (s *inlineSource) Next() (any, string, error) {
	if s.next >= len(s.docs) {
		return nil, "", io.EOF
	}
	i := s.next
	s.next++
	where := fmt.Sprintf("%s[%d]", s.where, i)
	dec := json.NewDecoder(bytes.NewReader(s.docs[i]))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, "", fmt.Errorf("%s: invalid JSON: %w", where, err)
	}
	return doc, where, nil
}

// Close implements DocSource.
func (s *inlineSource) Close() error { return nil }

// ImportDocuments upserts every document of src into the keyspace, which must exist. The key is
// doc[keyField] (a string, or an integer formatted in base 10) when keyField is set, and a UUID
// otherwise. It stops before the next document once ctx is done and returns ctx.Err() with the
// count imported so far; any other failure also returns the count imported before it.
func (s *Server) ImportDocuments(ctx context.Context, src DocSource, bucketName, scopeName, collectionName, keyField string) (int, error) {
	if err := s.ConnectKeyspace(bucketName, scopeName, collectionName); err != nil {
		return 0, err
	}
	imported := 0
	for {
		if err := ctx.Err(); err != nil {
			return imported, err
		}
		doc, where, err := src.Next()
		if errors.Is(err, io.EOF) {
			return imported, nil
		}
		if err != nil {
			return imported, err
		}
		key, err := documentKey(doc, keyField, where)
		if err != nil {
			return imported, err
		}
		if err := s.Upsert(key, doc); err != nil {
			if keyField == "" { // cbctl import reports the raw SDK error
				return imported, err
			}
			return imported, fmt.Errorf("%s: %w", where, err)
		}
		imported++
	}
}

// documentKey returns the key of doc: a UUID when keyField is empty, otherwise the string or
// integer value of that top-level field.
func documentKey(doc any, keyField, where string) (string, error) {
	if keyField == "" {
		return uuid.NewString(), nil
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s: a document needs to be a JSON object to take its key from %q", where, keyField)
	}
	v, ok := obj[keyField]
	if !ok {
		return "", fmt.Errorf("%s: keyField %q is missing", where, keyField)
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", fmt.Errorf("%s: keyField %q is empty", where, keyField)
		}
		return t, nil
	case json.Number:
		if _, err := strconv.ParseInt(t.String(), 10, 64); err != nil {
			f, ferr := strconv.ParseFloat(t.String(), 64)
			if ferr != nil || f != math.Trunc(f) || math.Abs(f) > maxExactFloat {
				return "", fmt.Errorf("%s: keyField %q must be a string or an integer, not %s", where, keyField, t)
			}
			return strconv.FormatFloat(f, 'f', 0, 64), nil
		}
		n, _ := strconv.ParseInt(t.String(), 10, 64) // parsed above
		return strconv.FormatInt(n, 10), nil
	case float64:
		if t != math.Trunc(t) || math.Abs(t) > maxExactFloat {
			return "", fmt.Errorf("%s: keyField %q must be a string or an integer, not %v", where, keyField, t)
		}
		return strconv.FormatFloat(t, 'f', 0, 64), nil
	}
	return "", fmt.Errorf("%s: keyField %q must be a string or an integer", where, keyField)
}

// maxExactFloat is the largest integer a float64 holds exactly (2^53).
const maxExactFloat = 1 << 53

// DecodeJSONLLine parses a single JSONL line. Blank lines return nil, nil.
func DecodeJSONLLine(line string, lineNumber int, path string) (any, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, nil
	}
	var doc any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON on line %d of %s", lineNumber, path)
	}
	return doc, nil
}

func forEachJSONLDocument(r io.Reader, path string, fn func(doc any) error) error {
	scanner := bufio.NewScanner(r)
	// Allow large JSON lines (default 64KiB is often too small).
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		doc, err := DecodeJSONLLine(scanner.Text(), lineNumber, path)
		if err != nil {
			return err
		}
		if doc == nil {
			continue
		}
		if err := fn(doc); err != nil {
			return err
		}
	}
	return scanner.Err()
}
