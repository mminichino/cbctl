package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
)

var (
	dumpUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	dumpTime = regexp.MustCompile(`Imported by cbctl \S+ on \S+`)
)

// stable removes what differs between runs by design: ids, which the fake draws at random, and
// the import time stamp.
func stable(text string) string {
	return dumpTime.ReplaceAllString(dumpUUID.ReplaceAllString(text, "<id>"), "Imported by cbctl <version> on <time>")
}

// dumpRichSeed is richSeed with one collection per scope: the fake lists the collections of a
// scope in map order, which would make two runs differ for reasons unrelated to the YAML layer.
func dumpRichSeed(s *clitest.Scenario) {
	_, cid := seedHealthy(s, capellafake.WithField("deletionProtection", true))
	s.Fake.SeedOnOffSchedule(cid, weekdaySchedule(), false)
	s.Fake.SeedAllowedCIDR(cid, "203.0.113.10/32", "Office VPN", "active")
	s.Fake.SeedAllowedCIDR(cid, "198.51.100.0/24", "", "expired")
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 1024),
		capellafake.WithField("storageBackend", "magma"), capellafake.WithField("durabilityLevel", "majority"))
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"suppliers": -1})
	s.Fake.SeedScope(cid, "orders", "billing", map[string]int{"invoices": 0})
	s.Fake.SeedBucket(cid, "sessions", capellafake.WithField("type", "ephemeral"),
		capellafake.WithField("evictionPolicy", "nruEviction"), capellafake.WithField("memoryAllocationInMb", 256),
		capellafake.WithField("storageBackend", ""))
	s.Fake.SeedCredential(cid, "orders_app", []capellaapi.AccessEntry{{
		Privileges: []string{"data_writer", "data_reader"},
		Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders",
			Scopes: []capellaapi.ResourceScope{{Name: "inventory", Collections: []string{"suppliers"}}}}}},
	}}, false)
	s.Fake.SeedAdvancedCredential(cid, "ops_admin", []string{"admin"})
}

// TestSyncDump writes the output of `capella sync --stdout` for the fixture orgs to
// $CBCTL_SYNC_DUMP_DIR, once for a new file and once for a refresh of the file just written. It
// does nothing otherwise. scripts/sync-byte-identity.sh runs it on two commits and diffs the
// files, which proves a change to the YAML layer leaves the sync output byte for byte as it was.
func TestSyncDump(t *testing.T) {
	dir := os.Getenv("CBCTL_SYNC_DUMP_DIR")
	if dir == "" {
		t.Skip("CBCTL_SYNC_DUMP_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name string
		seed func(s *clitest.Scenario)
	}{
		{"healthy", func(s *clitest.Scenario) { seedHealthy(s) }},
		{"rich", dumpRichSeed},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			s := newScenario(t)
			f.seed(s)
			fresh := mustScenario(t, s.Run("capella", "sync", "--stdout"), "sync --stdout")
			write(t, filepath.Join(dir, f.name+".new.yaml"), stable(fresh.Stdout))
			mustScenario(t, s.Run("capella", "sync"), "sync")
			refreshed := mustScenario(t, s.Run("capella", "sync", "--stdout"), "sync --stdout (refresh)")
			write(t, filepath.Join(dir, f.name+".refresh.yaml"), stable(refreshed.Stdout))
			write(t, filepath.Join(dir, f.name+".file.yaml"), stable(s.ReadFile("capella.yaml")))
		})
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
