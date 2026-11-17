package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// ttyRun runs a command with a terminal attached and stdin answers (the wizard reads stdin).
func ttyRun(t *testing.T, s *clitest.Scenario, stdin string, args ...string) clitest.Result {
	t.Helper()
	s.TTY(true)
	defer s.TTY(false)
	return s.RunContext(context.Background(), stdin, args...)
}

// fileUnchanged fails the test when capella.yaml differs from before or anything was written.
func fileUnchanged(t *testing.T, s *clitest.Scenario, before string) {
	t.Helper()
	if got := readFile(t, s); got != before {
		t.Errorf("capella.yaml changed:\n%s", got)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

// clusterEntryText is the YAML of cluster name in capella.yaml: from the start of its list item
// (where a pinned id sits) to the next cluster (or the end of the file).
func clusterEntryText(t *testing.T, s *clitest.Scenario, name string) string {
	t.Helper()
	f := readFile(t, s)
	i := strings.Index(f, "name: "+name+"\n")
	if i < 0 {
		t.Fatalf("no cluster %s in capella.yaml:\n%s", name, f)
	}
	if j := strings.LastIndex(f[:i], "\n      - "); j >= 0 {
		i = j + 1
	}
	rest := f[i:]
	if j := strings.Index(rest[1:], "\n      - "); j >= 0 {
		rest = rest[:j+1]
	}
	return rest
}

// TestClusterCreate_CAP038_AC1_InteractiveDeploys: the wizard asks, plans, confirms, deploys and
// records the cluster. As in capellactl, NAME without sizing flags asks the cluster type,
// provider, size, region, network and support plan.
func TestClusterCreate_CAP038_AC1_InteractiveDeploys(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	s.Answers("y")
	r := ttyRun(t, s, "\n\n\n\n\n\n", "capella", "cluster", "create", "dev-02")
	mustScenario(t, r, "wizard create")
	wantContains(t, r.Stderr+r.Stdout, "Cluster type (free, provisioned) [provisioned]", "Cloud provider (aws, gcp, azure) [aws]", "Region [us-east-1]", "Size (",
		"Network CIDR [10.0.16.0/24]", "+ cluster dev-02  aws us-east-1 · multi-AZ · 3 × 4 vCPU/16 GB", "cluster dev-02: healthy",
		"connectionString (dev-02)")
	if n := len(s.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters")); n != 1 {
		t.Errorf("cluster POSTs = %d, want 1", n)
	}
	wantContains(t, clusterEntryText(t, s, "dev-02"), "cloudProvider: {type: aws, region: us-east-1, cidr: 10.0.16.0/24}", "numOfNodes: 3")
}

// TestClusterCreate_CAP038_AC2_FlagsAndDefaults: provider defaults (gcp region, pd-ssd disk), the
// medium size, the support plan, and --size dev as a singleNode cluster.
func TestClusterCreate_CAP038_AC2_FlagsAndDefaults(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "qa-01", "--provider", "gcp", "--size", "medium",
		"--support-plan", "enterprise"), "create gcp")
	wantContains(t, clusterEntryText(t, s, "qa-01"), "region: us-east1", "compute: {cpu: 8, ram: 32}",
		"disk: {type: pd-ssd, storage: 50}", "plan: enterprise")
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "ci-01", "--size", "dev"), "create dev")
	wantContains(t, clusterEntryText(t, s, "ci-01"), "configurationType: singleNode", "availability: {type: single}")
}

