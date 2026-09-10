package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
)

const (
	indexCreateAttempts = 30
	indexCreateWait     = 2 * time.Second
)

// CollectionIsEmpty reports whether the collection has no documents.
// Missing collections are treated as empty.
func (s *Server) CollectionIsEmpty(bucketName, scopeName, collectionName string) (bool, error) {
	if err := s.requireCluster(); err != nil {
		return false, err
	}
	exists, err := s.CollectionExists(bucketName, scopeName, collectionName)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	if err := s.createPrimaryIndex(bucketName, scopeName, collectionName); err != nil {
		return false, err
	}
	keyspace := quoteKeyspace(bucketName, scopeName, collectionName)
	result, err := s.Cluster.Query(
		fmt.Sprintf("SELECT RAW 1 FROM %s LIMIT 1", keyspace),
		&gocb.QueryOptions{ScanConsistency: gocb.QueryScanConsistencyRequestPlus},
	)
	if err != nil {
		return false, err
	}
	defer result.Close()
	if result.Next() {
		return false, nil
	}
	return true, result.Err()
}

func (s *Server) createPrimaryIndex(bucketName, scopeName, collectionName string) error {
	var lastErr error
	for attempt := 0; attempt < indexCreateAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(indexCreateWait)
		}
		coll := s.Cluster.Bucket(bucketName).Scope(scopeName).Collection(collectionName)
		err := coll.QueryIndexes().CreatePrimaryIndex(&gocb.CreatePrimaryQueryIndexOptions{
			IgnoreIfExists: true,
			Deferred:       false,
		})
		if err == nil {
			_ = coll.QueryIndexes().WatchIndexes([]string{"#primary"}, 30*time.Second, nil)
			return nil
		}
		if !isRetryableIndexError(err) {
			return err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("primary index creation failed")
	}
	return lastErr
}

func quoteKeyspace(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = "`" + strings.ReplaceAll(part, "`", "``") + "`"
	}
	return strings.Join(quoted, ".")
}

func isRetryableIndexError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	needles := []string{
		"rebalance in progress",
		"keyspace not found",
		"indexing.error",
		"query service is not available",
		"service unavailable",
		"channel_closed",
		"no_more_retries",
		"endpoint_not_available",
		"ambiguous timeout",
		"unambiguous timeout",
		"timeout",
		"request canceled",
		"temporary failure",
	}
	for _, needle := range needles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
