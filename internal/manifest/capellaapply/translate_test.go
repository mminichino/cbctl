package capellaapply_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/capellaapply"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file (run go test -update): %v", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (run go test -update and review the diff)\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// TestTranslateGolden renders the shared manifest fixtures and compares the capella.yaml text.
func TestTranslateGolden(t *testing.T) {
	tests := []struct{ fixture, golden string }{
		{"capella-groups.yaml", "translate-groups.golden.yaml"},
		{"capella-size.yaml", "translate-size.golden.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			h := newHarness(t)
			h.env["APP_DB_PASSWORD"] = "Sup3r!Secret!Pass1"
			src, err := os.ReadFile(filepath.Join("..", "testdata", "valid", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			doc := h.doc(string(src))
			tr, err := capellaapply.Translate(doc, capellaapply.TranslateOptions{BaseDir: h.dir, CIDR: "10.0.10.0/24"})
			if err != nil {
				t.Fatalf("Translate: %v", err)
			}
			if strings.Contains(tr.YAML, "Sup3r!Secret!Pass1") {
				t.Fatal("the rendered YAML holds a password")
			}
			compareGolden(t, filepath.Join("testdata", tc.golden), tr.YAML)
		})
	}
}

// TestTranslateErrorsPointAtTheManifest checks that problems the capella.yaml rules find are
// reported at the manifest's own pointers, not at projects[0].clusters[0]....
func TestTranslateErrorsPointAtTheManifest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"bucket in credential", "  provider: aws\n  region: us-east-1\n  size: small\n  credentials:\n    - name: app\n      access:\n        privileges: read\n        bucket: missing\n", "/spec/credentials/0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			doc := h.doc(manifestYAML("c1", tc.body))
			_, err := capellaapply.Translate(doc, capellaapply.TranslateOptions{BaseDir: h.dir, CIDR: "10.0.10.0/24"})
			var verr *manifest.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if !strings.Contains(issuePointers(verr), tc.want) {
				t.Fatalf("issue pointers = %q, want one under %s", issuePointers(verr), tc.want)
			}
			if strings.Contains(verr.Error(), "projects[") {
				t.Fatalf("error leaks the synthetic path: %v", verr)
			}
		})
	}
}

func TestTranslateKeepsObservedSettings(t *testing.T) {
	h := newHarness(t)
	if r := h.run(manifestYAML("c1", "  provider: aws\n  region: us-east-1\n  size: small\n  description: kept\n  deletionProtection: true\n")); r.err != nil {
		t.Fatal(r.err)
	}
	cl := clusterNamed(t, h, "c1")
	doc := h.doc(manifestYAML("c1", "  provider: aws\n  region: us-east-1\n  size: small\n"))
	tr, err := capellaapply.Translate(doc, capellaapply.TranslateOptions{BaseDir: h.dir, CIDR: cl.CloudProvider.CIDR, Observed: capellaapply.Observed{Cluster: cl}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tr.YAML, "deletionProtection") {
		t.Fatalf("an absent deletionProtection was rendered:\n%s", tr.YAML)
	}
}

func TestApplyAllocatesFreeCIDR(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"c1", "c2"} {
		if r := h.run(manifestYAML(name, "  provider: aws\n  region: us-east-1\n  size: small\n")); r.err != nil {
			t.Fatalf("create %s: %v", name, r.err)
		}
	}
	a, b := clusterNamed(t, h, "c1").CloudProvider.CIDR, clusterNamed(t, h, "c2").CloudProvider.CIDR
	if a == "" || b == "" || a == b {
		t.Fatalf("CIDRs = %q and %q, want two different networks", a, b)
	}
}

func TestApplyBucketStorageBackendIsImmutable(t *testing.T) {
	h := newHarness(t)
	body := func(backend string) string {
		return "  provider: aws\n  region: us-east-1\n  size: small\n  buckets:\n    - name: orders\n      storageBackend: " + backend + "\n"
	}
	if r := h.run(manifestYAML("c1", body("couchstore"))); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", body("magma")))
	var verr *manifest.ValidationError
	if !errors.As(r.err, &verr) || !strings.Contains(issuePointers(verr), "/spec/buckets/0/storageBackend") {
		t.Fatalf("err = %v, want a storageBackend issue", r.err)
	}
	if h.writes() != 0 {
		t.Fatalf("invalid apply wrote: %v", h.f.Writes())
	}
}

func TestApplyPartialFailureReportsCompletedSteps(t *testing.T) {
	h := newHarness(t)
	h.f.FailNext("POST", `/buckets$`, 500, 10)
	r := h.run(manifestYAML("c1", smallBody))
	var ee *capellaapply.ExecError
	if !errors.As(r.err, &ee) {
		t.Fatalf("err = %v, want ExecError", r.err)
	}
	if len(ee.Completed) == 0 {
		t.Fatalf("no completed steps listed: %v", ee)
	}
	if r.rep.Status != manifest.ReportFailed || r.rep.Error == "" {
		t.Fatalf("report = %+v", r.rep)
	}
	failed := 0
	for _, s := range r.rep.Steps {
		if s.Status == manifest.StepFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed steps = %d, want 1: %+v", failed, r.rep.Steps)
	}
}
