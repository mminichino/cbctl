package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// ciWriteCommands is every command that changes Capella or capella.yaml, with arguments that
// change something in ciEnvironment (parity with capellactl's WRITE_COMMANDS). The file-managed
// variants run because ciEnvironment has a capella.yaml.
var ciWriteCommands = [][]string{
	{"project", "create", "nova-qa"},
	{"project", "update", "nova-dev", "--description", "CI"},
	{"cluster", "create", "--name", "nova-dev-02", "--size", "dev", "--region", "us-west-2"},
	{"cluster", "update", "nova-dev-01", "--description", "CI"},
	{"cluster", "scale", "nova-dev-01", "--nodes", "4"},
	{"cluster", "power", "nova-dev-01", "--off"},
	{"cluster", "schedule", "set", "nova-dev-01", "--timezone", "US/Pacific", "--weekdays", "08:00-18:00"},
	{"bucket", "add", "nova-dev-01", "events", "--memory", "256"},
	{"bucket", "update", "nova-dev-01", "orders", "--memory", "512"},
	{"bucket", "delete", "nova-dev-01", "orders"},
	{"scope", "add", "nova-dev-01", "orders", "billing"},
	{"scope", "delete", "nova-dev-01", "orders", "inventory"},
	{"collection", "add", "nova-dev-01", "orders", "inventory", "suppliers"},
	{"collection", "update", "nova-dev-01", "orders", "inventory", "items", "--max-ttl", "60"},
	{"collection", "delete", "nova-dev-01", "orders", "inventory", "items"},
	{"cidr", "create", "nova-dev-01", "198.51.100.0/24"},
	{"cidr", "delete", "nova-dev-01", "203.0.113.10/32"},
	{"credential", "create", "nova-dev-01", "reports", "--privileges", "read", "--bucket", "orders"},
	{"credential", "delete", "nova-dev-01", "orders_ro"},
	{"cluster", "delete", "nova-dev-01"},
	{"project", "delete", "nova-dev", "--cascade"},
	{"destroy"},
	{"apply", "--prune"},
}

// ciEnvironment is a healthy cluster with a bucket, scope, collection, allowed CIDR and
// credential, synced into capella.yaml, plus a bucket only in Capella for apply --prune.
func ciEnvironment(t *testing.T) *clitest.Scenario {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	pid := s.Fake.SeedProject("nova-dev")
	cid := s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	s.Fake.SeedBucket(cid, "orders")
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"items": 0})
	if _, err := s.Fake.Client().CreateAllowedCIDR(context.Background(), pid, cid, "203.0.113.10/32", "VPN", ""); err != nil {
		t.Fatal(err)
	}
	s.Fake.SeedCredential(cid, "orders_ro", []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources:  capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders"}}},
	}}, false)
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.SeedBucket(cid, "stray")
	s.Fake.ResetCalls()
	return s
}

func ciName(args []string) string {
	n := len(args)
	if n > 3 {
		n = 3
	}
	return strings.Join(args[:n], " ")
}

