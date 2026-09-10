package rest

import (
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

// CreateCluster bootstraps a Couchbase Server cluster.
// Returns created=false when already initialized.
func (c *Client) CreateCluster(cfg *config.Config, options map[string]string) (bool, error) {
	merged := cfg.MergedProperties(options)
	nodes := ParseServerNodes(merged)
	if len(nodes) == 0 {
		return false, fmt.Errorf("at least one Couchbase Server node must be configured")
	}
	useSSL := cfg.SSL
	useExtAPI := ParseUseExtAPI(merged)
	first := nodes[0]
	firstInternalHost, _ := ParseHostPort(first.IP, 8091)
	endpoint := NodeEndpoint(first, useSSL, useExtAPI)

	if c.IsClusterInitialized(endpoint, cfg.Username, cfg.Password) {
		return false, nil
	}

	endpoints := make([]Endpoint, 0, len(nodes))
	for _, node := range nodes {
		endpoints = append(endpoints, NodeEndpoint(node, useSSL, useExtAPI))
	}
	if err := c.WaitForNodesAPI(endpoints, 60); err != nil {
		return false, err
	}

	quotas := CalculateServerQuotas(first, merged)
	if err := c.InitializeSingleNode(endpoint, cfg.Username, cfg.Password, first.Services, quotas, firstInternalHost); err != nil {
		return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
	}

	nodeHosts := []string{ClusterInitHostname(firstInternalHost)}
	for _, node := range nodes[1:] {
		internalHost, _ := ParseHostPort(node.IP, 8091)
		if err := c.AddNode(endpoint, cfg.Username, cfg.Password, internalHost, node.Services); err != nil {
			return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
		}
		nodeHosts = append(nodeHosts, ClusterInitHostname(internalHost))
	}

	if len(nodes) > 1 {
		if err := c.Rebalance(endpoint, cfg.Username, cfg.Password, nodeHosts); err != nil {
			return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
		}
	}

	if err := c.WaitForCluster(endpoint, cfg.Username, cfg.Password, 60); err != nil {
		return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
	}
	if err := c.WaitForRebalanceComplete(endpoint, cfg.Username, cfg.Password, 120); err != nil {
		return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
	}
	if err := c.WaitForClusterServices(endpoint, cfg.Username, cfg.Password, 120); err != nil {
		return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
	}
	if err := c.WaitForQueryReady(endpoint, cfg.Username, cfg.Password, 60); err != nil {
		return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
	}
	if err := c.ApplyAlternateAddresses(nodes, cfg.Username, cfg.Password, useSSL, useExtAPI); err != nil {
		return false, fmt.Errorf("failed to create Couchbase Server cluster: %w", err)
	}
	return true, nil
}

// ClusterExists reports whether the cluster is initialized.
func (c *Client) ClusterExists(cfg *config.Config, options map[string]string) bool {
	merged := cfg.MergedProperties(options)
	nodes := ParseServerNodes(merged)
	if len(nodes) == 0 {
		return false
	}
	endpoint := NodeEndpoint(nodes[0], cfg.SSL, ParseUseExtAPI(merged))
	return c.IsClusterInitialized(endpoint, cfg.Username, cfg.Password)
}

// ClusterMap returns formatted cluster map text.
func (c *Client) ClusterMap(cfg *config.Config) (string, error) {
	endpoint := ForServer(cfg.Hostname, cfg.SSL)
	payload, err := c.GetPoolsDefault(endpoint, cfg.Username, cfg.Password)
	if err != nil {
		return "", err
	}
	return FormatClusterMapText(cfg.Hostname, nil, payload), nil
}

// ApplyAlternateAddresses configures external alternate addresses on each node.
func (c *Client) ApplyAlternateAddresses(nodes []models.ClusterNodeConfig, username, password string, useSSL, useExtAPI bool) error {
	for _, node := range nodes {
		if node.AlternateAddress == "" {
			continue
		}
		altHost, _ := ParseHostPort(node.AlternateAddress, 8091)
		// Setup is performed against the node's own management endpoint (internal).
		ep := ForServer(strings.TrimSpace(ParseHostOnly(node.IP)), useSSL)
		if err := c.SetupAlternateAddress(ep, username, password, altHost, node.AlternatePorts); err != nil {
			// Prefer management via ext API endpoint when configured.
			_ = useExtAPI
			return err
		}
	}
	return nil
}

// ParseHostOnly returns host without port.
func ParseHostOnly(value string) string {
	host, _ := ParseHostPort(value, 8091)
	return host
}
