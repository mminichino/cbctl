package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// capellaEnv points the CLI at a fake Capella and isolates it from the developer's environment.
func capellaEnv(t *testing.T, f *capellafake.Fake) {
	t.Helper()
	t.Setenv("CBCTL_HOME", t.TempDir())
	t.Setenv("CAPELLA_API_BASE_URL", f.URL)
	t.Setenv("CAPELLA_API_KEY", f.APIKey)
	t.Setenv("CAPELLA_ORG_ID", f.OrgID)
	for _, k := range []string{"CAPELLA_TOKEN", "CBCTL_PROFILE", "CBCTL_QUIET", "CI"} {
		t.Setenv(k, "")
	}
	clk := engine.NewFakeClock(time.Time{})
	old := clockFactory
	clockFactory = func() engine.Clock { return clk }
	out, errW := logging.Outputs()
	t.Cleanup(func() {
		clockFactory = old
		logging.SetOutputs(out, errW)
		logging.SetLevel(logging.LevelInfo)
		redact.Reset()
	})
}

// probeResult is what runProbe captured.
type probeResult struct {
	Stdout, Stderr string
	Err            error
	RT             *runtime
}

// runProbe runs args against a throwaway capella-like command group with one leaf, "probe", that
// builds a runtime and hands it to fn. It uses the same global flags and hook as the real group.
func runProbe(t *testing.T, args []string, fn func(rt *runtime) error) probeResult {
	t.Helper()
	root := &cobra.Command{Use: "capella", SilenceUsage: true, SilenceErrors: true,
		PersistentPreRunE: capellaPersistentPreRun}
	addCapellaGlobalFlags(root)
	var res probeResult
	leaf := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		rt, err := newRuntime(cmd, apiFlagsOf(cmd))
		if err != nil {
			return err
		}
		res.RT = rt
		if fn == nil {
			return nil
		}
		return fn(rt)
	}}
	flags = addAPIFlags(leaf)
	root.AddCommand(leaf)

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	logging.SetOutputs(&stdout, &stderr)
	root.SetArgs(append([]string{"probe"}, args...))
	res.Err = root.Execute()
	res.Stdout, res.Stderr = stdout.String(), stderr.String()
	return res
}

// flags carries the most recent probe's apiFlags to its RunE.
var flags *apiFlags

func apiFlagsOf(*cobra.Command) *apiFlags { return flags }

func exitCodeOf(t *testing.T, err error) ExitCode {
	t.Helper()
	var ce *CLIError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *CLIError", err)
	}
	return ce.Code
}

func TestCapellaGlobalFlagsRegistered(t *testing.T) {
	cmd := newCapellaCmd()
	pf := cmd.PersistentFlags()
	tests := []struct {
		name, short, def string
	}{
		{"output", "o", "text"},
		{"yes", "y", "false"},
		{"auto-approve", "", "false"},
		{"quiet", "q", "false"},
		{"no-wait", "", "false"},
		{"timeout", "", "1h0m0s"},
		{"log-level", "", "info"},
		{"profile", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fl := pf.Lookup(tc.name)
			if fl == nil {
				t.Fatalf("persistent flag --%s missing", tc.name)
			}
			if fl.Shorthand != tc.short {
				t.Errorf("shorthand = %q, want %q", fl.Shorthand, tc.short)
			}
			if fl.DefValue != tc.def {
				t.Errorf("default = %q, want %q", fl.DefValue, tc.def)
			}
		})
	}
	if cmd.PersistentPreRunE == nil {
		t.Error("capella has no persistent hook")
	}
}