// TestClusterCreate_CAP038_AC4_InvalidInputWritesNothing: each bad value exits 3 with its reason,
// and neither the file nor Capella changes.
func TestClusterCreate_CAP038_AC4_InvalidInputWritesNothing(t *testing.T) {
	cases := []struct {
		args []string
		msg  string
	}{
		{[]string{"--provider", "gcp", "--disk-type", "gp3"}, "disk type gp3 is not valid for gcp"},
		{[]string{"--nodes", "2"}, "must be between 3 and 27"},
		{[]string{"--size", "dev", "--availability", "multi"}, "singleNode clusters must use availability: single"},
		{[]string{"--cidr", "10.10.0.5/24"}, "is not a network address"},
		{[]string{"--provider", "azure", "--region", "us-east-1"},
			"us-east-1 is not an azure region name; it looks like an aws region (azure regions look like eastus"},
		{[]string{"--provider", "gcp", "--region", "eastus"}, "eastus is not a gcp region name; it looks like an azure region"},
		{[]string{"--cidr", "10.0.0.0/24"}, "overlaps the network of cluster 'envclu'"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			s, _ := syncedGroups(t, nil, nil)
			before := readFile(t, s)
			r := s.Run(append([]string{"capella", "--yes", "cluster", "create", "bad-01"}, tc.args...)...)
			wantExit(t, r, cli.ExitConfigInvalid)
			wantContains(t, r.Stderr, tc.msg)
			fileUnchanged(t, s, before)
		})
	}
}

// TestClusterCreate_CAP038_AC4_InteractiveReasks: a CIDR with host bits set is re-asked, then the
// default range is accepted and the cluster deployed.
func TestClusterCreate_CAP038_AC4_InteractiveReasks(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	s.Answers("y")
	r := ttyRun(t, s, "\n\n\n\n10.0.0.5/23\n\n\n", "capella", "cluster", "create", "dev-03")
	mustScenario(t, r, "wizard re-ask")
	wantContains(t, r.Stderr+r.Stdout, "is not a network address")
	if n := strings.Count(r.Stderr+r.Stdout, "Network CIDR"); n != 2 {
		t.Errorf("Network CIDR asked %d times, want 2", n)
	}
	clusterEntryText(t, s, "dev-03")
}

// TestClusterCreate_CAP038_AC4_ExistingProblemsStopBeforeQuestions: a capella.yaml that is
// already invalid stops create before any question is asked.
func TestClusterCreate_CAP038_AC4_ExistingProblemsStopBeforeQuestions(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	text := readFile(t, s)
	at := strings.Index(text, "serviceGroups:")
	if at < 0 {
		t.Fatalf("no serviceGroups in capella.yaml:\n%s", text)
	}
	ind := text[strings.LastIndex(text[:at], "\n")+1 : at]
	bad := text[:at] + "databaseCredentials:\n" + ind + "  - name: app\n" + ind + "    access:\n" + ind +
		"      - privileges: [read]\n" + ind + "        resources:\n" + ind + "          buckets:\n" + ind +
		"            - name: ordrs\n" + ind + text[at:]
	s.WriteFile("capella.yaml", bad)
	r := ttyRun(t, s, strings.Repeat("\n", 20), "capella", "cluster", "create", "bad-01")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "already had 1 problem", "credential app")
	if strings.Contains(r.Stderr+r.Stdout, "Cloud provider") {
		t.Errorf("asked questions over an invalid file:\n%s", r.Stderr)
	}
	fileUnchanged(t, s, bad)
}

// TestClusterCreate_CAP038_AC4_InteractiveFlagConflictNotReasked: conflicting flags exit 3 instead
// of re-asking, even on a terminal.
func TestClusterCreate_CAP038_AC4_InteractiveFlagConflictNotReasked(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	before := readFile(t, s)
	r := ttyRun(t, s, "\n", "capella", "cluster", "create", "bad-01", "--provider", "azure", "--region", "eastus",
		"--size", "dev", "--availability", "multi", "--support-plan", "developer pro")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "singleNode clusters must use availability: single")
	if out := r.Stderr + r.Stdout; strings.Contains(out, "Please try again") || strings.Contains(out, "Cloud provider") {
		t.Errorf("flag conflict re-asked:\n%s", out)
	}
	fileUnchanged(t, s, before)
}

