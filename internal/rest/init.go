package rest

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

// ClusterInitRequest holds parameters for POST /clusterInit.
type ClusterInitRequest struct {
	Hostname     string
	Username     string
	Password     string
	Services     []string
	Quotas       map[string]int
	ClusterName  string
	DataPath     string
	AllowedHosts string // empty defaults to Hostname; use "*" to allow any join host
}

// quotaService is a cluster memory quota Couchbase requires even when the
// service is not running. Query is omitted: it has no memory quota.
type quotaService struct {
	Name  string
	Field string
	Min   int // lowest MiB the cluster-init / pools API accepts
}

func quotaServiceList() []quotaService {
	return []quotaService{
		{Name: "data", Field: "memoryQuota", Min: 256},
		{Name: "index", Field: "indexMemoryQuota", Min: 256},
		{Name: "fts", Field: "ftsMemoryQuota", Min: 256},
		{Name: "analytics", Field: "cbasMemoryQuota", Min: 1024},
		{Name: "eventing", Field: "eventingMemoryQuota", Min: 256},
	}
}

func quotaMinimum(service string) (int, bool) {
	for _, svc := range quotaServiceList() {
		if svc.Name == service {
			return svc.Min, true
		}
	}
	return 0, false
}

// AvailableMemoryMiB is 80% of memoryTotal bytes, in mebibytes.
func AvailableMemoryMiB(memoryTotalBytes int64) int {
	if memoryTotalBytes <= 0 {
		return 0
	}
	return int(memoryTotalBytes * 4 / 5 / 1024 / 1024)
}

// CalculateServerQuotas computes quotas from a node RAM size in GiB.
// Prefer CalculateServiceQuotas with the node's reported memoryTotal.
func CalculateServerQuotas(node models.ClusterNodeConfig, options map[string]string) map[string]int {
	var bytes int64
	if node.RAMGiB > 0 {
		bytes = int64(node.RAMGiB) << 30
	}
	return CalculateServiceQuotas(bytes, node.Services, options)
}

// CalculateServiceQuotas splits 80% of memoryTotal evenly across enabled
// quota services. Query is not included. Services that are not enabled are
// set to the minimum the API accepts so Couchbase does not apply a larger default.
func CalculateServiceQuotas(memoryTotalBytes int64, services []string, options map[string]string) map[string]int {
	onNode := quotaServicesOnNode(services)
	share := 0
	if len(onNode) > 0 {
		share = AvailableMemoryMiB(memoryTotalBytes) / len(onNode)
	}
	quotas := make(map[string]int, len(quotaServiceList()))
	for _, svc := range quotaServiceList() {
		base := svc.Min
		if onNode[svc.Name] {
			base = share
			if base < svc.Min {
				base = svc.Min
			}
		}
		quota := readQuotaOverride(options, config.ServerQuotaKey(svc.Name), base)
		if quota < svc.Min {
			quota = svc.Min
		}
		quotas[svc.Name] = quota
	}
	return quotas
}

func quotaServicesOnNode(services []string) map[string]bool {
	out := map[string]bool{}
	for _, service := range services {
		norm := NormalizeServerService(service)
		if _, ok := quotaMinimum(norm); ok {
			out[norm] = true
		}
	}
	return out
}

// quotasForNewServices selects quotas for services this node runs that the
// cluster does not already run. The share comes from this node's memory and
// the quota services enabled on this node.
func quotasForNewServices(clusterServices map[string]bool, nodeServices []string, desired map[string]int) map[string]int {
	onNode := quotaServicesOnNode(nodeServices)
	updates := map[string]int{}
	for _, svc := range quotaServiceList() {
		if !onNode[svc.Name] || clusterServices[svc.Name] {
			continue
		}
		updates[svc.Name] = desired[svc.Name]
	}
	return updates
}

