package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/config/capellafile"
)

const envDoc = `apiVersion: cbctl/v1
project:
  name: envproj
clusters:
  - name: envclu
    cloudProvider:
      type: aws
      region: us-east-1
      cidr: 10.0.0.0/24
    availability:
      type: multi
    support:
      plan: developer pro
      timezone: PT
    serviceGroups:
      - node:
          compute: {cpu: 4, ram: 16}
          disk: {type: gp3, storage: 50, iops: 3000}
        numOfNodes: 3
        services: [data, index, query]
    deletionProtection: false
`

// envDir writes capella.yaml into a fresh directory, makes it the working directory and returns
// the file path.
func envDir(t *testing.T, doc string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "capella.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func noWrites(t *testing.T, f *capellafake.Fake) {
	t.Helper()
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if n := f.Count(m, "."); n != 0 {
			t.Errorf("fake saw %d %s requests, want none", n, m)
		}
	}
}

func TestValidateOfflineNeedsNoKey(t *testing.T) {
	envDir(t, envDoc)
	t.Setenv("CBCTL_HOME", t.TempDir())
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID", "CBCTL_CONFIG"} {
		t.Setenv(k, "")
	}
	res := mustRun(t, "validate")
	wantContains(t, res.Stdout, "capella.yaml is valid: 1 project, 1 cluster")

	js := jsonObject(t, mustRun(t, "-o", "json", "validate"))
	if js["valid"] != true {
		t.Errorf("valid = %v", js["valid"])
	}
	if counts, _ := js["counts"].(map[string]any); counts["clusters"] != float64(1) {
		t.Errorf("counts = %v", js["counts"])
	}
}

func TestValidateInvalidExits3(t *testing.T) {
	envDir(t, strings.Replace(envDoc, "region: us-east-1", "region: nowhere-1", 1)+"bogus: 1\n")
	t.Setenv("CBCTL_CONFIG", "")
	res := runResource(t, "validate")
	var ce *capellafile.ConfigError
	if !asConfigError(res.Err, &ce) || len(ce.Issues) == 0 {
		t.Fatalf("err = %v, want a ConfigError with issues", res.Err)
	}

	res = runResource(t, "-o", "json", "validate")
	js := jsonObject(t, res)
	if js["valid"] != false || len(js["issues"].([]any)) == 0 {
		t.Errorf("json = %v", js)
	}
	if !asConfigError(res.Err, &ce) {
		t.Errorf("json mode must still fail with the ConfigError, got %v", res.Err)
	}
}

