package capella

import "testing"

func TestNormalizeConnectString(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"cb.example.com", "couchbases://cb.example.com"},
		{"couchbases://cb.example.com", "couchbases://cb.example.com"},
		{"couchbase://cb.example.com", "couchbase://cb.example.com"},
		{"  host.cloud  ", "couchbases://host.cloud"},
	}
	for _, tt := range tests {
		got := NormalizeConnectString(tt.in)
		if got != tt.want {
			t.Errorf("NormalizeConnectString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"couchbases://cb.example.com", "cb.example.com"},
		{"couchbase://a,b", "a"},
		{"couchbases://host?opt=1", "host"},
		{"bare.host", "bare.host"},
	}
	for _, tt := range tests {
		got := ExtractHost(tt.in)
		if got != tt.want {
			t.Errorf("ExtractHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractHosts(t *testing.T) {
	hosts := extractHosts("couchbases://a.example.com,b.example.com:18091")
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
	if hosts[0].host != "a.example.com" || hosts[0].port != 0 {
		t.Errorf("host0 = %+v", hosts[0])
	}
	if hosts[1].host != "b.example.com" || hosts[1].port != 18091 {
		t.Errorf("host1 = %+v", hosts[1])
	}
}
