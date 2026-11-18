package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/manifest/serverapply"
	"github.com/mminichino/cbctl/internal/rest"
	"github.com/mminichino/cbctl/internal/server"
)

// applyFakeProv is a Provisioner for a cluster that starts empty.
type applyFakeProv struct {
	initialized bool
	joined      map[string]bool
	failJoin    string
}

func (f *applyFakeProv) BootstrapPrimary(rest.ProvisionOptions) (bool, error) {
	created := !f.initialized
	f.initialized = true
	return created, nil
}

func (f *applyFakeProv) JoinNode(o rest.ProvisionOptions) (bool, error) {
	if o.IPAddress == f.failJoin {
		return false, errors.New("management API not reachable")
	}
	if f.joined == nil {
		f.joined = map[string]bool{}
	}
	_, had := f.joined[o.IPAddress]
	if !had {
		f.joined[o.IPAddress] = true
	}
	return !had, nil
}

func (f *applyFakeProv) ClusterNodes(rest.Endpoint, string, string) ([]rest.NodeInfo, error) {
	nodes := []rest.NodeInfo{{Host: "10.0.0.1"}}
	for ip := range f.joined {
		nodes = append(nodes, rest.NodeInfo{Host: ip})
	}
	return nodes, nil
}

func (f *applyFakeProv) PendingNodes(rest.Endpoint, string, string) ([]string, error) {
	var p []string
	for ip, pending := range f.joined {
		if pending {
			p = append(p, ip)
		}
	}
	return p, nil
}

func (f *applyFakeProv) RebalanceCluster(rest.ProvisionOptions) error {
	for ip := range f.joined {
		f.joined[ip] = false
	}
	return nil
}

// applyFakeData is an in-memory DataModel.
type applyFakeData struct{ buckets, scopes, colls map[string]int }

func newApplyFakeData() *applyFakeData {
	return &applyFakeData{buckets: map[string]int{}, scopes: map[string]int{}, colls: map[string]int{}}
}

func (f *applyFakeData) Connect(context.Context, *config.Config) error { return nil }
func (f *applyFakeData) Disconnect()                                   {}
func (f *applyFakeData) BucketQuotaMiB(n string) (int, bool, error) {
	q, ok := f.buckets[n]
	return q, ok, nil
}
func (f *applyFakeData) CreateBucket(n string, q, _ int) error { f.buckets[n] = q; return nil }
func (f *applyFakeData) ScopeExists(b, s string) (bool, error) {
	_, ok := f.scopes[b+"."+s]
	return ok, nil
}
func (f *applyFakeData) CreateScope(b, s string) error { f.scopes[b+"."+s] = 0; return nil }
func (f *applyFakeData) CollectionExists(b, s, c string) (bool, error) {
	_, ok := f.colls[b+"."+s+"."+c]
	return ok, nil
}
func (f *applyFakeData) CreateCollection(b, s, c string) error {
	f.colls[b+"."+s+"."+c] = 0
	return nil
}
func (f *applyFakeData) CollectionIsEmpty(b, s, c string) (bool, error) {
	return f.colls[b+"."+s+"."+c] == 0, nil
}
func (f *applyFakeData) ImportDocuments(_ context.Context, src server.DocSource, b, s, c, _ string) (int, error) {
	n := 0
	for {
		if _, _, err := src.Next(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return n, err
		}
		n++
	}
	f.colls[b+"."+s+"."+c] += n
	return n, nil
}

// useApplyFakes points the Server apply at in-memory fakes with a frozen clock.
func useApplyFakes(t *testing.T, prov *applyFakeProv, data *applyFakeData) {
	t.Helper()
	prev := serverApplyEnv
	serverApplyEnv = func(env *applyEnv) serverapply.Env {
		return serverapply.Env{
			Provisioner:  prov,
			NewDataModel: func() serverapply.DataModel { return data },
			Now:          func() time.Time { return time.Time{} },
			BaseDir:      env.Set.BaseDir,
			DryRun:       env.DryRun,
		}
	}
	t.Cleanup(func() { serverApplyEnv = prev })
}

const goldenServerYAML = `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: demo
spec:
  credentials:
    password: ${APPLY_GOLDEN_PW}
  nodes:
    - ipAddress: 10.0.0.1
    - ipAddress: 10.0.0.2
  buckets:
    - name: orders
      quotaMiB: 256
      scopes:
        - name: sales
          collections: [items]
      import:
        - scope: sales
          collection: items
          keyField: id
          data:
            - id: 1
            - id: 2
`

// TestApplyServerOutputGolden records the text output of cbctl apply for a Server manifest in
// the four terminal situations, for a first run, a repeat, a dry run and a failed join.
func TestApplyServerOutputGolden(t *testing.T) {
	env := map[string]string{"APPLY_GOLDEN_PW": "golden-pw-1"}
	cases := []struct {
		name  string
		args  []string
		setup func() (*applyFakeProv, *applyFakeData)
		code  int
	}{
		{"apply-server-fresh", []string{"apply", "-f", "-"}, func() (*applyFakeProv, *applyFakeData) {
			return &applyFakeProv{}, newApplyFakeData()
		}, 0},
		{"apply-server-repeat", []string{"apply", "-f", "-"}, func() (*applyFakeProv, *applyFakeData) {
			d := newApplyFakeData()
			d.buckets["orders"] = 256
			d.scopes["orders.sales"] = 0
			d.colls["orders.sales.items"] = 2
			return &applyFakeProv{initialized: true, joined: map[string]bool{"10.0.0.2": false}}, d
		}, 0},
		{"apply-server-dry-run", []string{"apply", "-f", "-", "--dry-run"}, func() (*applyFakeProv, *applyFakeData) {
			return &applyFakeProv{}, newApplyFakeData()
		}, 0},
		{"apply-server-join-failed", []string{"apply", "-f", "-"}, func() (*applyFakeProv, *applyFakeData) {
			return &applyFakeProv{failJoin: "10.0.0.2"}, newApplyFakeData()
		}, 1},
	}
	for _, c := range cases {
		for _, e := range outputEnvs {
			t.Run(c.name+"/"+e.name, func(t *testing.T) {
				p, d := c.setup()
				useApplyFakes(t, p, d)
				oc := outcase{env: mergeEnv(e.env, env), outTTY: e.tty, errTTY: e.tty, stdin: goldenServerYAML,
					args: append(append([]string{}, e.flags...), c.args...)}
				stdout, stderr, code := oc.run(t)
				if code != c.code {
					t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, c.code, stdout, stderr)
				}
				checkGolden(t, filepath.Join("testdata", "output", c.name+"."+e.name+".golden"), renderGolden(stdout, stderr, code))
				if strings.Contains(stdout+stderr, "golden-pw-1") {
					t.Errorf("output leaks the password")
				}
				if !e.colors && strings.Contains(stdout+stderr, "\x1b[") {
					t.Errorf("escape code without color")
				}
			})
		}
	}
}

func mergeEnv(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

// checkGolden compares got with the golden file at path; -update-output rewrites it.
func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updateOutputGoldens {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-output to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