// TestClusterCreate_CAP038_AC5_ExistingName: a name in the file points at cluster update; a name
// only in Capella points at sync. Both exit 4 with no writes.
func TestClusterCreate_CAP038_AC5_ExistingName(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	r := s.Run("capella", "--yes", "cluster", "create", "envclu")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "cluster envclu already exists", "cluster update")
	pid := projectID(t, s)
	s.Fake.SeedCluster(pid, "made-in-ui", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.9.0.0/24"}))
	s.Fake.ResetCalls()
	r = s.Run("capella", "--yes", "cluster", "create", "made-in-ui")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "cluster made-in-ui already exists", "sync")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestClusterUpdate_CAP038_AC7_ChangesFileAndApplies: managed update shows the diff, records the
// new values and sends one PUT.
func TestClusterUpdate_CAP038_AC7_ChangesFileAndApplies(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "update", "envclu", "--support-plan", "enterprise",
		"--description", "now enterprise", "--cpu", "8", "--ram", "32"), "update")
	wantContains(t, r.Stdout, `support.plan:`, `"enterprise"`)
	wantContains(t, clusterEntryText(t, s, "envclu"), "plan: enterprise", "description: now enterprise", "cpu: 8", "ram: 32")
	if n := len(s.Fake.Calls("PUT", "/v4/organizations/*/projects/*/clusters/*")); n != 1 {
		t.Errorf("cluster PUTs = %d, want 1", n)
	}
}

// TestClusterUpdate_CAP038_AC7_NoImmutableOptions: cluster update offers no flag for a value
// Capella cannot change.
func TestClusterUpdate_CAP038_AC7_NoImmutableOptions(t *testing.T) {
	s := newScenario(t)
	r := mustScenario(t, s.Run("capella", "cluster", "update", "--help"), "help")
	for _, opt := range []string{"--provider", "--region", "--cidr", "--availability"} {
		if strings.Contains(r.Stdout, opt) {
			t.Errorf("cluster update --help offers %s", opt)
		}
	}
}

// TestClusterUpdate_CAP038_AC7_InteractiveAsks: update with no flags on a terminal asks each value,
// Enter keeps it.
func TestClusterUpdate_CAP038_AC7_InteractiveAsks(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	s.Answers("y")
	r := mustScenario(t, ttyRun(t, s, "\nenterprise\n\n\n\n", "capella", "cluster", "update", "envclu"), "interactive update")
	wantContains(t, r.Stderr+r.Stdout, "Press Enter to keep the current value")
	wantContains(t, clusterEntryText(t, s, "envclu"), "plan: enterprise")
}

// TestClusterCLI_CAP038_AC8_DeclineWritesNothing: declining a managed scale leaves the file and
// Capella alone.
func TestClusterCLI_CAP038_AC8_DeclineWritesNothing(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	before := readFile(t, s)
	s.TTY(true)
	s.Answers("n")
	r := mustScenario(t, s.Run("capella", "cluster", "scale", "envclu", "--nodes", "4"), "declined scale")
	wantContains(t, r.Stderr+r.Stdout, "Cancelled")
	fileUnchanged(t, s, before)
}

// TestClusterCLI_CAP038_BlockedChangeWritesNothing: a scale below the limit is refused before the
// file or Capella changes.
func TestClusterCLI_CAP038_BlockedChangeWritesNothing(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	before := readFile(t, s)
	r := s.Run("capella", "--yes", "cluster", "scale", "envclu", "--nodes", "2")
	if c := cli.ExitCode(r.Code); c != cli.ExitConfigInvalid && c != cli.ExitBlocked {
		t.Fatalf("exit %d, want 3 or 4\n%s", r.Code, r.Stderr)
	}
	fileUnchanged(t, s, before)
}

// TestClusterCLI_CAP038_AC11_CreateThenInSync: after a managed create, apply and sync both find
// nothing to do.
func TestClusterCLI_CAP038_AC11_CreateThenInSync(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "dev-02"), "create")
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "--yes", "apply"), "apply")
	wantContains(t, r.Stdout, "No changes")
	r = mustScenario(t, s.Run("capella", "sync", "--dry-run"), "sync --dry-run")
	wantContains(t, r.Stdout+r.Stderr, "already in sync")
	clitest.AssertNoWrites(t, s.Fake)
}

