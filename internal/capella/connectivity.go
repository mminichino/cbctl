package capella

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	managerPortTLS   = 18091
	managerPortPlain = 8091
	defaultPoll      = 2 * time.Second
	defaultDialTO    = 3 * time.Second
	defaultSRVTO     = 30 * time.Second
	defaultSRVPoll   = 2 * time.Second
)

var ipv6HostPort = regexp.MustCompile(`^\[([^\]]+)](?::(\d+))?$`)

// HostPort is a TCP target for Capella manager connectivity.
type HostPort struct {
	Host string
	Port int
}

// Connectivity checks Capella cluster manager reachability via DNS SRV + TCP :18091.
type Connectivity struct {
	SRVLookupTimeout      time.Duration
	SRVLookupPollInterval time.Duration
}

// NewConnectivity returns connectivity checker defaults.
func NewConnectivity() *Connectivity {
	return &Connectivity{
		SRVLookupTimeout:      defaultSRVTO,
		SRVLookupPollInterval: defaultSRVPoll,
	}
}

// CheckConnectivity polls until a manager port is reachable or timeout elapses.
func (c *Connectivity) CheckConnectivity(connectString string, tls bool, timeout time.Duration) bool {
	if c == nil {
		c = NewConnectivity()
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	targets := c.ResolveTargets(connectString, tls, true)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, t := range targets {
			if canConnect(t.Host, t.Port) {
				return true
			}
		}
		refreshed := c.ResolveTargets(connectString, tls, false)
		if len(refreshed) > 0 {
			targets = refreshed
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := defaultPoll
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
	return false
}

// ResolveTargets resolves connect-string hosts to manager HostPorts.
func (c *Connectivity) ResolveTargets(connectString string, tls bool, waitForSRV bool) []HostPort {
	if c == nil {
		c = NewConnectivity()
	}
	defaultPort := managerPortPlain
	if tls {
		defaultPort = managerPortTLS
	}
	hosts := extractHosts(connectString)
	var targets []HostPort
	var srvCandidate string
	if len(hosts) == 1 && hosts[0].port == 0 {
		srvCandidate = hosts[0].host
	}
	for _, h := range hosts {
		if h.port != 0 {
			targets = append(targets, HostPort{Host: h.host, Port: h.port})
			continue
		}
		if srvCandidate != "" && h.host == srvCandidate {
			targets = append(targets, c.resolveSRVTargets(h.host, tls, defaultPort, waitForSRV)...)
		} else {
			targets = append(targets, HostPort{Host: h.host, Port: defaultPort})
		}
	}
	if len(targets) == 0 {
		return []HostPort{{Host: bareHostname(connectString), Port: defaultPort}}
	}
	return targets
}

func (c *Connectivity) resolveSRVTargets(hostname string, tls bool, defaultPort int, waitForSRV bool) []HostPort {
	deadline := time.Now()
	if waitForSRV {
		deadline = time.Now().Add(c.SRVLookupTimeout)
	}
	for {
		names, err := LookupSRVHostnames(hostname, tls)
		if err == nil && len(names) > 0 {
			out := make([]HostPort, 0, len(names))
			for _, n := range names {
				out = append(out, HostPort{Host: n, Port: defaultPort})
			}
			return out
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := c.SRVLookupPollInterval
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
	return []HostPort{{Host: hostname, Port: defaultPort}}
}

// LookupSRVHostnames looks up _couchbases._tcp or _couchbase._tcp records.
func LookupSRVHostnames(hostname string, tls bool) ([]string, error) {
	service := "couchbase"
	if tls {
		service = "couchbases"
	}
	_, addrs, err := net.LookupSRV(service, "tcp", hostname)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, strings.TrimSuffix(a.Target, "."))
	}
	return out, nil
}

type hostPortOpt struct {
	host string
	port int // 0 means unset
}

func extractHosts(connectString string) []hostPortOpt {
	value := strings.TrimSpace(connectString)
	if value == "" {
		return nil
	}
	if !strings.Contains(value, "://") {
		value = "couchbases://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil
	}
	netloc := parsed.Host
	if netloc == "" {
		netloc = parsed.Path
	}
	if at := strings.Index(netloc, "@"); at >= 0 {
		netloc = netloc[at+1:]
	}
	if slash := strings.Index(netloc, "/"); slash >= 0 {
		netloc = netloc[:slash]
	}
	if netloc == "" {
		return nil
	}
	var results []hostPortOpt
	for _, part := range strings.Split(netloc, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if m := ipv6HostPort.FindStringSubmatch(part); m != nil {
			port := 0
			if m[2] != "" {
				port, _ = strconv.Atoi(m[2])
			}
			results = append(results, hostPortOpt{host: m[1], port: port})
			continue
		}
		if strings.Count(part, ":") == 1 {
			host, portStr, _ := strings.Cut(part, ":")
			if port, err := strconv.Atoi(portStr); err == nil {
				results = append(results, hostPortOpt{host: host, port: port})
			} else {
				results = append(results, hostPortOpt{host: part})
			}
		} else {
			results = append(results, hostPortOpt{host: part})
		}
	}
	return results
}

func bareHostname(connectString string) string {
	hosts := extractHosts(connectString)
	if len(hosts) > 0 {
		return hosts[0].host
	}
	return connectString
}

func canConnect(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), defaultDialTO)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// NormalizeConnectString ensures a couchbases:// (or couchbase://) scheme.
func NormalizeConnectString(connectString string) string {
	value := strings.TrimSpace(connectString)
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "couchbases://") || strings.HasPrefix(value, "couchbase://") {
		return value
	}
	return "couchbases://" + value
}

// ExtractHost strips scheme/query from a connect string host.
func ExtractHost(connectString string) string {
	if strings.TrimSpace(connectString) == "" {
		return ""
	}
	stripped := connectString
	for _, prefix := range []string{"couchbases://", "couchbase://"} {
		if strings.HasPrefix(stripped, prefix) {
			stripped = stripped[len(prefix):]
			break
		}
	}
	if i := strings.Index(stripped, ","); i >= 0 {
		stripped = stripped[:i]
	}
	if i := strings.Index(stripped, "?"); i >= 0 {
		stripped = stripped[:i]
	}
	return stripped
}
