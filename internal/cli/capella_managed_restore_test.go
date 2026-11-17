package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
)

// managedFixture applies envDoc so envproj/envclu exist, then returns the fake, the file path
// and the file's text at that point.
func managedFixture(t *testing.T) (*capellafake.Fake, string, string) {
	t.Helper()
	f := resEnv(t)
	f.SettlePolls(0)
	path := envDir(t, envDoc)
	t.Setenv("CBCTL_CONFIG", "")
	mustRun(t, "apply", "--yes")
	return f, path, string(mustRead(t, path))
}

func wantUnchanged(t *testing.T, path, before string) {
	t.Helper()
	if got := string(mustRead(t, path)); got != before {
		t.Errorf("capella.yaml changed:\n--- before\n%s\n--- after\n%s", before, got)
	}
}

// TestManaged_CAP038_AC8_RejectedCreateRestoresFile: when Capella refuses the write a managed
// command made, the file goes back to what it was, so it never describes what was never made.
func TestManaged_CAP038_AC8_RejectedCreateRestoresFile(t *testing.T) {
	cases := []struct {
		name  string
		route string
		args  []string
	}{
		{"cluster create 422", `/clusters$`, []string{"cluster", "create", "--name", "fresh", "--size", "dev", "--region", "us-west-2"}},
		{"cluster create 500", `/clusters$`, []string{"cluster", "create", "--name", "fresh", "--size", "dev", "--region", "us-west-2"}},
		{"cluster clone 422", `/clusters$`, []string{"cluster", "clone", "envclu", "envclu-eu", "--region", "eu-west-1"}},
		{"bucket add 422", `/buckets$`, []string{"bucket", "add", "envclu", "orders", "--memory", "256"}},
		{"scope add 422", `/scopes$`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, path, before := managedFixture(t)
			args := tc.args
			if args == nil {
				mustRun(t, "bucket", "add", "envclu", "orders", "--yes")
				before = string(mustRead(t, path))
				args = []string{"scope", "add", "envclu", "orders", "eu"}
			}
			status := 422
			if strings.HasSuffix(tc.name, "500") {
				status = 500
			}
			f.FailNext("POST", tc.route, status, 1)
			res := runResource(t, append(args, "--yes")...)
			if res.Err == nil {
				t.Fatalf("command succeeded with a rejected write\n%s", res.Stdout)
			}
			out := res.Stdout + res.Stderr + res.Err.Error()
			wantContains(t, out, "Capella did not accept the change, so capella.yaml was restored")
			if strings.Contains(out, "re-run apply") {
				t.Errorf("restored file still told to re-run apply:\n%s", out)
			}
			wantUnchanged(t, path, before)

			// The same command works once Capella accepts it.
			mustRun(t, append(args, "--yes")...)
			if string(mustRead(t, path)) == before {
				t.Error("successful rerun did not record the change")
			}
		})
	}
}

// TestManaged_CAP038_AC8_AcceptedCreateKeepsFile: when the write was accepted and only the wait
// fails, the resource exists in Capella, so the file keeps it.
func TestManaged_CAP038_AC8_AcceptedCreateKeepsFile(t *testing.T) {
	f, path, _ := managedFixture(t)
	f.SettlePolls(3)
	f.FailNext("GET", `/clusters/[^/]+$`, 500, 50)
	res := runResource(t, "cluster", "create", "--name", "fresh", "--size", "dev", "--region", "us-west-2", "--yes")
	if res.Err == nil {
		t.Fatal("create succeeded although every status read failed")
	}
	if strings.Contains(res.Stdout+res.Stderr+res.Err.Error(), "was restored") {
		t.Errorf("file restored after an accepted write:\n%s%s", res.Stdout, res.Stderr)
	}
	wantFile(t, path, []string{"name: fresh"})
}

// TestManaged_CAP038_AC8_DroppedConnectionKeepsFile: a write that may have landed (connection
// lost after the request was sent) is never undone in the file.
func TestManaged_CAP038_AC8_DroppedConnectionKeepsFile(t *testing.T) {
	for _, o := range []capellafake.FaultOptions{{DropAfterRead: true}, {ApplyThenDrop: true}} {
		name := "drop after read"
		if o.ApplyThenDrop {
			name = "apply then drop"
		}
		t.Run(name, func(t *testing.T) {
			f, path, _ := managedFixture(t)
			f.FailNextOpts("POST", "/v4/organizations/*/projects/*/clusters/*/buckets", o)
			res := runResource(t, "bucket", "add", "envclu", "orders", "--memory", "256", "--yes")
			if res.Err == nil {
				// A retried idempotent create may still succeed; the file must then have the bucket.
				wantFile(t, path, []string{"name: orders"})
				return
			}
			if strings.Contains(res.Stdout+res.Stderr+res.Err.Error(), "was restored") {
				t.Errorf("file restored after an ambiguous write:\n%s%s\n%v", res.Stdout, res.Stderr, res.Err)
			}
			wantFile(t, path, []string{"name: orders"})
		})
	}
}

// TestManaged_CAP038_AC8_NoApplyNeverRestores: --no-apply writes the file and sends nothing.
func TestManaged_CAP038_AC8_NoApplyNeverRestores(t *testing.T) {
	f, path, _ := managedFixture(t)
	f.ResetLog()
	mustRun(t, "bucket", "add", "envclu", "orders", "--no-apply", "--yes")
	wantFile(t, path, []string{"name: orders"})
	noWrites(t, f)
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("no backup kept: %v", err)
	}
}
