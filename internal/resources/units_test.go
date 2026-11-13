package resources

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/plan"
)

func group(nodes int, ram int, services ...string) Obj {
	svcs := make([]any, len(services))
	for i, s := range services {
		svcs[i] = s
	}
	return Obj{"numOfNodes": int64(nodes), "services": svcs,
		"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(ram)}}}
}

func TestScalingHelpers(t *testing.T) {
	groups := []Obj{group(3, 16, "data", "index"), group(2, 8, "query")}
	if DataNodes(groups) != 3 || DataMemory(groups) != 48 {
		t.Fatalf("nodes %d memory %d", DataNodes(groups), DataMemory(groups))
	}
	if ReducesCapacity(groups, groups) {
		t.Fatal("same groups reduce capacity")
	}
	if !ReducesCapacity(groups, groups[:1]) {
		t.Fatal("dropping a group reduces capacity")
	}
	if !ReducesCapacity(groups, []Obj{group(2, 16, "data", "index"), groups[1]}) {
		t.Fatal("fewer data nodes reduce capacity")
	}
	if ReducesCapacity(groups, []Obj{group(4, 16, "data", "index"), groups[1]}) {
		t.Fatal("more data nodes do not reduce capacity")
	}
	after := AfterChange(groups, []Obj{{"services": []any{"data", "index"}, "numOfNodes": int64(5)}})
	if len(after) != 1 || DataNodes(after) != 5 || DataMemory(after) != 80 {
		t.Fatalf("after %+v", after)
	}
	if got := AfterChange(groups, nil); len(got) != 2 {
		t.Fatalf("a nil desired means unchanged: %+v", got)
	}
}

func TestScalingCheck(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	cid := e.f.SeedCluster(pid, "one", "healthy")
	three := []Obj{group(3, 16, "data", "index", "query")}
	ctx := t.Context()

	tests := []struct {
		name    string
		old     []Obj
		updated []Obj
		wantAny string
		skip    bool
	}{
		{"unchanged", three, three, "", false},
		{"scale out", three, []Obj{group(5, 16, "data", "index", "query")}, "", false},
		{"last data group", three, []Obj{group(3, 16, "index", "query")}, "last group that runs data", false},
		{"drops index service", three, []Obj{group(3, 16, "data", "query")}, "index", false},
		{"drops query service", three, []Obj{group(3, 16, "data", "index")}, "query", false},
		{"memory shrink without capacity data", three, []Obj{group(3, 4, "data", "index", "query")}, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocked, checks, err := Check(ctx, e.rc, pid, cid, tc.old, tc.updated)
			if err != nil {
				t.Fatal(err)
			}
			joinedReasons := strings.Join(blocked, "; ")
			if tc.wantAny == "" && len(blocked) != 0 {
				t.Fatalf("unexpected block: %s", joinedReasons)
			}
			if tc.wantAny != "" && !strings.Contains(joinedReasons, tc.wantAny) {
				t.Fatalf("blocked %q, want mention of %q", joinedReasons, tc.wantAny)
			}
			if len(blocked) > 0 && checks != "" {
				t.Fatalf("checks remark on a blocked change: %q", checks)
			}
		})
	}

	t.Run("service removal flags", func(t *testing.T) {
		e.rc.AllowServiceRemoval, e.rc.AllowServiceGroupRemoval = true, true
		blocked, _, err := Check(ctx, e.rc, pid, cid, three, []Obj{group(3, 16, "data", "query")})
		e.rc.AllowServiceRemoval, e.rc.AllowServiceGroupRemoval = false, false
		if err != nil || len(blocked) != 0 {
			t.Fatalf("blocked %v err %v", blocked, err)
		}
	})
}

func TestScheduleExpansion(t *testing.T) {
	hib := Obj{"timezone": "US/Eastern", "weekdays": "08:00-18:00", "weekend": "off", "friday": "08:00-14:30"}
	body := Expand(hib)
	days := objsOf(body["days"])
	if len(days) != 7 || days[0]["day"] != "monday" || days[6]["day"] != "sunday" {
		t.Fatalf("days %+v", days)
	}
	if got := DayText(days[0]); got != "08:00–18:00" {
		t.Fatalf("monday %q", got)
	}
	if got := DayText(days[4]); got != "08:00–14:30" {
		t.Fatalf("friday %q", got)
	}
	if days[5]["state"] != "off" || days[6]["state"] != "off" {
		t.Fatalf("weekend %+v %+v", days[5], days[6])
	}
	if got := WeekSummary(body); got != "US/Eastern · Mon–Thu 08:00–18:00 · Fri 08:00–14:30 · Sat–Sun off" {
		t.Fatalf("summary %q", got)
	}
	if e := ExpandDay("monday", "00:00-24:00"); intOf(objOf(e["to"])["hour"]) != 0 {
		t.Fatalf("24:00 is sent as 0:00: %+v", e)
	}
	if got := DayText(ExpandDay("monday", "08:00-24:00")); got != "08:00–24:00" {
		t.Fatalf("end of day %q", got)
	}
	on := Expand(Obj{"timezone": "US/Pacific"})
	for _, d := range objsOf(on["days"]) {
		if d["state"] != "on" {
			t.Fatalf("a day without a value is on: %+v", d)
		}
	}
}

