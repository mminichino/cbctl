package rest

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
				{OTPNode: "ns_1@cb-node.internal", Hostname: "10.81.0.2:8091"},
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
	otp, _, err = findNodeInGroups(groups, "10.81.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if otp != "ns_1@cb-node.internal" {
		t.Fatalf("otp=%s", otp)
	}
	if _, _, err := findNodeInGroups(groups, "10.0.0.9"); err == nil {
		t.Fatal("expected missing node error")
	}
}

func TestServerGroupRevisionFromURI(t *testing.T) {
	// Couchbase omits a top-level rev field and puts it on uri.
	const uri = "/pools/default/serverGroups?rev=93706877"
	if got := serverGroupRevision(nil, uri); got != "93706877" {
		t.Fatalf("rev from uri = %q", got)
	}
	if got := serverGroupRevision(float64(112632175), ""); got != "112632175" {
		t.Fatalf("rev from number = %q", got)
	}
	if got := serverGroupsUpdatePath(uri, ""); got != uri {
		t.Fatalf("update path = %q", got)
	}
	if got := serverGroupsUpdatePath("", "42"); got != "/pools/default/serverGroups?rev=42" {
		t.Fatalf("update path from rev = %q", got)
	}
	if got := serverGroupsUpdatePath("/pools/default/serverGroups", ""); got != "" {
		t.Fatalf("missing rev path = %q", got)
	}
}

func TestOtpHostEmpty(t *testing.T) {
	if got := otpHost(""); got != "" {
		t.Fatalf("empty otp host = %q", got)
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

func TestToRESTServicesEmptyIsArbiter(t *testing.T) {
	if got := ToRESTServices(nil); got != "" {
		t.Fatalf("nil services=%q", got)
	}
	if got := ToRESTServices([]string{}); got != "" {
		t.Fatalf("empty services=%q", got)
	}
}

func TestAddNodePostsEmptyServicesForArbiter(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/controller/addNode" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"otpNode":"ns_1@10.0.0.3"}`))
	}))
	t.Cleanup(srv.Close)

	host, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	ep := Endpoint{Host: host, AdminPort: port}
	if err := NewClient().AddNode(ep, "Administrator", "password", "10.0.0.3", nil); err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values["services"]; !ok {
		t.Fatalf("services field missing from form %q (omitting defaults to kv)", gotBody)
	}
	if values.Get("services") != "" {
		t.Fatalf("services=%q want empty for arbiter", values.Get("services"))
	}
	if values.Get("hostname") != "10.0.0.3" {
		t.Fatalf("hostname=%q", values.Get("hostname"))
	}
}

func TestJoinNodeArbiterRejectsServices(t *testing.T) {
	_, err := NewClient().JoinNode(ProvisionOptions{
		IPAddress:      "10.0.0.3",
		RallyIPAddress: "10.0.0.1",
		Username:       "Administrator",
		Password:       "password",
		Arbiter:        true,
		Services:       []string{"data"},
	})
	if err == nil || !strings.Contains(err.Error(), "arbiter") {
		t.Fatalf("expected arbiter services error, got %v", err)
	}
}
