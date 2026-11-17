package cli

import (
	"strings"
	"testing"
)

func wantFile(t *testing.T, path string, present []string, absent ...string) {
	t.Helper()
	text := string(mustRead(t, path))
	for _, p := range present {
		if !strings.Contains(text, p) {
			t.Errorf("capella.yaml lacks %q:\n%s", p, text)
		}
	}
	for _, a := range absent {
		if strings.Contains(text, a) {
			t.Errorf("capella.yaml still has %q:\n%s", a, text)
		}
	}
}

func TestManagedDataModelCommands(t *testing.T) {
	resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	mustRun(t, "apply", "--yes")

	mustRun(t, "bucket", "add", "envclu", "orders", "--memory", "256", "--replicas", "1", "--yes")
	wantFile(t, path, []string{"name: orders", "memoryAllocationInMb: 256", "replicas: 1"})

	wantContains(t, mustRun(t, "bucket", "add", "envclu", "orders", "--yes").Stdout, "already exists")

	mustRun(t, "bucket", "update", "envclu", "orders", "--memory", "512", "--yes")
	wantFile(t, path, []string{"memoryAllocationInMb: 512"}, "memoryAllocationInMb: 256")
	wantExit(t, runResource(t, "bucket", "update", "envclu", "orders", "--yes"), ExitConfigInvalid, "nothing to update")
	wantExit(t, runResource(t, "bucket", "update", "envclu", "ghost", "--memory", "512", "--yes"), ExitConfigInvalid, "bucket ghost is not in capella.yaml")

	mustRun(t, "scope", "add", "envclu", "orders", "eu", "--yes")
	wantFile(t, path, []string{"scopes:", "name: eu"})
	mustRun(t, "collection", "add", "envclu", "orders", "eu", "sales", "--max-ttl", "60", "--yes")
	wantFile(t, path, []string{"name: sales", "maxTTL: 60"})
	mustRun(t, "collection", "update", "envclu", "orders", "eu", "sales", "--max-ttl", "120", "--yes")
	wantFile(t, path, []string{"maxTTL: 120"}, "maxTTL: 60")
	wantExit(t, runResource(t, "collection", "update", "envclu", "orders", "eu", "ghost", "--max-ttl", "1", "--yes"), ExitConfigInvalid, "not in capella.yaml")

	mustRun(t, "collection", "delete", "envclu", "orders", "eu", "sales", "--yes")
	wantFile(t, path, nil, "name: sales")
	mustRun(t, "scope", "delete", "envclu", "orders", "eu", "--yes")
	wantFile(t, path, nil, "name: eu")
	mustRun(t, "bucket", "delete", "envclu", "orders", "--yes")
	wantFile(t, path, nil, "name: orders")
}

func TestManagedCIDRAndCredential(t *testing.T) {
	resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	t.Setenv("APP_PW", "Str0ng!Passw0rd#1")
	mustRun(t, "apply", "--yes")

	mustRun(t, "cidr", "create", "envclu", "203.0.113.10", "--comment", "office", "--yes")
	wantFile(t, path, []string{"cidr: 203.0.113.10/32", "comment: office"})
	wantContains(t, mustRun(t, "cidr", "create", "envclu", "203.0.113.10/32", "--yes").Stdout, "already allowed")
	wantExit(t, runResource(t, "cidr", "create", "envclu", "0.0.0.0/0", "--yes"), ExitConfigInvalid, "every IP")

	mustRun(t, "bucket", "add", "envclu", "orders", "--memory", "256", "--yes")
	mustRun(t, "credential", "create", "envclu", "appuser", "--privileges", "read+write", "--bucket", "orders",
		"--password-env", "APP_PW", "--yes")
	wantFile(t, path, []string{"name: appuser", "${APP_PW}", "data_reader", "data_writer"}, "Str0ng!Passw0rd#1")
	wantExit(t, runResource(t, "credential", "update", "envclu", "appuser", "--yes"), ExitConfigInvalid, "nothing to update")
	mustRun(t, "credential", "update", "envclu", "appuser", "--privileges", "read", "--bucket", "orders", "--yes")
	wantFile(t, path, []string{"data_reader"}, "data_writer")

	mustRun(t, "credential", "delete", "envclu", "appuser", "--yes")
	wantFile(t, path, nil, "name: appuser")
	mustRun(t, "cidr", "delete", "envclu", "203.0.113.10", "--yes")
	wantFile(t, path, nil, "203.0.113.10")
}