func TestConfigDiscovery(t *testing.T) {
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	other := filepath.Join(t.TempDir(), "prod.yaml")
	if err := os.WriteFile(other, []byte(strings.ReplaceAll(envDoc, "envclu", "prodclu")+""), 0o644); err != nil {
		t.Fatal(err)
	}

	wantContains(t, mustRun(t, "validate").Stdout, "capella.yaml is valid")
	wantContains(t, mustRun(t, "-c", other, "validate").Stdout, "prod.yaml is valid")
	wantContains(t, mustRun(t, "--config", other, "validate").Stdout, "prod.yaml is valid")

	t.Setenv("CBCTL_CONFIG", other)
	wantContains(t, mustRun(t, "validate").Stdout, "prod.yaml is valid")
	t.Setenv("CBCTL_CONFIG", "")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ce := wantExitErr(t, runResource(t, "validate"), ExitConfigInvalid, "not found")
	if !strings.Contains(ce.Action, "cbctl capella sync") {
		t.Errorf("action = %q", ce.Action)
	}

	yml := filepath.Join(filepath.Dir(path), "capella.yml")
	if err := os.WriteFile(yml, []byte(envDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	wantContains(t, mustRun(t, "validate").Stdout, "capella.yml is valid")
	if err := os.WriteFile(path, []byte(envDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runResource(t, "validate"); res.Err == nil {
		t.Error("both capella.yaml and capella.yml must fail")
	}
}

func TestPlanApplyIdempotent(t *testing.T) {
	f := resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")

	res := mustRun(t, "plan")
	wantContains(t, res.Stdout, "Project envproj - organization "+f.OrgID, "+ project envproj", "+ cluster envclu", "Plan: ")
	noWrites(t, f)

	res = runResource(t, "plan", "--detailed-exitcode")
	wantExit(t, res, ExitChanges, "")
	noWrites(t, f)

	js := jsonObject(t, mustRun(t, "-o", "json", "plan"))
	actions := js["plan"].(map[string]any)["actions"].([]any)
	if len(actions) == 0 {
		t.Fatalf("plan json has no actions: %v", js)
	}

	res = runResource(t, "apply")
	wantExit(t, res, ExitBlocked, "no terminal")
	noWrites(t, f)

	res = mustRun(t, "apply", "--yes")
	wantContains(t, res.Stdout, "KIND", "envclu", "done")
	wantContains(t, res.Stderr, "Apply complete in")
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(text), "\n    id: ")+strings.Count(string(text), "\n  - id: ")+strings.Count(string(text), "\n  id: ") < 2 {
		t.Errorf("created project and cluster ids were not pinned in the file:\n%s", text)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("no .bak after pinning ids: %v", err)
	}
	if _, err := capellafile.Load(path, os.Getenv); err != nil {
		t.Errorf("the pinned file no longer loads: %v", err)
	}

	f.ResetLog()
	res = mustRun(t, "apply", "--yes")
	wantContains(t, res.Stdout, "No changes. Capella matches capella.yaml.")
	noWrites(t, f)

	res = mustRun(t, "plan", "--detailed-exitcode")
	wantContains(t, res.Stdout, "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked.")

	js = jsonObject(t, mustRun(t, "-o", "json", "apply"))
	if r, _ := js["result"].(map[string]any); r["status"] != "ok" {
		t.Errorf("result = %v", js["result"])
	}
}

func TestDriftAndDestroy(t *testing.T) {
	f := resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	mustRun(t, "apply", "--yes")

	wantContains(t, mustRun(t, "drift").Stdout, "No drift: Capella matches capella.yaml.")
	mustRun(t, "drift", "--detailed-exitcode")

	projects, _ := f.Client().ListProjects(t.Context())
	var pid, cid string
	for _, p := range projects {
		if p.Name == "envproj" {
			pid = p.ID
		}
	}
	clusters, _ := f.Client().ListClusters(t.Context(), pid)
	cid = clusters[0].ID
	fx := &resFixture{Fake: f, Client: f.Client(), ProjectID: pid, ClusterID: cid}
	fx.addBucket(t, "stray")

	res := runResource(t, "drift", "--detailed-exitcode")
	wantExit(t, res, ExitChanges, "")
	wantContains(t, res.Stdout, "Unmanaged", "stray", "Drift: 0 changed, 0 missing, 1 unmanaged.")
	js := jsonObject(t, mustRun(t, "-o", "json", "drift"))
	if len(js["unmanaged"].([]any)) != 1 {
		t.Errorf("unmanaged = %v", js["unmanaged"])
	}

	if err := os.WriteFile(path, []byte(strings.Replace(string(mustRead(t, path)), "deletionProtection: false", "deletionProtection: true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "apply", "--yes")
	wantExit(t, runResource(t, "destroy", "--yes"), ExitBlocked, "deletion protection is on for envclu")
	if err := os.WriteFile(path, []byte(strings.Replace(string(mustRead(t, path)), "deletionProtection: true", "deletionProtection: false", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "apply", "--yes")

	wantExit(t, runResource(t, "destroy"), ExitBlocked, "no terminal")
	res = mustRun(t, "destroy", "--yes")
	wantContains(t, res.Stdout, "- cluster envclu", "- project envproj")
	wantContains(t, res.Stderr, "Destroy complete in")
	if got := string(mustRead(t, path)); strings.Contains(got, "id:") || !strings.Contains(got, "envclu") {
		t.Errorf("destroy must drop the ids and keep the description:\n%s", got)
	}
	wantContains(t, mustRun(t, "destroy", "--yes").Stdout, "nothing to destroy")
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGuide(t *testing.T) {
	list := mustRun(t, "guide", "topics").Stdout
	for _, name := range []string{"getting-started", "config", "credentials", "scaling", "backup", "idempotency", "hibernation", "ci", "safety", "environment", "exit-codes"} {
		wantContains(t, list, name)
		if text := mustRun(t, "guide", name).Stdout; strings.TrimSpace(text) == "" {
			t.Errorf("guide %s is empty", name)
		}
	}
	wantContains(t, mustRun(t, "guide").Stdout, "Getting started:", "Topics")
	wantContains(t, mustRun(t, "guide", "config", "cloudProvider.region").Stdout, "region")

	ce := wantExitErr(t, runResource(t, "guide", "nope"), ExitConfigInvalid, `unknown guide topic "nope"`)
	wantContains(t, ce.Action, "getting-started", "exit-codes")
	wantExit(t, runResource(t, "guide", "config", "cloudProvidr.region"), ExitConfigInvalid, "")
}

func TestSyncRoundTrip(t *testing.T) {
	f := resEnv(t)
	fx := &resFixture{Fake: f, Client: f.Client(), ProjectName: "proj", ClusterName: "clu"}
	fx.ProjectID = f.SeedProject("proj")
	fx.ClusterID = f.SeedCluster(fx.ProjectID, "clu", "healthy")
	fx.addBucket(t, "orders")
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_CONFIG", "")

	res := mustRun(t, "sync", "--stdout")
	wantContains(t, res.Stdout, "apiVersion: cbctl/v1", "name: proj", "name: clu", "name: orders")
	if _, err := os.Stat("capella.yaml"); err == nil {
		t.Fatal("--stdout wrote a file")
	}
	if _, err := capellafile.LoadText(res.Stdout, "stdout", dir, os.Getenv); err != nil {
		t.Fatalf("sync output does not validate: %v\n%s", err, res.Stdout)
	}

	f.ResetLog()
	res = runResource(t, "sync", "--dry-run")
	wantExit(t, res, ExitChanges, "does not exist yet")
	noWrites(t, f)

	res = mustRun(t, "sync")
	wantContains(t, res.Stdout, "Imported 1 project, 1 cluster", "capella.yaml")
	noWrites(t, f)

	wantContains(t, mustRun(t, "validate").Stdout, "capella.yaml is valid")
	res = mustRun(t, "plan", "--detailed-exitcode")
	wantContains(t, res.Stdout, "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked.")
	wantContains(t, mustRun(t, "drift", "--detailed-exitcode").Stdout, "No drift")

	res = mustRun(t, "sync")
	wantContains(t, res.Stdout, "already in sync with Capella")
	mustRun(t, "sync", "--dry-run")

	fx.addBucket(t, "second")
	res = runResource(t, "sync", "--dry-run")
	wantExit(t, res, ExitChanges, "out of sync")
	wantContains(t, res.Stdout, "+++ capella.yaml (from Capella)", "second")
	res = mustRun(t, "sync")
	wantContains(t, res.Stdout, "capella.yaml updated from Capella")
	if _, err := os.Stat("capella.yaml.bak"); err != nil {
		t.Errorf("no .bak after refresh: %v", err)
	}
	wantContains(t, mustRun(t, "plan").Stdout, "Plan: 0 to add")
}

func TestSyncOrgMismatch(t *testing.T) {
	resEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_CONFIG", "")
	doc := strings.Replace(envDoc, "project:", "organizationId: some-other-org\nproject:", 1)
	if err := os.WriteFile("capella.yaml", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ce := wantExitErr(t, runResource(t, "sync"), ExitConfigInvalid, "is for organization some-other-org")
	wantContains(t, ce.Action, "--force", "--file")
}

func TestInitNonInteractive(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_HOME", t.TempDir())
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID", "CBCTL_CONFIG"} {
		t.Setenv(k, "")
	}
	ce := wantExitErr(t, runResource(t, "init", "--non-interactive"), ExitConfigInvalid, "missing required options")
	wantContains(t, ce.Message, "--org-id", "--project", "--cluster")

	args := []string{"init", "--non-interactive", "--org-id", "org-1", "--project", "nova", "--cluster", "nova-01",
		"--cidr", "203.0.113.10/32", "--bucket", "orders", "--scope", "inv:items,stock", "--credential", "app", "--hibernation", "office-hours"}
	res := mustRun(t, append(args, "--dry-run")...)
	wantContains(t, res.Stdout, "organizationId: org-1", "name: nova-01", "${CBCTL_PWD_NOVA_01_APP}", "weekdays: 08:00-18:00")
	if strings.Contains(res.Stdout, "Placeholder") {
		t.Error("the validation placeholder leaked into the file")
	}
	if _, err := os.Stat("capella.yaml"); err == nil {
		t.Fatal("--dry-run wrote a file")
	}

	res = mustRun(t, args...)
	wantContains(t, res.Stdout, "Wrote capella.yaml (validated)", "export CBCTL_PWD_NOVA_01_APP")
	if _, err := capellafile.Load(filepath.Join(dir, "capella.yaml"), func(k string) string {
		if k == "CBCTL_PWD_NOVA_01_APP" {
			return "Aa1!aaaaaaaa"
		}
		return ""
	}); err != nil {
		t.Fatalf("written file does not validate: %v", err)
	}
	if text := string(mustRead(t, filepath.Join(dir, "capella.yaml"))); strings.Contains(text, "Aa1!") || strings.Contains(text, "Placeholder") {
		t.Errorf("a password value was written:\n%s", text)
	}

	wantExit(t, runResource(t, args...), ExitError, "already exists")
	mustRun(t, append(args, "--force")...)
	if _, err := os.Stat("capella.yaml.bak"); err != nil {
		t.Errorf("no .bak with --force: %v", err)
	}

	bad := runResource(t, "init", "--non-interactive", "--org-id", "org-1", "--project", "nova", "--cluster", "nova-01", "--file", "x.yaml",
		"--hibernation", "office-hours", "--office-hours", "08:15-18:00")
	wantExit(t, bad, ExitConfigInvalid, "capella.yaml with")
}

func TestInitInteractive(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_HOME", t.TempDir())
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID", "CBCTL_CONFIG"} {
		t.Setenv(k, "")
	}
	fakeTTY(t)
	answers := strings.Join([]string{
		"new",    // start
		"org-9",  // organization id
		"web",    // project
		"web-01", // cluster
		"aws",    // provider
		"",       // region default
		"",       // network
		"dev",    // preset
		"",       // support plan
		"y",      // protect
		"n",      // hibernate
		"",       // cidrs
		"",       // bucket
	}, "\n") + "\n"
	res := runResourceIn(t, answers, "init")
	if res.Err != nil {
		t.Fatalf("init: %v\nstdout: %s\nstderr: %s", res.Err, res.Stdout, res.Stderr)
	}
	wantContains(t, res.Stdout, "Wrote capella.yaml (validated)")
	text := string(mustRead(t, filepath.Join(dir, "capella.yaml")))
	wantContains(t, text, "organizationId: org-9", "name: web-01", "configurationType: singleNode")

	res = runResourceIn(t, "new\norg-9\nweb\nweb-01\naws\n\nnot-a-cidr\n", "init", "--file", "b.yaml", "--dry-run")
	wantContains(t, res.Stderr, "Please answer again")
	if res.Err != errInitEOF {
		t.Errorf("err = %v, want the end-of-input error", res.Err)
	}
}

func TestInitOverwriteDecline(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_HOME", t.TempDir())
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID", "CBCTL_CONFIG"} {
		t.Setenv(k, "")
	}
	path := filepath.Join(dir, "capella.yaml")
	if err := os.WriteFile(path, []byte("keep-me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeTTY(t)
	res := runResourceIn(t, "n\n", "init")
	if res.Err != nil {
		t.Fatalf("init: %v\n%s\n%s", res.Err, res.Stdout, res.Stderr)
	}
	wantContains(t, res.Stdout, "Nothing was written.")
	if got := string(mustRead(t, path)); got != "keep-me\n" {
		t.Fatalf("file changed: %q", got)
	}
}

func TestInitImportNeedsAPIKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_HOME", t.TempDir())
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID", "CBCTL_CONFIG"} {
		t.Setenv(k, "")
	}
	fakeTTY(t)
	res := runResourceIn(t, "import\n", "init", "--file", "imported.yaml", "--dry-run")
	wantExit(t, res, ExitConfigInvalid, "importing an existing Capella project needs an API key")
}

func TestInitImportFromFake(t *testing.T) {
	f := resEnv(t)
	pid := f.SeedProject("alpha")
	f.SeedCluster(pid, "alpha-01", "healthy")
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_CONFIG", "")
	fakeTTY(t)
	res := runResourceIn(t, "import\n1\nalpha-01\n", "init", "--file", "from-capella.yaml")
	if res.Err != nil {
		t.Fatalf("init import: %v\n%s\n%s", res.Err, res.Stdout, res.Stderr)
	}
	text := string(mustRead(t, filepath.Join(dir, "from-capella.yaml")))
	wantContains(t, text, "name: alpha", "name: alpha-01")
	noWrites(t, f)
}

func TestInitAllowAnyIPConfirm(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CBCTL_HOME", t.TempDir())
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_TOKEN", "CAPELLA_ORG_ID", "CBCTL_CONFIG"} {
		t.Setenv(k, "")
	}
	fakeTTY(t)
	answers := strings.Join([]string{
		"new", "org-9", "web", "web-01", "aws", "", "", "dev", "", "y", "n",
		"0.0.0.0/0", // cidrs
		"y",         // allow any IP
		"",          // no bucket
	}, "\n") + "\n"
	res := runResourceIn(t, answers, "init", "--file", "anyip.yaml", "--dry-run")
	if res.Err != nil {
		t.Fatalf("init: %v\n%s\n%s", res.Err, res.Stdout, res.Stderr)
	}
	wantContains(t, res.Stdout, "allowAnyIp: true", "0.0.0.0/0")
}

func TestDisplayPathUncleanWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	// A working directory spelled with ".." (for example a TMPDIR set from a relative path)
	// still shows files below it relative.
	unclean := base + "/a/../work" // not filepath.Join: it would clean the path
	if err := os.MkdirAll(filepath.Join(base, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(unclean)
	t.Setenv("PWD", unclean)
	if got := displayPath(filepath.Join(base, "work", "capella.yaml")); got != "capella.yaml" {
		t.Errorf("displayPath = %q, want capella.yaml", got)
	}
	if got := displayPath(filepath.Join(base, "other", "capella.yaml")); got != filepath.Join(base, "other", "capella.yaml") {
		t.Errorf("a path outside the working directory changed: %q", got)
	}
}
