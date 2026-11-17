package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationGuideNamesEveryChange keeps docs/capella/migrating-from-capellactl.md in step with
// the code: every removed capellactl verb, every deprecated command and every legacy
// environment variable must be named in it, and in the short guide topic.
func TestMigrationGuideNamesEveryChange(t *testing.T) {
	read := func(rel string) string {
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	guide := read("docs/capella/migrating-from-capellactl.md")
	topic := read("internal/guide/topics/migrate.md")
	for path := range removedVerbs {
		for name, doc := range map[string]string{"guide": guide, "topic": topic} {
			verb := strings.TrimPrefix(path, "capella ")
			if !strings.Contains(doc, verb) {
				t.Errorf("migration %s does not name the removed verb %q", name, path)
			}
		}
	}
	for _, l := range legacyCommands {
		path := "cbctl capella " + strings.Join(l.Path, " ")
		if !strings.Contains(guide, path) {
			t.Errorf("migration guide does not name the deprecated command %q", path)
		}
		if !strings.Contains(topic, strings.Join(l.Path, " ")) {
			t.Errorf("migrate topic does not name the deprecated command %q", path)
		}
	}
	for _, v := range []string{"CAPELLACTL_CONFIG", "CAPELLACTL_PROFILE", "CAPELLACTL_QUIET", "CAPELLACTL_HOME", "BEARER_TOKEN", "ORG_ID", "API_BASE_URL"} {
		if !strings.Contains(guide, "`"+v+"`") || !strings.Contains(topic, "`"+v+"`") {
			t.Errorf("legacy variable %s is missing from the migration guide or topic", v)
		}
	}
	for _, verb := range []string{"configure", "bucket create", "scope create", "collection create", "import", "help", "completion", "project apply", "cluster apply"} {
		if !strings.Contains(guide, "capellactl "+verb) {
			t.Errorf("migration guide has no row for 'capellactl %s'", verb)
		}
	}
}
