package capellafile

import (
	"path/filepath"
	"testing"
)

// TestCapellactlExamplesValidate loads every capellactl example and doc snapshot unchanged
// (apiVersion capellactl/v1): a capellactl user's files must keep working.
func TestCapellactlExamplesValidate(t *testing.T) {
	files, err := filepath.Glob("testdata/capellactl/*.yaml")
	// fragments/ holds doc fences that elide parts with "…"; they were never loadable, also not
	// by capellactl, and are kept only as a record of what the docs showed.
	if err != nil || len(files) < 5 {
		t.Fatalf("capellactl snapshots: %d files, %v", len(files), err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			cfg, err := Load(f, testEnv(nil))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.Projects()) == 0 {
				t.Fatal("no projects")
			}
		})
	}
}
