//go:build integration

package integration_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/secrets"
	"github.com/mminichino/cbctl/internal/settings"
)

// liveCapella drives the real cbctl binary against the real Capella. It is skipped unless
// sandbox/API credentials are set. Prefer CBCTL_IT_TARGET=sandbox with
// CAPELLA_SANDBOX_*; CAPELLA_API_KEY/ORG_ID still work. Cluster-cost parts need
// CBCTL_IT_CLUSTERS=1 (CBCTL_LIVE_CLUSTER=1 remains an alias).
type liveCapella struct {
	t      *testing.T
	bin    string
	prefix string
}

// newLiveCapella builds cbctl once and picks a unique, prefixed name for everything the run creates.
func newLiveCapella(t *testing.T) *liveCapella {
	t.Helper()
	if wd, err := os.Getwd(); err == nil {
		if _, _, err := settings.LoadDotEnv(wd); err != nil {
			t.Logf("ignoring .env: %v", err)
		}
	}
	if os.Getenv("CBCTL_IT_TARGET") == "fake" {
		t.Skip("CBCTL_IT_TARGET=fake: live binary Capella test is separate from the harness suite")
	}
	key := os.Getenv("CAPELLA_SANDBOX_API_KEY")
	org := os.Getenv("CAPELLA_SANDBOX_ORG_ID")
	if key == "" || org == "" {
		key = os.Getenv("CAPELLA_API_KEY")
		org = os.Getenv("CAPELLA_ORG_ID")
	}
	if key == "" || org == "" {
		t.Skip("CAPELLA_SANDBOX_API_KEY/ORG_ID or CAPELLA_API_KEY/ORG_ID are not set; skipping live Capella tests")
	}
	t.Setenv("CAPELLA_API_KEY", key)
	t.Setenv("CAPELLA_ORG_ID", org)
	bin := filepath.Join(t.TempDir(), "cbctl")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/cbctl")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building cbctl: %v\n%s", err, out)
	}
	prefix := os.Getenv("CBCTL_TEST_PREFIX")
	if prefix == "" {
		prefix = "cbctl-ci"
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	return &liveCapella{t: t, bin: bin, prefix: prefix + "-" + hex.EncodeToString(suffix)}
}

// name returns a run-unique resource name.
func (l *liveCapella) name(part string) string { return l.prefix + "-" + part }

