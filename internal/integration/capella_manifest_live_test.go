//go:build integration

package integration_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/secrets"
)

// TestCapellaManifestApply_Live applies a CouchbaseCapella manifest to the real Capella with the
// cbctl binary: create, then an unchanged re-apply, then an inline import. It creates a billable
// cluster, so it needs CBCTL_IT_CLUSTERS=1 and the sandbox credentials of TestCapellaLive. The
// project name starts with the run prefix (cbctl-ci-), so the janitor removes it.
func TestCapellaManifestApply_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live test in short mode")
	}
	l := newLiveCapella(t)
	if os.Getenv("CBCTL_IT_CLUSTERS") != "1" && os.Getenv("CBCTL_LIVE_CLUSTER") != "1" {
		t.Skip("CBCTL_IT_CLUSTERS=1 is not set; skipping the parts that create (billable) clusters")
	}
	l.cleanup()

	dir := t.TempDir()
	t.Setenv("CBCTL_LIVE_MANIFEST_PWD", secrets.MustGenerate(32))
	manifest := "apiVersion: cbctl/v1\n" +
		"kind: CouchbaseCapella\n" +
		"metadata:\n  name: " + l.name("mclu") + "\n" +
		"spec:\n" +
		"  project: " + l.name("mproj") + "\n" +
		"  provider: aws\n  region: us-east-1\n  size: dev\n" +
		"  allowedCidrs:\n    - cidr: 0.0.0.0/0\n      allowAnyIp: true\n      comment: live test\n" +
		"  buckets:\n    - name: live\n      memoryMb: 256\n" +
		"      scopes:\n        - name: s\n          collections:\n            - name: c\n" +
		"      import:\n        - scope: s\n          collection: c\n          credential: app\n          keyField: id\n" +
		"          data:\n            - { id: a, v: 1 }\n            - { id: b, v: 2 }\n" +
		"  credentials:\n    - name: app\n      password: ${CBCTL_LIVE_MANIFEST_PWD}\n" +
		"      access:\n        privileges: read+write\n        bucket: live\n"
	path := filepath.Join(dir, "live.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	apply := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(l.bin, append([]string{"apply", "-f", path, "--yes"}, args...)...)
		cmd.Dir = dir
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		if err := cmd.Run(); err != nil {
			t.Fatalf("cbctl apply %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, o.String(), e.String())
		}
		return o.String(), e.String()
	}

	if out, _ := apply("--dry-run"); !strings.Contains(out, "dry run") && !strings.Contains(out, "Plan:") {
		t.Fatalf("dry run printed no plan:\n%s", out)
	}
	if out, errOut := apply(); !strings.Contains(out, "applied") {
		t.Fatalf("first apply did not report applied:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	out, errOut := apply()
	if !strings.Contains(out, "no changes") && !strings.Contains(out, "password not changed") {
		t.Fatalf("second apply was not a no-op:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "capella.yaml")); err == nil {
		t.Fatal("apply wrote a capella.yaml")
	}
}
