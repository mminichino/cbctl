package resources

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/plan"
)

func TestCoverHelpers_util(t *testing.T) {
	t.Parallel()
	if intOf(int64(7)) != 7 || intOf(float64(3.9)) != 3 || intOf(float32(2.1)) != 2 || intOf("x") != 0 {
		t.Fatalf("intOf")
	}
	if !isNumber(int64(1)) || !isNumber(float64(1.5)) || !isNumber(float32(1)) || isNumber("x") {
		t.Fatalf("isNumber")
	}
	if toFloat(float64(1.25)) != 1.25 || toFloat(int64(4)) != 4 {
		t.Fatalf("toFloat")
	}
	if truthy(nil) || truthy("") || truthy(false) || !truthy("a") || !truthy(float64(0.1)) {
		t.Fatalf("truthy")
	}
	if orZero(nil) != int64(0) || orZero("") != int64(0) || orZero(int64(3)) != int64(3) {
		t.Fatalf("orZero")
	}
	if nilIfEmptyAny("") != nil || nilIfEmptyAny("x") != "x" {
		t.Fatalf("nilIfEmptyAny")
	}
	if got := anyStrings([]string{"a", "b"}); len(got) != 2 || got[0] != "a" {
		t.Fatalf("anyStrings: %v", got)
	}
	if !sameSorted([]string{"b", "a"}, []string{"a", "b"}) || sameSorted([]string{"a"}, []string{"a", "b"}) {
		t.Fatalf("sameSorted")
	}
	if commas(1234567) != "1,234,567" || commas(-99) != "-99" {
		t.Fatalf("commas")
	}
	if plural(1, "item") != "1 item" || plural(2, "item") != "2 items" {
		t.Fatalf("plural")
	}
}

func TestCoverHelpers_apiSentinelsAndPaths(t *testing.T) {
	t.Parallel()
	if !isForbidden(capellaapi.ErrForbidden) || isForbidden(capellaapi.ErrNotFound) {
		t.Fatal("isForbidden")
	}
	if !isNotFound(capellaapi.ErrNotFound) || isConflict(capellaapi.ErrNotFound) {
		t.Fatal("isNotFound/isConflict")
	}
	if ignoreNotFound(capellaapi.ErrNotFound) != nil {
		t.Fatal("ignoreNotFound 404")
	}
	if ignoreNotFound(errors.New("x")) == nil {
		t.Fatal("ignoreNotFound other")
	}
	a := &rawAPI{org: "org-1"}
	if !strings.Contains(a.freeBucket("p", "c", "b"), "/freeTier/") {
		t.Fatalf("freeBucket path: %s", a.freeBucket("p", "c", "b"))
	}
	if !strings.Contains(a.freeCluster("p", "c"), "/freeTier/") {
		t.Fatalf("freeCluster path")
	}
	if !strings.Contains(a.user("p", "c", "u1"), "/users/") {
		t.Fatalf("user path")
	}
}

