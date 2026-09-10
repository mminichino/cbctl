package server

import (
	"errors"

	"github.com/couchbase/gocb/v2"

	"github.com/mminichino/cbctl/internal/config"
)

// CreateBucket creates a bucket if it does not already exist.
// A quota of 0 is treated as the default (128 MiB).
func (s *Server) CreateBucket(name string, quota int, replicas int) error {
	if err := s.requireCluster(); err != nil {
		return err
	}
	exists, err := s.BucketExists(name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if quota == 0 {
		quota = config.DefaultBucketQuotaMiB
	}
	settings := gocb.CreateBucketSettings{
		BucketSettings: gocb.BucketSettings{
			Name:                 name,
			RAMQuotaMB:           uint64(quota),
			NumReplicas:          uint32(replicas),
			BucketType:           gocb.CouchbaseBucketType,
			StorageBackend:       gocb.StorageBackendCouchstore,
			FlushEnabled:         false,
			ReplicaIndexDisabled: false,
		},
		ConflictResolutionType: gocb.ConflictResolutionTypeSequenceNumber,
	}
	err = s.Cluster.Buckets().CreateBucket(settings, nil)
	if err != nil && !errors.Is(err, gocb.ErrBucketExists) {
		return err
	}
	return nil
}

// DropBucket drops a bucket, ignoring not-found errors.
func (s *Server) DropBucket(name string) error {
	if err := s.requireCluster(); err != nil {
		return err
	}
	err := s.Cluster.Buckets().DropBucket(name, nil)
	if err != nil && !errors.Is(err, gocb.ErrBucketNotFound) {
		return err
	}
	return nil
}

// BucketExists reports whether the named bucket exists.
func (s *Server) BucketExists(name string) (bool, error) {
	return s.IsBucket(name)
}

// IsBucket reports whether the named bucket exists.
func (s *Server) IsBucket(name string) (bool, error) {
	if err := s.requireCluster(); err != nil {
		return false, err
	}
	if name == "" {
		return false, nil
	}
	buckets, err := s.Cluster.Buckets().GetAllBuckets(nil)
	if err != nil {
		return false, err
	}
	_, ok := buckets[name]
	return ok, nil
}
