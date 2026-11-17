package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/cli"
)

// TestDrift_CAP031_AC1_CleanThenUnmanaged ports drift detection onto Scenario:
// clean after apply, then an unmanaged bucket shows under drift --detailed-exitcode.
func TestDrift_CAP031_AC1_CleanThenUnmanaged(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)

	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes")
	wantContains(t, mustScenario(t, s.Run("capella", "drift"), "drift").Stdout,
		"No drift: Capella matches capella.yaml.")
	mustScenario(t, s.Run("capella", "drift", "--detailed-exitcode"), "drift --detailed-exitcode")

	projects, err := s.Fake.Client().ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pid, cid string
	for _, p := range projects {
		if p.Name == "envproj" {
			pid = p.ID
		}
	}
	if pid == "" {
		t.Fatal("project envproj not found after apply")
	}
	clusters, err := s.Fake.Client().ListClusters(context.Background(), pid)
	if err != nil || len(clusters) == 0 {
		t.Fatalf("clusters: %v len=%d", err, len(clusters))
	}
	cid = clusters[0].ID
	if _, err := s.Fake.Client().CreateBucket(context.Background(), pid, cid, capellaapi.CreateBucketRequest{
		Name: "stray", Type: "couchbase", StorageBackend: "couchstore", MemoryAllocationInMb: 100,
		BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 1,
	}); err != nil {
		t.Fatalf("seed stray bucket: %v", err)
	}

	r := s.Run("capella", "drift", "--detailed-exitcode")
	if r.Code != int(cli.ExitChanges) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitChanges, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stdout, "Unmanaged", "stray", "Drift: 0 changed, 0 missing, 1 unmanaged.")

	js := mustScenario(t, s.Run("capella", "-o", "json", "drift"), "drift -o json").JSON(t).(map[string]any)
	unmanaged, _ := js["unmanaged"].([]any)
	if len(unmanaged) != 1 {
		t.Errorf("unmanaged = %v", js["unmanaged"])
	}
}

// TestDrift_CAP031_DestroyBlockedByProtection ports destroy refusal when deletion protection is on.
func TestDrift_CAP031_DestroyBlockedByProtection(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply --yes")

	protected := strings.Replace(s.ReadFile("capella.yaml"), "deletionProtection: false", "deletionProtection: true", 1)
	writeCapellaYAML(t, s, protected)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply protection")

	r := s.Run("capella", "destroy", "--yes")
	if r.Code != int(cli.ExitBlocked) {
		t.Fatalf("exit = %d, want %d\n%s\n%s", r.Code, cli.ExitBlocked, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "deletion protection is on for envclu")

	unprotected := strings.Replace(s.ReadFile("capella.yaml"), "deletionProtection: true", "deletionProtection: false", 1)
	writeCapellaYAML(t, s, unprotected)
	mustScenario(t, s.Run("capella", "apply", "--yes"), "apply unprotect")

	r = s.Run("capella", "destroy")
	if r.Code != int(cli.ExitBlocked) {
		t.Fatalf("destroy without --yes exit = %d, want %d\n%s\n%s", r.Code, cli.ExitBlocked, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stderr+r.Stdout, "no terminal")

	r = mustScenario(t, s.Run("capella", "destroy", "--yes"), "destroy --yes")
	wantContains(t, r.Stdout, "- cluster envclu", "- project envproj")
	wantContains(t, r.Stderr, "Destroy complete")
	got := s.ReadFile("capella.yaml")
	if strings.Contains(got, "id:") || !strings.Contains(got, "envclu") {
		t.Errorf("destroy must drop ids and keep the description:\n%s", got)
	}
	wantContains(t, mustScenario(t, s.Run("capella", "destroy", "--yes"), "destroy again").Stdout, "nothing to destroy")
}
