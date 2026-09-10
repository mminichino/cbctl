package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/mminichino/cbctl/internal/config"
)

const (
	testBucket     = "__test"
	testDocumentID = "cbctl-cluster-test"
	testAttempts   = 10
	testRetryWait  = 500 * time.Millisecond
)

var testDocument = map[string]any{
	"ok":     true,
	"source": "cbctl cluster test",
}

// FatalError is a non-retryable connectivity-test failure.
type FatalError struct {
	Msg string
}

func (e *FatalError) Error() string { return e.Msg }

// ClusterTest runs put/get against bucket (or a temporary __test bucket when bucket is empty).
func (s *Server) ClusterTest(bucket string) error {
	if err := s.requireCluster(); err != nil {
		return err
	}

	manageBucket := bucket == ""
	targetBucket := bucket
	if manageBucket {
		targetBucket = testBucket
		exists, err := s.IsBucket(targetBucket)
		if err != nil {
			return err
		}
		if exists {
			if err := s.DropBucket(targetBucket); err != nil {
				return err
			}
		}
		if err := s.CreateBucket(targetBucket, config.DefaultBucketQuotaMiB, 0); err != nil {
			return err
		}
	} else {
		exists, err := s.IsBucket(targetBucket)
		if err != nil {
			return err
		}
		if !exists {
			return &FatalError{Msg: fmt.Sprintf("Bucket %q does not exist", targetBucket)}
		}
	}

	var lastErr error
	for attempt := 0; attempt < testAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(testRetryWait)
		}
		err := s.runKVProbe(targetBucket)
		if err == nil {
			if manageBucket {
				_ = s.DropBucket(targetBucket)
			}
			return nil
		}
		var fatal *FatalError
		if errors.As(err, &fatal) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("cluster KV test failed after retries: %v", lastErr)
}

func (s *Server) runKVProbe(bucket string) error {
	if err := s.ConnectKeyspace(bucket, "_default", "_default"); err != nil {
		return err
	}
	if err := s.Upsert(testDocumentID, testDocument); err != nil {
		return err
	}
	fetched, err := s.Get(testDocumentID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fetched, testDocument) {
		return &FatalError{
			Msg: fmt.Sprintf("test document mismatch: expected %#v, got %#v", testDocument, fetched),
		}
	}
	return nil
}

// RunConnectivityTest connects using cfg and verifies KV put/get.
// When bucket is empty, a temporary __test bucket is created and removed.
func RunConnectivityTest(cfg *config.Config, bucket string) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	s := New()
	manageBucket := bucket == ""

	cleanup := func() {
		if manageBucket && s.Cluster != nil {
			if ok, err := s.IsBucket(testBucket); err == nil && ok {
				_ = s.DropBucket(testBucket)
			}
		}
		s.Disconnect()
	}
	defer cleanup()

	if err := s.Connect(context.Background(), cfg); err != nil {
		return err
	}
	return s.ClusterTest(bucket)
}
