package server

import (
	"fmt"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/rest"
)

// CreateCluster bootstraps a Server cluster via the management REST API.
// Returns created=false when the cluster is already initialized.
func CreateCluster(cfg *config.Config, options map[string]string) (bool, error) {
	return rest.NewClient().CreateCluster(cfg, options)
}

// ClusterExists reports whether the cluster is already initialized.
func ClusterExists(cfg *config.Config, options map[string]string) bool {
	return rest.NewClient().ClusterExists(cfg, options)
}

// ClusterMap returns formatted cluster host map text.
func ClusterMap(cfg *config.Config) (string, error) {
	return rest.NewClient().ClusterMap(cfg)
}

// CreateClusterOnServer creates a cluster using this server's config when cfg is nil.
func (s *Server) CreateClusterOnServer(cfg *config.Config, options map[string]string) (bool, error) {
	if cfg == nil {
		if s.Config == nil {
			return false, fmt.Errorf("config is required")
		}
		cfg = s.Config
	}
	return CreateCluster(cfg, options)
}

// ClusterExistsOnServer reports whether the cluster is initialized.
func (s *Server) ClusterExistsOnServer(cfg *config.Config, options map[string]string) bool {
	if cfg == nil {
		cfg = s.Config
	}
	if cfg == nil {
		return false
	}
	return ClusterExists(cfg, options)
}

// ClusterMapOnServer returns the cluster host map.
func (s *Server) ClusterMapOnServer(cfg *config.Config) (string, error) {
	if cfg == nil {
		if s.Config == nil {
			return "", fmt.Errorf("config is required")
		}
		cfg = s.Config
	}
	return ClusterMap(cfg)
}
