// Package server provides Couchbase Server SDK operations for cbctl.
package server

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"

	"github.com/mminichino/cbctl/internal/config"
)

// Server wraps a gocb Cluster and connection config.
type Server struct {
	Cluster *gocb.Cluster
	Config  *config.Config

	bucket     *gocb.Bucket
	scope      *gocb.Scope
	collection *gocb.Collection
}

// New returns an unconnected Server.
func New() *Server {
	return &Server{}
}

// Connect opens an SDK connection using cfg.
func (s *Server) Connect(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if s.Cluster != nil {
		return nil
	}
	s.Config = cfg.Clone()

	connStr := buildConnString(cfg)

	kvTimeout := cfg.KVTimeout
	if kvTimeout <= 0 {
		kvTimeout = config.DefaultKVTimeout
	}
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = config.DefaultConnectTimeout
	}
	queryTimeout := cfg.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = config.DefaultQueryTimeout
	}

	opts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{
			Username: cfg.Username,
			Password: cfg.Password,
		},
		TimeoutsConfig: gocb.TimeoutsConfig{
			KVTimeout:      time.Duration(kvTimeout) * time.Second,
			ConnectTimeout: time.Duration(connectTimeout) * time.Second,
			QueryTimeout:   time.Duration(queryTimeout) * time.Second,
		},
		IoConfig: gocb.IoConfig{
			DisableMutationTokens: true,
		},
	}

	if cfg.SSL {
		if cfg.CACertPath != "" {
			pool, err := loadCACertPool(cfg.CACertPath)
			if err != nil {
				return err
			}
			opts.SecurityConfig = gocb.SecurityConfig{TLSRootCAs: pool}
		} else {
			opts.SecurityConfig = gocb.SecurityConfig{TLSSkipVerify: true}
		}
	}

	cluster, err := gocb.Connect(connStr, opts)
	if err != nil {
		return fmt.Errorf("connect %s: %w", connStr, err)
	}

	waitFor := time.Duration(connectTimeout) * time.Second
	if waitFor < 60*time.Second {
		waitFor = 60 * time.Second
	}
	waitOpts := &gocb.WaitUntilReadyOptions{
		Context:      ctx,
		ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeManagement},
	}
	if err := cluster.WaitUntilReady(waitFor, waitOpts); err != nil {
		_ = cluster.Close(nil)
		return fmt.Errorf("wait until ready: %w", err)
	}

	s.Cluster = cluster
	return nil
}

func buildConnString(cfg *config.Config) string {
	host := strings.TrimSpace(cfg.Hostname)
	if strings.HasPrefix(host, "couchbase://") || strings.HasPrefix(host, "couchbases://") {
		connStr := host
		if cfg.Network != "" && !strings.Contains(connStr, "network=") {
			sep := "?"
			if strings.Contains(connStr, "?") {
				sep = "&"
			}
			connStr += sep + "network=" + cfg.Network
		}
		return connStr
	}
	scheme := "couchbase://"
	if cfg.SSL {
		scheme = "couchbases://"
	}
	connStr := scheme + host
	if cfg.Network != "" {
		connStr += "?network=" + cfg.Network
	}
	return connStr
}

// Disconnect closes the SDK connection and clears keyspace handles.
func (s *Server) Disconnect() {
	s.bucket = nil
	s.scope = nil
	s.collection = nil
	if s.Cluster != nil {
		_ = s.Cluster.Close(nil)
		s.Cluster = nil
	}
}

func loadCACertPool(path string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("no certificates found in %s", path)
	}
	return pool, nil
}

func (s *Server) requireCluster() error {
	if s == nil || s.Cluster == nil {
		return fmt.Errorf("cluster is not connected")
	}
	return nil
}

func (s *Server) requireCollection() error {
	if err := s.requireCluster(); err != nil {
		return err
	}
	if s.collection == nil {
		return fmt.Errorf("collection is not connected")
	}
	return nil
}
