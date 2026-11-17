package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mminichino/cbctl/internal/settings"
)

// noConfigDir runs the test in an empty directory, so no capella.yaml is found.
func noConfigDir(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("CBCTL_CONFIG", "")
}

func TestLegacyTableResolves(t *testing.T) {
	root := newCapellaCmd()
	rules, err := os.ReadFile(filepath.Join("..", "..", ".cursor", "rules", "cbctl.mdc"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range legacyCommands {
		c, rest, err := root.Find(l.Path)
		if err != nil || len(rest) != 0 || c.Name() != l.Path[len(l.Path)-1] {
			t.Errorf("legacy path %v does not resolve", l.Path)
		}
		// Every replacement names a documented command.
		first := strings.Fields(strings.TrimPrefix(l.Replacement, "cbctl "))
		if len(first) < 3 || !strings.Contains(string(rules), "cbctl "+strings.Join(first[:3], " ")) {
			t.Errorf("replacement %q of %v is not in cbctl.mdc", l.Replacement, l.Path)
		}
	}
	for _, p := range [][]string{{"cluster", "exists"}, {"cluster", "destroy"}, {"cluster", "test"}, {"bucket", "exists"},
		{"cluster", "apply"}, {"project", "apply"}, {"help"}} {
		c, _, _ := root.Find(p)
		if !c.Hidden {
			t.Errorf("%v must be hidden", p)
		}
	}
	for _, p := range [][]string{{"cluster", "create"}, {"bucket", "create"}, {"cluster", "schedule", "apply"}} {
		c, _, _ := root.Find(p)
		if c.Hidden {
			t.Errorf("%v must stay visible", p)
		}
	}
}

func TestDeprecateLegacyWarnsOnStderrOnly(t *testing.T) {
	ran := false
	cmd := &cobra.Command{Use: "exists", RunE: func(c *cobra.Command, _ []string) error {
		ran = true
		c.Println("true")
		return nil
	}}
	parent := &cobra.Command{Use: "cbctl"}
	parent.AddCommand(cmd)
	deprecateLegacy(cmd, "cbctl capella cluster get CLUSTER")
	var out, errW bytes.Buffer
	parent.SetOut(&out)
	parent.SetErr(&errW)
	parent.SetArgs([]string{"exists", "--help=false"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran || out.String() != "true\n" {
		t.Errorf("stdout = %q, want exactly true", out.String())
	}
	wantContains(t, errW.String(), "deprecated", "cbctl capella cluster get CLUSTER")
	if !cmd.Hidden {
		t.Error("not hidden")
	}
}

func TestValidateAllowCIDR(t *testing.T) {
	tests := []struct {
		in       string
		allowAny bool
		want     string
		bad      bool
	}{
		{"", false, "", false},
		{"", true, "0.0.0.0/0", false},
		{"203.0.113.0/24", false, "203.0.113.0/24", false},
		{"203.0.113.7", false, "203.0.113.7/32", false},
		{"0.0.0.0/0", false, "", true},
		{"::/0", false, "", true},
		{"0.0.0.0/0", true, "0.0.0.0/0", false},
		{"nope", false, "", true},
	}
	for _, tc := range tests {
		got, err := validateAllowCIDR(tc.in, tc.allowAny)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("validateAllowCIDR(%q, %v) = %q, %v", tc.in, tc.allowAny, got, err)
		}
		if err != nil && exitCodeOf(t, err) != ExitConfigInvalid {
			t.Errorf("%q: exit %d", tc.in, exitCodeOf(t, err))
		}
	}
}

func TestCapellaTimeoutParse(t *testing.T) {
	for in, want := range map[string]time.Duration{"90": 90 * time.Second, "90S": 90 * time.Second, "5m": 5 * time.Minute,
		"1h30m": 90 * time.Minute, "1H30M": 90 * time.Minute, "24h": 24 * time.Hour} {
		var v capellaTimeout
		if err := v.Set(in); err != nil || time.Duration(v) != want {
			t.Errorf("Set(%q) = %v, %v; want %v", in, time.Duration(v), err, want)
		}
		if v.Type() != "duration" {
			t.Error("type must stay duration")
		}
		if d, err := time.ParseDuration(v.String()); err != nil || d != want {
			t.Errorf("String() %q does not round-trip", v.String())
		}
	}
	for _, in := range []string{"0", "-1", "abc", "", "25h", "-5m"} {
		var v capellaTimeout
		if err := v.Set(in); err == nil {
			t.Errorf("Set(%q) accepted", in)
		}
	}
}

func TestEnvQuiet(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "true": true, "YES": true, "on": true, "": false, "0": false,
		"false": false, "no": false, "Off": false, "maybe": false} {
		if got := envQuiet(v); got != want {
			t.Errorf("envQuiet(%q) = %v", v, got)
		}
	}
}

func TestCapellactlArity(t *testing.T) {
	if !capellactlArity("bucket", []string{"c", "b"}) || capellactlArity("bucket", []string{"b"}) ||
		!capellactlArity("scope", []string{"c", "b", "s"}) || !capellactlArity("collection", []string{"c", "b", "s", "x"}) ||
		capellactlArity("cluster", []string{"a", "b"}) {
		t.Error("capellactlArity is wrong")
	}
}

func TestLegacyPasswordSafety(t *testing.T) {
	resEnv(t)
	noConfigDir(t)
	wantExit(t, runResource(t, "cluster", "test", "--database", "c1", "-u", "app", "-p", "secret"), ExitConfigInvalid, "-p/--password")
	wantExit(t, runResource(t, "bucket", "exists", "b", "--database", "c1"), ExitConfigInvalid, "database user")
	wantExit(t, runResource(t, "bucket", "exists", "b", "--database", "c1", "-u", "app"), ExitConfigInvalid, "needs a password")
	wantExit(t, runResource(t, "bucket", "exists", "b", "--database", "c1", "-u", "app", "--password-env", "CBCTL_TEST_UNSET_PW"),
		ExitConfigInvalid, "CBCTL_TEST_UNSET_PW")
	wantExit(t, runResource(t, "cluster", "create", "--database", "c1", "--allow-cidr", "0.0.0.0/0"), ExitConfigInvalid, "allows any IP")
	wantExit(t, runResource(t, "cluster", "create", "--database", "c1", "--size", "dev"), ExitConfigInvalid, "cannot combine")
	wantExit(t, runResource(t, "cluster", "create", "c2", "--database", "c1"), ExitConfigInvalid, "differ")

	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var cf capellaFlags
	cmd := &cobra.Command{Use: "x"}
	cf.addTo(cmd)
	cf.username, cf.passFile = "app", pw
	cmd.SetContext(context.Background())
	cfg, err := cf.prepare(cmd, true)
	if err != nil || cfg.Password != "s3cret" || cfg.Username != "app" {
		t.Errorf("prepare = %+v, %v", cfg, err)
	}
}

func TestDirectClusterCreate(t *testing.T) {
	f := resEnv(t)
	noConfigDir(t)
	pid := f.SeedProject("proj")
	f.SeedCluster(pid, "existing", "healthy")

	wantExit(t, runResource(t, "cluster", "create", "a", "--name", "b", "--yes"), ExitConfigInvalid, "differ")
	wantExit(t, runResource(t, "cluster", "create", "--yes"), ExitConfigInvalid, "name is required")

	f.ResetLog()
	res := runResource(t, "cluster", "create", "fresh", "--size", "small", "--provider", "aws", "--region", "us-east-1", "--yes", "--no-wait")
	if res.Err != nil {
		t.Fatalf("direct create: %v\n%s\n%s", res.Err, res.Stdout, res.Stderr)
	}
	if n := f.Count("POST", "/clusters$"); n != 1 {
		t.Errorf("cluster POSTs = %d, want 1", n)
	}
	for _, p := range []string{"allowedcidrs", "users"} {
		if n := f.Count("POST", p); n != 0 {
			t.Errorf("%s POSTs = %d, want 0", p, n)
		}
	}
	if strings.Contains(res.Stdout+res.Stderr, "capella.yaml") {
		t.Errorf("direct create mentions the work file:\n%s%s", res.Stdout, res.Stderr)
	}
	if _, err := os.Stat("capella.yaml"); err == nil {
		t.Error("direct create left a capella.yaml behind")
	}

	f.ResetLog()
	res = mustRun(t, "cluster", "create", "existing", "--yes")
	wantContains(t, res.Stdout, "already exists")
	noWrites(t, f)

	// Without a terminal and --yes the confirmation blocks.
	f.ResetLog()
	wantExit(t, runResource(t, "cluster", "create", "other", "--size", "dev"), ExitBlocked, "no terminal")
	noWrites(t, f)

	// An unknown --project is a usage error, and several projects need --project.
	wantExit(t, runResource(t, "cluster", "create", "x", "--project", "nope", "--yes"), ExitConfigInvalid, "not found")
	f.SeedProject("second")
	wantExit(t, runResource(t, "cluster", "create", "x", "--yes"), ExitConfigInvalid, "several projects")
}

func TestClusterWizard(t *testing.T) {
	f := resEnv(t)
	noConfigDir(t)
	f.SeedProject("proj")
	fakeTTY(t)

	t.Run("custom groups with a re-asked answer", func(t *testing.T) {
		f.ResetLog()
		in := "wiz-01\nmars\naws\n\ncustom\ndata:oops\ndata:3x4/16\nindex,query:2x4/16\n\n\ny\n"
		res := runResourceIn(t, in, "cluster", "create", "--no-wait")
		if res.Err != nil {
			t.Fatalf("wizard: %v\n%s\n%s", res.Err, res.Stdout, res.Stderr)
		}
		wantContains(t, res.Stderr, "choose one of", "Service group")
		body := map[string]any{}
		for _, r := range f.RequestLog() {
			if r.Method == "POST" && strings.HasSuffix(r.Path, "/clusters") {
				body = decodeJSON(t, r.Body)
			}
		}
		if body["name"] != "wiz-01" {
			t.Fatalf("POST body = %v\n%s\n%s", body, res.Stdout, res.Stderr)
		}
		if groups, _ := body["serviceGroups"].([]any); len(groups) != 2 {
			t.Errorf("serviceGroups = %v, want 2", body["serviceGroups"])
		}
	})

	t.Run("EOF cancels with no writes", func(t *testing.T) {
		f.ResetLog()
		res := runResourceIn(t, "wiz-02\n", "cluster", "create")
		wantExit(t, res, ExitError, "Cancelled")
		noWrites(t, f)
	})

	t.Run("three bad answers give up", func(t *testing.T) {
		res := runResourceIn(t, "wiz-03\nx\ny\nz\n", "cluster", "create")
		wantExit(t, res, ExitConfigInvalid, "no valid answer")
	})
}

func TestCapellactlCreateForwardsToAdd(t *testing.T) {
	fx := newFixture(t)
	noConfigDir(t)

	fx.Fake.ResetLog()
	res := mustRun(t, "bucket", "create", "clu", "orders", "--memory", "512")
	wantContains(t, res.Stderr, "'capella bucket create' is 'capella bucket add'")
	body := lastBody(fx, "POST", "/buckets")
	if body["name"] != "orders" || body["memoryAllocationInMb"] != float64(512) {
		t.Errorf("POST body = %v", body)
	}

	fx.Fake.ResetLog()
	mustRun(t, "bucket", "create", "clu", "events")
	body = lastBody(fx, "POST", "/buckets")
	if body["memoryAllocationInMb"] != float64(1024) || body["storageBackend"] != nil {
		t.Errorf("default body = %v", body)
	}

	wantExit(t, runResource(t, "bucket", "create", "orders"), ExitConfigInvalid, "CLUSTER NAME")
	wantExit(t, runResource(t, "bucket", "create", "clu", "x", "--quota", "5"), ExitConfigInvalid, "--quota")

	mustRun(t, "scope", "create", "clu", "orders", "sales")
	mustRun(t, "collection", "create", "clu", "orders", "sales", "eu")
	res = mustRun(t, "collection", "list", "clu", "orders", "sales")
	wantContains(t, res.Stdout, "eu")
	wantExit(t, runResource(t, "scope", "create", "sales"), ExitConfigInvalid, "CLUSTER BUCKET SCOPE")
}

func TestLegacyDestroyDelegatesToDelete(t *testing.T) {
	fx := newFixture(t)
	noConfigDir(t)

	fx.Fake.ResetLog()
	res := runResource(t, "cluster", "destroy", "--database", "clu")
	wantExit(t, res, ExitBlocked, "")
	wantContains(t, res.Stderr, "deprecated", "cluster delete")
	if n := fx.Fake.Count("DELETE", "."); n != 0 {
		t.Errorf("DELETE calls = %d, want 0", n)
	}

	if err := fx.Client.SetDeletionProtection(context.Background(), fx.ProjectID, fx.ClusterID, true); err != nil {
		t.Fatal(err)
	}
	wantExit(t, runResource(t, "cluster", "destroy", "--database", "clu", "--yes"), ExitBlocked, "")
	if err := fx.Client.SetDeletionProtection(context.Background(), fx.ProjectID, fx.ClusterID, false); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "cluster", "destroy", "--database", "missing", "--yes")
	mustRun(t, "cluster", "destroy", "--database", "clu", "--yes", "--no-wait")
	if n := fx.Fake.Count("DELETE", "/clusters/"); n != 1 {
		t.Errorf("DELETE calls = %d, want 1", n)
	}
}

func TestImportSyncHint(t *testing.T) {
	resEnv(t)
	noConfigDir(t)
	for _, args := range [][]string{{"import"}, {"import", "--full"}, {"import", "--cluster", "c1"}, {"import", "--stdout"}} {
		res := runResource(t, args...)
		ce := wantExitErr(t, res, ExitConfigInvalid, "")
		if !strings.Contains(ce.Action, "capella sync") {
			t.Errorf("%v: hint = %q", args, ce.Action)
		}
	}
	wantExit(t, runResource(t, "import", "f.jsonl", "bad"), ExitConfigInvalid, "bucket.scope.collection")
}

func TestScheduleApplyIsTargetedApply(t *testing.T) {
	f := resEnv(t)
	envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	mustRun(t, "apply", "--yes")
	mustRun(t, "cluster", "schedule", "set", "envclu", "--timezone", "US/Pacific", "--weekdays", "08:00-18:00",
		"--weekend", "off", "--no-apply", "--yes")
	f.ResetLog()
	mustRun(t, "cluster", "schedule", "apply", "envclu", "--yes")
	if n := f.Count("POST", "onOffSchedule") + f.Count("PUT", "onOffSchedule"); n != 1 {
		t.Errorf("schedule writes = %d, want 1", n)
	}
	res := mustRun(t, "apply", "--target", "schedule/envclu", "--yes")
	wantContains(t, res.Stdout, "No changes")
	wantExit(t, runResource(t, "cluster", "schedule", "apply", "envclu", "--target", "cluster/envclu"), ExitConfigInvalid, "--target")

	res = mustRun(t, "cluster", "apply", "envclu", "--yes")
	wantContains(t, res.Stderr, "deprecated", "apply --target cluster/NAME")
	mustRun(t, "project", "apply", "envproj", "--yes")
}

func TestCapellaHelpAlias(t *testing.T) {
	res := mustRun(t, "help", "topics")
	if strings.TrimSpace(res.Stdout) == "" {
		t.Error("help topics printed nothing")
	}
}

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return m
}

