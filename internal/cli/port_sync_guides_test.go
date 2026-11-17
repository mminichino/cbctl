package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
)

// Ports of capellactl's tests/scenario/test_first_environment.py (CAP-033 AC5) and
// tests/scenario/test_readme_walkthrough.py (CAP-038 AC12, CAP-054 AC7): docs/capella/getting-started.md
// works as written against the fake Capella and never asks for a manual edit.

const guidePW = "Orders-Passw0rd-2026!"

// guideSteps returns the "Step by step" section of docs/capella/getting-started.md.
func guideSteps(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(findRepoRoot(t), "docs", "capella", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	i, j := strings.Index(text, "## Step by step"), strings.Index(text, "## capella.yaml: written by cbctl")
	if i < 0 || j < i {
		t.Fatal("getting-started.md: 'Step by step' or 'capella.yaml: written by cbctl' heading missing")
	}
	return text[i:j]
}

// stepByStepShell returns the shell blocks of the step-by-step section.
func stepByStepShell(t *testing.T) string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile("(?s)```bash\n(.*?)```").FindAllStringSubmatch(guideSteps(t), -1) {
		out = append(out, m[1])
	}
	return strings.Join(out, "\n")
}

// guideScenario is an empty folder with no API key in the environment: configure provides it.
func guideScenario(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	s.Env["CAPELLA_API_KEY"], s.Env["CAPELLA_ORG_ID"] = "", ""
	t.Setenv("CAPELLA_API_KEY", "")
	t.Setenv("CAPELLA_ORG_ID", "")
	s.Env["ORDERS_APP_PASSWORD"] = guidePW
	return s
}

// guideKey is the key the user pastes into configure: the one the fake accepts.
func guideKey(s *clitest.Scenario) string { return s.Fake.APIKey }

// wantEndState: a healthy nova-dev-01 with bucket orders and credential orders_app, recorded in
// capella.yaml without a secret, and a plan with nothing to do.
func wantEndState(t *testing.T, s *clitest.Scenario) {
	t.Helper()
	ctx := t.Context()
	c := s.Fake.Client()
	var cluster *capellaapi.Cluster
	var projectID string
	ps, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		cs, err := c.ListClusters(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := range cs {
			if cs[i].Name == "nova-dev-01" {
				cluster, projectID = &cs[i], p.ID
			}
		}
	}
	if cluster == nil {
		t.Fatal("no cluster nova-dev-01 in Capella")
	}
	if st := s.Fake.ClusterState(cluster.ID); st != "healthy" {
		t.Errorf("nova-dev-01 is %s, want healthy", st)
	}
	bs, err := c.ListBuckets(ctx, projectID, cluster.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].Name != "orders" {
		t.Errorf("buckets = %v, want orders", bs)
	}
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "nova-dev-01", "orders", "orders_app", "${ORDERS_APP_PASSWORD}")
	wantNotContains(t, text, guidePW, guideKey(s))
	r := planClean(t, s)
	wantContains(t, r.Stdout, "= bucket nova-dev-01/orders", "= credential nova-dev-01/orders_app")
}

