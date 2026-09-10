package rest

import (
	"strings"
)

// DefaultServerServices are the CLI default services for Server nodes.
var DefaultServerServices = []string{"data", "index", "query", "fts"}

// DefaultCapellaServices are the default Capella service group services.
var DefaultCapellaServices = []string{"data", "query", "index", "search"}

// NormalizeServerService maps aliases to internal Server service names.
func NormalizeServerService(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "kv", "data":
		return "data"
	case "n1ql", "query":
		return "query"
	case "index":
		return "index"
	case "fts", "search":
		return "fts"
	case "eventing":
		return "eventing"
	case "cbas", "analytics":
		return "analytics"
	default:
		return strings.ToLower(strings.TrimSpace(service))
	}
}

// NormalizeCapellaService maps aliases to Capella service names.
func NormalizeCapellaService(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "kv", "data":
		return "data"
	case "n1ql", "query":
		return "query"
	case "index":
		return "index"
	case "fts", "search":
		return "search"
	case "eventing":
		return "eventing"
	case "cbas", "analytics":
		return "analytics"
	default:
		return strings.ToLower(strings.TrimSpace(service))
	}
}

// ToRESTService maps an internal Server service to REST form names.
func ToRESTService(service string) string {
	switch NormalizeServerService(service) {
	case "data":
		return "kv"
	case "query":
		return "n1ql"
	case "index":
		return "index"
	case "fts":
		return "fts"
	case "eventing":
		return "eventing"
	case "analytics":
		return "cbas"
	default:
		return service
	}
}

// ToRESTServices joins services as REST form values.
func ToRESTServices(services []string) string {
	parts := make([]string, 0, len(services))
	for _, s := range services {
		parts = append(parts, ToRESTService(s))
	}
	return strings.Join(parts, ",")
}

// ParseServices splits a comma list using Server normalization.
func ParseServices(value string, defaults []string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		out := make([]string, len(defaults))
		copy(out, defaults)
		return out
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, NormalizeServerService(part))
	}
	return out
}

// ParseCapellaServices splits a comma list using Capella normalization.
func ParseCapellaServices(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		out := make([]string, len(DefaultCapellaServices))
		copy(out, DefaultCapellaServices)
		return out
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, NormalizeCapellaService(part))
	}
	return out
}

// IsQueryService reports whether the service is query/n1ql.
func IsQueryService(service string) bool {
	return NormalizeServerService(service) == "query"
}

// ParseAlternatePorts parses "kv:9000,n1ql:9050" into REST service ports.
func ParseAlternatePorts(value string) (map[string]int, error) {
	ports := map[string]int{}
	value = strings.TrimSpace(value)
	if value == "" {
		return ports, nil
	}
	for _, part := range strings.Split(value, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		service, portText, ok := strings.Cut(item, ":")
		if !ok {
			return nil, ErrInvalidAlternatePort(item)
		}
		var port int
		if _, err := fmtSscanf(portText, &port); err != nil {
			return nil, err
		}
		ports[ToRESTService(strings.TrimSpace(service))] = port
	}
	return ports, nil
}

func fmtSscanf(s string, port *int) (int, error) {
	s = strings.TrimSpace(s)
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, ErrInvalidAlternatePort(s)
		}
		n = n*10 + int(r-'0')
	}
	if s == "" {
		return 0, ErrInvalidAlternatePort(s)
	}
	*port = n
	return 1, nil
}

// ErrInvalidAlternatePort is returned for bad port map fragments.
type ErrInvalidAlternatePort string

func (e ErrInvalidAlternatePort) Error() string {
	return "invalid alternate port " + string(e) + "; expected service:port pairs"
}