// TestNoFlagCollisions merges persistent flags into every command of the real tree, which panics
// on a duplicate shorthand.
func TestNoFlagCollisions(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		t.Run(c.CommandPath(), func(t *testing.T) {
			if err := c.ParseFlags(nil); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

func TestLegacyLeafFlagsUnchanged(t *testing.T) {
	legacy := []string{"token", "api-host", "org", "org-id", "project", "project-id", "database", "database-id",
		"user-email", "user-id", "allow-cidr", "username", "password"}
	tests := []struct {
		path  []string
		extra []string
	}{
		{[]string{"cluster", "create"}, nil},
		{[]string{"cluster", "exists"}, nil},
		{[]string{"cluster", "destroy"}, nil},
		{[]string{"cluster", "test"}, []string{"bucket"}},
		{[]string{"bucket", "create"}, []string{"quota", "replicas"}},
		{[]string{"bucket", "exists"}, nil},
		{[]string{"scope", "create"}, []string{"bucket"}},
		{[]string{"scope", "exists"}, []string{"bucket"}},
		{[]string{"collection", "create"}, []string{"bucket", "scope"}},
		{[]string{"collection", "exists"}, []string{"bucket", "scope"}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.path, " "), func(t *testing.T) {
			root := newCapellaCmd()
			leaf, _, err := root.Find(tc.path)
			if err != nil || leaf.Name() != tc.path[len(tc.path)-1] {
				t.Fatalf("Find(%v) = %v, %v", tc.path, leaf, err)
			}
			var got []string
			leaf.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { got = append(got, f.Name) })
			want := append(append([]string{}, legacy...), tc.extra...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("flags = %v, want %v", got, want)
			}
			if f := leaf.Flags().Lookup("username"); f == nil || f.Shorthand != "u" {
				t.Errorf("-u shorthand lost: %+v", f)
			}
			if f := leaf.Flags().Lookup("password"); f == nil || f.Shorthand != "p" {
				t.Errorf("-p shorthand lost: %+v", f)
			}
		})
	}
}

func TestLegacyHelpStillWorks(t *testing.T) {
	for _, path := range [][]string{
		{"capella", "--help"},
		{"capella", "cluster", "test", "--help"},
		{"capella", "cluster", "create", "--help"},
		{"capella", "bucket", "create", "--help"},
		{"capella", "configure", "--help"},
	} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&out)
			rootCmd.SetArgs(path)
			t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !strings.Contains(out.String(), "Usage:") {
				t.Fatalf("no usage in help output:\n%s", out.String())
			}
		})
	}
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"capella", "--help"})
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--output", "--yes", "--quiet", "--no-wait", "--timeout", "--log-level", "--profile"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("capella --help does not list %s:\n%s", want, out.String())
		}
	}
}

