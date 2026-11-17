package capellafake_test

import (
	"regexp"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func canonPath(p string) string { return pathParam.ReplaceAllString(p, "{}") }

func TestFakeInventoryCoverage(t *testing.T) {
	f := capellafake.New(t)
	got := map[string]bool{}
	for _, e := range f.InventoryRoutes() {
		got[e.Method+" "+canonPath(e.Path)] = true
	}
	var missing []string
	for _, e := range capellaapi.Inventory {
		key := e.Method + " " + canonPath(e.Path)
		if !got[key] {
			missing = append(missing, e.Method+" "+e.Path)
		}
		delete(got, key)
	}
	if len(missing) > 0 {
		t.Errorf("fake missing inventory routes (%d):\n  %v", len(missing), missing)
	}
	if len(got) > 0 {
		extra := make([]string, 0, len(got))
		for k := range got {
			extra = append(extra, k)
		}
		t.Errorf("fake has routes not in inventory (%d):\n  %v", len(extra), extra)
	}
}
