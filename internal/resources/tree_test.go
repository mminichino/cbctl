package resources

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/plan"
)

func kinds(rs []plan.Reconciler) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Kind())
	}
	return out
}

func TestBuildTreeShape(t *testing.T) {
	root := BuildTree()
	if root.Kind() != plan.RootKind {
		t.Fatalf("root kind %q", root.Kind())
	}
	if got := kinds(root.Children()); strings.Join(got, ",") != "project" {
		t.Fatalf("root children %v", got)
	}
	project := root.Children()[0]
	if got := kinds(project.Children()); strings.Join(got, ",") != "cluster" {
		t.Fatalf("project children %v", got)
	}
	cluster := project.Children()[0]
	want := "deletion-protection,schedule,backup-schedule,allowed-cidr,bucket,credential"
	if got := kinds(cluster.Children()); strings.Join(got, ",") != want {
		t.Fatalf("cluster children %v, want %s", got, want)
	}
	var bucket plan.Reconciler
	for _, c := range cluster.Children() {
		if c.Kind() == "bucket" {
			bucket = c
		}
	}
	if got := kinds(bucket.Children()); strings.Join(got, ",") != "bucket-backup-schedule,scope" {
		t.Fatalf("bucket children %v", got)
	}
	scope := bucket.Children()[1]
	if got := kinds(scope.Children()); strings.Join(got, ",") != "collection" {
		t.Fatalf("scope children %v", got)
	}
}

func TestBuildTreeBindsEveryReconciler(t *testing.T) {
	var walk func(r plan.Reconciler)
	walk = func(r plan.Reconciler) {
		if r.Kind() == "" {
			t.Fatalf("reconciler without a kind: %T", r)
		}
		for _, c := range r.Children() {
			walk(c)
		}
	}
	walk(BuildTree())
}

func TestPackageImportRule(t *testing.T) {
	allowed := map[string]bool{
		"plan": true, "config/capellafile": true, "capellaapi": true, "engine": true, "secrets": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			_, rel, ok := strings.Cut(path, "/internal/")
			if ok && !allowed[rel] && !(strings.HasSuffix(f, "_test.go") && rel == "capellafake") {
				t.Errorf("%s imports internal/%s", f, rel)
			}
		}
	}
}
