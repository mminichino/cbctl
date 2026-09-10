package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/couchbase/gocb/v2"

	"github.com/mminichino/cbctl/internal/config"
)

// CreateScope creates a scope under bucket. "_default" is a no-op.
func (s *Server) CreateScope(bucketName, scopeName string) error {
	if err := s.requireCluster(); err != nil {
		return err
	}
	if scopeName == "" {
		scopeName = "_default"
	}
	if scopeName == "_default" {
		return nil
	}
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	b := s.Cluster.Bucket(bucketName)
	err := b.Collections().CreateScope(scopeName, nil)
	if err != nil && !errors.Is(err, gocb.ErrScopeExists) {
		return err
	}
	return nil
}

// CreateCollection creates a collection under bucket/scope. "_default" is a no-op.
func (s *Server) CreateCollection(bucketName, scopeName, collectionName string) error {
	if err := s.requireCluster(); err != nil {
		return err
	}
	if scopeName == "" {
		scopeName = "_default"
	}
	if collectionName == "" {
		collectionName = "_default"
	}
	if collectionName == "_default" {
		return nil
	}
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	b := s.Cluster.Bucket(bucketName)
	err := b.Collections().CreateCollection(gocb.CollectionSpec{
		Name:      collectionName,
		ScopeName: scopeName,
	}, nil)
	if err != nil && !errors.Is(err, gocb.ErrCollectionExists) {
		return err
	}
	return nil
}

// ScopeExists reports whether scope exists under bucket.
func (s *Server) ScopeExists(bucketName, scopeName string) (bool, error) {
	if err := s.requireCluster(); err != nil {
		return false, err
	}
	ok, err := s.BucketExists(bucketName)
	if err != nil || !ok {
		return false, err
	}
	scopes, err := s.Cluster.Bucket(bucketName).Collections().GetAllScopes(nil)
	if err != nil {
		return false, err
	}
	for _, scope := range scopes {
		if scope.Name == scopeName {
			return true, nil
		}
	}
	return false, nil
}

// CollectionExists reports whether collection exists under bucket/scope.
func (s *Server) CollectionExists(bucketName, scopeName, collectionName string) (bool, error) {
	if err := s.requireCluster(); err != nil {
		return false, err
	}
	ok, err := s.BucketExists(bucketName)
	if err != nil || !ok {
		return false, err
	}
	scopes, err := s.Cluster.Bucket(bucketName).Collections().GetAllScopes(nil)
	if err != nil {
		return false, err
	}
	for _, scope := range scopes {
		if scope.Name != scopeName {
			continue
		}
		for _, coll := range scope.Collections {
			if coll.Name == collectionName {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

// EnsureCollection creates bucket (128 MiB), scope, and collection as needed, then connects.
func (s *Server) EnsureCollection(bucketName, scopeName, collectionName string) (*gocb.Collection, error) {
	if err := s.requireCluster(); err != nil {
		return nil, err
	}
	if err := s.CreateBucket(bucketName, config.DefaultBucketQuotaMiB, 0); err != nil {
		return nil, err
	}
	if err := s.CreateScope(bucketName, scopeName); err != nil {
		return nil, err
	}
	if err := s.CreateCollection(bucketName, scopeName, collectionName); err != nil {
		return nil, err
	}
	if err := s.ConnectKeyspace(bucketName, scopeName, collectionName); err != nil {
		return nil, err
	}
	return s.collection, nil
}

// ConnectKeyspace opens bucket/scope/collection handles on the Server.
func (s *Server) ConnectKeyspace(bucketName, scopeName, collectionName string) error {
	if err := s.requireCluster(); err != nil {
		return err
	}
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	if scopeName == "" {
		scopeName = "_default"
	}
	if collectionName == "" {
		collectionName = "_default"
	}

	b := s.Cluster.Bucket(bucketName)
	_ = b.WaitUntilReady(15*time.Second, nil)
	s.bucket = b
	s.scope = b.Scope(scopeName)
	s.collection = s.scope.Collection(collectionName)

	if s.Config != nil {
		s.Config.Bucket = bucketName
		s.Config.Scope = scopeName
		s.Config.Collection = collectionName
	}
	return nil
}
