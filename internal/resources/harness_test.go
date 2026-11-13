package resources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/plan"
)

// env is one test world: a fake Capella, a loaded file and a run context on a fake clock.
type env struct {
	t     *testing.T
	f     *capellafake.Fake
	cfg   *capellafile.Config
	rc    *plan.RunContext
	dir   string
	lines []string
	warns []string
}

func newEnv(t *testing.T, yaml string) *env {
	t.Helper()
	e := &env{t: t, f: capellafake.New(t), dir: t.TempDir()}
	e.load(yaml)
	return e
}

func (e *env) load(yaml string) {
	e.t.Helper()
	cfg, err := capellafile.LoadText(yaml, "capella.yaml", e.dir, func(k string) string {
		if k == "APP_PW" {
			return "Passw0rd!Passw0rd!"
		}
		return ""
	})
	if err != nil {
		e.t.Fatalf("load: %v", err)
	}
	e.cfg = cfg
	e.rc = plan.NewRunContext(e.f.Client(), cfg)
	e.rc.Clock = engine.NewFakeClock(time.Time{})
	e.rc.Timeout = time.Hour
	e.rc.Progress = func(s string) { e.lines = append(e.lines, s) }
	e.rc.Warn = func(what, action string) { e.warns = append(e.warns, what+" | "+action) }
}

func (e *env) plan(opts ...plan.Options) *plan.Plan {
	e.t.Helper()
	return e.planWith(BuildTree(), opts...)
}

func (e *env) planWith(root plan.Reconciler, opts ...plan.Options) *plan.Plan {
	e.t.Helper()
	var o plan.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	p, err := plan.BuildPlan(context.Background(), e.rc, root, o)
	if err != nil {
		e.t.Fatalf("BuildPlan: %v", err)
	}
	return p
}

func (e *env) apply(p *plan.Plan) *plan.ApplyResult {
	e.t.Helper()
	res := plan.Execute(context.Background(), e.rc, p)
	if !res.OK() {
		e.t.Fatalf("apply failed: %+v (lines: %v)", res.Failed, e.lines)
	}
	return res
}

// types returns "kind/key:TYPE" for every action that is not hidden.
func types(p *plan.Plan) []string {
	var out []string
	for _, a := range p.Actions {
		if a.Hidden {
			continue
		}
		out = append(out, a.Kind+"/"+a.Key+":"+string(a.Type))
	}
	return out
}

func find(t *testing.T, p *plan.Plan, kind, key string) *plan.Action {
	t.Helper()
	for _, a := range p.Actions {
		if a.Kind == kind && a.Key == key {
			return a
		}
	}
	t.Fatalf("no %s %q in plan: %v", kind, key, types(p))
	return nil
}

func requireType(t *testing.T, p *plan.Plan, kind, key string, want plan.ActionType) *plan.Action {
	t.Helper()
	a := find(t, p, kind, key)
	if a.Type != want {
		t.Fatalf("%s %s: type %s (reason %q), want %s", kind, key, a.Type, a.Reason, want)
	}
	return a
}

func requireAllNoop(t *testing.T, p *plan.Plan) {
	t.Helper()
	for _, a := range p.Actions {
		if a.Type.IsWrite() || a.Type == plan.Blocked {
			t.Fatalf("expected a converged plan, got %s/%s: %s %s", a.Kind, a.Key, a.Type, a.Reason)
		}
	}
}

func requests(f *capellafake.Fake, method, re string) int { return f.Count(method, re) }

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

const baseYAML = `apiVersion: cbctl/v1
project:
  name: sandbox
clusters:
  - name: one
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.0.0.0/20 }
    availability: { type: multi }
    support: { plan: basic, timezone: ET }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 3
        services: [data, index, query]
`
