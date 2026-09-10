package rest

import (
	"reflect"
	"testing"

	"github.com/mminichino/cbctl/internal/models"
)

func TestParseNodeSpec(t *testing.T) {
	defaults := []string{"data", "index", "query", "fts"}
	tests := []struct {
		name     string
		spec     string
		wantHost string
		wantRAM  int
		wantSvc  []string
		wantAlt  string
		wantErr  bool
	}{
		{
			name:     "host only",
			spec:     "10.0.0.1",
			wantHost: "10.0.0.1",
			wantRAM:  4,
			wantSvc:  defaults,
		},
		{
			name:     "services and ram",
			spec:     "10.0.0.2=data,index@8",
			wantHost: "10.0.0.2",
			wantRAM:  8,
			wantSvc:  []string{"data", "index"},
		},
		{
			name:     "alternate with ports",
			spec:     "10.0.0.1=data,query@4#ext.example.com;kv:9000,n1ql:9050",
			wantHost: "10.0.0.1",
			wantRAM:  4,
			wantSvc:  []string{"data", "query"},
			wantAlt:  "ext.example.com",
		},
		{
			name:    "invalid ram",
			spec:    "host@abc",
			wantErr: true,
		},
		{
			name:    "empty services",
			spec:    "host=",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNodeSpec(tt.spec, defaults, 4)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Host != tt.wantHost {
				t.Errorf("host=%q want %q", got.Host, tt.wantHost)
			}
			if got.RAMGiB != tt.wantRAM {
				t.Errorf("ram=%d want %d", got.RAMGiB, tt.wantRAM)
			}
			if !reflect.DeepEqual(got.Services, tt.wantSvc) {
				t.Errorf("services=%v want %v", got.Services, tt.wantSvc)
			}
			if got.AlternateAddress != tt.wantAlt {
				t.Errorf("alt=%q want %q", got.AlternateAddress, tt.wantAlt)
			}
			if tt.wantAlt == "ext.example.com" {
				if got.AlternatePorts["kv"] != 9000 || got.AlternatePorts["n1ql"] != 9050 {
					t.Errorf("ports=%v", got.AlternatePorts)
				}
			}
		})
	}
}

func TestCalculateServerQuotasValues(t *testing.T) {
	node := models.ClusterNodeConfig{
		RAMGiB:   4,
		Services: []string{"data", "index", "query", "fts"},
	}
	quotas := CalculateServerQuotas(node, nil)
	// available = floor(4*1024*0.8)=3276; count=3; default=1092
	if quotas["data"] != 1092 || quotas["index"] != 1092 || quotas["fts"] != 1092 {
		t.Fatalf("quotas=%v", quotas)
	}
	if _, ok := quotas["query"]; ok {
		t.Fatalf("query should not have quota")
	}
}

func TestNormalizeServices(t *testing.T) {
	if NormalizeServerService("kv") != "data" {
		t.Fatal("kv -> data")
	}
	if ToRESTService("query") != "n1ql" {
		t.Fatal("query -> n1ql")
	}
	if NormalizeCapellaService("fts") != "search" {
		t.Fatal("fts -> search")
	}
}

func TestClusterInitHostname(t *testing.T) {
	if ClusterInitHostname("localhost") != "127.0.0.1" {
		t.Fatal()
	}
	if ClusterInitHostname("node1") != "node1.local" {
		t.Fatal()
	}
	if ClusterInitHostname("10.0.0.1") != "10.0.0.1" {
		t.Fatal()
	}
}

func TestFormatClusterMapText(t *testing.T) {
	text := FormatClusterMapText("127.0.0.1", nil, map[string]any{
		"nodes": []any{
			map[string]any{
				"configuredHostname": "127.0.0.1:8091",
				"version":            "7.6.0",
				"os":                 "x86_64-linux",
				"services":           []any{"kv", "n1ql", "index"},
			},
		},
	})
	if !reflect.DeepEqual(text != "", true) {
		t.Fatal("empty map text")
	}
	if got := text; got == "" || got[:len("Cluster Host List:")] != "Cluster Host List:" {
		t.Fatalf("unexpected: %q", text)
	}
}
