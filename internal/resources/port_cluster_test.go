package resources

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
)

// fiveByEight seeds a healthy cluster of 5 data nodes with 8 GB each: the fake reports half the
// RAM as bucket quota, 20,480 MB in total, as in capellactl's guard fixture. One bucket allocates
// allocMB with one replica.
func fiveByEight(t *testing.T, e *env, pid, name string, allocMB int) string {
	t.Helper()
	cid := e.f.SeedCluster(pid, name, "healthy", capellafake.WithField("serviceGroups", []any{map[string]any{
		"numOfNodes": 5, "services": []any{"data", "index", "query"},
		"node": map[string]any{"compute": map[string]any{"cpu": 4, "ram": 8}, "disk": map[string]any{"type": "gp3", "storage": 50, "iops": 3000}},
	}}))
	e.f.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", allocMB), capellafake.WithField("replicas", 1))
	return cid
}

// TestScaleInGuard_CAP016_AC1_MemoryMath: 12,288 MB of buckets fit in 4/5 of 20,480 MB, and the
// checks line gives both numbers and the replica requirement.
func TestScaleInGuard_CAP016_AC1_MemoryMath(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	cid := fiveByEight(t, e, pid, "one", 12288)
	blocked, checks, err := Check(t.Context(), e.rc, pid, cid, []Obj{group(5, 8, "data", "index", "query")}, []Obj{group(4, 8, "data", "index", "query")})
	if err != nil || len(blocked) != 0 {
		t.Fatalf("blocked %v err %v", blocked, err)
	}
	if want := "checks: memory ok (12,288 / ~16,384 MB) · replicas ok (max 1, needs 2 data nodes)"; checks != want {
		t.Fatalf("checks = %q, want %q", checks, want)
	}
}

// TestScaleInGuard_CAP016_AC2_BoundaryEqualAllowed: allocated == projected is allowed; one MB
// more is blocked.
func TestScaleInGuard_CAP016_AC2_BoundaryEqualAllowed(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	old, updated := []Obj{group(5, 8, "data", "index", "query")}, []Obj{group(4, 8, "data", "index", "query")}

	equal := fiveByEight(t, e, pid, "equal", 16384)
	blocked, checks, err := Check(t.Context(), e.rc, pid, equal, old, updated)
	if err != nil || len(blocked) != 0 || !strings.Contains(checks, "memory ok (16,384 / ~16,384 MB)") {
		t.Fatalf("equal: blocked %v checks %q err %v", blocked, checks, err)
	}

	over := fiveByEight(t, e, pid, "over", 16385)
	blocked, _, err = Check(t.Context(), e.rc, pid, over, old, updated)
	if err != nil || len(blocked) != 1 || !strings.Contains(blocked[0], "buckets allocate 16,385 MB") {
		t.Fatalf("over: blocked %v err %v", blocked, err)
	}
}

// TestScaleInGuard_CAP016_AC5_NoDataLeftWhateverFlags: no flag lets the last data group go.
func TestScaleInGuard_CAP016_AC5_NoDataLeftWhateverFlags(t *testing.T) {
	e := newEnv(t, baseYAML)
	pid := e.f.SeedProject("sandbox")
	cid := e.f.SeedCluster(pid, "one", "healthy")
	e.rc.AllowServiceRemoval, e.rc.AllowServiceGroupRemoval, e.rc.SkipCapacityCheck = true, true, true
	blocked, _, err := Check(t.Context(), e.rc, pid, cid, []Obj{group(3, 16, "data", "index", "query")}, []Obj{group(3, 16, "index", "query")})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"scale-in blocked: removing the last group that runs data"}; !reflect.DeepEqual(blocked, want) {
		t.Fatalf("blocked = %v, want %v", blocked, want)
	}
}

// TestScalingClassify_CAP017_AC1_Vertical: more CPU and RAM per node is a "scale up" and nothing
// else.
func TestScalingClassify_CAP017_AC1_Vertical(t *testing.T) {
	g := Obj{"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)}}, "numOfNodes": int64(3), "services": []any{"data"}}
	up := Obj{"node": Obj{"compute": Obj{"cpu": int64(8), "ram": int64(32)}}, "numOfNodes": int64(3), "services": []any{"data"}}
	_, blocked, labels := ServiceGroupChanges(Obj{"serviceGroups": []any{up}}, Obj{"serviceGroups": []any{g}})
	if len(blocked) != 0 || !reflect.DeepEqual(labels, map[string]bool{"scale up": true}) {
		t.Fatalf("blocked %v labels %v", blocked, labels)
	}
}

// TestBuildUpdateBody_CAP012_AC1_Overlay: the PUT body is the observed cluster with the file
// overlaid: support is merged key by key and the observed service groups are kept.
func TestBuildUpdateBody_CAP012_AC1_Overlay(t *testing.T) {
	groups := []any{Obj{"node": Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)}}, "numOfNodes": int64(3), "services": []any{"data"}}}
	observed := Obj{"name": "c", "description": "d", "support": Obj{"plan": "basic", "timezone": "ET"}, "serviceGroups": groups}
	body := BuildUpdateBody(observed, Obj{"name": "c", "support": Obj{"plan": "enterprise"}}, false)
	want := Obj{"name": "c", "description": "d", "support": Obj{"plan": "enterprise", "timezone": "ET"}, "serviceGroups": groups}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v\nwant   %#v", body, want)
	}
}

// TestBuildUpdateBody_CAP012_AC2_PreservesUnsetFields: a field the file leaves out keeps its
// observed value.
func TestBuildUpdateBody_CAP012_AC2_PreservesUnsetFields(t *testing.T) {
	observed := Obj{"name": "c", "description": "Prod", "support": Obj{"plan": "basic"}, "serviceGroups": []any{}}
	if got := BuildUpdateBody(observed, Obj{"name": "c", "support": Obj{"plan": "enterprise"}}, false)["description"]; got != "Prod" {
		t.Fatalf("description = %v, want Prod", got)
	}
}