// TestCI_CAP042_AC3_UniformRefusal: with no terminal and no --yes, every write command refuses
// with the same error and action, writes nothing to Capella, leaves capella.yaml as it was and
// never reads stdin. Under CI the action names the pipeline step.
func TestCI_CAP042_AC3_UniformRefusal(t *testing.T) {
	for _, args := range ciWriteCommands {
		t.Run(ciName(args), func(t *testing.T) {
			s := ciEnvironment(t)
			before := s.ReadFile("capella.yaml")
			argv := append([]string{"capella"}, args...)
			r := s.RunReader(context.Background(), clitest.ForbiddenStdin(t, strings.Join(args, " ")), argv...)
			wantExit(t, r, cli.ExitBlocked)
			wantContains(t, r.Stderr, "confirmation required but no terminal is attached", "re-run with --yes")
			if strings.Contains(r.Stderr, "pipeline step") {
				t.Errorf("pipeline hint outside CI:\n%s", r.Stderr)
			}
			clitest.AssertNoWrites(t, s.Fake)
			if after := s.ReadFile("capella.yaml"); after != before {
				t.Errorf("capella.yaml changed:\n%s", after)
			}

			s.Env["CI"] = "true"
			r = s.RunReader(context.Background(), clitest.ForbiddenStdin(t, strings.Join(args, " ")), argv...)
			wantExit(t, r, cli.ExitBlocked)
			wantContains(t, r.Stderr, "add --yes to this pipeline step")
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}

// TestCI_CAP042_AC1_FlagApprovesEveryWriteCommand: --yes runs each write command to completion
// with no prompt.
func TestCI_CAP042_AC1_FlagApprovesEveryWriteCommand(t *testing.T) {
	for _, args := range ciWriteCommands {
		t.Run(ciName(args), func(t *testing.T) {
			s := ciEnvironment(t)
			argv := append([]string{"capella"}, args...)
			r := s.RunReader(context.Background(), clitest.ForbiddenStdin(t, strings.Join(args, " ")), append(argv, "--yes")...)
			mustScenario(t, r, argv...)
			if len(s.Fake.Writes()) == 0 {
				t.Error("the command changed nothing in Capella")
			}
			if strings.Contains(r.Stdout+r.Stderr, "[y/N]") || strings.Contains(r.Stdout+r.Stderr, "Type the") {
				t.Errorf("prompted despite --yes:\n%s", r.Stderr)
			}
		})
	}
}

// TestCI_CAP042_AC1_FlagAliases: --yes, -y and --auto-approve all approve.
func TestCI_CAP042_AC1_FlagAliases(t *testing.T) {
	for _, flag := range []string{"--yes", "-y", "--auto-approve"} {
		t.Run(flag, func(t *testing.T) {
			s := ciEnvironment(t)
			mustScenario(t, s.Run("capella", flag, "bucket", "delete", "nova-dev-01", "orders"), flag)
			var methods []string
			for _, c := range s.Fake.Writes() {
				methods = append(methods, c.Method)
			}
			if strings.Join(methods, ",") != "DELETE" {
				t.Errorf("writes = %v, want one DELETE", methods)
			}
		})
	}
}

// TestCI_CAP042_AC1_EnvVarIsNotApproval: no environment variable approves a change.
func TestCI_CAP042_AC1_EnvVarIsNotApproval(t *testing.T) {
	s := ciEnvironment(t)
	before := s.ReadFile("capella.yaml")
	for _, k := range []string{"CBCTL_AUTO_APPROVE", "CAPELLACTL_AUTO_APPROVE", "CBCTL_YES", "CI"} {
		s.Env[k] = "true"
	}
	r := s.Run("capella", "bucket", "delete", "nova-dev-01", "orders")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "confirmation required but no terminal is attached")
	clitest.AssertNoWrites(t, s.Fake)
	if s.ReadFile("capella.yaml") != before {
		t.Error("capella.yaml changed")
	}
}

// TestCI_CAP042_AC2_NoCommandReadsStdin: commands that would ask for something, run with no
// terminal, never read stdin; a missing required answer exits 3 naming the flag.
func TestCI_CAP042_AC2_NoCommandReadsStdin(t *testing.T) {
	cases := []struct {
		args []string
		code cli.ExitCode
		msg  string
	}{
		{[]string{"capella", "cluster", "create", "--direct"}, cli.ExitConfigInvalid, "the cluster name is required"},
		{[]string{"capella", "cluster", "clone", "nova-dev-01", "copy", "--direct"}, cli.ExitConfigInvalid, "missing --region: there is no terminal to ask for it"},
		{[]string{"capella", "cluster", "group", "add", "nova-dev-01", "--direct"}, cli.ExitConfigInvalid, "missing --services: there is no terminal to ask for it"},
		{[]string{"capella", "init", "--force"}, cli.ExitConfigInvalid, "missing required options for --non-interactive"},
		{[]string{"configure", "--profile", "ci"}, cli.ExitConfigInvalid, ""},
	}
	for _, tc := range cases {
		t.Run(ciName(tc.args), func(t *testing.T) {
			s := ciEnvironment(t)
			r := s.RunReader(context.Background(), clitest.ForbiddenStdin(t, strings.Join(tc.args, " ")), tc.args...)
			if r.Code == 0 {
				t.Fatalf("succeeded with nothing to answer\n%s", r.Stdout)
			}
			if r.Code != int(tc.code) {
				t.Errorf("exit = %d, want %d\n%s", r.Code, tc.code, r.Stderr)
			}
			if tc.msg != "" {
				wantContains(t, r.Stderr, tc.msg)
			}
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}
