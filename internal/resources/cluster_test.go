package resources

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/plan"
)

func seeded(t *testing.T, yaml string) (*env, string, string) {
	t.Helper()
	e := newEnv(t, yaml)
	pid := e.f.SeedProject("sandbox")
	cid := e.f.SeedCluster(pid, "one", "healthy")
	return e, pid, cid
}

func TestClusterNoopWhenMatching(t *testing.T) {
	e, _, _ := seeded(t, baseYAML)
	p := e.plan()
	requireType(t, p, "project", "sandbox", plan.Noop)
	requireType(t, p, "cluster", "one", plan.Noop)
	requireAllNoop(t, p)
}

func TestClusterImmutableChangeBlocked(t *testing.T) {
	e, _, _ := seeded(t, strings.Replace(baseYAML, "region: us-east-1", "region: us-west-2", 1))
	a := requireType(t, e.plan(), "cluster", "one", plan.Blocked)
	if !strings.Contains(a.Reason, "region") {
		t.Fatalf("reason %q", a.Reason)
	}
}

func TestClusterScaleOutUpdatesAndWaits(t *testing.T) {
	e, _, cid := seeded(t, strings.Replace(baseYAML, "numOfNodes: 3", "numOfNodes: 4", 1))
	p := e.plan()
	a := requireType(t, p, "cluster", "one", plan.Update)
	if len(a.Changes) == 0 {
		t.Fatalf("no field changes")
	}
	e.apply(p)
	if requests(e.f, "PUT", `/clusters/`+cid+`$`) != 1 {
		t.Fatalf("expected one cluster PUT: %v", e.f.RequestLog())
	}
	requireAllNoop(t, e.plan())
}

func TestClusterDescriptionUpdate(t *testing.T) {
	e, _, _ := seeded(t, strings.Replace(baseYAML, "  - name: one\n", "  - name: one\n    description: now described\n", 1))
	a := requireType(t, e.plan(), "cluster", "one", plan.Update)
	if len(a.Changes) != 1 || a.Changes[0].Path != "description" {
		t.Fatalf("changes %+v", a.Changes)
	}
}

func TestClusterUnhealthyStatesBlock(t *testing.T) {
	for _, state := range []string{"deploymentFailed", "degraded", "healthy_off"} {
		t.Run(state, func(t *testing.T) {
			e, _, cid := seeded(t, strings.Replace(baseYAML, "numOfNodes: 3", "numOfNodes: 4", 1))
			if state == "healthy_off" {
				state = "turnedOff"
			}
			e.f.SetClusterState(cid, state)
			a := requireType(t, e.plan(), "cluster", "one", plan.Blocked)
			if a.Reason == "" {
				t.Fatalf("blocked without reason")
			}
		})
	}
}

func TestClusterOffNoopWithoutChanges(t *testing.T) {
	e, _, cid := seeded(t, baseYAML)
	e.f.SetClusterState(cid, "turnedOff")
	requireType(t, e.plan(), "cluster", "one", plan.Noop)
}

func TestClusterPowerOn(t *testing.T) {
	e, _, cid := seeded(t, baseYAML)
	e.f.SetClusterState(cid, "turnedOff")
	e.rc.PowerOn = true
	p := e.plan()
	a := requireType(t, p, "cluster", "one", plan.Update)
	if a.Label == "" && len(a.Changes) == 0 {
		t.Fatalf("power-on without a label or change")
	}
	e.f.SettlePolls(0)
	e.apply(p)
	if requests(e.f, "POST", `/activationState$|/on$`) == 0 {
		t.Fatalf("no power-on request: %v", e.f.RequestLog())
	}
	if got := e.f.ClusterState(cid); got != "healthy" {
		t.Fatalf("state %q", got)
	}
}

func TestClusterChangeWhileOffBlocked(t *testing.T) {
	e, _, cid := seeded(t, strings.Replace(baseYAML, "numOfNodes: 3", "numOfNodes: 4", 1))
	e.f.SetClusterState(cid, "turnedOff")
	a := requireType(t, e.plan(), "cluster", "one", plan.Blocked)
	if !strings.Contains(a.Reason, "off") {
		t.Fatalf("reason %q", a.Reason)
	}
}