func TestManagedSchedulesAndBackups(t *testing.T) {
	resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")

	mustRun(t, "cluster", "schedule", "set", "envclu", "--timezone", "US/Pacific", "--weekdays", "08:00-18:00", "--weekend", "off", "--no-apply", "--yes")
	wantFile(t, path, []string{"hibernation:", "timezone: US/Pacific", "weekdays: 08:00-18:00", "weekend: \"off\""})
	wantExit(t, runResource(t, "cluster", "schedule", "set", "envclu", "--friday", "9", "--no-apply", "--yes"), ExitConfigInvalid, "friday")
	mustRun(t, "cluster", "schedule", "pause", "envclu", "--no-apply", "--yes")
	wantFile(t, path, []string{"paused: true"})
	mustRun(t, "cluster", "schedule", "resume", "envclu", "--no-apply", "--yes")
	wantFile(t, path, nil, "paused")
	mustRun(t, "cluster", "schedule", "delete", "envclu", "--no-apply", "--yes")
	wantFile(t, path, []string{"ensure: absent"})

	mustRun(t, "cluster", "backup", "schedule", "set", "envclu", "--every", "24", "--retain", "30d", "--start", "02:00", "--copy-to", "us-west-2", "--no-apply", "--yes")
	wantFile(t, path, []string{"backupSchedule:", "interval: 24", "retention: 720", "startTime: \"02:00\"", "us-west-2"})
	mustRun(t, "cluster", "backup", "schedule", "set", "envclu", "--no-copies", "--no-apply", "--yes")
	wantFile(t, path, nil, "us-west-2")
	mustRun(t, "cluster", "backup", "schedule", "delete", "envclu", "--no-apply", "--yes")
	wantFile(t, path, []string{"ensure: absent"})

	mustRun(t, "bucket", "add", "envclu", "orders", "--memory", "256", "--no-apply", "--yes")
	mustRun(t, "bucket", "backup", "schedule", "set", "envclu", "orders", "--day", "monday", "--retention", "90days", "--no-apply", "--yes")
	wantFile(t, path, []string{"dayOfWeek: monday", "retentionTime: 90days"})
	mustRun(t, "bucket", "backup", "schedule", "delete", "envclu", "orders", "--no-apply", "--yes")
	wantFile(t, path, []string{"ensure: absent"})
}

func TestManagedClusterCommands(t *testing.T) {
	resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")

	mustRun(t, "cluster", "update", "envclu", "--description", "main", "--support-plan", "enterprise", "--cpu", "8", "--ram", "32", "--no-apply", "--yes")
	wantFile(t, path, []string{"description: main", "plan: enterprise", "cpu: 8", "ram: 32"})
	wantExit(t, runResource(t, "cluster", "update", "envclu", "--no-apply", "--yes"), ExitConfigInvalid, "nothing to update")

	mustRun(t, "cluster", "group", "add", "envclu", "--services", "search", "--nodes", "2", "--no-apply", "--yes")
	wantFile(t, path, []string{"services: [search]", "numOfNodes: 2"})
	mustRun(t, "cluster", "group", "remove", "envclu", "search", "--no-apply", "--yes")
	wantFile(t, path, nil, "search")

	mustRun(t, "cluster", "create", "--name", "fresh", "--size", "dev", "--region", "us-west-2", "--no-apply", "--yes")
	wantFile(t, path, []string{"name: fresh", "configurationType: singleNode", "region: us-west-2", "cidr: 10.0.1.0/24"})
	// An entry that was never deployed is replaced on a rerun (capellactl); a deployed one is not.
	res := mustRun(t, "cluster", "create", "--name", "fresh", "--size", "dev", "--region", "us-east-2", "--no-apply", "--yes")
	wantContains(t, res.Stderr, "fresh is in capella.yaml but was never deployed to Capella; replacing it")
	wantFile(t, path, []string{"region: us-east-2", "cidr: 10.0.1.0/24"}, "region: us-west-2")
	wantExit(t, runResource(t, "cluster", "create", "--name", "x", "--group", "data:oops", "--no-apply", "--yes"), ExitConfigInvalid, "--group")
	mustRun(t, "cluster", "create", "--name", "grouped", "--group", "data:3x4/16", "--group", "search:2x4/16", "--no-apply", "--yes")
	wantFile(t, path, []string{"name: grouped", "services: [search]"})

	mustRun(t, "cluster", "clone", "envclu", "envclu-eu", "--region", "eu-west-1", "--no-apply", "--yes")
	wantFile(t, path, []string{"name: envclu-eu", "region: eu-west-1", "Clone of envclu"})

	mustRun(t, "cluster", "delete", "fresh", "--no-apply", "--yes")
	wantFile(t, path, nil, "name: fresh")
}

func TestManagedDeleteDeclinedKeepsFile(t *testing.T) {
	resEnv(t)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	mustRun(t, "apply", "--yes")
	mustRun(t, "bucket", "add", "envclu", "orders", "--memory", "256", "--yes")
	before := string(mustRead(t, path))

	old := ttyCheck
	ttyCheck = func(any) bool { return true }
	t.Cleanup(func() { ttyCheck = old })
	res := runResourceIn(t, "n\n", "bucket", "delete", "envclu", "orders")
	if res.Err != nil {
		t.Fatalf("declining must not fail: %v", res.Err)
	}
	wantContains(t, res.Stdout+res.Stderr, "cancelled")
	runResourceIn(t, "n\n", "project", "delete", "envproj")
	if string(mustRead(t, path)) != before {
		t.Error("a declined delete changed the file")
	}
}
