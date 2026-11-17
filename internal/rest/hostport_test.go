package rest

import (
	"net/http"
	"testing"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/models"
)

func TestParseHostPortForms(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantPort int
	}{
		{"", config.DefaultHostname, 8091},
		{"cb.local", "cb.local", 8091},
		{"10.0.0.1:18091", "10.0.0.1", 18091},
		{"10.0.0.1:18091/path", "10.0.0.1", 18091},
		{"host:abc", "host", 8091},
		{"host:0", "host", 8091},
		{"host:70000", "host", 8091},
		{"::1", "::1", 8091},
		{"fe80::1:2", "fe80::1:2", 8091},
		{"[::1]", "::1", 8091},
		{"[::1]:9000", "::1", 9000},
		{"[2001:db8::5]:18091/x", "2001:db8::5", 18091},
		{"[::1", "::1", 8091},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			host, port := ParseHostPort(tt.in, 8091)
			if host != tt.wantHost || port != tt.wantPort {
				t.Fatalf("ParseHostPort(%q) = %q, %d; want %q, %d", tt.in, host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestAddNodeHostname(t *testing.T) {
	tests := map[string]string{
		"10.0.0.2":       "10.0.0.2",
		"10.0.0.2:8091":  "10.0.0.2",
		"10.0.0.2:9000":  "10.0.0.2:9000",
		"[fd00::2]":      "fd00::2",
		"[fd00::2]:9000": "[fd00::2]:9000",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			if got := addNodeHostname(in); got != want {
				t.Fatalf("addNodeHostname(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestEndpointURLsBracketIPv6(t *testing.T) {
	ep := ForServer("[::1]:9000", false)
	if got := ep.adminURL("/pools"); got != "http://[::1]:9000/pools" {
		t.Fatalf("adminURL = %q", got)
	}
	if got := ep.queryURL("/query/service"); got != "http://[::1]:8093/query/service" {
		t.Fatalf("queryURL = %q", got)
	}
	if got := ForServer("10.0.0.1", true).adminURL("/x"); got != "https://10.0.0.1:18091/x" {
		t.Fatalf("IPv4 adminURL = %q", got)
	}
}

func TestClusterInitHostnameIPv6(t *testing.T) {
	for _, h := range []string{"::1", "fd00::2"} {
		if got := ClusterInitHostname(h); got != h {
			t.Fatalf("ClusterInitHostname(%q) = %q", h, got)
		}
	}
}

// With --ext-api the alternate-address setup goes to the node's alternate address, the same
// endpoint every other create step uses.
func TestApplyAlternateAddressesUsesExtAPI(t *testing.T) {
	ext := newFakeCB(t)
	ext.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}})
	nodes := []models.ClusterNodeConfig{{IP: "192.0.2.1:1", AlternateAddress: ext.addr()}}
	if err := NewClient().ApplyAlternateAddresses(nodes, "u", "p", false, true); err != nil {
		t.Fatal(err)
	}
	if n := ext.count(http.MethodPut, "/node/controller/setupAlternateAddresses/external"); n != 1 {
		t.Fatalf("setup on ext endpoint = %d, want 1", n)
	}
}