func TestScheduleNextTransitions(t *testing.T) {
	body := Expand(Obj{"timezone": "US/Eastern", "weekdays": "08:00-18:00", "weekend": "off"})
	ny, err := time.LoadLocation("US/Eastern")
	if err != nil {
		t.Skip("no tz database")
	}
	// Wednesday 2026-11-11 10:15 Eastern.
	now := time.Date(2026, 11, 11, 10, 15, 0, 0, ny)
	if got := StateAt(body, now); got != "on" {
		t.Fatalf("state %q", got)
	}
	if got := NextText(body, now); got != "off at Wed 18:00 US/Eastern" {
		t.Fatalf("next %q", got)
	}
	// Friday evening: the next on is Monday 08:00.
	fri := time.Date(2026, 11, 13, 19, 0, 0, 0, ny)
	if got := StateAt(body, fri); got != "off" {
		t.Fatalf("state %q", got)
	}
	if got := NextOnText(body, fri); got != "Mon 08:00 US/Eastern" {
		t.Fatalf("next on %q", got)
	}
	if s, at, ok := NextTransition(Expand(Obj{"timezone": "US/Eastern"}), now); ok {
		t.Fatalf("an always-on schedule never changes: %s %v", s, at)
	}
	if got := NextText(Obj{"timezone": "Not/AZone", "days": body["days"]}, now); got != "" {
		t.Fatalf("unknown zone: %q", got)
	}
}

