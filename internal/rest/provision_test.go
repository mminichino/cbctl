package rest

import (
	"strings"
	"testing"
)

func TestNodeInPools(t *testing.T) {
	payload := map[string]any{
		"nodes": []any{
			map[string]any{
				"otpNode":            "ns_1@10.0.0.1",
				"configuredHostname": "10.0.0.1:8091",
				"hostname":           "10.0.0.1:8091",
			},
			map[string]any{
				"otpNode":            "ns_1@10.0.0.2",
				"configuredHostname": "10.0.0.2:8091",
			},
		},
	}
	if !nodeInPools(payload, "10.0.0.1") {
		t.Fatal("expected 10.0.0.1 in cluster")
	}
	if !nodeInPools(payload, "10.0.0.2:8091") {
		t.Fatal("expected 10.0.0.2 in cluster")
	}
	if nodeInPools(payload, "10.0.0.3") {
		t.Fatal("did not expect 10.0.0.3 in cluster")
	}
}

func TestRebuildGroupsMovingNode(t *testing.T) {
	groups := []ServerGroup{
		{
			Name: "Group 1",
			URI:  "/pools/default/serverGroups/0",
			Nodes: []ServerGroupNode{
				{OTPNode: "ns_1@10.0.0.1"},
				{OTPNode: "ns_1@10.0.0.2"},
			},
		},
		{
			Name:  "us-east-1a",
			URI:   "/pools/default/serverGroups/abc",
			Nodes: []ServerGroupNode{},
		},
	}
	moved := rebuildGroupsMovingNode(groups, "ns_1@10.0.0.2", "/pools/default/serverGroups/0", "/pools/default/serverGroups/abc")
	if len(moved) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(moved))
	}
	g0Nodes := moved[0]["nodes"].([]map[string]string)
	g1Nodes := moved[1]["nodes"].([]map[string]string)
	if len(g0Nodes) != 1 || g0Nodes[0]["otpNode"] != "ns_1@10.0.0.1" {
		t.Fatalf("group 0 nodes = %#v", g0Nodes)
	}
	if len(g1Nodes) != 1 || g1Nodes[0]["otpNode"] != "ns_1@10.0.0.2" {
		t.Fatalf("group 1 nodes = %#v", g1Nodes)
	}
}

func TestFindNodeInGroups(t *testing.T) {
	groups := []ServerGroup{
		{
			Name: "Group 1",
			URI:  "/pools/default/serverGroups/0",
			Nodes: []ServerGroupNode{
				{OTPNode: "ns_1@10.0.0.1"},
			},
		},
	}
	otp, uri, err := findNodeInGroups(groups, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if otp != "ns_1@10.0.0.1" || uri != "/pools/default/serverGroups/0" {
		t.Fatalf("otp=%s uri=%s", otp, uri)
	}
	if _, _, err := findNodeInGroups(groups, "10.0.0.9"); err == nil {
		t.Fatal("expected missing node error")
	}
}

func TestClusterInitRequestPaths(t *testing.T) {
	// Smoke-test field assembly by posting against a closed port is overkill;
	// verify helpers used by provisioner mode.
	if got := ClusterInitHostname("localhost"); got != "127.0.0.1" {
		t.Fatalf("localhost -> %s", got)
	}
	if got := ClusterInitHostname("10.0.0.1"); got != "10.0.0.1" {
		t.Fatalf("ip -> %s", got)
	}
	if !hasService([]string{"data", "search"}, "fts") {
		t.Fatal("search should normalize to fts")
	}
	if hasService([]string{"data"}, "query") {
		t.Fatal("query should be absent")
	}
}

func TestParseServicesSearchAlias(t *testing.T) {
	got := ParseServices("data,search,query", DefaultServerServices)
	joined := strings.Join(got, ",")
	if joined != "data,fts,query" {
		t.Fatalf("got %s", joined)
	}
}
