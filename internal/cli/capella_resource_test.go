package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// resEnv starts a fake Capella and points the CLI at it.
func resEnv(t *testing.T) *capellafake.Fake {
	t.Helper()
	f := capellafake.New(t)
	capellaEnv(t, f)
	return f
}

// resResult is what one command run produced.
type resResult struct {
	Stdout, Stderr string
	Err            error
}

// runResource runs "cbctl capella <args>" in-process on a fresh command tree, so flag values never
// leak from one run into the next. Confirmation prompts have no terminal unless a test fakes one.
func runResource(t *testing.T, args ...string) resResult {
	t.Helper()
	return runResourceIn(t, "", args...)
}

func runResourceIn(t *testing.T, stdin string, args ...string) resResult {
	t.Helper()
	root := &cobra.Command{Use: "cbctl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newCapellaCmd())
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(stdin))
	logging.SetOutputs(&stdout, &stderr)
	root.SetArgs(append([]string{"capella"}, args...))
	err := root.ExecuteContext(context.Background())
	return resResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

// mustRun runs the command and fails the test when it returns an error.
func mustRun(t *testing.T, args ...string) resResult {
	t.Helper()
	res := runResource(t, args...)
	if res.Err != nil {
		t.Fatalf("capella %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), res.Err, res.Stdout, res.Stderr)
	}
	return res
}

// wantExit asserts that the run failed with a CLIError carrying code and returns it.
func wantExit(t *testing.T, res resResult, code ExitCode, msgPart string) *CLIError {
	t.Helper()
	var ce *CLIError
	if !errors.As(res.Err, &ce) {
		t.Fatalf("err = %v, want a *CLIError with exit code %d\nstdout: %s\nstderr: %s", res.Err, code, res.Stdout, res.Stderr)
	}
	if ce.Code != code || !strings.Contains(ce.Message, msgPart) {
		t.Fatalf("CLIError = {%d %q}, want exit %d containing %q", ce.Code, ce.Message, code, msgPart)
	}
	return ce
}

// jsonObject decodes stdout as exactly one JSON object.
func jsonObject(t *testing.T, res resResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &m); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, res.Stdout)
	}
	return m
}

// jsonArray decodes stdout as exactly one JSON array.
func jsonArray(t *testing.T, res resResult) []any {
	t.Helper()
	var a []any
	if err := json.Unmarshal([]byte(res.Stdout), &a); err != nil {
		t.Fatalf("stdout is not one JSON array: %v\n%s", err, res.Stdout)
	}
	return a
}

// wantContains fails unless s contains every part.
func wantContains(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Errorf("output does not contain %q:\n%s", p, s)
		}
	}
}

// resFixture is a project with one healthy cluster.
type resFixture struct {
	Fake        *capellafake.Fake
	Client      *capellaapi.Client
	ProjectName string
	ProjectID   string
	ClusterName string
	ClusterID   string
}

// newFixture seeds project "proj" with healthy cluster "clu".
func newFixture(t *testing.T) *resFixture {
	t.Helper()
	f := resEnv(t)
	fx := &resFixture{Fake: f, Client: f.Client(), ProjectName: "proj", ClusterName: "clu"}
	fx.ProjectID = f.SeedProject(fx.ProjectName)
	fx.ClusterID = f.SeedCluster(fx.ProjectID, fx.ClusterName, "healthy")
	return fx
}

// addBucket creates a bucket straight through the API.
func (fx *resFixture) addBucket(t *testing.T, name string) {
	t.Helper()
	_, err := fx.Client.CreateBucket(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.CreateBucketRequest{
		Name: name, Type: "couchbase", StorageBackend: "couchstore", MemoryAllocationInMb: 100,
		BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 1,
	})
	if err != nil {
		t.Fatalf("seeding bucket %s: %v", name, err)
	}
}

// fakeTTY makes confirm() believe a terminal is attached for the duration of the test.
func fakeTTY(t *testing.T) {
	t.Helper()
	old := ttyCheck
	ttyCheck = func(any) bool { return true }
	t.Cleanup(func() { ttyCheck = old })
}

// setClock replaces the clock the runtime uses (capellaEnv installs one at the zero time).
func setClock(t *testing.T, now time.Time) *engine.FakeClock {
	t.Helper()
	clk := engine.NewFakeClock(now)
	old := clockFactory
	clockFactory = func() engine.Clock { return clk }
	t.Cleanup(func() { clockFactory = old })
	return clk
}