// TestClusterCLI_CAP038_AC8_RerunReplacesUndeployedEntry: create of a name that is in the file but
// was never deployed replaces the entry, keeps its children and records the id.
func TestClusterCLI_CAP038_AC8_RerunReplacesUndeployedEntry(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "qa-01", "--provider", "gcp", "--no-apply"), "no-apply")
	mustScenario(t, s.Run("capella", "--yes", "bucket", "add", "qa-01", "events", "--no-apply"), "bucket add")
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "qa-01", "--provider", "azure", "--region", "eastus"), "rerun")
	wantContains(t, r.Stderr, "qa-01 is in capella.yaml but was never deployed to Capella; replacing it")
	e := clusterEntryText(t, s, "qa-01")
	wantContains(t, e, "type: azure", "region: eastus", "name: events", "id: ")
	if n := strings.Count(readFile(t, s), "name: qa-01\n"); n != 1 {
		t.Errorf("qa-01 entries = %d, want 1", n)
	}
}

// TestClusterCLI_CAP038_AC8_DeployedEntryStillBlocked: an entry with an id (deployed once, then
// deleted outside cbctl) is not replaced.
func TestClusterCLI_CAP038_AC8_DeployedEntryStillBlocked(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "qa-01"), "create")
	mustScenario(t, s.Run("capella", "--yes", "cluster", "delete", "qa-01", "--direct"), "delete outside the file")
	r := s.Run("capella", "--yes", "cluster", "create", "qa-01")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "cluster qa-01 already exists")
}

// TestClusterUpdate_CAP038_AC7_AzurePremiumDiskNotResized: a P6 disk's fixed size is never sent
// on update, and --disk-size on it exits 3.
func TestClusterUpdate_CAP038_AC7_AzurePremiumDiskNotResized(t *testing.T) {
	s, _ := syncedGroups(t, nil, nil)
	mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "demo-01", "--provider", "azure", "--region", "eastus",
		"--size", "dev"), "create azure")
	mustScenario(t, s.Run("capella", "--yes", "sync"), "sync")
	wantContains(t, clusterEntryText(t, s, "demo-01"), "type: P6", "storage: 64")
	s.Fake.ResetCalls()
	mustScenario(t, s.Run("capella", "--yes", "cluster", "update", "demo-01", "--description", "demo"), "update")
	put := onePut(t, s)
	for _, g := range putGroups(t, put) {
		disk := g["node"].(map[string]any)["disk"].(map[string]any)
		if _, ok := disk["storage"]; ok {
			t.Errorf("PUT sent storage for a P6 disk: %v", disk)
		}
	}
	r := mustScenario(t, s.Run("capella", "--yes", "apply"), "apply")
	wantContains(t, r.Stdout, "No changes")
	r = s.Run("capella", "--yes", "cluster", "update", "demo-01", "--disk-size", "128")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "has a size and IOPS set by the type")
}

// TestClusterCLI_CAP038_AC8_NewKeysInImportOrder: keys added by commands land where sync writes
// them, so a later sync finds the file in sync.
func TestClusterCLI_CAP038_AC8_NewKeysInImportOrder(t *testing.T) {
	s, cid := syncedGroups(t, nil, func(f *capellafake.Fake, cid string) {
		f.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 1024))
	})
	_ = cid
	s.Env["APP_PW"] = "S3cure-Passw0rd!"
	mustScenario(t, s.Run("capella", "--yes", "credential", "create", "envclu", "app", "--privileges", "read",
		"--bucket", "orders", "--password-env", "APP_PW"), "credential create")
	mustScenario(t, s.Run("capella", "--yes", "cluster", "update", "envclu", "--description", "demo cluster"), "update")
	mustScenario(t, s.Run("capella", "--yes", "cluster", "protect", "envclu", "--on"), "protect")
	e := clusterEntryText(t, s, "envclu")
	order := []string{"name: envclu", "description:", "deletionProtection:", "buckets:", "databaseCredentials:"}
	last := -1
	for _, k := range order {
		i := strings.Index(e, k)
		if i < 0 || i < last {
			t.Fatalf("key %q out of import order in:\n%s", k, e)
		}
		last = i
	}
	r := mustScenario(t, s.Run("capella", "sync", "--dry-run"), "sync --dry-run")
	wantContains(t, r.Stdout+r.Stderr, "already in sync")
}

