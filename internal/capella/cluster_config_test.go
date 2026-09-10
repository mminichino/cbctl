package capella

import (
	"encoding/json"
	"testing"

	"github.com/mminichino/cbctl/internal/models"
	"github.com/mminichino/cbctl/internal/rest"
)

func TestSingleNodeDefaults(t *testing.T) {
	cfg := NewClusterConfig().SingleNode(nil)
	if cfg.AvailabilityType != AvailabilitySingleZone {
		t.Fatalf("availability = %s, want single", cfg.AvailabilityType)
	}
	if len(cfg.ServiceGroups) != 1 {
		t.Fatalf("service groups = %d, want 1", len(cfg.ServiceGroups))
	}
	g := cfg.ServiceGroups[0]
	if g.NumOfNodes != 1 {
		t.Errorf("numOfNodes = %d, want 1", g.NumOfNodes)
	}
	if g.Storage != 100 {
		t.Errorf("storage = %d, want 100", g.Storage)
	}
	if g.CPU != 4 || g.RAM != 16 {
		t.Errorf("cpu/ram = %d/%d, want 4/16", g.CPU, g.RAM)
	}
	if len(g.Services) != len(rest.DefaultCapellaServices) {
		t.Errorf("services = %v", g.Services)
	}
}

func TestClusterConfigCreateAWSDefaults(t *testing.T) {
	cfg := NewClusterConfig().SingleNode(rest.DefaultCapellaServices)
	req, err := cfg.Create("demo")
	if err != nil {
		t.Fatal(err)
	}
	if req.Name != "demo" {
		t.Errorf("name = %s", req.Name)
	}
	if req.CloudProvider.Type != "aws" {
		t.Errorf("cloud = %s", req.CloudProvider.Type)
	}
	if req.CloudProvider.Region != "us-east-2" {
		t.Errorf("region = %s, want us-east-2", req.CloudProvider.Region)
	}
	if req.Availability.Type != "single" {
		t.Errorf("availability = %s", req.Availability.Type)
	}
	if req.Support.Plan != "developer pro" {
		t.Errorf("plan = %s", req.Support.Plan)
	}
	if req.Support.Timezone != "PT" {
		t.Errorf("timezone = %s", req.Support.Timezone)
	}
	if len(req.ServiceGroups) != 1 || req.ServiceGroups[0].NumOfNodes != 1 {
		t.Errorf("serviceGroups = %+v", req.ServiceGroups)
	}
	disk := req.ServiceGroups[0].Node.Disk
	if disk == nil || disk.Type != "gp3" || disk.Storage == nil || *disk.Storage != 100 {
		t.Errorf("disk = %+v", disk)
	}
	if disk.IOPS == nil || *disk.IOPS != 4370 {
		t.Errorf("iops = %v, want 4370", disk.IOPS)
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["cloudProvider"]; !ok {
		t.Errorf("missing cloudProvider camelCase: %s", raw)
	}
	if _, ok := m["serviceGroups"]; !ok {
		t.Errorf("missing serviceGroups camelCase: %s", raw)
	}
}

func TestBuildClusterConfigFromNodes(t *testing.T) {
	empty := BuildClusterConfig(nil)
	if empty.AvailabilityType != AvailabilitySingleZone || len(empty.ServiceGroups) != 1 {
		t.Fatalf("empty nodes config = %+v", empty)
	}

	nodes := []models.CapellaNodeConfig{
		{CPU: 8, RAM: 32, Services: []string{"data", "query"}},
	}
	cfg := BuildClusterConfig(nodes)
	if len(cfg.ServiceGroups) != 1 {
		t.Fatalf("groups = %d", len(cfg.ServiceGroups))
	}
	g := cfg.ServiceGroups[0]
	if g.CPU != 8 || g.RAM != 32 || g.NumOfNodes != 1 || g.Storage != 100 {
		t.Errorf("group = %+v", g)
	}
}

func TestDiskConfigAWS(t *testing.T) {
	d, err := DiskConfigAWS(50)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != "gp3" || d.IOPS == nil || *d.IOPS != 3000 {
		t.Errorf("disk for 50 = %+v", d)
	}
}