func TestSchemaTimezonesLoad(t *testing.T) {
	raw, err := os.ReadFile("../config/capellafile/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	zones := schema.Defs["cluster"].Properties["hibernation"].Properties["timezone"].Enum
	if len(zones) < 20 {
		t.Fatalf("expected the schema's timezone list, got %d", len(zones))
	}
	for _, z := range zones {
		if zone(z) == nil {
			t.Errorf("timezone %q does not load", z)
		}
	}
}

func TestBackupHelpers(t *testing.T) {
	if StartHM("02:00") != "02:00" || StartHM("2026-01-01T03:30:00Z") != "03:30" || StartHM(nil) != "" {
		t.Fatal("StartHM")
	}
	now := time.Date(2026, 11, 13, 10, 30, 0, 0, time.UTC)
	if got := NextInstant("02:00", now); got != "2026-11-14T02:00:00Z" {
		t.Fatalf("next instant %q", got)
	}
	if got := NextInstant("12:00", now); got != "2026-11-13T12:00:00Z" {
		t.Fatalf("next instant %q", got)
	}
	body := SnapshotBody(Obj{"interval": int64(24), "retention": int64(720), "startTime": "02:00"}, now)
	if body["interval"] != int64(24) || body["retention"] != int64(720) {
		t.Fatalf("body %+v", body)
	}
	if got := BucketSchedule(Obj{"dayOfWeek": "monday"}); got["dayOfWeek"] != "monday" || got["incrementalEvery"] != int64(24) {
		t.Fatalf("schedule %+v", got)
	}
	if got := BucketSummary(Obj{"dayOfWeek": "monday", "startAt": int64(3), "incrementalEvery": int64(12), "retentionTime": "90days"}); got !=
		"weekly full on monday at 03:00 UTC · incremental every 12 h · kept 90 days" {
		t.Fatalf("summary %q", got)
	}
}

func TestCIDRHelpers(t *testing.T) {
	if Canonical("203.0.113.10") != "203.0.113.10/32" || Canonical("10.0.0.0/8") != "10.0.0.0/8" {
		t.Fatalf("canonical %q %q", Canonical("203.0.113.10"), Canonical("10.0.0.0/8"))
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !IsExpired("2020-01-01T00:00:00Z", now) || IsExpired("2030-01-01T00:00:00Z", now) || IsExpired(nil, now) {
		t.Fatal("IsExpired")
	}
	changes := CIDRChanges(Obj{"cidr": "1.2.3.4/32", "comment": "b"}, Obj{"cidr": "1.2.3.4/32", "comment": "a"})
	if len(changes) != 1 || changes[0].Path != "comment" {
		t.Fatalf("changes %+v", changes)
	}
	if got := CIDRChanges(Obj{"cidr": "1.2.3.4/32"}, Obj{"cidr": "1.2.3.4/32", "comment": "a"}); len(got) != 1 || got[0].After != nil {
		t.Fatalf("an omitted comment means none: %+v", got)
	}
}

func TestClusterHelpers(t *testing.T) {
	if !IsFree(Obj{"support": Obj{"plan": "Free"}}) || IsFree(Obj{"support": Obj{"plan": "basic"}}) {
		t.Fatal("IsFree reads support.plan")
	}
	g := APIGroup(Obj{"numOfNodes": int64(3), "services": []any{"data"},
		"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)}, "disk": Obj{"type": "gp3", "storage": int64(50), "iops": int64(3000)}}}, "aws")
	if objOf(objOf(g["node"])["disk"])["type"] != "gp3" {
		t.Fatalf("group %+v", g)
	}
	if !FixedSizeDisk("azure", "P30") || FixedSizeDisk("aws", "gp3") {
		t.Fatal("FixedSizeDisk")
	}
	observed := Obj{"name": "one", "description": "old", "cloudProvider": Obj{"type": "aws"}, "support": Obj{"plan": "basic", "timezone": "ET"},
		"serviceGroups": []any{group(3, 16, "data")}, "availability": Obj{"type": "multi"}}
	body := BuildUpdateBody(observed, Obj{"name": "one", "description": "new"}, false)
	if body["description"] != "new" || body["name"] != "one" {
		t.Fatalf("update body %+v", body)
	}
}

func TestBucketHelpers(t *testing.T) {
	observed := Obj{"name": "orders", "type": "couchbase", "storageBackend": "couchstore", "memoryAllocationInMb": int64(256),
		"replicas": int64(1), "durabilityLevel": "none", "flush": false, "timeToLiveInSeconds": int64(0), "conflictResolution": "seqno",
		"stats": Obj{"itemCount": int64(5)}}
	if _, ok := ObservedView(observed)["stats"]; ok {
		t.Fatal("stats must not be compared")
	}
	immutable, updatable := BucketChanges(Obj{"name": "orders", "memoryAllocationInMb": int64(512), "type": "ephemeral"}, observed, false)
	if len(immutable) != 1 || immutable[0].Path != "type" || len(updatable) != 1 || updatable[0].Path != "memoryAllocationInMb" {
		t.Fatalf("immutable %+v updatable %+v", immutable, updatable)
	}
	if got := BuildBucketUpdateBody(observed, Obj{"name": "orders", "memoryAllocationInMb": int64(512)}, false); intOf(got["memoryAllocationInMb"]) != 512 || got["replicas"] != int64(1) {
		t.Fatalf("update body %+v", got)
	}
}

func TestFreeTierClusterAndBucketFlow(t *testing.T) {
	e := newEnv(t, `apiVersion: cbctl/v1
project:
  name: sandbox
clusters:
  - name: free-1
    tier: free
    cloudProvider: { type: aws, region: us-east-2 }
    allowedCidrs:
      - cidr: 203.0.113.10
    buckets:
      - name: scratch
        memoryAllocationInMb: 100
        scopes:
          - name: app
            collections:
              - name: docs
`)
	p := e.plan()
	requireType(t, p, "cluster", "free-1", plan.Create)
	e.apply(p)
	if requests(e.f, "POST", `/clusters/freeTier$`) != 1 || requests(e.f, "POST", `/buckets/freeTier$`) != 1 {
		t.Fatalf("free-tier endpoints not used: %v", e.f.RequestLog())
	}
	requireAllNoop(t, e.plan())
}

func TestKeepsRequestOrderParentBeforeChild(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	order := map[string]int{}
	for i, r := range e.f.RequestLog() {
		if r.Method != "POST" {
			continue
		}
		for _, key := range []string{"/projects", "/clusters", "/buckets", "/scopes", "/collections", "/users", "/allowedcidrs"} {
			if strings.HasSuffix(r.Path, key) {
				if _, ok := order[key]; !ok {
					order[key] = i
				}
			}
		}
	}
	chain := []string{"/projects", "/clusters", "/buckets", "/scopes", "/collections"}
	for i := 1; i < len(chain); i++ {
		if order[chain[i-1]] >= order[chain[i]] {
			t.Fatalf("%s created after %s: %v", chain[i-1], chain[i], order)
		}
	}
}

func TestPlanErrorOnUnknownPinnedProject(t *testing.T) {
	var pe *plan.PlanError
	e := newEnv(t, strings.Replace(baseYAML, "name: sandbox", "name: sandbox\n  id: 00000000-0000-0000-0000-000000000000", 1))
	_, err := plan.BuildPlan(t.Context(), e.rc, BuildTree(), plan.Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.As(err, &pe) || !strings.Contains(pe.Error(), "not found") {
		t.Fatalf("error %v", err)
	}
}
