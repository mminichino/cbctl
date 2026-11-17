package capellafile

import (
	"os"
	"testing"
)

// TestDocs_CAP033_AC1_StoryExamplesPresent: the example files of capellactl's documentation
// (capella, single-node-dev, gcp-prod, azure-prod) are all among the snapshots that must load.
func TestDocs_CAP033_AC1_StoryExamplesPresent(t *testing.T) {
	for _, name := range []string{"capella", "single-node-dev", "gcp-prod", "azure-prod"} {
		path := "testdata/capellactl/example-" + name + ".yaml"
		if _, err := os.Stat(path); err != nil {
			t.Errorf("capellactl example %s.yaml: %v", name, err)
			continue
		}
		if _, err := Load(path, testEnv(nil)); err != nil {
			t.Errorf("capellactl example %s.yaml does not load: %v", name, err)
		}
	}
}
