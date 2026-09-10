package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/couchbase/gocb/v2"
)

// Upsert stores a document in the connected collection.
func (s *Server) Upsert(docID string, content any) error {
	if err := s.requireCollection(); err != nil {
		return err
	}
	opts := &gocb.UpsertOptions{Timeout: 5 * time.Second}
	_, err := s.collection.Upsert(docID, content, opts)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// Get fetches a document as a map. Missing documents return nil, nil.
func (s *Server) Get(docID string) (map[string]any, error) {
	if err := s.requireCollection(); err != nil {
		return nil, err
	}
	result, err := s.collection.Get(docID, nil)
	if err != nil {
		if errors.Is(err, gocb.ErrDocumentNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var content map[string]any
	if err := result.Content(&content); err != nil {
		return nil, err
	}
	return content, nil
}