func TestNegationFlags(t *testing.T) {
	root := newCapellaCmd()
	for _, p := range [][]string{{"cluster", "update"}, {"cluster", "group", "add"}, {"bucket", "backup", "schedule", "set"}} {
		c, _, err := root.Find(p)
		if err != nil {
			t.Fatal(err)
		}
		var neg string
		for _, name := range negatableFlags {
			if c.Flags().Lookup(name) != nil {
				neg = name
				break
			}
		}
		if neg == "" {
			t.Fatalf("%v has no negatable flag", p)
		}
		fl := c.Flags().Lookup("no-" + neg)
		if fl == nil || !fl.Hidden {
			t.Fatalf("%v: --no-%s missing or visible", p, neg)
		}
		if err := c.Flags().Parse([]string{"--no-" + neg}); err != nil {
			t.Fatal(err)
		}
		if !c.Flags().Changed(neg) || c.Flags().Lookup(neg).Value.String() != "false" {
			t.Errorf("%v: --no-%s did not set --%s=false", p, neg, neg)
		}
	}
}

func TestConfigureLegacyOutputFormat(t *testing.T) {
	lf := newLegacyFixture(t, "")
	if err := (settings.ProfileStore{Path: settings.ProfilesPath(lf.home)}).Save(map[string]settings.Profile{
		"b": {OrganizationID: "org-b"}, "a": {OrganizationID: "org-a"}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := lf.run("configure", "list", "-o", "json", "--output-format", "legacy")
	var m map[string]map[string]any
	if code != 0 || json.Unmarshal([]byte(stdout), &m) != nil || m["a"]["organizationId"] != "org-a" {
		t.Fatalf("legacy list: exit %d %s", code, stdout)
	}
	stdout, _, code = lf.run("configure", "list", "-o", "json")
	var rows []map[string]any
	if code != 0 || json.Unmarshal([]byte(stdout), &rows) != nil || len(rows) != 2 || rows[0]["profile"] != "a" {
		t.Fatalf("cbctl list (sorted): exit %d %s", code, stdout)
	}
	stdout, _, code = lf.run("configure", "show", "--profile", "a", "-o", "json", "--output-format", "legacy")
	var show map[string]map[string]string
	if code != 0 || json.Unmarshal([]byte(stdout), &show) != nil || show["organization"]["value"] != "org-a" {
		t.Fatalf("legacy show: exit %d %s", code, stdout)
	}
	if _, _, code = lf.run("configure", "show", "--output-format", "xml"); code != int(ExitConfigInvalid) {
		t.Errorf("bad --output-format: exit %d", code)
	}
	// CBCTL_PROFILE (and capellactl's CAPELLACTL_PROFILE) selects the profile remove acts on.
	lf.env["CAPELLACTL_PROFILE"] = "b"
	if _, stderr, code := lf.run("configure", "remove", "-y"); code != 0 || !strings.Contains(stderr, "CAPELLACTL_PROFILE is deprecated") {
		t.Fatalf("remove: exit %d %s", code, stderr)
	}
	left, _ := settings.ProfileStore{Path: settings.ProfilesPath(lf.home)}.Load()
	if _, ok := left["b"]; ok || len(left) != 1 {
		t.Errorf("profiles after remove = %v", left)
	}
}