// TestGuides_CAP033_AC5_FirstEnvironmentFromScratch: steps 1 → 3 → 5 → 6 → 9 of the guide, from an
// organization without projects: configure, project create, cluster create (every default),
// bucket add, credential create.
func TestGuides_CAP033_AC5_FirstEnvironmentFromScratch(t *testing.T) {
	guide := stepByStepShell(t)
	s := guideScenario(t)
	s.TTY(true)

	wantContains(t, guide, "cbctl configure") // step 1
	r := mustScenario(t, s.RunWith(guideKey(s)+"\n", "configure"), "configure")
	wantContains(t, r.Stdout+r.Stderr, `Saved profile "default"`,
		"This organization has no projects yet. Next: cbctl capella project create NAME")

	wantContains(t, guide, "cbctl capella project create") // step 3: also starts capella.yaml
	r = mustScenario(t, s.Run("capella", "project", "create", "nova-dev", "--description", "Nova development"), "project create")
	wantContains(t, r.Stdout, "created project nova-dev", "Next: cbctl capella cluster create NAME")
	wantNames(t, "projects", docProjects(t, s), "nova-dev")

	wantContains(t, guide, "cbctl capella cluster create") // step 5: Enter accepts every default
	s.Answers("y")
	r = mustScenario(t, s.RunWith("\n\n\n\n\n\n\n\n", "capella", "cluster", "create", "nova-dev-01", "--project", "nova-dev"),
		"cluster create")
	wantContains(t, r.Stdout+r.Stderr, "cluster nova-dev-01: healthy")

	wantContains(t, guide, "cbctl capella bucket add") // step 6
	s.Answers("y")
	mustScenario(t, s.Run("capella", "bucket", "add", "nova-dev-01", "orders", "--memory", "1024"), "bucket add")

	wantContains(t, guide, "cbctl capella credential create", "--password-env") // step 9
	s.Answers("y")
	mustScenario(t, s.Run("capella", "credential", "create", "nova-dev-01", "orders_app", "--privileges", "read+write",
		"--bucket", "orders", "--password-env", "ORDERS_APP_PASSWORD"), "credential create")

	s.TTY(false)
	wantEndState(t, s)
}

// TestGuides_CAP033_AC5_FirstEnvironmentWithInit: the guide's whole-environment path: init
// --non-interactive writes capella.yaml, then validate → plan → apply.
func TestGuides_CAP033_AC5_FirstEnvironmentWithInit(t *testing.T) {
	guide := stepByStepShell(t)
	for _, c := range []string{"cbctl capella init --non-interactive", "cbctl capella validate", "cbctl capella plan", "cbctl capella apply"} {
		wantContains(t, guide, c)
	}
	s := guideScenario(t)
	s.TTY(true)
	mustScenario(t, s.RunWith(guideKey(s)+"\n", "configure"), "configure")
	s.TTY(false)

	mustScenario(t, s.Run("capella", "init", "--non-interactive", "--project", "nova-dev", "--cluster", "nova-dev-01",
		"--provider", "aws", "--region", "us-east-1", "--preset", "small", "--bucket", "orders", "--bucket-memory", "1024",
		"--credential", "orders_app", "--access", "read-write", "--password-env", "ORDERS_APP_PASSWORD"), "init --non-interactive")
	mustScenario(t, s.Run("capella", "validate"), "validate")
	r := s.Run("capella", "plan")
	wantContains(t, r.Stdout, "+ cluster nova-dev-01", "+ bucket nova-dev-01/orders", "+ credential nova-dev-01/orders_app")
	mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	wantEndState(t, s)
}