func TestCoverHelpers_clusterText(t *testing.T) {
	t.Parallel()
	g := Obj{
		"numOfNodes": int64(3),
		"node": Obj{
			"compute": Obj{"cpu": int64(4), "ram": int64(16)},
			"disk":    Obj{"type": "gp3", "storage": int64(50)},
		},
	}
	if !strings.Contains(groupSize(g), "3 × 4 vCPU/16 GB") || !strings.Contains(groupSize(g), "gp3") {
		t.Fatalf("groupSize: %s", groupSize(g))
	}
	if !versionMatches("", "7.6.2") || !versionMatches("7.6", "7.6.2") || versionMatches("7.6", "7.5.0") {
		t.Fatal("versionMatches")
	}
	d := Obj{
		"serviceGroups": []any{
			Obj{"services": []any{"data"}, "numOfNodes": int64(3), "node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)}}},
			Obj{"services": []any{"index"}, "numOfNodes": int64(2), "node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(8)}}},
		},
	}
	lines := CreateGroupLines(d)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "+ serviceGroups[") {
		t.Fatalf("CreateGroupLines: %v", lines)
	}
	if CreateGroupLines(Obj{"serviceGroups": []any{g}}) != nil {
		t.Fatal("single group should yield nil lines")
	}
}

func TestCoverHelpers_accessNamesBucket(t *testing.T) {
	t.Parallel()
	access := []any{
		Obj{"resources": Obj{"buckets": []any{Obj{"name": "orders"}, Obj{"name": "other"}}}},
	}
	if !accessNamesBucket(access, "orders") || accessNamesBucket(access, "missing") {
		t.Fatal("accessNamesBucket")
	}
}

func TestCoverHelpers_unmanagedLabelsAndDeletes(t *testing.T) {
	t.Parallel()
	parent := plan.NewAction(newCluster(), "cluster", "nova", plan.Noop)
	parent.CreatedID = "cl-1"

	bs := newBackupSchedule()
	label := bs.UnmanagedLabel(plan.Observed{Body: Obj{"type": "weekly", "retentionTime": int64(30)}}, parent)
	if !strings.Contains(label, "backup-schedule nova") {
		t.Fatalf("backup UnmanagedLabel: %s", label)
	}
	da, err := bs.DeleteAction(context.Background(), nil, plan.Observed{Body: Obj{"type": "weekly"}}, parent)
	if err != nil || da.Type != plan.Delete || da.Key != "nova" {
		t.Fatalf("backup DeleteAction: %+v %v", da, err)
	}

	sch := newSchedule()
	sl := sch.UnmanagedLabel(plan.Observed{Body: Obj{"timezone": "UTC", "days": []any{}}}, parent)
	if !strings.Contains(sl, "schedule nova") {
		t.Fatalf("schedule UnmanagedLabel: %s", sl)
	}
	sda, err := sch.DeleteAction(context.Background(), nil, plan.Observed{Body: Obj{}}, parent)
	if err != nil || sda.Type != plan.Delete {
		t.Fatalf("schedule DeleteAction: %+v %v", sda, err)
	}

	cidr := newAllowedCIDR()
	obs := plan.Observed{Body: Obj{"cidr": "10.0.0.0/24", "comment": "office"}}
	cl := cidr.UnmanagedLabel(obs, parent)
	if !strings.Contains(cl, "allowed-cidr") || !strings.Contains(cl, "office") {
		t.Fatalf("cidr UnmanagedLabel: %s", cl)
	}
	cda, err := cidr.DeleteAction(context.Background(), nil, obs, parent)
	if err != nil || cda.Type != plan.Delete {
		t.Fatalf("cidr DeleteAction: %+v %v", cda, err)
	}

	proj := newProject()
	pda, err := proj.DeleteAction(context.Background(), nil, plan.Observed{ID: "p1", Body: Obj{"name": "sandbox"}}, nil)
	if err != nil || pda.Type != plan.Delete || pda.Key != "sandbox" {
		t.Fatalf("project DeleteAction: %+v %v", pda, err)
	}

	bucket := plan.NewAction(newBucket(), "bucket", "nova/orders", plan.Noop)
	bucket.Parent = parent
	scope := newScope()
	sda2, err := scope.DeleteAction(context.Background(), nil, plan.Observed{ID: "app", Body: Obj{"name": "app"}}, bucket)
	if err != nil || sda2.Type != plan.Delete {
		t.Fatalf("scope DeleteAction: %+v %v", sda2, err)
	}
	col := newCollection()
	cda2, err := col.DeleteAction(context.Background(), nil, plan.Observed{ID: "items", Body: Obj{"name": "items"}}, sda2)
	if err != nil || cda2.Type != plan.Delete {
		t.Fatalf("collection DeleteAction: %+v %v", cda2, err)
	}
}

func TestCoverHelpers_childBlockAndEnsureReady(t *testing.T) {
	t.Parallel()
	r := newCluster()
	a := plan.NewAction(r, "cluster", "nova", plan.Noop)
	a.Data = map[string]any{"state": "turnedOff"}
	a.CreatedID = "cl-1"
	proj := plan.NewAction(newProject(), "project", "sandbox", plan.Noop)
	proj.CreatedID = "p1"
	a.Parent = proj

	rc := plan.NewRunContext(nil, nil)
	rc.Clock = engine.NewFakeClock(time.Time{})
	reason, err := r.ChildBlockReason(context.Background(), rc, a, "bucket")
	if err != nil || !strings.Contains(reason, "turned off") {
		t.Fatalf("ChildBlockReason: %q %v", reason, err)
	}
	if reason, _ := r.ChildBlockReason(context.Background(), rc, a, "schedule"); reason != "" {
		t.Fatalf("schedule should not block: %q", reason)
	}
	rc.PowerOn = true
	if reason, _ := r.ChildBlockReason(context.Background(), rc, a, "bucket"); reason != "" {
		t.Fatalf("PowerOn clears block: %q", reason)
	}

	a.Data["state"] = "healthy"
	ok, err := r.EnsureReady(context.Background(), rc, a, nil)
	if err != nil || !ok {
		t.Fatalf("healthy EnsureReady: %v %v", ok, err)
	}
	a.Data["state"] = "turnedOff"
	child := plan.NewAction(newSchedule(), "schedule", "nova", plan.Noop)
	ok, err = r.EnsureReady(context.Background(), rc, a, child)
	if err != nil || !ok {
		t.Fatalf("schedule while off: %v %v", ok, err)
	}
	a.Data["state"] = "deploying"
	rc.NoWait = true
	ok, err = r.EnsureReady(context.Background(), rc, a, nil)
	if err != nil || ok {
		t.Fatalf("NoWait transitional: %v %v", ok, err)
	}
}

func TestCoverHelpers_bucketDeleteDetails(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	cid := e.f.SeedCluster(pid, "one", "healthy")
	bid := e.f.SeedBucket(cid, "orders")
	_ = bid
	e.f.SeedCredential(cid, "app-user", []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources:  capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders"}}},
	}}, false)

	lines, err := BucketDeleteDetails(context.Background(), e.f.Client(), pid, cid, Obj{
		"name":  "orders",
		"stats": Obj{"itemCount": int64(12), "memoryUsedInMib": int64(8)},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "orders") || !strings.Contains(joined, "12") {
		t.Fatalf("lines: %v", lines)
	}
	names, err := DependentCredentials(context.Background(), e.f.Client(), pid, cid, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatalf("expected dependent credential, got %v", names)
	}
}

