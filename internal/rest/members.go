package rest

import (
	"fmt"
	"strings"
)

// membershipInactiveAdded is clusterMembership of a node that was added but not yet rebalanced in.
const membershipInactiveAdded = "inactiveAdded"

// NodeInfo describes one node of a cluster as /pools/default reports it.
type NodeInfo struct {
	// Host is the node's management host without port.
	Host string
	// Membership is clusterMembership: "active", "inactiveAdded" or "inactiveFailed".
	Membership string
	// Services are the node's services as the REST API names them (kv, index, n1ql, fts, ...).
	Services []string
}

// Pending reports whether the node was added but not yet rebalanced in.
func (n NodeInfo) Pending() bool { return n.Membership == membershipInactiveAdded }

// ClusterNodes lists the nodes of the cluster that endpoint belongs to, in the order the
// cluster reports them.
func (c *Client) ClusterNodes(endpoint Endpoint, username, password string) ([]NodeInfo, error) {
	payload, err := c.GetPoolsDefault(endpoint, username, password)
	if err != nil {
		return nil, fmt.Errorf("reading cluster nodes: %w", err)
	}
	return nodeInfos(payload), nil
}

// PendingNodes returns the hosts of the nodes that were added but not rebalanced in
// (clusterMembership inactiveAdded); empty when a rebalance has nothing to do.
func (c *Client) PendingNodes(endpoint Endpoint, username, password string) ([]string, error) {
	nodes, err := c.ClusterNodes(endpoint, username, password)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range nodes {
		if n.Pending() {
			out = append(out, n.Host)
		}
	}
	return out, nil
}

// ClusterName returns the clusterName of the cluster that endpoint belongs to. initialized is
// false for a node that is not part of any cluster (or whose admin credentials are refused);
// name is empty for a cluster that was created without one.
func (c *Client) ClusterName(endpoint Endpoint, username, password string) (name string, initialized bool, err error) {
	if !c.IsClusterInitialized(endpoint, username, password) {
		return "", false, nil
	}
	payload, err := c.GetPoolsDefault(endpoint, username, password)
	if err != nil {
		return "", true, fmt.Errorf("reading the cluster name: %w", err)
	}
	name, _ = payload["clusterName"].(string)
	return strings.TrimSpace(name), true, nil
}

// nodeInfos reads the nodes of a /pools/default payload.
func nodeInfos(payload map[string]any) []NodeInfo {
	list, _ := payload["nodes"].([]any)
	out := make([]NodeInfo, 0, len(list))
	for _, item := range list {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		host := nodeRecordHost(record)
		if host == "" {
			continue
		}
		info := NodeInfo{Host: strings.ToLower(host)}
		info.Membership, _ = record["clusterMembership"].(string)
		if services, ok := record["services"].([]any); ok {
			for _, s := range services {
				if name, ok := s.(string); ok {
					info.Services = append(info.Services, name)
				}
			}
		}
		out = append(out, info)
	}
	return out
}