// reconcileServiceQuotas sizes services on this node and pins services that
// are not running anywhere to their minimum. Quotas for services already
// running on another node are left unchanged.
func reconcileServiceQuotas(clusterServices map[string]bool, nodeServices []string, desired map[string]int) map[string]int {
	onNode := quotaServicesOnNode(nodeServices)
	updates := map[string]int{}
	for _, svc := range quotaServiceList() {
		if onNode[svc.Name] || !clusterServices[svc.Name] {
			updates[svc.Name] = desired[svc.Name]
		}
	}
	return updates
}

func changedQuotas(current, updates map[string]int) map[string]int {
	if len(updates) == 0 {
		return nil
	}
	out := map[string]int{}
	for svc, quota := range updates {
		if current[svc] != quota {
			out[svc] = quota
		}
	}
	return out
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
	return c.ClusterInit(endpoint, ClusterInitRequest{
		Hostname: clusterHostname,
		Username: username,
		Password: password,
		Services: services,
		Quotas:   quotas,
	})
}

// ClusterInit posts /clusterInit with full provisioner options (no auth).
func (c *Client) ClusterInit(endpoint Endpoint, req ClusterInitRequest) error {
	resolved := ClusterInitHostname(req.Hostname)
	allowed := strings.TrimSpace(req.AllowedHosts)
	if allowed == "" {
		allowed = resolved
	}
	fields := map[string]string{
		"hostname":           resolved,
		"username":           req.Username,
		"password":           req.Password,
		"port":               "SAME",
		"services":           ToRESTServices(req.Services),
		"allowedHosts":       allowed,
		"indexerStorageMode": "plasma",
	}
	if name := strings.TrimSpace(req.ClusterName); name != "" {
		fields["clusterName"] = name
	}
	if path := strings.TrimSpace(req.DataPath); path != "" {
		fields["dataPath"] = path
		fields["indexPath"] = path
		fields["analyticsPath"] = path
		fields["eventingPath"] = path
	}
	applyQuotaFields(fields, req.Quotas)
	return c.PostForm(endpoint, nil, nil, "/clusterInit", fields)
}

func applyQuotaFields(fields map[string]string, quotas map[string]int) {
	for _, svc := range quotaServiceList() {
		v, ok := quotas[svc.Name]
		if !ok {
			continue
		}
		fields[svc.Field] = strconv.Itoa(v)
	}
}

// MemoryTotalBytes returns ramGiB as bytes when the caller set an explicit
// override. Otherwise it reads memoryTotal from the node. ramGiB <= 0 means unset.
func (c *Client) MemoryTotalBytes(endpoint Endpoint, username, password string, ramGiB int) (int64, error) {
	if ramGiB > 0 {
		return int64(ramGiB) << 30, nil
	}
	return c.NodeMemoryTotal(endpoint, username, password)
}

// NodeMemoryTotal reads memoryTotal (bytes) from GET /nodes/self.
// An uninitialized node answers without credentials; a provisioned node requires them.
func (c *Client) NodeMemoryTotal(endpoint Endpoint, username, password string) (int64, error) {
	payload, err := c.getNodeSelf(endpoint, username, password)
	if err != nil {
		return 0, err
	}
	n, ok := anyInt64(payload["memoryTotal"])
	if !ok || n <= 0 {
		return 0, fmt.Errorf("node did not report memoryTotal")
	}
	return n, nil
}

func (c *Client) getNodeSelf(endpoint Endpoint, username, password string) (map[string]any, error) {
	var payload map[string]any
	status, err := c.GetJSON(endpoint, nil, nil, "/nodes/self", &payload)
	if err == nil {
		return payload, nil
	}
	if status == http.StatusUnauthorized && strings.TrimSpace(username) != "" {
		payload = map[string]any{}
		if _, err2 := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/nodes/self", &payload); err2 != nil {
			return nil, err2
		}
		return payload, nil
	}
	return nil, err
}

