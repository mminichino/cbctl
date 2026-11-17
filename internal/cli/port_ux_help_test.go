package cli_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mminichino/cbctl/internal/cli"
)

// TestWaiter_CAP007_AC9_DeleteDotsAndTotalTime: cluster delete on a terminal draws progress dots
// on stderr and reports the total time; off a terminal no dots are drawn.
func TestWaiter_CAP007_AC9_DeleteDotsAndTotalTime(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, tty := range []bool{true, false} {
		s := newScenario(t)
		s.Fake.SettlePolls(3)
		pid := s.Fake.SeedProject("alpha")
		s.Fake.SeedCluster(pid, "c1", "healthy")
		s.TTY(tty)
		r := mustScenario(t, s.Run("capella", "--yes", "cluster", "delete", "c1"))
		if !regexp.MustCompile(`\[info\] - cluster c1: deleted \(\d+s\)\n`).MatchString(r.Stdout) {
			t.Errorf("tty=%v: no total time line:\n%s", tty, r.Stdout)
		}
		wantContains(t, r.Stdout, "deleted cluster c1 (snapshot backups retained)")
		if dots := strings.Contains(r.Stderr, "."); dots != tty {
			t.Errorf("tty=%v: stderr = %q", tty, r.Stderr)
		}
	}
}

// TestCompletion_CAP034_AC6_Commands: completing a partial command offers the matching commands.
func TestCompletion_CAP034_AC6_Commands(t *testing.T) {
	s := newScenario(t)
	r := mustScenario(t, s.Run("__complete", "capella", "clu"))
	wantContains(t, r.Stdout, "cluster\tCapella cluster operations\n")
	r = mustScenario(t, s.Run("__complete", "capella", "cluster", "sc"))
	wantContains(t, r.Stdout, "scale\t", "schedule\t")
	wantNotContains(t, r.Stdout, "delete\t")
}

// TestCompletion_CAP034_AC6_ClusterNamesFromConfig: a CLUSTER argument completes from the
// cluster names in capella.yaml, offline.
func TestCompletion_CAP034_AC6_ClusterNamesFromConfig(t *testing.T) {
	s := newScenario(t)
	writeCapellaYAML(t, s, scenarioDoc)
	for _, args := range [][]string{
		{"__complete", "capella", "cluster", "get", "env"},
		{"__complete", "capella", "cluster", "scale", ""},
	} {
		r := mustScenario(t, s.Run(args...))
		if !strings.HasPrefix(r.Stdout, "envclu\n") {
			t.Errorf("%v: %q", args, r.Stdout)
		}
	}
	r := mustScenario(t, s.Run("__complete", "capella", "cluster", "get", "zzz"))
	wantNotContains(t, r.Stdout, "envclu")
	if n := len(s.Fake.RequestLog()); n != 0 {
		t.Errorf("completion made %d API calls", n)
	}
}

// TestCompletion_CAP034_AC6_ScriptsGenerated: cbctl completion writes a script for each shell.
func TestCompletion_CAP034_AC6_ScriptsGenerated(t *testing.T) {
	s := newScenario(t)
	for shell, want := range map[string]string{
		"bash":       "__start_cbctl",
		"zsh":        "compdef",
		"fish":       "complete -c cbctl",
		"powershell": "Register-ArgumentCompleter",
	} {
		r := mustScenario(t, s.Run("completion", shell))
		wantContains(t, r.Stdout, want)
	}
}

// TestHelp_CAP034_AC1_RootHelpContainsSections: capella --help has the usage, examples, command
// list and flags sections and points to per-command help.
func TestHelp_CAP034_AC1_RootHelpContainsSections(t *testing.T) {
	r := mustScenario(t, newScenario(t).Run("capella", "--help"))
	wantContains(t, r.Stdout, "Capella cloud operations\n\nUsage:\n  cbctl capella [flags]\n  cbctl capella [command]\n",
		"\nExamples:\n  cbctl capella init\n", "\nAvailable Commands:\n", "\nFlags:\n",
		"  -o, --output string", `Use "cbctl capella [command] --help" for more information about a command.`)
}

// TestHelp_CAP034_AC2_MatchesDashDashHelp: help CMD prints exactly CMD --help.
func TestHelp_CAP034_AC2_MatchesDashDashHelp(t *testing.T) {
	s := newScenario(t)
	for _, cmd := range [][]string{{"plan"}, {"cluster", "scale"}, {"bucket", "add"}} {
		want := mustScenario(t, s.Run(append(append([]string{"capella"}, cmd...), "--help")...)).Stdout
		for _, prefix := range [][]string{{"help", "capella"}, {"capella", "help"}} {
			got := mustScenario(t, s.Run(append(prefix, cmd...)...)).Stdout
			if got != want {
				t.Errorf("%v %v differs from --help:\n%s\nwant:\n%s", prefix, cmd, got, want)
			}
		}
	}
}

