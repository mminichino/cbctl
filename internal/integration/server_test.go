//go:build integration && server

// The Server Docker flow is built only with -tags=integration,server (make test-server-it):
// it needs Docker and a Couchbase Server image, so it stays out of the Capella fake and
// sandbox suites and runs in its own CI job (P-TST-18, ci-sandbox-findings §server-it).

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestServerCLIFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	// Capella fake/sandbox suite (make test-integration-fake) must stay offline and fast.
	if os.Getenv("CBCTL_IT_TARGET") == "fake" {
		t.Skip("CBCTL_IT_TARGET=fake: Server Docker flow is separate from Capella integration")
	}
	if !dockerAvailable() {
		// In the server-it CI job a silent skip would hide the coverage this test provides.
		if os.Getenv("CBCTL_IT_REQUIRE_DOCKER") == "1" {
			t.Fatal("docker is not available and CBCTL_IT_REQUIRE_DOCKER=1")
		}
		t.Skip("docker is not available; skipping Server container integration")
	}
	ctx := context.Background()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "couchbase/server:enterprise-8.0.3",
			ExposedPorts: []string{
				"8091/tcp", "8092/tcp", "8093/tcp", "8094/tcp", "11210/tcp",
			},
			WaitingFor: wait.ForHTTP("/ui/index.html").WithPort("8091/tcp").WithStartupTimeout(10 * time.Minute),
			HostConfigModifier: func(hc *container.HostConfig) {
				localhost := netip.MustParseAddr("127.0.0.1")
				hc.PortBindings = network.PortMap{
					network.MustParsePort("8091/tcp"):  {{HostIP: localhost, HostPort: "8091"}},
					network.MustParsePort("8092/tcp"):  {{HostIP: localhost, HostPort: "8092"}},
					network.MustParsePort("8093/tcp"):  {{HostIP: localhost, HostPort: "8093"}},
					network.MustParsePort("8094/tcp"):  {{HostIP: localhost, HostPort: "8094"}},
					network.MustParsePort("11210/tcp"): {{HostIP: localhost, HostPort: "11210"}},
				}
			},
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start couchbase: %v", err)
	}

	cfg := config.New()
	cfg.Hostname = "127.0.0.1"
	cfg.Username = "Administrator"
	cfg.Password = "password"
	cfg.SSL = false
	cfg.ConnectTimeout = 60

	waitForPools(t, "127.0.0.1:8091", 5*time.Minute)

	if server.ClusterExists(cfg, nil) {
		t.Fatal("expected uninitialized cluster before create")
	}

	created, err := server.CreateCluster(cfg, map[string]string{
		"couchbase.server.0.ip":       "127.0.0.1",
		"couchbase.server.0.ram":      "2",
		"couchbase.server.0.services": "data,index,query,fts",
	})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	if !created {
		t.Fatal("expected cluster to be created")
	}

	created, err = server.CreateCluster(cfg, nil)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if created {
		t.Fatal("expected already configured")
	}
	if !server.ClusterExists(cfg, nil) {
		t.Fatal("expected cluster exists")
	}

	text, err := server.ClusterMap(cfg)
	if err != nil {
		t.Fatalf("cluster map: %v", err)
	}
	if !strings.Contains(text, "Cluster Host List:") {
		t.Fatalf("unexpected map: %s", text)
	}

	s := server.New()
	defer s.Disconnect()
	var connectErr error
	for attempt := 0; attempt < 10; attempt++ {
		connectErr = s.Connect(ctx, cfg)
		if connectErr == nil {
			break
		}
		s.Disconnect()
		time.Sleep(3 * time.Second)
	}
	if connectErr != nil {
		t.Fatalf("connect: %v", connectErr)
	}

	bucket := "cli-test"
	scope := "cli_scope"
	collection := "cli_collection"

	if err := s.CreateBucket(bucket, 128, 0); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	ok, err := s.BucketExists(bucket)
	if err != nil || !ok {
		t.Fatalf("bucket exists: ok=%v err=%v", ok, err)
	}
	if err := s.CreateScope(bucket, scope); err != nil {
		t.Fatalf("create scope: %v", err)
	}
	if err := s.CreateCollection(bucket, scope, collection); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	dir := t.TempDir()
	jsonl := filepath.Join(dir, "docs.jsonl")
	if err := os.WriteFile(jsonl, []byte("{\"n\":1}\n{\"n\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty, err := s.CollectionIsEmpty(bucket, scope, collection)
	if err != nil {
		t.Fatalf("empty check: %v", err)
	}
	if !empty {
		t.Fatal("expected empty collection")
	}
	n, err := s.PopulateCollection(context.Background(), jsonl, bucket, scope, collection)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d", n)
	}

	if err := s.ClusterTest(bucket); err != nil {
		t.Fatalf("cluster test: %v", err)
	}
}

func waitForPools(t *testing.T, mgmtHost string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://" + mgmtHost + "/pools"
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s", url)
}

func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}
