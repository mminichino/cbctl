package rest

import (
	"fmt"
	"math"
	"strings"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

// CalculateServerQuotas computes memory quotas for non-query services.
func CalculateServerQuotas(node models.ClusterNodeConfig, options map[string]string) map[string]int {
	availableMiB := int(math.Floor(float64(node.RAMGiB) * 1024 * 0.8))
	quotaServiceCount := 0
	for _, service := range node.Services {
		if !IsQueryService(service) {
			quotaServiceCount++
		}
	}
	if quotaServiceCount == 0 {
		quotaServiceCount = 1
	}
	defaultQuota := availableMiB / quotaServiceCount
	if defaultQuota < 256 {
		defaultQuota = 256
	}
	quotas := map[string]int{}
	for _, service := range node.Services {
		if IsQueryService(service) {
			continue
		}
		norm := NormalizeServerService(service)
		quotas[norm] = readQuotaOverride(options, config.ServerQuotaKey(norm), defaultQuota)
	}
	return quotas
}

func readQuotaOverride(options map[string]string, key string, defaultQuota int) int {
	value := strings.TrimSpace(options[key])
	if value == "" {
		return defaultQuota
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return defaultQuota
	}
	return n
}

// ClusterInitHostname normalizes hostnames for /clusterInit.
func ClusterInitHostname(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1"
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return "127.0.0.1"
	}
	if strings.Contains(lower, ".") {
		return host
	}
	return host + ".local"
}

// InitializeSingleNode posts /clusterInit (no auth).
func (c *Client) InitializeSingleNode(endpoint Endpoint, username, password string, services []string, quotas map[string]int, clusterHostname string) error {
	resolved := ClusterInitHostname(clusterHostname)
	fields := map[string]string{
		"hostname":           resolved,
		"username":           username,
		"password":           password,
		"port":               "SAME",
		"services":           ToRESTServices(services),
		"allowedHosts":       resolved,
		"indexerStorageMode": "plasma",
	}
	applyQuotaFields(fields, quotas)
	return c.PostForm(endpoint, nil, nil, "/clusterInit", fields)
}

func applyQuotaFields(fields map[string]string, quotas map[string]int) {
	if v, ok := quotas["data"]; ok {
		fields["memoryQuota"] = fmt.Sprintf("%d", v)
	}
	if v, ok := quotas["index"]; ok {
		fields["indexMemoryQuota"] = fmt.Sprintf("%d", v)
	}
	if v, ok := quotas["fts"]; ok {
		fields["ftsMemoryQuota"] = fmt.Sprintf("%d", v)
	}
	if v, ok := quotas["eventing"]; ok {
		fields["eventingMemoryQuota"] = fmt.Sprintf("%d", v)
	}
	if v, ok := quotas["analytics"]; ok {
		fields["cbasMemoryQuota"] = fmt.Sprintf("%d", v)
	}
}

// AddNode posts /controller/addNode.
func (c *Client) AddNode(endpoint Endpoint, username, password, nodeHost string, services []string) error {
	fields := map[string]string{
		"hostname": nodeHost,
		"user":     username,
		"password": password,
		"services": ToRESTServices(services),
	}
	return c.PostForm(endpoint, Ptr(username), Ptr(password), "/controller/addNode", fields)
}

// Rebalance posts /controller/rebalance with ns_1@ hosts.
func (c *Client) Rebalance(endpoint Endpoint, username, password string, nodeHosts []string) error {
	known := make([]string, 0, len(nodeHosts))
	for _, h := range nodeHosts {
		known = append(known, "ns_1@"+h)
	}
	return c.PostForm(endpoint, Ptr(username), Ptr(password), "/controller/rebalance", map[string]string{
		"knownNodes": strings.Join(known, ","),
	})
}

// SetupAlternateAddress PUTs external alternate address configuration.
func (c *Client) SetupAlternateAddress(endpoint Endpoint, username, password, alternateHost string, ports map[string]int) error {
	fields := map[string]string{"hostname": alternateHost}
	for service, port := range ports {
		fields[service] = fmt.Sprintf("%d", port)
	}
	return c.PutForm(endpoint, Ptr(username), Ptr(password), "/node/controller/setupAlternateAddresses/external", fields)
}