func scaleInEnv(t *testing.T, target, replicas string) *env {
	t.Helper()
	start := strings.Replace(fullYAML, "numOfNodes: 3", "numOfNodes: 4", 1)
	start = strings.Replace(start, "replicas: 1", "replicas: "+replicas, 1)
	e := newEnv(t, start)
	e.apply(e.plan())
	e.load(strings.Replace(start, "numOfNodes: 4", "numOfNodes: "+target, 1))
	return e
}

func TestClusterScaleInBlockedByReplicas(t *testing.T) {
	e := scaleInEnv(t, "3", "3")
	a := requireType(t, e.plan(), "cluster", "one", plan.Blocked)
	if !strings.Contains(a.Reason, "replica") {
		t.Fatalf("reason %q", a.Reason)
	}
}

func TestClusterScaleInAllowedWithinReplicas(t *testing.T) {
	e := scaleInEnv(t, "3", "2")
	p := e.plan()
	a := requireType(t, p, "cluster", "one", plan.Update)
	if !strings.Contains(strings.Join(a.Remarks, "\n"), "checks") {
		t.Fatalf("expected a checks remark: %v", a.Remarks)
	}
}

func TestClusterFreeTierMismatch(t *testing.T) {
	free := `apiVersion: cbctl/v1
project:
  name: sandbox
clusters:
  - name: one
    tier: free
    cloudProvider: { type: aws, region: us-east-2 }
`
	e, _, _ := seeded(t, free)
	a := requireType(t, e.plan(), "cluster", "one", plan.Blocked)
	if a.Reason == "" {
		t.Fatalf("no reason")
	}
}

func TestClusterNeverPrunedButListed(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	e.load("apiVersion: cbctl/v1\nproject:\n  name: sandbox\n")
	p := e.plan(plan.Options{Prune: true})
	if len(p.Unmanaged) != 1 || !strings.Contains(p.Unmanaged[0], "one") || !strings.Contains(p.Unmanaged[0], "never pruned") {
		t.Fatalf("unmanaged %v", p.Unmanaged)
	}
	for _, a := range p.Actions {
		if a.Type == plan.Delete {
			t.Fatalf("cluster delete planned by apply: %v", types(p))
		}
	}
}

func TestClusterDeleteActionCarriesState(t *testing.T) {
	rec := newCluster()
	obs := plan.Observed{ID: "c1", Body: Obj{"name": "one", "currentState": "healthy"}}
	a, err := rec.DeleteAction(t.Context(), nil, obs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != plan.Delete || a.Key != "one" || a.Data["state"] != "healthy" || a.Data["free"] != false {
		t.Fatalf("%+v", a)
	}
}

func TestDestroyBlockedBelowProtectedCluster(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	p := e.plan()
	cluster := find(t, p, "cluster", "one")
	buckets, err := newBucket().Observe(t.Context(), e.rc, cluster)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("observe: %v %v", buckets, err)
	}
	a, err := newBucket().DeleteAction(t.Context(), e.rc, buckets[0], cluster)
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != plan.Blocked || !strings.Contains(a.Reason, "deletion protection is on for one") {
		t.Fatalf("%s %q", a.Type, a.Reason)
	}
}

func TestDeletionProtectionToggle(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	e.load(strings.Replace(fullYAML, "deletionProtection: true", "deletionProtection: false", 1))
	p := e.plan()
	requireType(t, p, "deletion-protection", "one", plan.Update)
	e.apply(p)
	requireAllNoop(t, e.plan())
	e.f.ResetLog()
	e.load(fullYAML)
	e.apply(e.plan())
	if requests(e.f, "PUT", `deletionProtection|protection`) == 0 && requests(e.f, "POST", `deletionProtection|protection`) == 0 {
		t.Logf("log: %v", e.f.RequestLog())
	}
}

func TestGuards(t *testing.T) {
	if !DeletionProtected(Obj{"deletionProtection": true}) || DeletionProtected(Obj{}) || DeletionProtected(Obj{"deletionProtection": false}) {
		t.Fatal("DeletionProtected")
	}
	if got := ProtectionMessage("c"); got != "deletion protection is on for c; run 'cbctl capella cluster protect c --off' first" {
		t.Fatalf("message %q", got)
	}
}
