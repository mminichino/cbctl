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

func TestParseNodeSpecOmittedRAM(t *testing.T) {
	got, err := ParseNodeSpec("10.0.0.1", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.RAMGiB != 0 {
		t.Fatalf("omitted ram=%d", got.RAMGiB)
	}
	got, err = ParseNodeSpec("10.0.0.1@8", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.RAMGiB != 8 {
		t.Fatalf("explicit ram=%d", got.RAMGiB)
	}
	if _, err := ParseNodeSpec("10.0.0.1@0", nil, 0); err == nil {
		t.Fatal("expected @0 to be rejected")
	}
}

func TestParseServerNodesRAMOptional(t *testing.T) {
	nodes := ParseServerNodes(map[string]string{
		"couchbase.server.0.ip": "10.0.0.1",
	})
	if len(nodes) != 1 || nodes[0].RAMGiB != 0 {
		t.Fatalf("nodes=%+v", nodes)
	}
	nodes = ParseServerNodes(map[string]string{
		"couchbase.server.0.ip":  "10.0.0.1",
		"couchbase.server.0.ram": "8",
	})
	if nodes[0].RAMGiB != 8 {
		t.Fatalf("ram=%d", nodes[0].RAMGiB)
	}
}

func TestMemoryTotalBytesUsesOverride(t *testing.T) {
	got, err := NewClient().MemoryTotalBytes(Endpoint{}, "", "", 8)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(8)<<30 {
		t.Fatalf("override bytes=%d", got)
	}
	quotas := CalculateServiceQuotas(got, []string{"data", "index", "query", "fts"}, nil)
	// 8 GiB * 80% = 6553 MiB, split across data/index/fts.
	if quotas["data"] != 2184 || quotas["analytics"] != 1024 {
		t.Fatalf("quotas=%v", quotas)
	}
}

func TestCalculateServerQuotasValues(t *testing.T) {
	node := models.ClusterNodeConfig{
		RAMGiB:   4,
		Services: []string{"data", "index", "query", "fts"},
	}
	quotas := CalculateServerQuotas(node, nil)
	// 4 GiB * 80% = 3276 MiB, split across data/index/fts. Query has no quota.
	if quotas["data"] != 1092 || quotas["index"] != 1092 || quotas["fts"] != 1092 {
		t.Fatalf("quotas=%v", quotas)
	}
	if quotas["analytics"] != 1024 || quotas["eventing"] != 256 {
		t.Fatalf("unused quotas=%v", quotas)
	}
	if _, ok := quotas["query"]; ok {
		t.Fatalf("query should not have quota")
	}
}

func TestCalculateServiceQuotasFromMemoryTotal(t *testing.T) {
	const mem = int64(32) << 30
	if got := AvailableMemoryMiB(mem); got != 26214 {
		t.Fatalf("available=%d", got)
	}
	quotas := CalculateServiceQuotas(mem, []string{"data", "index", "query", "fts"}, nil)
	if quotas["data"] != 8738 || quotas["index"] != 8738 || quotas["fts"] != 8738 {
		t.Fatalf("enabled quotas=%v", quotas)
	}
	if quotas["analytics"] != 1024 || quotas["eventing"] != 256 {
		t.Fatalf("unused quotas=%v", quotas)
	}

	// A node that adds analytics shares available RAM across the four quota services on that node.
	withAnalytics := CalculateServiceQuotas(mem, []string{"data", "index", "query", "fts", "analytics"}, nil)
	if withAnalytics["analytics"] != 6553 || withAnalytics["data"] != 6553 {
		t.Fatalf("analytics node quotas=%v", withAnalytics)
	}
	if withAnalytics["eventing"] != 256 {
		t.Fatalf("eventing=%d", withAnalytics["eventing"])
	}
}

func TestQuotasForNewServices(t *testing.T) {
	const mem = int64(32) << 30
	desired := CalculateServiceQuotas(mem, []string{"kv", "n1ql", "index", "fts", "cbas"}, nil)
	cluster := map[string]bool{"data": true, "query": true, "index": true, "fts": true}
	updates := quotasForNewServices(cluster, []string{"data", "index", "query", "fts", "analytics"}, desired)
	if len(updates) != 1 || updates["analytics"] != 6553 {
		t.Fatalf("updates=%v", updates)
	}

	already := quotasForNewServices(map[string]bool{"analytics": true}, []string{"analytics"}, desired)
	if len(already) != 0 {
		t.Fatalf("expected no update, got %v", already)
	}
}

func TestReconcileServiceQuotasLeavesRemoteServices(t *testing.T) {
	const mem = int64(32) << 30
	desired := CalculateServiceQuotas(mem, []string{"data", "index", "query", "fts"}, nil)
	cluster := map[string]bool{"data": true, "index": true, "query": true, "fts": true, "analytics": true}
	updates := reconcileServiceQuotas(cluster, []string{"data", "index", "query", "fts"}, desired)
	if _, ok := updates["analytics"]; ok {
		t.Fatalf("analytics running elsewhere should be unchanged: %v", updates)
	}
	if updates["data"] != 8738 || updates["eventing"] != 256 {
		t.Fatalf("updates=%v", updates)
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
