//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
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
	n, err := s.PopulateCollection(jsonl, bucket, scope, collection)
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

func TestCapellaSkippedWithoutToken(t *testing.T) {
	token := os.Getenv("CAPELLA_TOKEN")
	if token == "" {
		t.Skip("CAPELLA_TOKEN not set; skipping live Capella integration")
	}
	t.Logf("CAPELLA_TOKEN present; extend this test for live Capella flows")
	_ = fmt.Sprintf("%s", token[:min(4, len(token))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
