//go:build integration

package integration_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/settings"
)

// liveCapella drives the real cbctl binary against the real Capella. It is skipped unless
// CAPELLA_API_KEY and CAPELLA_ORG_ID are set (directly or in .env). Cluster-cost parts need
// CBCTL_LIVE_CLUSTER=1.
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
	if os.Getenv("CAPELLA_API_KEY") == "" || os.Getenv("CAPELLA_ORG_ID") == "" {
		t.Skip("CAPELLA_API_KEY and CAPELLA_ORG_ID are not set; skipping live Capella tests")
	}
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
	out, errOut, err := l.exec(args...)
	if err != nil {
		l.t.Fatalf("cbctl capella %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

func (l *liveCapella) exec(args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(l.bin, append([]string{"capella", "--yes"}, args...)...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

// cleanup registers the janitor: everything the run created lives in a project starting with the
// run prefix, so one cascading delete removes it, and a failure names what is left behind.
func (l *liveCapella) cleanup() {
	l.t.Cleanup(func() {
		if _, errOut, err := l.exec("janitor", "--prefix", l.prefix); err != nil {
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

	t.Run("cluster", func(t *testing.T) {
		if os.Getenv("CBCTL_LIVE_CLUSTER") != "1" {
			t.Skip("CBCTL_LIVE_CLUSTER=1 is not set; skipping the parts that create (billable) clusters")
		}
		// Flow once enabled: cluster create (free tier when available, else the smallest provisioned
		// cluster) -> bucket/scope/collection -> credential -> cidr -> cluster protect on/off ->
		// cluster scale and power off/on (provisioned only) -> cluster backup schedule set/show/delete
		// -> deletes.
		t.Skip("live cluster flow not implemented yet")
	})
}