func TestCoverHelpers_keyspaceDeleteDetails(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	cid := e.f.SeedCluster(pid, "one", "healthy")
	bid := e.f.SeedBucket(cid, "orders")
	e.f.SeedScope(cid, "orders", "app", map[string]int{"items": 0})
	e.f.SeedCredential(cid, "app-user", []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{
			Name:   "orders",
			Scopes: []capellaapi.ResourceScope{{Name: "app", Collections: []string{"items"}}},
		}}},
	}}, false)

	lines, err := KeyspaceDeleteDetails(context.Background(), e.f.Client(), pid, cid, "orders", bid, "app", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("expected scope delete details")
	}
	names, err := DependentScopeCredentials(context.Background(), e.f.Client(), pid, cid, "orders", "app", "items")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatalf("expected dependent scope credential, got %v", names)
	}
}

func TestCoverHelpers_projectID(t *testing.T) {
	t.Parallel()
	if projectID(nil) != "" {
		t.Fatal("nil")
	}
	p := plan.NewAction(newProject(), "project", "sandbox", plan.Noop)
	p.CreatedID = "p1"
	if projectID(p) != "p1" {
		t.Fatalf("project self: %q", projectID(p))
	}
	c := plan.NewAction(newCluster(), "cluster", "nova", plan.Noop)
	c.Parent = p
	if projectID(c) != "p1" {
		t.Fatalf("ancestor: %q", projectID(c))
	}
}

func TestCoverHelpers_clusterMatch(t *testing.T) {
	t.Parallel()
	r := newCluster()
	obs := []plan.Observed{
		{ID: "c1", Body: Obj{"name": "nova"}},
		{ID: "c2", Body: Obj{"name": "prod"}},
	}
	got, err := r.Match(Obj{"id": "c2", "name": "x"}, obs)
	if err != nil || len(got) != 1 || got[0].ID != "c2" {
		t.Fatalf("pinned: %v %v", got, err)
	}
	_, err = r.Match(Obj{"id": "missing"}, obs)
	if err == nil {
		t.Fatal("expected missing pin error")
	}
	got, err = r.Match(Obj{"name": "nova"}, obs)
	if err != nil || len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("by name: %v %v", got, err)
	}
}

func TestCoverHelpers_optionsAndBodies(t *testing.T) {
	t.Parallel()
	if Canonical("10.0.0.1") != "10.0.0.1/32" {
		t.Fatalf("Canonical bare IP: %s", Canonical("10.0.0.1"))
	}
	if Canonical("  not-a-cidr  ") != "not-a-cidr" {
		t.Fatalf("Canonical fallback: %q", Canonical("  not-a-cidr  "))
	}
	opts := Obj{"intervals": []any{int64(1), int64(2), int64(4)}, "minimumRetention": int64(24), "maximumRetention": int64(720)}
	if p := OptionsProblem(opts, Obj{"interval": int64(3), "retention": int64(48)}, nil, false); !strings.Contains(p, "interval") {
		t.Fatalf("bad interval: %q", p)
	}
	if p := OptionsProblem(opts, Obj{"interval": int64(2), "retention": int64(1)}, nil, false); !strings.Contains(p, "retention") {
		t.Fatalf("bad retention: %q", p)
	}
	if p := OptionsProblem(opts, Obj{"interval": int64(2), "retention": int64(48), "copyToRegions": []any{"a", "b", "c"}}, nil, false); !strings.Contains(p, "at most two") {
		t.Fatalf("too many regions: %q", p)
	}
	if p := OptionsProblem(opts, Obj{"interval": int64(2), "retention": int64(48), "copyToRegions": []any{"eu-west-1"}}, []string{"us-east-1"}, true); !strings.Contains(p, "copies cannot") {
		t.Fatalf("bad region: %q", p)
	}
	if OptionsProblem(opts, Obj{"interval": int64(2), "retention": int64(48)}, nil, false) != "" {
		t.Fatal("valid schedule should be clean")
	}

	g := Obj{
		"numOfNodes": int64(3),
		"services":   []any{"data"},
		"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)},
			"disk": Obj{"type": "P30", "storage": int64(1024), "iops": int64(5000)}},
	}
	ag := APIGroup(g, "azure")
	disk := objOf(objOf(ag["node"])["disk"])
	if disk["storage"] != nil || disk["iops"] != nil || disk["type"] != "P30" {
		t.Fatalf("APIGroup azure trim: %+v", disk)
	}

	freeBody := BuildBucketUpdateBody(Obj{"memoryAllocationInMb": int64(100)}, Obj{"memoryAllocationInMb": int64(200)}, true)
	if freeBody["memoryAllocationInMb"] != int64(200) {
		t.Fatalf("free update body: %+v", freeBody)
	}
	full := BuildBucketUpdateBody(Obj{
		"name": "orders", "memoryAllocationInMb": int64(100), "replicas": int64(1),
		"storageBackend": "couchstore", "durabilityLevel": "none", "bucketConflictResolution": "seqno",
	}, Obj{"replicas": int64(2)}, false)
	if full["replicas"] != int64(2) || full["durabilityLevel"] != "none" {
		t.Fatalf("paid update body: %+v", full)
	}
}

