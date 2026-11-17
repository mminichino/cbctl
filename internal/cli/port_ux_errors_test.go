package cli_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

const uxBadCredential = "    databaseCredentials:\n      - name: app\n        access:\n          - privileges: [read]\n" +
	"            resources:\n              buckets:\n                - name: ordrs\n"

// TestConfigErrors_CAP062_AC2_SameInPlan: plan on an invalid file exits 3 with the same problem
// block as validate (with an action line) and makes no API call.
func TestConfigErrors_CAP062_AC2_SameInPlan(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc+uxBadCredential)
	plan := s.Run("capella", "plan")
	wantExit(t, plan, cli.ExitConfigInvalid)
	wantContains(t, plan.Stderr,
		"[error] - capella.yaml has 1 problem:\n",
		"  project envproj › cluster envclu › credential app › access[0].resources.buckets[0].name (line 28)\n",
		"    the credential gives access to bucket 'ordrs', which capella.yaml does not list for cluster 'envclu'\n",
		"    action: 'cbctl capella sync --project envproj' adds the bucket if it exists in Capella, or 'cbctl capella bucket add envclu ordrs --project envproj' adds it\n")
	if n := len(s.Fake.RequestLog()); n != 0 {
		t.Fatalf("plan on an invalid file made %d API calls", n)
	}
	validate := s.Run("capella", "validate")
	wantExit(t, validate, cli.ExitConfigInvalid)
	if validate.Stderr != plan.Stderr {
		t.Errorf("validate and plan differ:\nvalidate: %s\nplan: %s", validate.Stderr, plan.Stderr)
	}
}

// TestConfigErrors_CAP062_AC3_CausedByChange: a command whose edit adds a problem reports
// "This change is not valid, so nothing was changed" and leaves the file and Capella untouched.
func TestConfigErrors_CAP062_AC3_CausedByChange(t *testing.T) {
	s, base := uxApplied(t, "")
	r := s.Run("capella", "--yes", "cluster", "create", "bad-01", "--cidr", "10.0.0.0/24")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr,
		"[error] - This change is not valid, so nothing was changed:\n",
		"  project envproj › cluster bad-01 › cloudProvider.cidr",
		"10.0.0.0/24 overlaps the network of cluster 'envclu' (10.0.0.0/24)")
	wantNotContains(t, r.Stderr, "Already in capella.yaml")
	if got := s.ReadFile("capella.yaml"); got != base {
		t.Errorf("capella.yaml changed:\n%s", got)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestConfigErrors_CAP062_AC3_Both: when the file already had a problem and the command adds
// another, the new one is reported first, then the old one under "Already in capella.yaml"
// (as capellactl's test: a bad support plan in the file; the command adds a bad replica count,
// which only the file's validation rejects).
func TestConfigErrors_CAP062_AC3_Both(t *testing.T) {
	s, base := uxApplied(t, "    buckets:\n      - name: orders\n        memoryAllocationInMb: 256\n")
	writeCapellaYAML(t, s, strings.Replace(base, "plan: developer pro", "plan: gold", 1))
	r := s.Run("capella", "--yes", "bucket", "update", "envclu", "orders", "--replicas", "7")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "This change is not valid, so nothing was changed:", "Already in capella.yaml before this command:")
	fresh := strings.Index(r.Stderr, "bucket orders › replicas")
	kept := strings.Index(r.Stderr, "Already in capella.yaml")
	old := strings.Index(r.Stderr, "support.plan")
	if fresh < 0 || kept < fresh || old < kept {
		t.Errorf("new problem, then the old one under its heading expected:\n%s", r.Stderr)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// TestConfigErrors_CAP062_AC5_ReaskProviderAndSize: wizard answers that make the config invalid
// (single-node dev size with --availability multi) are explained and provider and size are
// asked again, keeping the name.
func TestConfigErrors_CAP062_AC5_ReaskProviderAndSize(t *testing.T) {
	s, _ := uxApplied(t, "")
	s.TTY(true)
	s.Answers("y")
	// As capellactl's test: azure + dev is invalid with --availability multi; provider and size
	// are asked again and aws + small deploys. Answers: name, provider, size, region, network,
	// support plan; then provider, size, region, network, support plan.
	r := mustScenario(t, s.RunWith("nova-dev-03\nazure\ndev\n\n\n\naws\nsmall\n\n\n\n",
		"capella", "cluster", "create", "--availability", "multi", "--no-wait"))
	wantContains(t, r.Stderr,
		"Cluster name: Cloud provider (aws, gcp, azure) [aws]: ",
		"[error] - This change is not valid, so nothing was changed:\n",
		"singleNode clusters must use availability: single\n",
		"  → Please try again: choose the cloud provider and size again.\nCloud provider (aws, gcp, azure) [aws]: Size")
	if n := strings.Count(r.Stderr, "Cluster name:"); n != 1 {
		t.Errorf("name asked %d times, want 1", n)
	}
	wantContains(t, r.Stdout, "  + cluster nova-dev-03  aws us-east-1 · multi-AZ · 3 × 4 vCPU/16 GB")
	wantContains(t, s.ReadFile("capella.yaml"), "name: nova-dev-03")
	posts := s.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters")
	if len(posts) != 1 {
		t.Fatalf("cluster POSTs = %d", len(posts))
	}
	cp, _ := posts[0].Body.(map[string]any)["cloudProvider"].(map[string]any)
	if cp["type"] != "aws" {
		t.Fatalf("created with %v", posts[0].Body)
	}
}

// TestCLI_CAP006_AC6_PrintsMessageAndHint: a cluster POST rejected with 422 exits 1 with the
// Capella message and an action line.
func TestCLI_CAP006_AC6_PrintsMessageAndHint(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedProject("envproj")
	writeCapellaYAML(t, s, scenarioDoc)
	s.Fake.FailNextOpts("POST", "/v4/organizations/*/projects/*/clusters",
		capellafake.FaultOptions{Status: 422, Code: 4001, Message: "The requested CIDR is not available."})
	r := s.Run("capella", "apply", "--yes")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "[error] - Failed: cluster envclu: ", "422 The requested CIDR is not available.",
		"\n  → Fix the problem, then run the command again; completed changes are not repeated.")
}

// TestHelp_CAP034_AC5_ValidationErrorHasTopic: a validation error names the guide topic that
// explains the field.
func TestHelp_CAP034_AC5_ValidationErrorHasTopic(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc+"    hibernation:\n      timezone: US/Pacific\n      weekend: sometimes\n")
	r := s.Run("capella", "validate")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "hibernation.weekend (line 24)", "See 'cbctl capella guide hibernation'.")
	wantExit(t, s.Run("capella", "guide", "hibernation"), cli.ExitOK)
}