// ReconcileServiceQuotas sets enabled-service quotas from this node's memory
// and resets services that are not running on any node to their minimums.
func (c *Client) ReconcileServiceQuotas(endpoint Endpoint, username, password string, nodeServices []string, options map[string]string, ramGiB int) error {
	mem, err := c.MemoryTotalBytes(endpoint, username, password, ramGiB)
	if err != nil {
		return fmt.Errorf("read node memory: %w", err)
	}
	desired := CalculateServiceQuotas(mem, nodeServices, options)
	payload, err := c.GetPoolsDefault(endpoint, username, password)
	if err != nil {
		return err
	}
	updates := changedQuotas(serviceQuotasFromPools(payload), reconcileServiceQuotas(servicesFromPools(payload), nodeServices, desired))
	if len(updates) == 0 {
		return nil
	}
	if err := c.SetServiceQuotas(endpoint, username, password, updates); err != nil {
		return fmt.Errorf("set service quotas: %w", err)
	}
	return nil
}

// RaiseQuotasForNewServices increases quotas for services this node introduces.
// Each new service gets an equal share of this node's available RAM, divided by
// the quota services running on this node.
func (c *Client) RaiseQuotasForNewServices(rally, node Endpoint, username, password string, nodeServices []string, ramGiB int) error {
	mem, err := c.MemoryTotalBytes(node, username, password, ramGiB)
	if err != nil {
		return fmt.Errorf("read node memory: %w", err)
	}
	desired := CalculateServiceQuotas(mem, nodeServices, nil)
	payload, err := c.GetPoolsDefault(rally, username, password)
	if err != nil {
		return err
	}
	updates := changedQuotas(serviceQuotasFromPools(payload), quotasForNewServices(servicesFromPools(payload), nodeServices, desired))
	if len(updates) == 0 {
		return nil
	}
	if err := c.SetServiceQuotas(rally, username, password, updates); err != nil {
		return fmt.Errorf("set service quotas: %w", err)
	}
	return nil
}

// SetServiceQuotas posts service memory quotas to /pools/default.
func (c *Client) SetServiceQuotas(endpoint Endpoint, username, password string, quotas map[string]int) error {
	fields := map[string]string{}
	applyQuotaFields(fields, quotas)
	if len(fields) == 0 {
		return nil
	}
	return c.PostForm(endpoint, Ptr(username), Ptr(password), "/pools/default", fields)
}

func serviceQuotasFromPools(payload map[string]any) map[string]int {
	out := map[string]int{}
	for _, svc := range quotaServiceList() {
		n, ok := anyInt64(payload[svc.Field])
		if !ok {
			continue
		}
		out[svc.Name] = int(n)
	}
	return out
}

func servicesFromPools(payload map[string]any) map[string]bool {
	out := map[string]bool{}
	nodes, _ := payload["nodes"].([]any)
	for _, item := range nodes {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		services, _ := record["services"].([]any)
		for _, service := range services {
			name, ok := service.(string)
			if !ok {
				continue
			}
			out[NormalizeServerService(name)] = true
		}
	}
	return out
}

func anyInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), n > 0
	case int:
		return int64(n), n > 0
	case int64:
		return n, n > 0
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil && i > 0
	default:
		return 0, false
	}
}

// SetNodePaths posts /nodes/self/controller/settings for storage paths.
// Auth is optional: pass empty username/password before the node is provisioned.
func (c *Client) SetNodePaths(endpoint Endpoint, username, password, dataPath string) error {
	path := strings.TrimSpace(dataPath)
	if path == "" {
		return nil
	}
	fields := map[string]string{
		"path":          path,
		"index_path":    path,
		"cbas_path":     path,
		"eventing_path": path,
	}
	var userPtr, passPtr *string
	if strings.TrimSpace(username) != "" {
		userPtr = Ptr(username)
		passPtr = Ptr(password)
	}
	return c.PostForm(endpoint, userPtr, passPtr, "/nodes/self/controller/settings", fields)
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
		host := strings.TrimSpace(h)
		if host == "" {
			continue
		}
		if strings.HasPrefix(host, "ns_1@") {
			known = append(known, host)
			continue
		}
		hostOnly, _ := ParseHostPort(host, 8091)
		known = append(known, "ns_1@"+hostOnly)
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
