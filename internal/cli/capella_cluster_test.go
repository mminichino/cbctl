package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/logging"
)

// capellaRun is the outcome of one run of the real capella command tree.
type capellaRun struct {
	Stdout, Stderr string
	Err            error
}

// code is the exit code the run would end with.
func (r capellaRun) code() ExitCode {
	if r.Err == nil {
		return ExitOK
	}
	var ce *CLIError
	if asCLIError(r.Err, &ce) && ce.Code != 0 {
		return ce.Code
	}
	return ExitError
}

func asCLIError(err error, target **CLIError) bool { return errors.As(err, target) }

// newFake starts a fake Capella and points the CLI at it (fake clock, so waits are instant).
func newFake(t *testing.T) *capellafake.Fake {
	t.Helper()
	f := capellafake.New(t)
	capellaEnv(t, f)
	return f
}

// runCap runs "cbctl capella ARGS" against the environment set up by newFake.
func runCap(t *testing.T, args ...string) capellaRun {
	t.Helper()
	root := newCapellaCmd()
	root.SilenceUsage, root.SilenceErrors = true, true
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	logging.SetOutputs(&stdout, &stderr)
	root.SetArgs(args)
	err := root.Execute()
	return capellaRun{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func (r capellaRun) mustOK(t *testing.T) capellaRun {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("command failed: %v\nstdout:\n%s\nstderr:\n%s", r.Err, r.Stdout, r.Stderr)
	}
	return r
}

func (r capellaRun) mustFail(t *testing.T, code ExitCode, contains string) capellaRun {
	t.Helper()
	if r.Err == nil {
		t.Fatalf("command succeeded, want exit %d (%q)\nstdout:\n%s", code, contains, r.Stdout)
	}
	if got := r.code(); got != code {
		t.Fatalf("exit code = %d, want %d (err %v)", got, code, r.Err)
	}
	var ce *CLIError
	msg := r.Err.Error()
	if asCLIError(r.Err, &ce) {
		msg = ce.Message + " " + ce.Action
	}
	if !strings.Contains(msg, contains) {
		t.Fatalf("error %q does not contain %q", msg, contains)
	}
	return r
}

func (r capellaRun) wantStdout(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(r.Stdout, p) {
			t.Errorf("stdout does not contain %q:\n%s", p, r.Stdout)
		}
	}
}

// jsonOut decodes the single JSON document on stdout.
func (r capellaRun) jsonOut(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.Stdout), v); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, r.Stdout)
	}
}

// seedCluster seeds a project with a healthy 3-node cluster and returns the IDs.
func seedCluster(f *capellafake.Fake, project, name string) (projectID, clusterID string) {
	projectID = f.SeedProject(project)
	return projectID, f.SeedCluster(projectID, name, "healthy")
}

