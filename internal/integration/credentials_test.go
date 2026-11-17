//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/couchbase/gocb/v2"

	"github.com/mminichino/cbctl/internal/capella"
	"github.com/mminichino/cbctl/internal/integration"
)

const itCredPassword = "Cbctl!It-Passw0rd"

// TestCredentials_CAP027_AC8: create a credential with a supplied password, check that a re-plan
// has nothing to change (the password is never re-sent), read the connection string, then connect
// with the credential through the SDK. The fake has no data service, so on the fake (and on a
// sandbox without CAPELLA_SANDBOX_CLIENT_CIDR allowed) the SDK step is logged and not run.
func TestCredentials_CAP027_AC8(t *testing.T) {
	tg := integration.TargetFromEnv(t)
	tg.RequireClusters(t)
	if err := integration.RecordSuppliedSecret(tg, "it-cred", itCredPassword); err != nil {
		t.Fatal(err)
	}

	shared := integration.SharedProject(t, tg)
	cfg := filepath.Join(t.TempDir(), "capella.yaml")
	shared.ImportInto(t, tg, cfg, true)
	opts := []integration.CLIOption{
		integration.WithConfig(cfg),
		integration.WithEnv("IT_CRED_PASSWORD", itCredPassword),
	}
	name := "cbctl_it_app"
	t.Cleanup(func() {
		if r := tg.CLIOpts(t, opts, "credential", "delete", shared.Cluster, name); r.Code != 0 {
			t.Errorf("credential delete: exit %d\n%s", r.Code, r.Output)
		}
	})
	tg.MustCLI(t, opts, "credential", "create", shared.Cluster, name,
		"--privileges", "read+write", "--bucket", shared.Bucket, "--password-env", "IT_CRED_PASSWORD")

	r := tg.CLIOpts(t, append(opts, integration.NoApprove()), "plan", "--detailed-exitcode")
	if r.Code != 0 {
		t.Fatalf("plan after credential create: exit %d, want 0\n%s", r.Code, r.Output)
	}
	if tg.Fake != nil {
		posts := tg.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters/*/users")
		if len(posts) != 1 {
			t.Fatalf("credential creates: %d, want 1", len(posts))
		}
		body, _ := posts[0].Body.(map[string]any)
		if body["name"] != name || body["password"] != itCredPassword {
			t.Fatalf("credential create body: %v", posts[0])
		}
	}

	r = tg.MustCLI(t, []integration.CLIOption{integration.WithConfig(cfg), integration.NoApprove(), integration.Debug(false)},
		"-o", "json", "cluster", "get", shared.Cluster)
	var got struct {
		ConnectionString string `json:"connectionString"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil || got.ConnectionString == "" {
		t.Fatalf("cluster get -o json: no connectionString (%v)\n%s", err, r.Output)
	}

	if tg.Kind == "fake" {
		t.Log("SDK connect not run: the fake has no data service to connect to (a real cluster is needed)")
		return
	}
	if os.Getenv("CAPELLA_SANDBOX_CLIENT_CIDR") == "" {
		t.Log("SDK connect not run: needs CAPELLA_SANDBOX_CLIENT_CIDR (this machine's address, allowed on the shared cluster)")
		return
	}
	cluster, cleanup, err := capella.ConnectCluster(got.ConnectionString, name, itCredPassword, "", 10, 30, 30)
	if err != nil {
		t.Fatalf("SDK connect with %s: %v", name, err)
	}
	defer cleanup()
	defer capella.DisconnectCluster(cluster)
	if _, err := cluster.Bucket(shared.Bucket).DefaultCollection().Exists("cbctl-it-probe", &gocb.ExistsOptions{}); err != nil {
		t.Fatalf("SDK KV exists with %s: %v", name, err)
	}
}
