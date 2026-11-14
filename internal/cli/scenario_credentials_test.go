package cli_test

import (
	"context"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

// TestCredential_CAP027_AC1_DeclarativeApply applies a database credential from capella.yaml on Scenario.
func TestCredential_CAP027_AC1_DeclarativeApply(t *testing.T) {
	s := newScenario(t)
	s.Env["APP_PW"] = canaryPassword
	t.Setenv("APP_PW", canaryPassword)
	writeCapellaYAML(t, s, credentialDoc)

	r := mustScenario(t, s.Run("capella", "apply", "--yes"), "apply with credential")
	wantContains(t, r.Stdout, "app", "orders")
	clitest.AssertNoSecret(t, r, canaryPassword, s.Fake.APIKey)

	projects, err := s.Fake.Client().ListProjects(context.Background())
	if err != nil || len(projects) == 0 {
		t.Fatalf("projects: %v", err)
	}
	var pid string
	for _, p := range projects {
		if p.Name == "envproj" {
			pid = p.ID
			break
		}
	}
	clusters, err := s.Fake.Client().ListClusters(context.Background(), pid)
	if err != nil || len(clusters) == 0 {
		t.Fatalf("clusters: %v", err)
	}
	creds, err := s.Fake.Client().ListCredentials(context.Background(), pid, clusters[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range creds {
		if c.Name == "app" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("credential app not found: %+v", creds)
	}

	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "apply", "--yes"), "apply rerun")
	wantContains(t, r.Stdout, "No changes. Capella matches capella.yaml.")
	clitest.AssertNoWrites(t, s.Fake)
	clitest.AssertNoSecret(t, r, canaryPassword, s.Fake.APIKey)
}