// TestHelp_CAP034_AC3_TopicPlainWhenNotTTY: guide topics printed off a terminal have no ANSI
// escape codes.
func TestHelp_CAP034_AC3_TopicPlainWhenNotTTY(t *testing.T) {
	s := newScenario(t)
	for _, topic := range []string{"", "getting-started", "hibernation", "exit-codes", "config"} {
		args := []string{"capella", "guide"}
		if topic != "" {
			args = append(args, topic)
		}
		r := mustScenario(t, s.Run(args...))
		if strings.Contains(r.Stdout, "\x1b[") || r.Stdout == "" {
			t.Errorf("guide %s: styled or empty output %q", topic, r.Stdout)
		}
	}
}

// TestHelp_CAP034_AC7_OfflineNoKey: guide and help work with no API key, no organization and no
// reachable Capella.
func TestHelp_CAP034_AC7_OfflineNoKey(t *testing.T) {
	s := newScenario(t)
	for _, k := range []string{"CAPELLA_API_KEY", "CAPELLA_ORG_ID"} {
		s.Env[k] = ""
		t.Setenv(k, "")
	}
	s.Env["CAPELLA_API_BASE_URL"] = "http://127.0.0.1:1"
	t.Setenv("CAPELLA_API_BASE_URL", "http://127.0.0.1:1")
	for _, args := range [][]string{
		{"capella", "guide"}, {"capella", "guide", "hibernation"},
		{"capella", "guide", "config", "clusters.buckets.storageBackend"},
		{"capella", "help", "plan"}, {"capella", "--help"},
	} {
		mustScenario(t, s.Run(args...), args...)
	}
	if n := len(s.Fake.RequestLog()); n != 0 {
		t.Errorf("offline help made %d API calls", n)
	}
}

// TestCLI_CAP001_AC1_VersionOutput: --version prints the cbctl version and the API version.
func TestCLI_CAP001_AC1_VersionOutput(t *testing.T) {
	var out, errW strings.Builder
	code := cli.Run(t.Context(), []string{"--version"}, cli.Deps{Version: "1.2.3", Stdout: &out, Stderr: &errW})
	if code != 0 || out.String() != "cbctl version 1.2.3\nCapella Management API v4.0\n"+cli.LicenceLine+"\n" || errW.Len() != 0 {
		t.Fatalf("exit %d stdout %q stderr %q", code, out.String(), errW.String())
	}
}

// capellactl scripts become `cbctl capella ...`, so `cbctl capella --version` must work too.
func TestCLI_CAP001_AC1_CapellaVersionMatchesRoot(t *testing.T) {
	want := "cbctl version 1.2.3\nCapella Management API v4.0\n" + cli.LicenceLine + "\n"
	for _, args := range [][]string{{"capella", "--version"}, {"capella", "-q", "--version"}} {
		var out, errW strings.Builder
		code := cli.Run(t.Context(), args, cli.Deps{Version: "1.2.3", Stdout: &out, Stderr: &errW})
		if code != 0 || out.String() != want || errW.Len() != 0 {
			t.Fatalf("%v: exit %d stdout %q stderr %q", args, code, out.String(), errW.String())
		}
	}
	var out, errW strings.Builder
	if code := cli.Run(t.Context(), []string{"capella", "cluster", "--version"}, cli.Deps{Version: "1.2.3", Stdout: &out, Stderr: &errW}); code != 3 {
		t.Fatalf("capella cluster --version: exit %d, want 3 (stdout %q)", code, out.String())
	}
}
func capellaTree(t *testing.T) []*cobra.Command {
	t.Helper()
	root := cli.NewRootCmd(cli.Deps{})
	capella, _, err := root.Find([]string{"capella"})
	if err != nil {
		t.Fatal(err)
	}
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		out = append(out, c)
		for _, sub := range c.Commands() {
			if !sub.Hidden && sub.Name() != "help" && sub.IsAvailableCommand() {
				walk(sub)
			}
		}
	}
	walk(capella)
	return out
}

// TestCLI_CAP001_AC2_EveryGroupHasExamples: every visible capella command with subcommands
// shows an Examples section.
func TestCLI_CAP001_AC2_EveryGroupHasExamples(t *testing.T) {
	s := newScenario(t)
	for _, c := range capellaTree(t) {
		if !c.HasAvailableSubCommands() {
			continue
		}
		path := strings.Fields(c.CommandPath())[1:]
		r := mustScenario(t, s.Run(append(path, "--help")...))
		if !strings.Contains(r.Stdout, "\nExamples:\n  cbctl ") {
			t.Errorf("%s --help has no examples", c.CommandPath())
		}
	}
}

// TestCLI_CAP001_AC2_RootListsAllGroups: capella --help lists every visible command group.
func TestCLI_CAP001_AC2_RootListsAllGroups(t *testing.T) {
	r := mustScenario(t, newScenario(t).Run("capella", "--help"))
	for _, name := range []string{"apply", "bucket", "cidr", "cluster", "collection", "configure", "credential",
		"destroy", "doctor", "drift", "guide", "init", "plan", "project", "scope", "sync", "validate"} {
		if !regexp.MustCompile(`(?m)^  ` + name + ` +\S`).MatchString(r.Stdout) {
			t.Errorf("capella --help does not list %s", name)
		}
	}
}
