package guide

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var mentionsCapellactl = map[string]bool{"config": true, "environment": true, "exit-codes": true, "migrate": true}

func TestTopicsAreListedAndRender(t *testing.T) {
	want := []string{"getting-started", "config", "credentials", "scaling", "backup", "idempotency", "hibernation", "ci", "safety", "environment", "exit-codes", "migrate"}
	got := Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for _, name := range got {
		text, err := Render(name)
		if err != nil || strings.TrimSpace(text) == "" {
			t.Errorf("Render(%q) = %q, %v", name, text, err)
		}
		// Only the pages that explain moving from capellactl may name it.
		if strings.Contains(text, "capellactl") && !mentionsCapellactl[name] {
			t.Errorf("topic %s still mentions capellactl", name)
		}
	}
	if _, err := Render("nope"); err == nil || !strings.Contains(err.Error(), "getting-started") {
		t.Errorf("unknown topic error = %v, want the topic list", err)
	}
	list := TopicList()
	for _, name := range want {
		if !strings.Contains(list, name) {
			t.Errorf("TopicList is missing %s", name)
		}
	}
}

func TestEveryTopicFileIsRegistered(t *testing.T) {
	files, err := filepath.Glob("topics/*.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".md")
		if !Exists(name) {
			t.Errorf("topics/%s.md is not registered", name)
		}
	}
	if len(files) != len(topics) {
		t.Errorf("%d topic files for %d topics", len(files), len(topics))
	}
}

// Every topic named by an issue or an action must have a page.
func TestReferencedTopicsExist(t *testing.T) {
	re := regexp.MustCompile(`Topic(?:\s*:|\s*=)\s*"([a-z-]+)"`)
	roots := []string{"../config/capellafile", "../plan", "../cli", "../resources"}
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, _ := os.ReadFile(path)
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				seen[m[1]] = true
			}
			return nil
		})
	}
	if len(seen) == 0 {
		t.Fatal("found no topic references; the pattern is stale")
	}
	for name := range seen {
		if !Exists(name) {
			t.Errorf("topic %q is referenced in code but has no guide page", name)
		}
	}
}

func TestFieldLookup(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"cloudProvider.region", []string{"projects.clusters.cloudProvider.region", "type:"}},
		{"clusters.cloudProvider.type", []string{"allowed:    aws, gcp, azure"}},
		{"clusters.buckets.storageBackend", []string{"type:"}},
		{"projects.clusters.serviceGroups[0].numOfNodes", []string{"range:      1–27"}},
		{"apiVersion", []string{"type:       string"}},
	} {
		got, err := Field(tc.in)
		if err != nil {
			t.Errorf("Field(%q): %v", tc.in, err)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("Field(%q) = %q, missing %q", tc.in, got, w)
			}
		}
	}
	_, err := Field("clusters.bucket.storagebackend")
	if err == nil || !strings.Contains(err.Error(), "unknown config field") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := Field("clusters.buckets.storageBackendd"); err == nil {
		t.Error("expected an error for a typo")
	}
	if _, err := Field(""); err == nil {
		t.Error("expected an error for an empty path")
	}
}

func TestFieldsTree(t *testing.T) {
	tree := Fields()
	for _, want := range []string{"apiVersion: string", "projects: list", "  clusters: list", "cloudProvider: object", "storageBackend"} {
		if !strings.Contains(tree, want) {
			t.Errorf("Fields() is missing %q:\n%s", want, tree)
		}
	}
}
