package resources

import (
	"reflect"
	"testing"

	"github.com/mminichino/cbctl/internal/plan"
)

// TestBucketBody_CAP023_AC3_Overlay: the PUT body is observed overlaid with desired; flush is sent
// as flushEnabled, priority/durability/ttl are kept, and id/name/type/stats are dropped.
func TestBucketBody_CAP023_AC3_Overlay(t *testing.T) {
	observed := Obj{"id": "b3JkZXJz", "name": "orders", "type": "couchbase", "memoryAllocationInMb": int64(1024),
		"durabilityLevel": "majority", "replicas": int64(2), "timeToLiveInSeconds": int64(600), "flush": true,
		"priority": int64(4), "stats": Obj{"itemCount": int64(3)}}
	got := BuildBucketUpdateBody(observed, Obj{"name": "orders", "memoryAllocationInMb": int64(2048), "type": "couchbase"}, false)
	want := Obj{"memoryAllocationInMb": int64(2048), "durabilityLevel": "majority", "replicas": int64(2),
		"timeToLiveInSeconds": int64(600), "flushEnabled": true, "priority": int64(4)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v\nwant %#v", got, want)
	}
}

// TestBucketSummary_CAP023_AC1_CreateSummary: the plan summary of a bucket create.
func TestBucketSummary_CAP023_AC1_CreateSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    Obj
		free bool
		want string
	}{
		{"couchbase", Obj{"type": "couchbase", "storageBackend": "magma", "memoryAllocationInMb": int64(1024),
			"replicas": int64(1), "durabilityLevel": "majority"}, false, "couchbase · magma · 1024 MB · 1 replica · durability majority"},
		{"ephemeral", Obj{"type": "ephemeral", "memoryAllocationInMb": int64(256), "replicas": int64(2)}, false, "ephemeral · 256 MB · 2 replicas"},
		{"free tier", Obj{"memoryAllocationInMb": int64(100)}, true, "free tier · 100 MB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BucketCreateSummary(tc.d, tc.free); got != tc.want {
				t.Errorf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

// collectionScope is a scope action under a cluster running version ("" = unknown).
func collectionScope(version string) *plan.Action {
	body := Obj{"name": "nova-dev-01"}
	if version != "" {
		body["couchbaseServer"] = Obj{"version": version}
	}
	cluster := &plan.Action{Kind: kindCluster, Key: "nova-dev-01", Observed: &plan.Observed{ID: "c-1", Body: body}}
	bucket := &plan.Action{Kind: kindBucket, Key: "nova-dev-01/orders", Parent: cluster}
	return &plan.Action{Kind: kindScope, Key: "nova-dev-01/orders/inventory", Desired: Obj{"name": "inventory"}, Parent: bucket}
}

// TestCollections_CAP025_AC3_VersionGate: changing maxTTL is blocked before 7.6.0, allowed from
// 7.6.0 (also with a build suffix) and when the version is unknown.
func TestCollections_CAP025_AC3_VersionGate(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    plan.ActionType
	}{
		{"7.2.4", plan.Blocked},
		{"7.6.0", plan.Update},
		{"7.6.2-1234", plan.Update},
		{"", plan.Update},
	} {
		t.Run("version "+tc.version, func(t *testing.T) {
			obs := &plan.Observed{ID: "s", Body: Obj{"name": "s", "maxTTL": int64(0)}}
			a, err := newCollection().Diff(t.Context(), nil, Obj{"name": "s", "maxTTL": int64(-1)}, obs, collectionScope(tc.version))
			if err != nil {
				t.Fatal(err)
			}
			if a.Type != tc.want {
				t.Errorf("type = %s, want %s (%s)", a.Type, tc.want, a.Reason)
			}
		})
	}
}

// TestCollections_CAP025_AC1_CreateSummary: a create shows "maxTTL N" only when maxTTL is declared.
func TestCollections_CAP025_AC1_CreateSummary(t *testing.T) {
	rec := newCollection()
	a, err := rec.Diff(t.Context(), nil, Obj{"name": "items"}, nil, collectionScope("8.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != plan.Create || a.Summary != "" {
		t.Errorf("no maxTTL: %s %q", a.Type, a.Summary)
	}
	if a, _ = rec.Diff(t.Context(), nil, Obj{"name": "s", "maxTTL": int64(-1)}, nil, collectionScope("8.0.1")); a.Summary != "maxTTL -1" {
		t.Errorf("summary = %q, want maxTTL -1", a.Summary)
	}
}

// TestCredentialNormalize_CAP027_AC3_GroupingDoesNotMatter: two grants on one bucket equal one
// grant with both privileges.
func TestCredentialNormalize_CAP027_AC3_GroupingDoesNotMatter(t *testing.T) {
	split := []any{
		Obj{"privileges": []any{"data_reader"}, "resources": Obj{"buckets": []any{Obj{"name": "orders"}}}},
		Obj{"privileges": []any{"data_writer"}, "resources": Obj{"buckets": []any{Obj{"name": "orders"}}}},
	}
	merged := []any{Obj{"privileges": []any{"read", "write"}, "resources": Obj{"buckets": []any{Obj{"name": "orders"}}}}}
	if ch := AccessChanges(split, merged); len(ch) != 0 {
		t.Fatalf("changes = %+v, want none", ch)
	}
}

// TestCredential_CAP027_AC4_ChangesPerResource: access changes are listed one line per resource.
func TestCredential_CAP027_AC4_ChangesPerResource(t *testing.T) {
	inv := Obj{"buckets": []any{Obj{"name": "orders", "scopes": []any{Obj{"name": "inv"}}}}}
	before := []any{Obj{"privileges": []any{"data_reader"}, "resources": inv}}
	after := []any{Obj{"privileges": []any{"data_reader", "data_writer"}, "resources": inv}, Obj{"privileges": []any{"read"}}}
	got := AccessChanges(after, before)
	type row struct {
		path          string
		before, after any
	}
	var rows []row
	for _, c := range got {
		rows = append(rows, row{c.Path, c.Before, c.After})
	}
	want := []row{{"orders/inv", "read", "read+write"}, {"(all buckets)", nil, "read"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("changes = %+v\nwant %+v", rows, want)
	}
}
