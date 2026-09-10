package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

// PopulateCollection ensures the keyspace exists, imports JSONL documents with UUID keys,
// and returns the number of documents upserted.
func (s *Server) PopulateCollection(path, bucketName, scopeName, collectionName string) (int, error) {
	if _, err := s.EnsureCollection(bucketName, scopeName, collectionName); err != nil {
		return 0, err
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	imported := 0
	err = forEachJSONLDocument(f, path, func(doc any) error {
		if err := s.Upsert(uuid.NewString(), doc); err != nil {
			return err
		}
		imported++
		return nil
	})
	return imported, err
}

// DecodeJSONLLine parses a single JSONL line. Blank lines return nil, nil.
func DecodeJSONLLine(line string, lineNumber int, path string) (any, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, nil
	}
	var doc any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, fmt.Errorf("Invalid JSON on line %d of %s", lineNumber, path)
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
