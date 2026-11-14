package port_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPortReviewMarkers fails if any committed backlog/capella doc still has
// unresolved <!-- port-review: ... --> comments.
func TestNoPortReviewMarkers(t *testing.T) {
	roots := []string{
		filepath.Join("..", "..", "..", "docs", "backlog"),
		filepath.Join("..", "..", "..", "docs", "capella"),
	}
	var hits []string
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".md") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "<!-- port-review:") {
				hits = append(hits, path)
			}
			return nil
		})
	}
	if len(hits) > 0 {
		t.Fatalf("unresolved port-review markers in:\n  %s", strings.Join(hits, "\n  "))
	}
}