func TestReadGlobalsValidation(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)
	tests := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"bad output", []string{"-o", "xml"}, `invalid output format "xml"`},
		{"zero timeout", []string{"--timeout", "0"}, "--timeout must be positive"},
		{"negative timeout", []string{"--timeout", "-5m"}, "--timeout must be positive"},
		{"bad log level", []string{"--log-level", "loud"}, `invalid log level "loud"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runProbe(t, tc.args, nil)
			if res.Err == nil || !strings.Contains(res.Err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want it to contain %q", res.Err, tc.wantMsg)
			}
			if got := exitCodeOf(t, res.Err); got != ExitConfigInvalid {
				t.Errorf("exit code = %d, want %d", got, ExitConfigInvalid)
			}
		})
	}
}

func TestReadGlobalsValues(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)
	tests := []struct {
		name  string
		args  []string
		env   map[string]string
		check func(t *testing.T, rt *runtime)
	}{
		{"defaults", nil, nil, func(t *testing.T, rt *runtime) {
			if rt.JSON || rt.Yes || rt.Quiet || rt.NoWait || rt.Timeout != 60*time.Minute {
				t.Errorf("unexpected defaults: %+v", rt)
			}
		}},
		{"json", []string{"-o", "json"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.JSON {
				t.Error("JSON not set")
			}
		}},
		{"output case-insensitive", []string{"--output", "JSON"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.JSON {
				t.Error("JSON not set")
			}
		}},
		{"yes short", []string{"-y"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.Yes {
				t.Error("Yes not set")
			}
		}},
		{"auto-approve alias", []string{"--auto-approve"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.Yes {
				t.Error("--auto-approve did not set Yes")
			}
		}},
		{"quiet flag", []string{"-q"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.Quiet {
				t.Error("Quiet not set")
			}
		}},
		{"quiet env", nil, map[string]string{"CBCTL_QUIET": "1"}, func(t *testing.T, rt *runtime) {
			if !rt.Quiet {
				t.Error("CBCTL_QUIET=1 did not set Quiet")
			}
		}},
		{"quiet env other value", nil, map[string]string{"CBCTL_QUIET": "0"}, func(t *testing.T, rt *runtime) {
			if rt.Quiet {
				t.Error("CBCTL_QUIET=0 set Quiet")
			}
		}},
		{"no-wait", []string{"--no-wait"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.NoWait {
				t.Error("NoWait not set")
			}
		}},
		{"timeout", []string{"--timeout", "90m"}, nil, func(t *testing.T, rt *runtime) {
			if rt.Timeout != 90*time.Minute {
				t.Errorf("Timeout = %v", rt.Timeout)
			}
		}},
		{"timeout hours", []string{"--timeout", "2h"}, nil, func(t *testing.T, rt *runtime) {
			if rt.Timeout != 2*time.Hour {
				t.Errorf("Timeout = %v", rt.Timeout)
			}
		}},
		{"timeout with no-wait is accepted", []string{"--timeout", "5m", "--no-wait"}, nil, func(t *testing.T, rt *runtime) {
			if !rt.NoWait || rt.Timeout != 5*time.Minute {
				t.Errorf("rt = %+v", rt)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			res := runProbe(t, tc.args, nil)
			if res.Err != nil {
				t.Fatalf("err = %v\nstderr: %s", res.Err, res.Stderr)
			}
			tc.check(t, res.RT)
		})
	}
}

func TestLogLevelAndQuiet(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)
	emit := func(rt *runtime) error {
		logging.Debug("debug-line")
		logging.Info("info-line")
		logging.Warning("warn-line")
		return nil
	}
	tests := []struct {
		name      string
		args      []string
		env       string
		want, not []string
	}{
		{"default", nil, "", []string{"info-line", "warn-line"}, []string{"debug-line"}},
		{"debug", []string{"--log-level", "debug"}, "", []string{"debug-line", "info-line", "warn-line"}, nil},
		{"error only", []string{"--log-level", "error"}, "", nil, []string{"debug-line", "info-line", "warn-line"}},
		{"quiet is warn", []string{"-q"}, "", []string{"warn-line"}, []string{"debug-line", "info-line"}},
		{"quiet env", nil, "1", []string{"warn-line"}, []string{"info-line"}},
		{"explicit level beats quiet", []string{"-q", "--log-level", "debug"}, "", []string{"debug-line", "info-line"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CBCTL_QUIET", tc.env)
			res := runProbe(t, tc.args, emit)
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			all := res.Stdout + res.Stderr
			for _, w := range tc.want {
				if !strings.Contains(all, w) {
					t.Errorf("output missing %q:\n%s", w, all)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(all, n) {
					t.Errorf("output contains %q:\n%s", n, all)
				}
			}
		})
	}
}

func TestJSONModeSendsLogsToStderr(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)
	res := runProbe(t, []string{"-o", "json"}, func(rt *runtime) error {
		logging.Info("info-line")
		rt.Out.Object(map[string]string{"k": "v"})
		return nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if strings.Contains(res.Stdout, "info-line") || !strings.Contains(res.Stderr, "info-line") {
		t.Errorf("info log must go to stderr in JSON mode\nstdout: %q\nstderr: %q", res.Stdout, res.Stderr)
	}
	// The redirect is undone when the command ends.
	out, _ := logging.Outputs()
	if out == io.Writer(os.Stderr) {
		t.Error("log stdout still redirected to stderr after the command")
	}
}

func TestNewRuntimeAuth(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)

	t.Run("missing key", func(t *testing.T) {
		t.Setenv("CAPELLA_API_KEY", "")
		res := runProbe(t, nil, nil)
		if res.Err == nil || !strings.Contains(res.Err.Error(), "Capella API key not set; use --token, set CAPELLA_API_KEY (or put it in .env), or run 'cbctl configure'") {
			t.Fatalf("err = %v", res.Err)
		}
		if got := exitCodeOf(t, res.Err); got != ExitConfigInvalid {
			t.Errorf("exit code = %d, want %d", got, ExitConfigInvalid)
		}
	})
	t.Run("missing key names the profile", func(t *testing.T) {
		t.Setenv("CAPELLA_API_KEY", "")
		res := runProbe(t, []string{"--profile", "prod"}, nil)
		if res.Err == nil || !strings.Contains(res.Err.Error(), `(profile "prod")`) {
			t.Fatalf("err = %v", res.Err)
		}
	})
	t.Run("token flag", func(t *testing.T) {
		t.Setenv("CAPELLA_API_KEY", "")
		res := runProbe(t, []string{"--token", f.APIKey}, func(rt *runtime) error {
			_, err := rt.Client.ListProjects(rt.Ctx)
			return err
		})
		if res.Err != nil {
			t.Fatalf("err = %v", res.Err)
		}
		if res.RT.Settings.APIKey.Source != "flag" {
			t.Errorf("source = %q, want flag", res.RT.Settings.APIKey.Source)
		}
	})
	t.Run("api-host flag beats env", func(t *testing.T) {
		t.Setenv("CAPELLA_API_BASE_URL", "http://127.0.0.1:1")
		res := runProbe(t, []string{"--api-host", f.URL}, func(rt *runtime) error {
			_, err := rt.Client.ListProjects(rt.Ctx)
			return err
		})
		if res.Err != nil {
			t.Fatalf("err = %v", res.Err)
		}
	})
	t.Run("wrong key surfaces 401", func(t *testing.T) {
		res := runProbe(t, []string{"--token", "wrong-key-value"}, func(rt *runtime) error {
			_, err := rt.Client.ListProjects(rt.Ctx)
			return err
		})
		if res.Err == nil || !strings.Contains(res.Err.Error(), "401") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestNewRuntimeOrgResolution(t *testing.T) {
	t.Run("org id from env", func(t *testing.T) {
		f := capellafake.New(t)
		capellaEnv(t, f)
		res := runProbe(t, nil, nil)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if got := res.RT.Client.OrgID(); got != f.OrgID {
			t.Errorf("OrgID = %q, want %q", got, f.OrgID)
		}
		if n := f.Count("GET", `^/v4/organizations$`); n != 0 {
			t.Errorf("organizations listed %d times although the org was configured", n)
		}
	})
	t.Run("org id flag", func(t *testing.T) {
		f := capellafake.New(t)
		capellaEnv(t, f)
		t.Setenv("CAPELLA_ORG_ID", "")
		res := runProbe(t, []string{"--org-id", f.OrgID}, nil)
		if res.Err != nil || res.RT.Client.OrgID() != f.OrgID {
			t.Fatalf("err = %v", res.Err)
		}
	})
	t.Run("single org is discovered", func(t *testing.T) {
		f := capellafake.New(t)
		capellaEnv(t, f)
		t.Setenv("CAPELLA_ORG_ID", "")
		res := runProbe(t, []string{"--log-level", "debug"}, func(rt *runtime) error {
			_, err := rt.Client.ListProjects(rt.Ctx)
			return err
		})
		if res.Err != nil {
			t.Fatalf("err = %v", res.Err)
		}
		if got := res.RT.Client.OrgID(); got != f.OrgID {
			t.Errorf("OrgID = %q, want %q", got, f.OrgID)
		}
		if !strings.Contains(res.Stderr+res.Stdout, "using organization "+f.OrgID) {
			t.Errorf("no debug log of the discovery:\n%s%s", res.Stdout, res.Stderr)
		}
	})
	t.Run("several orgs", func(t *testing.T) {
		f := capellafake.New(t)
		capellaEnv(t, f)
		t.Setenv("CAPELLA_ORG_ID", "")
		other := f.AddOrganization("second-org")
		res := runProbe(t, nil, nil)
		if res.Err == nil {
			t.Fatal("expected an error")
		}
		var ce *CLIError
		if !errors.As(res.Err, &ce) || ce.Code != ExitConfigInvalid {
			t.Fatalf("err = %#v", res.Err)
		}
		for _, want := range []string{"several organizations:", "cbctl-fake-org (" + f.OrgID + ")", "second-org (" + other + ")"} {
			if !strings.Contains(ce.Message, want) {
				t.Errorf("message %q lacks %q", ce.Message, want)
			}
		}
		if ce.Action != "pass --org-id or save one with 'cbctl configure'" {
			t.Errorf("action = %q", ce.Action)
		}
	})
	t.Run("no orgs", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := capellaapi.New("key-for-empty-org", capellaapi.WithBaseURL(srv.URL))
		if err != nil {
			t.Fatal(err)
		}
		_, err = discoverOrg(context.Background(), c)
		if err == nil || !strings.Contains(err.Error(), "no organizations visible to this API key") {
			t.Fatalf("err = %v", err)
		}
		if got := exitCodeOf(t, err); got != ExitConfigInvalid {
			t.Errorf("exit code = %d, want %d", got, ExitConfigInvalid)
		}
	})
	t.Run("token without access to the organization", func(t *testing.T) {
		f := capellafake.New(t)
		capellaEnv(t, f)
		f.FailNext("GET", `^/v4/organizations$`, 401, 1)
		t.Setenv("CAPELLA_ORG_ID", "")
		res := runProbe(t, nil, nil)
		if res.Err == nil || !strings.Contains(res.Err.Error(), "listing organizations") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestNewRuntimeUsesInjectedClock(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)
	res := runProbe(t, nil, nil)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if _, ok := res.RT.Clock.(*engine.FakeClock); !ok {
		t.Fatalf("Clock = %T, want the clockFactory fake", res.RT.Clock)
	}
	if _, ok := clockFactory().(*engine.FakeClock); !ok {
		t.Fatal("clockFactory not replaced")
	}
}

// defaultClockFactory is captured at package initialisation, before any test swaps clockFactory.
var defaultClockFactory = clockFactory

func TestDefaultClockFactory(t *testing.T) {
	if _, ok := defaultClockFactory().(engine.RealClock); !ok {
		t.Fatalf("default clock = %T, want engine.RealClock", defaultClockFactory())
	}
}
