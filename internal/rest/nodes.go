package rest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

var (
	serverNodePattern  = regexp.MustCompile(`^couchbase\.server\.(\d+)\.(.+)$`)
	capellaNodePattern = regexp.MustCompile(`^capella\.cluster\.node\.(\d+)\.(.+)$`)
)

// ParseUseExtAPI reads couchbase.server.extApi.
func ParseUseExtAPI(options map[string]string) bool {
	value := strings.TrimSpace(strings.ToLower(options[config.PropServerExtAPI]))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ParseHostPort splits host:port with a default port.
func ParseHostPort(value string, defaultPort int) (string, int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return config.DefaultHostname, defaultPort
	}
	hostValue := strings.SplitN(value, "/", 2)[0]
	if host, portText, ok := strings.Cut(hostValue, ":"); ok {
		port, err := strconv.Atoi(portText)
		if err != nil {
			return host, defaultPort
		}
		return host, port
	}
	return hostValue, defaultPort
}

// APIHostForNode returns the management host (and optional :port) for a node.
func APIHostForNode(node models.ClusterNodeConfig, useExtAPI bool) string {
	if useExtAPI && node.AlternateAddress != "" {
		return strings.TrimSpace(node.AlternateAddress)
	}
	return strings.TrimSpace(node.IP)
}

// NodeEndpoint builds a REST endpoint for a node.
func NodeEndpoint(node models.ClusterNodeConfig, useSSL, useExtAPI bool) Endpoint {
	return ForServer(APIHostForNode(node, useExtAPI), useSSL)
}

// ParseServerNodes builds node configs from property bags.
func ParseServerNodes(options map[string]string) []models.ClusterNodeConfig {
	nodes := map[int]*models.ClusterNodeConfig{}
	for key, value := range options {
		match := serverNodePattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		index, _ := strconv.Atoi(match[1])
		field := match[2]
		node := nodes[index]
		if node == nil {
			node = &models.ClusterNodeConfig{
				RAMGiB:         config.DefaultRAMGiB,
				Services:       append([]string(nil), DefaultServerServices...),
				AlternatePorts: map[string]int{},
			}
			nodes[index] = node
		}
		switch field {
		case "ip":
			node.IP = value
		case "ram":
			if n, err := strconv.Atoi(value); err == nil {
				node.RAMGiB = n
			}
		case "services":
			node.Services = ParseServices(value, DefaultServerServices)
		case "alternateAddress", "alternate", "external":
			node.AlternateAddress = strings.TrimSpace(value)
		case "alternatePorts":
			if ports, err := ParseAlternatePorts(value); err == nil {
				for k, v := range ports {
					node.AlternatePorts[k] = v
				}
			}
		default:
			if strings.HasPrefix(field, "alternatePort.") {
				service := strings.TrimSpace(strings.TrimPrefix(field, "alternatePort."))
				if service != "" && strings.TrimSpace(value) != "" {
					if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
						node.AlternatePorts[ToRESTService(service)] = n
					}
				}
			}
		}
	}
	if len(nodes) == 0 {
		return []models.ClusterNodeConfig{{
			IP:       options[config.PropHost],
			RAMGiB:   config.DefaultRAMGiB,
			Services: append([]string(nil), DefaultServerServices...),
		}}
	}
	keys := make([]int, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	sortInts(keys)
	out := make([]models.ClusterNodeConfig, 0, len(keys))
	for _, k := range keys {
		n := *nodes[k]
		if n.IP == "" {
			n.IP = config.DefaultHostname
		}
		if n.AlternatePorts == nil {
			n.AlternatePorts = map[string]int{}
		}
		out = append(out, n)
	}
	return out
}

// ParseCapellaNodes builds Capella node configs from properties.
func ParseCapellaNodes(options map[string]string) []models.CapellaNodeConfig {
	nodes := map[int]*models.CapellaNodeConfig{}
	for key, value := range options {
		match := capellaNodePattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		index, _ := strconv.Atoi(match[1])
		field := match[2]
		node := nodes[index]
		if node == nil {
			node = &models.CapellaNodeConfig{
				CPU:      4,
				RAM:      16,
				Services: append([]string(nil), DefaultCapellaServices...),
			}
			nodes[index] = node
		}
		switch field {
		case "cpu":
			if n, err := strconv.Atoi(value); err == nil {
				node.CPU = n
			}
		case "ram":
			if n, err := strconv.Atoi(value); err == nil {
				node.RAM = n
			}
		case "services":
			node.Services = ParseCapellaServices(value)
		}
	}
	keys := make([]int, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	sortInts(keys)
	out := make([]models.CapellaNodeConfig, 0, len(keys))
	for _, k := range keys {
		out = append(out, *nodes[k])
	}
	return out
}

// BuildServerOptions converts node specs into property bag options.
func BuildServerOptions(nodes []models.NodeSpec, extAPI bool) map[string]string {
	options := map[string]string{}
	for i, n := range nodes {
		prefix := fmt.Sprintf("couchbase.server.%d.", i)
		options[prefix+"ip"] = n.Host
		options[prefix+"ram"] = strconv.Itoa(n.RAMGiB)
		options[prefix+"services"] = strings.Join(n.Services, ",")
		if n.AlternateAddress != "" {
			options[prefix+"alternateAddress"] = n.AlternateAddress
		}
		if len(n.AlternatePorts) > 0 {
			parts := make([]string, 0, len(n.AlternatePorts))
			for svc, port := range n.AlternatePorts {
				parts = append(parts, fmt.Sprintf("%s:%d", svc, port))
			}
			options[prefix+"alternatePorts"] = strings.Join(parts, ",")
		}
	}
	if extAPI {
		options[config.PropServerExtAPI] = "true"
	}
	return options
}

func sortInts(a []int) {
	for i := 0; i < len(a); i++ {
		for j := i + 1; j < len(a); j++ {
			if a[j] < a[i] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}
