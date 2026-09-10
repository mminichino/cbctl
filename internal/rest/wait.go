package rest

import (
	"fmt"
	"strings"
	"time"
)

const pollInterval = 2 * time.Second

// IsClusterInitialized reports whether a cluster is already configured.
func (c *Client) IsClusterInitialized(endpoint Endpoint, username, password string) bool {
	var poolsDefault map[string]any
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default", &poolsDefault); err == nil {
		if nodes, ok := poolsDefault["nodes"].([]any); ok && len(nodes) > 0 {
			return true
		}
	}
	var pools map[string]any
	if _, err := c.GetJSON(endpoint, nil, nil, "/pools", &pools); err == nil {
		if list, ok := pools["pools"].([]any); ok && len(list) > 0 {
			return true
		}
	}
	return false
}

// WaitForNodeAPI waits until GET /pools succeeds without auth.
func (c *Client) WaitForNodeAPI(endpoint Endpoint, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		if _, err := c.GetJSON(endpoint, nil, nil, "/pools", &map[string]any{}); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("node API not ready on %s: %w", endpoint.Host, last)
}

// WaitForNodesAPI waits for every node management API.
func (c *Client) WaitForNodesAPI(endpoints []Endpoint, attempts int) error {
	for _, ep := range endpoints {
		if err := c.WaitForNodeAPI(ep, attempts); err != nil {
			return err
		}
	}
	return nil
}

// WaitForCluster waits until /pools/default has nodes.
func (c *Client) WaitForCluster(endpoint Endpoint, username, password string, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		var payload map[string]any
		if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default", &payload); err == nil {
			if nodes, ok := payload["nodes"].([]any); ok && len(nodes) > 0 {
				return nil
			}
			last = fmt.Errorf("pools/default has no nodes")
		} else {
			last = err
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("cluster not ready: %w", last)
}

// WaitForRebalanceComplete waits until rebalance is not running.
func (c *Client) WaitForRebalanceComplete(endpoint Endpoint, username, password string, attempts int) error {
	for i := 0; i < attempts; i++ {
		if !c.isRebalanceInProgress(endpoint, username, password) {
			return nil
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("rebalance did not complete in time")
}

func (c *Client) isRebalanceInProgress(endpoint Endpoint, username, password string) bool {
	var progress map[string]any
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default/rebalanceProgress", &progress); err == nil {
		if status, _ := progress["status"].(string); status == "running" {
			return true
		}
		if status, _ := progress["status"].(string); status == "none" {
			return false
		}
	}
	var pools map[string]any
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default", &pools); err == nil {
		if status, ok := pools["rebalanceStatus"].(string); ok && status != "" && status != "none" {
			return true
		}
	}
	var tasks []any
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default/tasks", &tasks); err == nil {
		for _, item := range tasks {
			task, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if task["type"] == "rebalance" && task["status"] == "running" {
				return true
			}
		}
	}
	return false
}

// WaitForClusterServices waits until kv, n1ql, and index are listed on node 0.
func (c *Client) WaitForClusterServices(endpoint Endpoint, username, password string, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		var payload map[string]any
		if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default", &payload); err != nil {
			last = err
			time.Sleep(pollInterval)
			continue
		}
		nodes, _ := payload["nodes"].([]any)
		if len(nodes) == 0 {
			last = fmt.Errorf("no nodes")
			time.Sleep(pollInterval)
			continue
		}
		node0, _ := nodes[0].(map[string]any)
		services, _ := node0["services"].([]any)
		have := map[string]bool{}
		for _, s := range services {
			if name, ok := s.(string); ok {
				have[name] = true
			}
		}
		if have["kv"] && have["n1ql"] && have["index"] {
			return nil
		}
		last = fmt.Errorf("required services not ready: %v", services)
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("cluster services not ready: %w", last)
}

// WaitForQueryReady waits until the query service accepts SELECT 1.
func (c *Client) WaitForQueryReady(endpoint Endpoint, username, password string, attempts int) error {
	_ = c.PostQueryForm(endpoint, Ptr(username), Ptr(password), "SELECT 1")
	var last error
	for i := 0; i < attempts; i++ {
		if err := c.PostQueryForm(endpoint, Ptr(username), Ptr(password), "SELECT 1"); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("query service not ready: %w", last)
}

// GetPoolsDefault returns /pools/default JSON.
func (c *Client) GetPoolsDefault(endpoint Endpoint, username, password string) (map[string]any, error) {
	var payload map[string]any
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default", &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// FormatClusterMapText formats cluster map output like the Python library.
func FormatClusterMapText(hostname string, srvRecords []map[string]string, clusterInfo map[string]any) string {
	var lines []string
	if len(srvRecords) > 0 {
		lines = append(lines, fmt.Sprintf("Name %s is a domain with SRV records:", hostname))
		for _, record := range srvRecords {
			lines = append(lines, fmt.Sprintf(" => %s (%s)", record["hostname"], record["address"]))
		}
		lines = append(lines, "")
	}
	lines = append(lines, "Cluster Host List:")
	nodes, _ := clusterInfo["nodes"].([]any)
	for index, item := range nodes {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var extHost string
		var extPorts map[string]any
		if alternate, ok := record["alternateAddresses"].(map[string]any); ok {
			if external, ok := alternate["external"].(map[string]any); ok {
				if h, ok := external["hostname"].(string); ok {
					extHost = h
				}
				if ports, ok := external["ports"].(map[string]any); ok {
					extPorts = ports
				}
			}
		}
		hostName, _ := record["configuredHostname"].(string)
		version, _ := record["version"].(string)
		ostype, _ := record["os"].(string)
		var serviceParts []string
		if services, ok := record["services"].([]any); ok {
			for _, s := range services {
				serviceParts = append(serviceParts, fmt.Sprint(s))
			}
		}
		parts := []string{fmt.Sprintf(" [%02d] int: %s", index+1, hostName)}
		if extHost != "" {
			parts = append(parts, "ext: "+extHost)
		}
		for key, val := range extPorts {
			parts = append(parts, fmt.Sprintf("%s:%v", key, val))
		}
		parts = append(parts, fmt.Sprintf("[Services] %s [version] %s [platform] %s",
			strings.Join(serviceParts, ","), version, ostype))
		lines = append(lines, strings.Join(parts, " "))
	}
	return strings.Join(lines, "\n")
}