const uxWarnDoc = "    allowedCidrs:\n      - cidr: 198.51.100.0/24\n        expiresAt: \"2026-01-01T00:00:00Z\"\n" +
	"    databaseCredentials:\n      - name: ops_ro\n        access:\n          - privileges: [read]\n"

// TestMessages_CAP063_AC3_Warnings: validate prints each warning with its location and a
// command to fix it; plan shows only the count.
func TestMessages_CAP063_AC3_Warnings(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+uxWarnDoc)
	v := mustScenario(t, s.Run("capella", "validate"))
	wantContains(t, v.Stderr,
		"[warning] - project envproj › cluster envclu › allowed IP range 198.51.100.0/24 › expiresAt (line 26): expiresAt is in the past, so Capella will remove this IP range\n  → Run 'cbctl capella cidr delete envclu 198.51.100.0/24' to remove it now, or set a later expiresAt.\n",
		"[warning] - project envproj › cluster envclu › credential ops_ro › access[0] (line 30): grants access to ALL buckets\n  → If that is not intended, limit the access to one bucket with 'cbctl capella credential update envclu ops_ro --bucket BUCKET'.\n")
	p := mustScenario(t, s.Run("capella", "plan"))
	if strings.TrimSpace(p.Stderr) != "[warning] - 2 warnings; run 'cbctl capella validate' to see them" {
		t.Errorf("plan stderr = %q", p.Stderr)
	}
}

// TestMessages_CAP063_AC6_ConfigWarningsOnlyWhereRelevant: file warnings appear on validate and
// plan, never on an unrelated resource command.
func TestMessages_CAP063_AC6_ConfigWarningsOnlyWhereRelevant(t *testing.T) {
	s, base := uxApplied(t, "")
	writeCapellaYAML(t, s, base+uxWarnDoc)
	wantContains(t, mustScenario(t, s.Run("capella", "validate")).Stderr, "[warning]")
	wantContains(t, mustScenario(t, s.Run("capella", "plan")).Stderr, "2 warnings")
	for _, args := range [][]string{
		{"capella", "cidr", "list", "envclu"},
		{"capella", "cluster", "get", "envclu"},
		{"capella", "project", "list"},
	} {
		r := mustScenario(t, s.Run(args...))
		wantNotContains(t, r.Stderr, "[warning]")
	}
}