// run executes "cbctl capella ARGS" and returns stdout; it fails the test on a non-zero exit.
func (l *liveCapella) run(args ...string) string {
	l.t.Helper()
	out, errOut, err := l.exec("", args...)
	if err != nil {
		l.t.Fatalf("cbctl capella %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

// runIn is run with a working directory so discovery finds ./capella.yaml.
func (l *liveCapella) runIn(dir string, args ...string) string {
	l.t.Helper()
	out, errOut, err := l.exec(dir, args...)
	if err != nil {
		l.t.Fatalf("cbctl capella %s (dir=%s): %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), dir, err, out, errOut)
	}
	return out
}

func (l *liveCapella) exec(dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(l.bin, append([]string{"capella", "--yes"}, args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

// cleanup registers the janitor: everything the run created lives in a project starting with the
// run prefix, so one cascading delete removes it, and a failure names what is left behind.
func (l *liveCapella) cleanup() {
	l.t.Cleanup(func() {
		if _, errOut, err := l.exec("", "janitor", "--prefix", l.prefix); err != nil {
			l.t.Errorf("LEFTOVERS: cleanup of projects starting with %q failed: %v\n%s\nrun: cbctl capella janitor --prefix %s --yes",
				l.prefix, err, errOut, l.prefix)
		}
	})
}

func TestCapellaLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live test in short mode")
	}
	l := newLiveCapella(t)
	l.cleanup()

	project := l.name("proj")
	t.Run("project", func(t *testing.T) {
		l.run("project", "create", project, "--description", "cbctl live test")
		if out := l.run("project", "list"); !strings.Contains(out, project) {
			t.Fatalf("project list does not show %s:\n%s", project, out)
		}
		l.run("project", "get", project)
		l.run("project", "update", project, "--description", "cbctl live test (updated)")
	})

	t.Run("read-only", func(t *testing.T) {
		l.run("cluster", "options")
		l.run("cluster", "list")
	})

	// §13.4: sync --stdout of the sandbox org parses back through LoadText, and plan of that
	// output is "No changes". Runs whenever the key is present (read-only toward Capella).
	t.Run("sync-plan-roundtrip", func(t *testing.T) {
		dir := t.TempDir()
		out := l.run("sync", "--stdout")
		cfg, err := capellafile.LoadText(out, "stdout", dir, os.Getenv)
		if err != nil {
			t.Fatalf("LoadText(sync --stdout): %v\n%s", err, out)
		}
		if cfg == nil {
			t.Fatal("LoadText returned nil config")
		}
		path := filepath.Join(dir, "capella.yaml")
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
		planOut := l.runIn(dir, "plan")
		if !strings.Contains(planOut, "Plan: 0 to add") && !strings.Contains(planOut, "No changes") {
			t.Fatalf("plan after sync is not clean:\n%s", planOut)
		}
	})

	// §13.4 declarative cluster flow (not the older Phase 2 imperative protect/scale/power path).
	t.Run("cluster", func(t *testing.T) {
		clusters := os.Getenv("CBCTL_IT_CLUSTERS") == "1" || os.Getenv("CBCTL_LIVE_CLUSTER") == "1"
		if os.Getenv("CBCTL_LIVE_CLUSTER") == "1" && os.Getenv("CBCTL_IT_CLUSTERS") == "" {
			t.Log("CBCTL_LIVE_CLUSTER=1 is deprecated; use CBCTL_IT_CLUSTERS=1")
		}
		if !clusters {
			t.Skip("CBCTL_IT_CLUSTERS=1 is not set; skipping the parts that create (billable) clusters")
		}
		dir := t.TempDir()
		envProject := l.name("env")
		envCluster := l.name("clu")
		free := l.freeTierAvailable()
		path := writeLiveYAML(t, dir, free, envProject, envCluster)
		pw := secrets.GeneratePassword(32)
		t.Setenv("CBCTL_LIVE_PWD", pw)

		validateOut := l.runIn(dir, "validate")
		if !strings.Contains(validateOut, "is valid") {
			t.Fatalf("validate:\n%s", validateOut)
		}
		l.runIn(dir, "plan")
		l.runIn(dir, "apply", "--yes")

		second := l.runIn(dir, "apply", "--yes")
		if !strings.Contains(second, "No changes. Capella matches capella.yaml.") {
			t.Fatalf("second apply is not a no-op:\n%s", second)
		}
		drift := l.runIn(dir, "drift")
		if !strings.Contains(drift, "No drift: Capella matches capella.yaml.") {
			t.Fatalf("drift not clean after apply:\n%s", drift)
		}

		// Out-of-band mutate must use --direct so file-managed mode does not absorb the scope.
		l.run("scope", "add", envCluster, "orders", "stray", "--project", envProject, "--direct")
		driftOut, driftErrOut, driftErr := l.exec(dir, "drift", "--detailed-exitcode")
		if driftErr == nil {
			t.Fatalf("drift --detailed-exitcode succeeded after unmanaged scope; stdout:\n%s\nstderr:\n%s", driftOut, driftErrOut)
		}
		if !strings.Contains(driftOut, "Unmanaged") || !strings.Contains(driftOut, "stray") {
			t.Fatalf("drift does not report unmanaged stray:\n%s\n%s", driftOut, driftErrOut)
		}

		l.runIn(dir, "apply", "--prune", "--yes")
		clean := l.runIn(dir, "drift")
		if !strings.Contains(clean, "No drift: Capella matches capella.yaml.") {
			t.Fatalf("drift not clean after prune:\n%s", clean)
		}

		l.runIn(dir, "destroy", "--yes")
		_ = path // kept for debugging failed runs (TempDir retains until test ends)
	})
}

func (l *liveCapella) freeTierAvailable() bool {
	l.t.Helper()
	out := l.run("-o", "json", "cluster", "options")
	var opts struct {
		FreeTier struct {
			Available bool `json:"available"`
		} `json:"freeTier"`
	}
	if err := json.Unmarshal([]byte(out), &opts); err != nil {
		l.t.Fatalf("cluster options json: %v\n%s", err, out)
	}
	return opts.FreeTier.Available
}

// writeLiveYAML writes a §13.4 capella.yaml (free-tier when available, else smallest provisioned).
func writeLiveYAML(t *testing.T, dir string, free bool, project, cluster string) string {
	t.Helper()
	org := os.Getenv("CAPELLA_ORG_ID")
	var body string
	if free {
		body = fmt.Sprintf(`apiVersion: cbctl/v1
organizationId: %s
project:
  name: %s
  description: cbctl live §13.4
clusters:
  - name: %s
    tier: free
    cloudProvider: { type: aws, region: us-east-2 }
    allowedCidrs:
      - cidr: 203.0.113.10/32
        comment: cbctl live
    buckets:
      - name: orders
        memoryAllocationInMb: 100
        scopes:
          - name: inventory
            collections:
              - name: items
    databaseCredentials:
      - name: live_app
        password: ${CBCTL_LIVE_PWD}
        access:
          - privileges: [read]
            resources:
              buckets:
                - name: orders
`, org, project, cluster)
	} else {
		// Derive a unique cluster VPC CIDR from the project suffix to avoid Capella collisions.
		octet := 50
		if parts := strings.Split(project, "-"); len(parts) > 0 {
			hexPart := parts[len(parts)-1]
			if n, err := hex.DecodeString(hexPart); err == nil && len(n) >= 1 {
				octet = int(n[0])%200 + 20
			}
		}
		body = fmt.Sprintf(`apiVersion: cbctl/v1
organizationId: %s
project:
  name: %s
  description: cbctl live §13.4
clusters:
  - name: %s
    description: cbctl live provisioned
    configurationType: singleNode
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.%d.0.0/24 }
    availability: { type: single }
    support: { plan: basic }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 1
        services: [data, index, query]
    deletionProtection: false
    allowedCidrs:
      - cidr: 203.0.113.10/32
        comment: cbctl live
    buckets:
      - name: orders
        memoryAllocationInMb: 256
        scopes:
          - name: inventory
            collections:
              - name: items
    databaseCredentials:
      - name: live_app
        password: ${CBCTL_LIVE_PWD}
        access:
          - privileges: [read]
            resources:
              buckets:
                - name: orders
`, org, project, cluster, octet)
	}
	path := filepath.Join(dir, "capella.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