// TestGuides_CAP038_AC12_WalkthroughSteps: the guide's steps, in order, against an organization
// with nova-dev/nova-dev-01 and bucket orders: configure discovers it, then every managed
// command keeps capella.yaml and Capella in step (apply finds nothing left to do), sync brings
// in a UI change, drift and destroy work, and apply builds the environment again.
func TestGuides_CAP038_AC12_WalkthroughSteps(t *testing.T) {
	s := guideScenario(t)
	_, cid := seedHealthy(s)
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 1024))
	ok := func(args ...string) string {
		t.Helper()
		r := mustScenario(t, s.Run(append([]string{"capella"}, append(args, "-y")...)...), strings.Join(args, " "))
		return r.Stdout + r.Stderr
	}
	noChanges := func() {
		t.Helper()
		wantContains(t, ok("apply"), "No changes")
	}

	s.TTY(true) // step 1
	s.Answers("y")
	r := mustScenario(t, s.RunWith(guideKey(s)+"\n", "configure"), "configure")
	wantContains(t, r.Stdout, "Imported 1 project, 1 cluster, 1 bucket")
	s.ReadFile("capella.yaml")

	s.TTY(false) // step 10
	wantContains(t, planClean(t, s).Stdout, "= cluster nova-dev-01")

	s.TTY(true) // step 11: Enter accepts every default
	s.Answers("y")
	r = mustScenario(t, s.RunWith("\n\n\n\n\n\n\n\n", "capella", "cluster", "create", "nova-dev-02"), "cluster create wizard")
	wantContains(t, r.Stdout+r.Stderr, "cluster nova-dev-02: healthy")
	s.TTY(false)
	ok("cluster", "create", "nova-qa-01", "--provider", "gcp", "--region", "us-east1", "--size", "medium", "--support-plan", "enterprise")

	for _, args := range [][]string{ // step 12
		{"cluster", "scale", "nova-dev-01", "--nodes", "4"},
		{"cluster", "update", "nova-dev-01", "--support-plan", "enterprise"},
		{"cluster", "update", "nova-dev-01", "--cpu", "8", "--ram", "32"},
	} {
		ok(args...)
	}
	noChanges()

	// step 13
	wantContains(t, ok("bucket", "add", "nova-dev-01", "events", "--memory", "1024", "--storage-backend", "magma", "--durability", "majority"),
		"+ bucket nova-dev-01/events")
	ok("bucket", "update", "nova-dev-01", "events", "--memory", "2048")
	wantContains(t, ok("bucket", "list", "nova-dev-01"), "events")
	wantContains(t, ok("bucket", "delete", "nova-dev-01", "events"), "deleted bucket events")
	ok("scope", "add", "nova-dev-01", "orders", "billing")
	wantContains(t, ok("collection", "add", "nova-dev-01", "orders", "billing", "invoices", "--max-ttl", "2592000"),
		"+ collection nova-dev-01/orders/billing/invoices")
	ok("collection", "update", "nova-dev-01", "orders", "billing", "invoices", "--max-ttl", "-1")
	wantContains(t, ok("collection", "list", "nova-dev-01", "orders", "billing"), "invoices")
	wantContains(t, ok("scope", "delete", "nova-dev-01", "orders", "billing"), "deleted scope billing")
	noChanges()

	// step 14
	wantContains(t, ok("credential", "create", "nova-dev-01", "orders_app", "--privileges", "read+write", "--bucket", "orders",
		"--password-env", "ORDERS_APP_PASSWORD"), "+ credential nova-dev-01/orders_app")
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "${ORDERS_APP_PASSWORD}")
	wantNotContains(t, text, guidePW)
	wantContains(t, ok("credential", "list", "nova-dev-01"), "orders_app")
	s.Env["ORDERS_APP_PASSWORD"] = "Orders-Passw0rd-2026-v2!"
	ok("credential", "update", "nova-dev-01", "orders_app", "--rotate-password")
	ok("credential", "delete", "nova-dev-01", "orders_app")
	noChanges()

	// step 15
	ok("cluster", "protect", "nova-dev-01", "--on")
	ok("cidr", "create", "nova-dev-01", "203.0.113.10/32", "--comment", "Office VPN")
	wantContains(t, ok("cidr", "list", "nova-dev-01"), "203.0.113.10/32")
	ok("cidr", "delete", "nova-dev-01", "203.0.113.10/32")
	ok("cluster", "delete", "nova-dev-02")
	r = s.Run("capella", "cluster", "delete", "nova-dev-01", "-y")
	wantExit(t, r, 4)
	wantContains(t, r.Stdout+r.Stderr, "deletion protection")
	ok("cluster", "protect", "nova-dev-01", "--off")
	noChanges()

	// step 17
	s.Fake.SetClusterField(cid, "description", "changed in the Capella UI")
	s.Fake.ResetCalls()
	wantExit(t, s.Run("capella", "sync", "--dry-run"), 2)
	wantContains(t, syncOK(t, s).Stdout, "+        description: changed in the Capella UI")
	wantContains(t, syncOK(t, s).Stdout, "already in sync")

	// step 16
	ok("cluster", "power", "nova-dev-01", "--off")
	ok("cluster", "power", "nova-dev-01", "--on")
	wantContains(t, ok("cluster", "schedule", "set", "nova-dev-01", "--timezone", "US/Pacific", "--weekdays", "08:00-18:00", "--weekend", "off"),
		"schedule nova-dev-01")
	wantContains(t, ok("cluster", "schedule", "show", "nova-dev-01"), "US/Pacific")
	ok("cluster", "schedule", "pause", "nova-dev-01", "--power-on")
	ok("cluster", "schedule", "resume", "nova-dev-01")
	ok("cluster", "scale", "nova-dev-01", "--step", "+1", "--max", "6")
	ok("cluster", "scale", "nova-dev-01", "--step", "-1", "--min", "3")

	// steps 18 and 23
	if r := s.Run("capella", "drift", "--detailed-exitcode"); r.Code != 0 && r.Code != 2 {
		t.Fatalf("drift: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	ok("apply", "--prune")
	wantContains(t, mustScenario(t, s.Run("capella", "drift"), "drift").Stdout, "No drift")
	wantContains(t, ok("destroy"), "Destroy complete")
	ok("apply") // capella.yaml was kept: apply builds it again
	ok("cluster", "scale", "nova-dev-01", "--nodes", "4")
	wantContains(t, ok("cluster", "scale", "nova-dev-01", "--nodes", "3"), "memory ok")
}

// TestGuides_CAP038_AC12_NoManualEditSteps: the step-by-step guide never asks for a hand edit and
// shows every managed command.
func TestGuides_CAP038_AC12_NoManualEditSteps(t *testing.T) {
	steps := guideSteps(t)
	for _, bad := range []string{`(?i)\bedit (the|your|this)? ?(file|capella\.yaml)\b`, "(?i)in `capella\\.yaml`, change",
		`(?i)add an entry under`, `(?i)uncomment`, `(?i)open capella\.yaml`} {
		if m := regexp.MustCompile(bad).FindString(steps); m != "" && !strings.Contains(steps, m+" by hand") {
			t.Errorf("a guide step asks for a manual edit: %q (/%s/)", m, bad)
		}
	}
	for _, c := range []string{"cbctl configure", "cbctl capella cluster create", "cbctl capella cluster scale",
		"cbctl capella cluster update", "cbctl capella bucket add", "cbctl capella bucket update",
		"cbctl capella bucket delete", "cbctl capella scope add", "cbctl capella collection add",
		"cbctl capella collection update", "cbctl capella scope delete", "cbctl capella sync", "cbctl capella init",
		"cbctl capella cluster protect", "cbctl capella cidr create", "cbctl capella cidr delete",
		"cbctl capella cluster delete", "cbctl capella credential create", "cbctl capella credential update",
		"cbctl capella credential delete"} {
		if !strings.Contains(steps, c) {
			t.Errorf("the step-by-step guide does not show %q", c)
		}
	}
}

// TestGuides_CAP054_AC7_DocsUseSync: customer docs use 'cbctl capella sync' for setup and refresh;
// 'cbctl capella import' is the JSON Lines importer and may only appear to say so.
func TestGuides_CAP054_AC7_DocsUseSync(t *testing.T) {
	root := findRepoRoot(t)
	docs, err := filepath.Glob(filepath.Join(root, "docs", "capella", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	topics, err := filepath.Glob(filepath.Join(root, "internal", "guide", "topics", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	docs = append(append(docs, topics...), filepath.Join(root, "README.md"))
	// A line that names the old command must also say what replaced it.
	importRx := regexp.MustCompile(`(capellactl|cbctl capella) import\b`)
	for _, doc := range docs {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if importRx.MatchString(line) && !strings.Contains(line, "sync") {
				t.Errorf("%s:%d tells users to run import instead of sync: %s", filepath.Base(doc), i+1, line)
			}
		}
	}
	wantContains(t, guideSteps(t), "cbctl capella sync")
}