func TestCoverHelpers_nextOn(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	cid := e.f.SeedCluster(pid, "one", "healthy")
	days := []any{}
	for _, d := range Days {
		days = append(days, Obj{"day": d, "from": Obj{"state": "off"}, "to": Obj{"state": "on", "hour": 9, "minute": 0}})
	}
	if err := e.f.Client().CreateSchedule(context.Background(), pid, cid, Obj{
		"timezone": "UTC", "days": days,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	r := newCluster()
	a := plan.NewAction(r, kindCluster, "one", plan.Noop)
	a.CreatedID = cid
	proj := plan.NewAction(newProject(), kindProject, "sandbox", plan.Noop)
	proj.CreatedID = pid
	a.Parent = proj
	if got := r.nextOn(context.Background(), e.rc, a); got == "" {
		t.Fatal("expected nextOn text with an active schedule")
	}
	if r.nextOn(context.Background(), e.rc, plan.NewAction(r, kindCluster, "x", plan.Noop)) != "" {
		t.Fatal("missing ids should yield empty nextOn")
	}
	a.Data = map[string]any{"state": "turnedOff"}
	reason, err := r.ChildBlockReason(context.Background(), e.rc, a, "bucket")
	if err != nil || !strings.Contains(reason, "schedule turns it on") {
		t.Fatalf("ChildBlockReason with nextOn: %q %v", reason, err)
	}
}

func TestCoverHelpers_moreEdges(t *testing.T) {
	t.Parallel()
	if !FixedSizeDisk("azure", "P30") || FixedSizeDisk("aws", "gp3") || FixedSizeDisk("azure", "Ultra") {
		t.Fatal("FixedSizeDisk")
	}
	sum := CreateSummary(Obj{
		"tier": "free", "cloudProvider": Obj{"type": "aws", "region": "us-east-1"},
	})
	if !strings.Contains(sum, "free tier") {
		t.Fatalf("free CreateSummary: %s", sum)
	}
	sum = CreateSummary(Obj{
		"cloudProvider": Obj{"type": "aws", "region": "us-east-1"},
		"availability":  Obj{"type": "multi"},
		"support":       Obj{"plan": "basic"},
		"serviceGroups": []any{
			Obj{"numOfNodes": int64(3), "services": []any{"data", "index"},
				"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)}}},
			Obj{"numOfNodes": int64(2), "services": []any{"query"},
				"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(8)}}},
		},
	})
	if !strings.Contains(sum, "service groups") {
		t.Fatalf("mds CreateSummary: %s", sum)
	}

	r := newCluster()
	a := plan.NewAction(r, kindCluster, "x", plan.Noop)
	a.Data = map[string]any{"state": "destroyFailed"}
	rc := plan.NewRunContext(nil, nil)
	ok, err := r.EnsureReady(context.Background(), rc, a, nil)
	if err != nil || ok {
		t.Fatalf("unknown state EnsureReady: %v %v", ok, err)
	}
	a.Data = map[string]any{} // nil state
	rc.NoWait = true
	ok, err = r.EnsureReady(context.Background(), rc, a, nil)
	if err != nil || ok {
		t.Fatalf("nil state NoWait: %v %v", ok, err)
	}

	if pid, cid := clusterIDs(nil); pid != "" || cid != "" {
		t.Fatal("clusterIDs nil")
	}
	if newBackupSchedule().Key(nil, nil) != "" || newSchedule().Key(nil, nil) != "" {
		t.Fatal("Key nil parent")
	}
	if newBucketBackupSchedule().Key(nil, nil) != "" {
		t.Fatal("bucket backup Key nil parent")
	}
}
