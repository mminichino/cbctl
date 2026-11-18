//go:build integration && server

package integration_test

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/rest"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startServerOnNetwork starts one Couchbase Server container on the user network nw. No host
// port is published: the test reaches the node on its network address.
func startServerOnNetwork(ctx context.Context, t *testing.T, nw, alias string) (string, error) {
	t.Helper()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          "couchbase/server:enterprise-8.0.3",
			ExposedPorts:   []string{"8091/tcp"},
			Networks:       []string{nw},
			NetworkAliases: map[string][]string{nw: {alias}},
			WaitingFor:     wait.ForHTTP("/ui/index.html").WithPort("8091/tcp").WithStartupTimeout(10 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		return "", err
	}
	return ctr.ContainerIP(ctx)
}

// TestServerManifestMulti applies a two-node CouchbaseServer manifest to two containers on a
// Docker network: the primary is initialized, the second node joins, the cluster rebalances,
// and a bucket with a replica is created. A second apply changes nothing. It runs only with
// CBCTL_IT_SERVER_MULTI=1 (the nightly job) and needs a Linux Docker host, where container
// addresses are reachable from the test process; Docker Desktop does not route to them, so
// there `make test-server-multi-it` runs this same test binary in a Linux container on the VM's
// network (scripts/server-it-linux.sh).
func TestServerManifestMulti(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if os.Getenv("CBCTL_IT_SERVER_MULTI") != "1" {
		t.Skip("CBCTL_IT_SERVER_MULTI is not 1: the two-node Server test runs in the nightly job only")
	}
	if os.Getenv("CBCTL_IT_TARGET") == "fake" {
		t.Skip("CBCTL_IT_TARGET=fake: Server Docker flow is separate from Capella integration")
	}
	if runtime.GOOS != "linux" {
		t.Skip("container network addresses are reachable from the host only on Linux; on Docker Desktop run make test-server-multi-it")
	}
	if !dockerAvailable() {
		if os.Getenv("CBCTL_IT_REQUIRE_DOCKER") == "1" {
			t.Fatal("docker is not available and CBCTL_IT_REQUIRE_DOCKER=1")
		}
		t.Skip("docker is not available; skipping Server container integration")
	}
	ctx := context.Background()

	nw, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	testcontainers.CleanupNetwork(t, nw)

	ip1, err := startServerOnNetwork(ctx, t, nw.Name, "cb1")
	if err != nil {
		t.Fatalf("start node 1: %v", err)
	}
	ip2, err := startServerOnNetwork(ctx, t, nw.Name, "cb2")
	if err != nil {
		t.Fatalf("start node 2: %v", err)
	}
	waitForPools(t, ip1+":8091", 5*time.Minute)
	waitForPools(t, ip2+":8091", 5*time.Minute)

	manifest := `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: it-multi
spec:
  credentials:
    username: Administrator
    password: ${CBCTL_IT_SERVER_PW}
  nodeDefaults:
    services: [data, index, query]
  nodes:
    - ipAddress: ` + ip1 + `
      primary: true
      ramGiB: 2
    - ipAddress: ` + ip2 + `
      ramGiB: 2
  buckets:
    - name: multi-it
      quotaMiB: 128
      replicas: 1
      scopes:
        - name: ops
          collections: [gates]
      import:
        - scope: ops
          collection: gates
          keyField: gateId
          data:
            - gateId: E10
              terminal: E
`
	dir := t.TempDir()
	stdout, stderr, code := applyManifest(t, dir, manifest)
	if code != 0 {
		t.Fatalf("first apply exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"[ok] - Cluster created on " + ip1,
		"[ok] - Node " + ip2 + " added to cluster at " + ip1,
		"[ok] - Cluster rebalanced",
		`[ok] - Bucket "multi-it" created`,
		"[ok] - CouchbaseServer/it-multi applied",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("first apply stdout lacks %q:\n%s", want, stdout)
		}
	}

	// Both nodes are active members and nothing waits to be rebalanced in.
	client := rest.NewClient()
	ep := rest.ForServer(ip1, false)
	nodes, err := client.ClusterNodes(ep, "Administrator", "password")
	if err != nil {
		t.Fatalf("cluster nodes: %v", err)
	}
	active := map[string]bool{}
	for _, n := range nodes {
		if n.Membership == "active" {
			active[n.Host] = true
		}
	}
	if len(nodes) != 2 || !active[ip1] || !active[ip2] {
		t.Errorf("cluster nodes = %+v, want %s and %s active", nodes, ip1, ip2)
	}
	if pending, err := client.PendingNodes(ep, "Administrator", "password"); err != nil || len(pending) != 0 {
		t.Errorf("pending nodes = %v, err = %v; want none", pending, err)
	}

	stdout, stderr, code = applyManifest(t, dir, manifest)
	if code != 0 {
		t.Fatalf("second apply exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "[ok]") {
		t.Errorf("second apply changed something:\n%s", stdout)
	}
	for _, want := range []string{"Node " + ip2 + " already configured", "Rebalance not needed", "no changes"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("second apply stdout lacks %q:\n%s", want, stdout)
		}
	}

	cfg := config.New()
	cfg.Hostname, cfg.Username, cfg.Password = ip1, "Administrator", "password"
	cfg.ConnectTimeout = 60
	s := server.New()
	defer s.Disconnect()
	if err := s.Connect(ctx, cfg); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := s.ConnectKeyspace("multi-it", "ops", "gates"); err != nil {
		t.Fatalf("keyspace: %v", err)
	}
	doc, err := s.Get("E10")
	if err != nil || doc["terminal"] != "E" {
		t.Errorf("get E10 = %v, err = %v", doc, err)
	}
}
