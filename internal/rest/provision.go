package rest

import (
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

// ProvisionOptions configures per-node cluster bootstrap, join, and rebalance.
// All targets are reached over the management REST API so commands work from a
// remote controller or when run on the node being configured.
type ProvisionOptions struct {
	IPAddress         string
	ExternalIPAddress string
	RallyIPAddress    string
	Services          []string
	ServerGroup       string
	DataPath          string
	ClusterName       string
	RAMGiB            int
	Username          string
	Password          string
	SSL               bool
}

// BootstrapPrimary initializes a single-node cluster on IPAddress.
// Returns created=false when the node is already initialized.
func (c *Client) BootstrapPrimary(opts ProvisionOptions) (bool, error) {
	if err := validateProvisionNode(opts); err != nil {
		return false, err
	}
	services := opts.Services
	if len(services) == 0 {
		services = append([]string(nil), DefaultServerServices...)
	}
	ram := opts.RAMGiB
	if ram <= 0 {
		ram = config.DefaultRAMGiB
	}

	local := ForServer(opts.IPAddress, opts.SSL)
	if err := c.WaitForNodeAPI(local, 60); err != nil {
		return false, err
	}
	if c.IsClusterInitialized(local, opts.Username, opts.Password) {
		return false, nil
	}

	if path := strings.TrimSpace(opts.DataPath); path != "" {
		if err := c.SetNodePaths(local, "", "", path); err != nil {
			return false, fmt.Errorf("set node paths: %w", err)
		}
	}

	quotas := CalculateServerQuotas(models.ClusterNodeConfig{
		IP:       opts.IPAddress,
		RAMGiB:   ram,
		Services: services,
	}, nil)

	internalHost, _ := ParseHostPort(opts.IPAddress, 8091)
	if err := c.ClusterInit(local, ClusterInitRequest{
		Hostname:     internalHost,
		Username:     opts.Username,
		Password:     opts.Password,
		Services:     services,
		Quotas:       quotas,
		ClusterName:  opts.ClusterName,
		DataPath:     opts.DataPath,
		AllowedHosts: "*",
	}); err != nil {
		return false, fmt.Errorf("cluster init: %w", err)
	}

	if err := c.WaitForCluster(local, opts.Username, opts.Password, 60); err != nil {
		return false, err
	}
	if err := c.applyExternalAddress(local, opts); err != nil {
		return false, err
	}
	if err := c.AssignNodeServerGroup(local, opts.Username, opts.Password, internalHost, opts.ServerGroup, 10); err != nil {
		return false, err
	}
	if hasService(services, "data") && hasService(services, "query") && hasService(services, "index") {
		if err := c.WaitForClusterServices(local, opts.Username, opts.Password, 120); err != nil {
			return false, err
		}
	}
	if hasService(services, "query") {
		_ = c.WaitForQueryReady(local, opts.Username, opts.Password, 30)
	}
	return true, nil
}

func hasService(services []string, want string) bool {
	want = NormalizeServerService(want)
	for _, s := range services {
		if NormalizeServerService(s) == want {
			return true
		}
	}
	return false
}

// JoinNode initializes the local node (paths) and adds it to the rally cluster without rebalancing.
// Returns joined=false when the node is already a cluster member.
func (c *Client) JoinNode(opts ProvisionOptions) (bool, error) {
	if err := validateProvisionNode(opts); err != nil {
		return false, err
	}
	rallyHost := strings.TrimSpace(opts.RallyIPAddress)
	if rallyHost == "" {
		return false, fmt.Errorf("--rally-ip-address is required")
	}
	services := opts.Services
	if len(services) == 0 {
		services = append([]string(nil), DefaultServerServices...)
	}

	local := ForServer(opts.IPAddress, opts.SSL)
	rally := ForServer(rallyHost, opts.SSL)
	if err := c.WaitForNodesAPI([]Endpoint{local, rally}, 60); err != nil {
		return false, err
	}
	if !c.IsClusterInitialized(rally, opts.Username, opts.Password) {
		return false, fmt.Errorf("rally cluster at %s is not initialized", rallyHost)
	}

	internalHost, _ := ParseHostPort(opts.IPAddress, 8091)
	if ok, err := c.IsNodeInCluster(rally, opts.Username, opts.Password, internalHost); err != nil {
		return false, err
	} else if ok {
		_ = c.applyExternalAddress(local, opts)
		_ = c.AssignNodeServerGroup(rally, opts.Username, opts.Password, internalHost, opts.ServerGroup, 5)
		return false, nil
	}

	if path := strings.TrimSpace(opts.DataPath); path != "" {
		// Fresh nodes accept path settings without credentials.
		if err := c.SetNodePaths(local, "", "", path); err != nil {
			// If the node already has credentials, retry with admin auth.
			if err2 := c.SetNodePaths(local, opts.Username, opts.Password, path); err2 != nil {
				return false, fmt.Errorf("set node paths: %w", err)
			}
		}
	}

	if err := c.AddNode(rally, opts.Username, opts.Password, internalHost, services); err != nil {
		if ok, checkErr := c.IsNodeInCluster(rally, opts.Username, opts.Password, internalHost); checkErr == nil && ok {
			return false, nil
		}
		return false, fmt.Errorf("add node: %w", err)
	}

	if err := c.WaitForNodeInCluster(rally, opts.Username, opts.Password, internalHost, 60); err != nil {
		return false, err
	}
	if err := c.applyExternalAddress(local, opts); err != nil {
		return false, err
	}
	if err := c.AssignNodeServerGroup(rally, opts.Username, opts.Password, internalHost, opts.ServerGroup, 10); err != nil {
		return false, err
	}
	return true, nil
}

// RebalanceCluster rebalances all known nodes on the rally cluster.
func (c *Client) RebalanceCluster(opts ProvisionOptions) error {
	rallyHost := strings.TrimSpace(opts.RallyIPAddress)
	if rallyHost == "" {
		rallyHost = strings.TrimSpace(opts.IPAddress)
	}
	if rallyHost == "" {
		return fmt.Errorf("--rally-ip-address is required")
	}
	rally := ForServer(rallyHost, opts.SSL)
	if err := c.WaitForNodeAPI(rally, 30); err != nil {
		return err
	}
	if !c.IsClusterInitialized(rally, opts.Username, opts.Password) {
		return fmt.Errorf("rally cluster at %s is not initialized", rallyHost)
	}

	hosts, err := c.ClusterNodeHosts(rally, opts.Username, opts.Password)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return fmt.Errorf("no nodes found in cluster")
	}
	if err := c.Rebalance(rally, opts.Username, opts.Password, hosts); err != nil {
		return fmt.Errorf("rebalance: %w", err)
	}
	if err := c.WaitForRebalanceComplete(rally, opts.Username, opts.Password, 180); err != nil {
		return err
	}
	return nil
}

