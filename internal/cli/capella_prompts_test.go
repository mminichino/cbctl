package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPrompts(t *testing.T) {
	t.Run("clone wizard picks source, region, name and project", func(t *testing.T) {
		f := resEnv(t)
		noConfigDir(t)
		pid, _ := seedCluster(f, "alpha", "c1")
		fakeTTY(t)
		// source #1, keep the project, region, accept the default name, confirm nothing else.
		res := runResourceIn(t, "1\n\nus-west-2\n\n", "cluster", "clone", "--no-wait")
		if res.Err != nil {
			t.Fatalf("clone wizard: %v\n%s\n%s", res.Err, res.Stdout, res.Stderr)
		}
		wantContains(t, res.Stderr, "Source cluster", "Region for the clone", "Name of the clone [c1-us-west-2]")
		clusterNamed(t, f, pid, "c1-us-west-2")
	})
	t.Run("clone asks only what is missing", func(t *testing.T) {
		f := resEnv(t)
		noConfigDir(t)
		pid, _ := seedCluster(f, "alpha", "c1")
		fakeTTY(t)
		res := runResourceIn(t, "copy-01\n", "cluster", "clone", "c1", "--region", "us-west-2", "--no-wait")
		if res.Err != nil {
			t.Fatalf("clone: %v\n%s", res.Err, res.Stderr)
		}
		if strings.Contains(res.Stderr, "Source cluster") || strings.Contains(res.Stderr, "Region for") {
			t.Errorf("asked for a value that was given:\n%s", res.Stderr)
		}
		clusterNamed(t, f, pid, "copy-01")
	})
	t.Run("clone wizard with no clusters", func(t *testing.T) {
		f := resEnv(t)
		noConfigDir(t)
		f.SeedProject("empty")
		fakeTTY(t)
		wantExit(t, runResourceIn(t, "", "cluster", "clone"), ExitError, "no clusters to clone")
	})
	t.Run("clone wizard EOF cancels", func(t *testing.T) {
		f := resEnv(t)
		noConfigDir(t)
		seedCluster(f, "alpha", "c1")
		fakeTTY(t)
		f.ResetLog()
		wantExit(t, runResourceIn(t, "1\n", "cluster", "clone"), ExitError, "Cancelled")
		noWrites(t, f)
	})
	t.Run("project create asks for a description", func(t *testing.T) {
		f := resEnv(t)
		noConfigDir(t)
		fakeTTY(t)
		res := runResourceIn(t, "Team sandbox\n", "project", "create", "p1")
		if res.Err != nil {
			t.Fatalf("project create: %v\n%s", res.Err, res.Stderr)
		}
		wantContains(t, res.Stderr, "Description (optional)")
		var body string
		for _, r := range f.RequestLog() {
			if r.Method == "POST" && strings.HasSuffix(r.Path, "/projects") {
				body = r.Body
			}
		}
		if !strings.Contains(body, "Team sandbox") {
			t.Errorf("POST body = %s", body)
		}
	})
	t.Run("project create without a terminal does not ask", func(t *testing.T) {
		resEnv(t)
		noConfigDir(t)
		res := runResource(t, "project", "create", "p2")
		if res.Err != nil || strings.Contains(res.Stderr, "Description") {
			t.Fatalf("err=%v stderr=%s", res.Err, res.Stderr)
		}
	})
	t.Run("group add asks for services", func(t *testing.T) {
		f := resEnv(t)
		noConfigDir(t)
		seedCluster(f, "alpha", "c1")
		fakeTTY(t)
		res := runResourceIn(t, "search\n", "cluster", "group", "add", "c1", "--nodes", "2", "--no-wait")
		if res.Err != nil {
			t.Fatalf("group add: %v\n%s", res.Err, res.Stderr)
		}
		wantContains(t, res.Stderr, "Services of the new group")
	})
}

func TestCapellaCompletionPointsToRoot(t *testing.T) {
	root := NewRootCmd(DefaultDeps())
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"capella", "completion", "bash"})
	err := root.Execute()
	if code := exitCodeOf(t, err); code != ExitConfigInvalid {
		t.Fatalf("exit %d, want 3", code)
	}
	var ce *CLIError
	if !errors.As(err, &ce) || !strings.Contains(ce.Action, "cbctl completion bash") {
		t.Errorf("hint missing: %v", err)
	}
}

func TestBucketAddAsksForMemory(t *testing.T) {
	f := resEnv(t)
	noConfigDir(t)
	seedCluster(f, "alpha", "c1")
	fakeTTY(t)
	res := runResourceIn(t, "x\n2048\n", "bucket", "add", "c1", "orders")
	if res.Err != nil {
		t.Fatalf("bucket add: %v\n%s", res.Err, res.Stderr)
	}
	wantContains(t, res.Stderr, "Memory quota in MB [1024]", "give a whole number of MB")
	var body string
	for _, r := range f.RequestLog() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/buckets") {
			body = r.Body
		}
	}
	if !strings.Contains(body, `"memoryAllocationInMb":2048`) {
		t.Errorf("POST body = %s", body)
	}
}
