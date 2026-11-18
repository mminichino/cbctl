//go:build integration && server

package integration_test

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const serverManifestYAML = `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: it-cluster
spec:
  credentials:
    username: Administrator
    password: ${CBCTL_IT_SERVER_PW}
  nodeDefaults:
    services: [data, index, query]
  nodes:
    - ipAddress: 127.0.0.1
      ramGiB: 2
  buckets:
    - name: manifest-it
      quotaMiB: 128
      scopes:
        - name: ops
          collections: [flights, gates]
      import:
        - scope: ops
          collection: flights
          file: flights.jsonl
          keyField: flightId
        - scope: ops
          collection: gates
          keyField: gateId
          data:
            - gateId: E10
              terminal: E
            - gateId: E12
              terminal: E
`

// applyManifest runs `cbctl apply -f -` in process against the manifest text.
func applyManifest(t *testing.T, dir, manifest string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	d := cli.DefaultDeps()
	env := map[string]string{"CBCTL_IT_SERVER_PW": "password"}
	d.Env = func(k string) string { return env[k] }
	d.LookupEnv = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	d.Stdout, d.Stderr = &out, &errBuf
	d.Stdin = strings.NewReader(manifest)
	d.Cwd = func() (string, error) { return dir, nil }
	d.Home = t.TempDir()
	d.ResetRedaction = true
	code = cli.Run(context.Background(), append([]string{"apply", "-f", "-"}, args...), d)
	return out.String(), errBuf.String(), code
}

// TestServerManifestApply applies a one-node CouchbaseServer manifest to a fresh container: the
// first run builds the cluster, the data model and the imports; the second run changes nothing
// and imports nothing because the collections are no longer empty (P4 acceptance 5).
func TestServerManifestApply(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if os.Getenv("CBCTL_IT_TARGET") == "fake" {
		t.Skip("CBCTL_IT_TARGET=fake: Server Docker flow is separate from Capella integration")
	}
	if !dockerAvailable() {
		if os.Getenv("CBCTL_IT_REQUIRE_DOCKER") == "1" {
			t.Fatal("docker is not available and CBCTL_IT_REQUIRE_DOCKER=1")
		}
		t.Skip("docker is not available; skipping Server container integration")
	}
	ctx := context.Background()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "couchbase/server:enterprise-8.0.3",
			ExposedPorts: []string{"8091/tcp", "8092/tcp", "8093/tcp", "8094/tcp", "11210/tcp"},
			WaitingFor:   wait.ForHTTP("/ui/index.html").WithPort("8091/tcp").WithStartupTimeout(10 * time.Minute),
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
	waitForPools(t, "127.0.0.1:8091", 5*time.Minute)

	dir := t.TempDir()
	flights := "{\"flightId\": \"UA100\", \"to\": \"SFO\"}\n\n{\"flightId\": \"UA200\", \"to\": \"JFK\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "flights.jsonl"), []byte(flights), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := applyManifest(t, dir, serverManifestYAML)
	if code != 0 {
		t.Fatalf("first apply exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"[ok] - Cluster created on 127.0.0.1",
		`[ok] - Bucket "manifest-it" created`,
		"[ok] - Imported 2 documents into manifest-it.ops.flights",
		"[ok] - Imported 2 documents into manifest-it.ops.gates",
		"[ok] - CouchbaseServer/it-cluster applied",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("first apply stdout lacks %q:\n%s", want, stdout)
		}
	}

	stdout, stderr, code = applyManifest(t, dir, serverManifestYAML)
	if code != 0 {
		t.Fatalf("second apply exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "[ok]") {
		t.Errorf("second apply changed something:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no changes") || !strings.Contains(stderr, "not empty; import skipped") {
		t.Errorf("second apply should be a no-op that skips both imports\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	cfg := config.New()
	cfg.Hostname, cfg.Username, cfg.Password = "127.0.0.1", "Administrator", "password"
	cfg.ConnectTimeout = 60
	for _, tc := range []struct{ coll, key, field, want string }{
		{"flights", "UA100", "to", "SFO"},
		{"flights", "UA200", "to", "JFK"},
		{"gates", "E10", "terminal", "E"},
		{"gates", "E12", "terminal", "E"},
	} {
		s := server.New()
		if err := s.Connect(ctx, cfg); err != nil {
			t.Fatalf("connect: %v", err)
		}
		if err := s.ConnectKeyspace("manifest-it", "ops", tc.coll); err != nil {
			s.Disconnect()
			t.Fatalf("keyspace %s: %v", tc.coll, err)
		}
		doc, err := s.Get(tc.key)
		s.Disconnect()
		if err != nil {
			t.Fatalf("get %s/%s: %v", tc.coll, tc.key, err)
		}
		if doc[tc.field] != tc.want {
			t.Errorf("%s/%s.%s = %v, want %s", tc.coll, tc.key, tc.field, doc[tc.field], tc.want)
		}
	}
}