// uiMade adds cluster ui-made (10.0.16.0/24, the next free range) to envproj in Capella after the
// file was synced.
func uiMade(t *testing.T) *clitest.Scenario {
	t.Helper()
	s, _ := syncedGroups(t, nil, nil)
	s.Fake.SeedCluster(projectID(t, s), "ui-made", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.0.16.0/24"}))
	s.Fake.ResetCalls()
	return s
}

// projectID is the Capella id of envproj.
func projectID(t *testing.T, s *clitest.Scenario) string {
	t.Helper()
	r := mustScenario(t, s.Run("capella", "project", "get", "envproj", "-o", "json"), "project get")
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &p); err != nil || p.ID == "" {
		t.Fatalf("project get: %v\n%s", err, r.Stdout)
	}
	return p.ID
}

// TestClusterCreate_CAP038_AC15_OverlapWithCapellaOnlyClusterRefused: a range overlapping a
// cluster that is in Capella but not the file exits 3 and suggests a free range.
func TestClusterCreate_CAP038_AC15_OverlapWithCapellaOnlyClusterRefused(t *testing.T) {
	for _, extra := range [][]string{nil, {"--no-apply"}, {"--free-tier"}} {
		t.Run(strings.Join(append([]string{"apply"}, extra...), " "), func(t *testing.T) {
			s := uiMade(t)
			before := readFile(t, s)
			r := s.Run(append([]string{"capella", "--yes", "cluster", "create", "test-01", "--cidr", "10.0.16.0/24"}, extra...)...)
			wantExit(t, r, cli.ExitConfigInvalid)
			wantContains(t, r.Stderr, "10.0.16.0/24 overlaps the network of cluster ui-made (10.0.16.0/24)",
				"in Capella but not in capella.yaml", "--cidr 10.0.17.0/24")
			fileUnchanged(t, s, before)
		})
	}
}

// TestClusterCreate_CAP038_AC15_OverlapTypedIsReasked: an overlapping range typed in the wizard is
// re-asked (only the CIDR), and the suggested default is used.
func TestClusterCreate_CAP038_AC15_OverlapTypedIsReasked(t *testing.T) {
	s := uiMade(t)
	s.Answers("y")
	r := mustScenario(t, ttyRun(t, s, "\n\n\n\n10.0.16.0/24\n\n\n", "capella", "cluster", "create", "test-01"), "wizard")
	out := r.Stderr + r.Stdout
	wantContains(t, out, "10.0.16.0/24 overlaps the network of cluster ui-made")
	if strings.Count(out, "Network CIDR") != 2 || strings.Count(out, "Cloud provider") != 1 {
		t.Errorf("want CIDR asked twice and provider once:\n%s", out)
	}
	wantContains(t, clusterEntryText(t, s, "test-01"), "cidr: 10.0.17.0/24")
}

// TestClusterCreate_CAP038_AC15_OtherProjectWarns: overlap with a cluster in another project only
// warns.
func TestClusterCreate_CAP038_AC15_OtherProjectWarns(t *testing.T) {
	s := uiMade(t)
	other := s.Fake.SeedProject("non-prod")
	s.Fake.SeedCluster(other, "np-01", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.20.0.0/24"}))
	r := mustScenario(t, s.Run("capella", "--yes", "cluster", "create", "test-01", "--cidr", "10.20.0.0/24"), "create")
	wantContains(t, r.Stderr, "10.20.0.0/24 overlaps the network of cluster np-01 (10.20.0.0/24) in project non-prod",
		"none needed unless you will connect the two networks")
	wantContains(t, clusterEntryText(t, s, "test-01"), "cidr: 10.20.0.0/24")
}