func validateProvisionNode(opts ProvisionOptions) error {
	if strings.TrimSpace(opts.IPAddress) == "" {
		return fmt.Errorf("--ip-address is required")
	}
	if strings.TrimSpace(opts.Username) == "" {
		return fmt.Errorf("username is required")
	}
	if strings.TrimSpace(opts.Password) == "" {
		return fmt.Errorf("password is required")
	}
	return nil
}

func (c *Client) applyExternalAddress(endpoint Endpoint, opts ProvisionOptions) error {
	ext := strings.TrimSpace(opts.ExternalIPAddress)
	if ext == "" {
		return nil
	}
	altHost, _ := ParseHostPort(ext, 8091)
	if err := c.SetupAlternateAddress(endpoint, opts.Username, opts.Password, altHost, nil); err != nil {
		return fmt.Errorf("set alternate address: %w", err)
	}
	return nil
}

// IsNodeInCluster reports whether nodeHost is listed in /pools/default.
func (c *Client) IsNodeInCluster(endpoint Endpoint, username, password, nodeHost string) (bool, error) {
	payload, err := c.GetPoolsDefault(endpoint, username, password)
	if err != nil {
		return false, err
	}
	return nodeInPools(payload, nodeHost), nil
}

// WaitForNodeInCluster waits until nodeHost appears in /pools/default.
func (c *Client) WaitForNodeInCluster(endpoint Endpoint, username, password, nodeHost string, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		ok, err := c.IsNodeInCluster(endpoint, username, password, nodeHost)
		if err == nil && ok {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("node %s not in cluster yet", nodeHost)
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("waiting for node %s: %w", nodeHost, last)
}

// ClusterNodeHosts returns management hostnames for all cluster nodes.
func (c *Client) ClusterNodeHosts(endpoint Endpoint, username, password string) ([]string, error) {
	payload, err := c.GetPoolsDefault(endpoint, username, password)
	if err != nil {
		return nil, err
	}
	nodes, _ := payload["nodes"].([]any)
	out := make([]string, 0, len(nodes))
	seen := map[string]bool{}
	for _, item := range nodes {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		host := nodeRecordHost(record)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out, nil
}

func nodeInPools(payload map[string]any, nodeHost string) bool {
	want, _ := ParseHostPort(nodeHost, 8091)
	want = strings.ToLower(want)
	nodes, _ := payload["nodes"].([]any)
	for _, item := range nodes {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(nodeRecordHost(record), want) {
			return true
		}
	}
	return false
}

func nodeRecordHost(record map[string]any) string {
	if otp, ok := record["otpNode"].(string); ok {
		if host := otpHost(otp); host != "" {
			return host
		}
	}
	for _, key := range []string{"configuredHostname", "hostname"} {
		if h, ok := record[key].(string); ok && strings.TrimSpace(h) != "" {
			host, _ := ParseHostPort(h, 8091)
			return host
		}
	}
	return ""
}