// seedFree creates a healthy free-tier cluster in a new project.
func seedFree(t *testing.T, f *capellafake.Fake, name string) (projectID, clusterID string) {
	t.Helper()
	projectID = f.SeedProject("free")
	id, err := f.Client().CreateFreeCluster(t.Context(), projectID, capellaapi.Object{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	f.SetClusterState(id, "healthy")
	return projectID, id
}

func clusterOf(t *testing.T, f *capellafake.Fake, projectID, clusterID string) *capellaapi.Cluster {
	t.Helper()
	c, err := f.Client().GetCluster(t.Context(), projectID, clusterID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClusterList(t *testing.T) {
	f := newFake(t)
	pa, _ := seedCluster(f, "alpha", "c1")
	f.SeedCluster(pa, "c2", "turnedOff")
	f.SeedProject("empty")

	t.Run("text", func(t *testing.T) {
		runCap(t, "cluster", "list").mustOK(t).wantStdout(t, "NAME", "c1", "c2", "alpha", "aws", "us-east-1", "turnedOff")
	})
	t.Run("one project", func(t *testing.T) {
		f.SeedCluster(f.SeedProject("beta"), "other", "healthy")
		out := runCap(t, "cluster", "list", "--project", "beta").mustOK(t)
		out.wantStdout(t, "other")
		if strings.Contains(out.Stdout, "c1") {
			t.Errorf("project filter leaked: %s", out.Stdout)
		}
	})
	t.Run("json", func(t *testing.T) {
		var items []clusterSummary
		runCap(t, "-o", "json", "cluster", "list").mustOK(t).jsonOut(t, &items)
		if len(items) < 2 || items[0].Nodes != 3 {
			t.Fatalf("items = %+v", items)
		}
	})
	t.Run("unknown project", func(t *testing.T) {
		runCap(t, "cluster", "list", "--project", "nope").mustFail(t, ExitConfigInvalid, "project nope not found")
	})
}

func TestClusterGet(t *testing.T) {
	f := newFake(t)
	_, id := seedCluster(f, "alpha", "c1")

	var d clusterDetail
	runCap(t, "-o", "json", "cluster", "get", "c1").mustOK(t).jsonOut(t, &d)
	if d.ID != id || d.Provider != "aws" || d.SupportPlan != "basic" || len(d.ServiceGroups) != 1 || d.ServiceGroups[0].Services != "data,index,query" {
		t.Fatalf("detail = %+v", d)
	}
	runCap(t, "cluster", "get", "c1").mustOK(t).wantStdout(t, "name: c1", "state: healthy")
	runCap(t, "cluster", "get", "missing").mustFail(t, ExitConfigInvalid, "cluster missing not found")
}

func TestClusterOptions(t *testing.T) {
	f := newFake(t)
	seedCluster(f, "alpha", "c1")

	t.Run("text", func(t *testing.T) {
		runCap(t, "cluster", "options").mustOK(t).wantStdout(t, "Free tier", "available", "Provisioned", "aws, gcp, azure", "basic, developer pro, enterprise", "disk-types")
	})
	t.Run("json", func(t *testing.T) {
		var o clusterOptions
		runCap(t, "-o", "json", "cluster", "options").mustOK(t).jsonOut(t, &o)
		if !o.FreeTier.Available || len(o.Provisioned.Sizes) == 0 || len(o.Provisioned.RegionsInUse) != 1 {
			t.Fatalf("options = %+v", o)
		}
	})
	t.Run("free tier in use", func(t *testing.T) {
		pid := f.SeedProject("free")
		if _, err := f.Client().CreateFreeCluster(t.Context(), pid, capellaapi.Object{
			"name": "freebie", "cloudProvider": capellaapi.Object{"type": "aws", "region": "us-east-2"}}); err != nil {
			t.Fatal(err)
		}
		var o clusterOptions
		runCap(t, "-o", "json", "cluster", "options").mustOK(t).jsonOut(t, &o)
		if o.FreeTier.Available || !strings.Contains(o.FreeTier.UsedBy, "freebie") {
			t.Fatalf("free tier = %+v", o.FreeTier)
		}
	})
}

func TestClusterDiskTypes(t *testing.T) {
	newFake(t)
	runCap(t, "cluster", "disk-types").mustOK(t).wantStdout(t, "gp3", "pd-ssd", "Ultra")
	out := runCap(t, "cluster", "disk-types", "--provider", "gcp").mustOK(t)
	out.wantStdout(t, "pd-ssd")
	if strings.Contains(out.Stdout, "\naws ") {
		t.Errorf("provider filter leaked: %s", out.Stdout)
	}
	var doc map[string]map[string]diskTypeView
	runCap(t, "-o", "json", "cluster", "disk-types").mustOK(t).jsonOut(t, &doc)
	if _, ok := doc["aws"]["gp3"]; !ok {
		t.Fatalf("doc = %v", doc)
	}
	runCap(t, "cluster", "disk-types", "--provider", "oci").mustFail(t, ExitConfigInvalid, "unknown provider")
}

func TestSuggestCIDR(t *testing.T) {
	tests := []struct {
		name string
		used []string
		want string
	}{
		{"none", nil, "10.0.0.0/24"},
		{"skips used", []string{"10.0.0.0/24"}, "10.0.1.0/24"},
		{"skips overlapping larger", []string{"10.0.0.0/20"}, "10.0.16.0/24"},
		{"ignores junk", []string{"nonsense"}, "10.0.0.0/24"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := suggestCIDR(tc.used); got != tc.want {
				t.Errorf("suggestCIDR = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestGroupLimits(t *testing.T) {
	tests := []struct {
		name         string
		idx          int
		single       bool
		lo, hi       int
		wantLo, want int
	}{
		{"first group", 0, false, 0, 0, 3, 27},
		{"other group", 1, false, 0, 0, 2, 24},
		{"single node", 0, true, 0, 0, 1, 1},
		{"override", 0, false, 5, 9, 5, 9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi := groupLimits(tc.idx, tc.single, tc.lo, tc.hi)
			if lo != tc.wantLo || hi != tc.want {
				t.Errorf("limits = %d-%d, want %d-%d", lo, hi, tc.wantLo, tc.want)
			}
		})
	}
}

func TestParseHours(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"168", 168, false}, {"168h", 168, false}, {"7d", 168, false}, {" 30D ", 720, false},
		{"30D", 720, false}, {"0", 0, true}, {"-5", 0, true}, {"", 0, true}, {"1.5d", 0, true}, {"soon", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseHours(tc.in, "--retain")
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("parseHours(%q) = %d, %v", tc.in, got, err)
			}
		})
	}
	for h, want := range map[int]string{24: "1 day", 720: "30 days", 36: "36 h", 0: "0 h"} {
		if got := hoursText(h); got != want {
			t.Errorf("hoursText(%d) = %q, want %q", h, got, want)
		}
	}
}

func TestOptionsProblem(t *testing.T) {
	opts := map[string]any{"intervals": []any{float64(6), float64(12)}, "minimumRetention": float64(24), "maximumRetention": float64(720)}
	regions := []string{"us-east-1", "eu-west-1"}
	tests := []struct {
		name      string
		interval  int
		retention int
		copies    []string
		want      string
	}{
		{"ok", 12, 48, []string{"eu-west-1"}, ""},
		{"interval", 5, 48, nil, "interval 5 is not permitted"},
		{"short retention", 12, 1, nil, "retention 1 h is not permitted"},
		{"long retention", 12, 721, nil, "retention 721 h is not permitted"},
		{"three copies", 12, 48, []string{"a", "b", "c"}, "at most two"},
		{"unknown region", 12, 48, []string{"mars-1"}, "mars-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := optionsProblem(opts, tc.interval, tc.retention, tc.copies, regions, true)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Fatalf("problem = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClusterCommandsKeepSecrets runs commands at debug level and checks that the API key never
// reaches stdout or stderr, and that clone prints each generated password once, on stdout only.
func TestClusterCommandsKeepSecrets(t *testing.T) {
	f := newFake(t)
	pid, id := seedCluster(f, "alpha", "c1")
	seedDataModel(t, f, pid, id)
	runs := [][]string{
		{"cluster", "list"}, {"cluster", "get", "c1"}, {"cluster", "options"},
		{"cluster", "scale", "c1", "--nodes", "4"}, {"cluster", "update", "c1", "--description", "x"},
		{"cluster", "backup", "schedule", "set", "c1", "--every", "12", "--retain", "30d"},
		{"cluster", "clone", "c1", "c2", "--region", "us-west-2", "--with-credentials"},
	}
	for _, args := range runs {
		out := runCap(t, append([]string{"--log-level", "debug"}, args...)...).mustOK(t)
		if all := out.Stdout + out.Stderr; strings.Contains(all, f.APIKey) {
			t.Fatalf("%v leaked the API key:\n%s", args, all)
		}
	}
	var doc struct {
		Passwords []struct{ Name, Password string } `json:"credentialPasswords"`
	}
	out := runCap(t, "-o", "json", "--log-level", "debug", "cluster", "clone", "c1", "c3", "--region", "eu-west-1", "--with-credentials").mustOK(t)
	out.jsonOut(t, &doc)
	if len(doc.Passwords) != 1 {
		t.Fatalf("passwords = %+v", doc.Passwords)
	}
	pw := doc.Passwords[0].Password
	if n := strings.Count(out.Stdout, pw); n != 1 {
		t.Fatalf("password appears %d times on stdout", n)
	}
	if strings.Contains(out.Stderr, pw) {
		t.Fatal("password leaked to stderr")
	}
}
