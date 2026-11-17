package capella

import (
	"net/http"
	"strings"
	"testing"
)

const fakeBucketsPath = fakeClusterPath + "/buckets"

func TestDefaultCreateBucketSettings(t *testing.T) {
	got := DefaultCreateBucketSettings("b")
	want := CreateBucketSettings{Name: "b", Type: "couchbase", StorageBackend: "couchstore", MemoryAllocationInMb: 128,
		BucketConflictResolution: "seqno", DurabilityLevel: "none", Replicas: 1}
	if got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
}

func TestBucketsList(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    any
		want    []string
		wantErr string
	}{
		{"two", 200, map[string]any{"data": []any{map[string]any{"id": "b1", "name": "one"}, map[string]any{"id": "b2", "name": "two"}}}, []string{"one", "two"}, ""},
		{"empty", 200, map[string]any{"data": []any{}}, nil, ""},
		{"not found is an error", 404, errBody(404), nil, "Bucket List Error (HTTP 404)"},
		{"bad JSON", 200, "{", nil, "Bucket List Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.reply(http.MethodGet, fakeBucketsPath, tt.status, tt.body)
			list, err := f.cluster().Buckets.List()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != len(tt.want) {
				t.Fatalf("list = %+v", list)
			}
			for i, w := range tt.want {
				if list[i].Name != w {
					t.Errorf("list[%d] = %q, want %q", i, list[i].Name, w)
				}
			}
		})
	}
}

func TestBucketsCreate(t *testing.T) {
	t.Run("name required", func(t *testing.T) {
		f := newFakeAPI(t)
		if _, err := f.cluster().Buckets.Create(CreateBucketSettings{}); err == nil {
			t.Fatal("want an error for an empty name")
		}
	})
	t.Run("existing bucket is reused", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{map[string]any{"id": "b1", "name": "app"}}})
		b := f.cluster().Buckets
		got, err := b.Create(DefaultCreateBucketSettings("app"))
		if err != nil || got.ID != "b1" || b.Bucket != got {
			t.Fatalf("Create = %+v, %v", got, err)
		}
		if n := f.countCalls(http.MethodPost, fakeBucketsPath); n != 0 {
			t.Errorf("POSTs = %d, want 0", n)
		}
	})
	t.Run("zero settings get defaults", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
		f.reply(http.MethodPost, fakeBucketsPath, 201, map[string]any{"id": "b9"})
		f.reply(http.MethodGet, fakeBucketsPath+"/b9", 200, map[string]any{"id": "b9", "name": "app", "memoryAllocationInMb": 128})
		b := f.cluster().Buckets
		got, err := b.Create(CreateBucketSettings{Name: "app", DurabilityLevel: "", Replicas: 2, Flush: true, TimeToLiveInSeconds: 60})
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "b9" || b.Bucket != got {
			t.Errorf("created = %+v", got)
		}
		posts := f.callsTo(http.MethodPost, fakeBucketsPath)
		if len(posts) != 1 {
			t.Fatalf("POSTs = %d", len(posts))
		}
		body := decodeBody(t, posts[0])
		want := map[string]any{"name": "app", "type": "couchbase", "storageBackend": "couchstore", "memoryAllocationInMb": 128.0,
			"bucketConflictResolution": "seqno", "durabilityLevel": "none", "replicas": 2.0, "flush": true, "timeToLiveInSeconds": 60.0}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("body[%s] = %v, want %v", k, body[k], v)
			}
		}
	})
	t.Run("explicit settings are sent, durability lowercased", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
		f.reply(http.MethodPost, fakeBucketsPath, 201, map[string]any{"id": "b9"})
		f.reply(http.MethodGet, fakeBucketsPath+"/b9", 200, map[string]any{"id": "b9", "name": "eph"})
		s := CreateBucketSettings{Name: "eph", Type: "ephemeral", StorageBackend: "magma", MemoryAllocationInMb: 512,
			BucketConflictResolution: "lww", DurabilityLevel: "Majority"}
		if _, err := f.cluster().Buckets.Create(s); err != nil {
			t.Fatal(err)
		}
		body := decodeBody(t, f.callsTo(http.MethodPost, fakeBucketsPath)[0])
		want := map[string]any{"type": "ephemeral", "storageBackend": "magma", "memoryAllocationInMb": 512.0,
			"bucketConflictResolution": "lww", "durabilityLevel": "majority"}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("body[%s] = %v, want %v", k, body[k], v)
			}
		}
	})
	tests := []struct {
		name    string
		setup   func(f *fakeAPI)
		wantErr string
	}{
		{"list fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeBucketsPath, 401, errBody(401))
		}, "Bucket List Error (HTTP 401)"},
		{"post fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
			f.reply(http.MethodPost, fakeBucketsPath, 422, errBody(422))
		}, "Bucket Create Error (HTTP 422)"},
		{"read back missing", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
			f.reply(http.MethodPost, fakeBucketsPath, 201, map[string]any{"id": "b9"})
			f.reply(http.MethodGet, fakeBucketsPath+"/b9", 404, errBody(404))
		}, "bucket creation failed: Bucket not found"},
		{"read back fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
			f.reply(http.MethodPost, fakeBucketsPath, 201, map[string]any{"id": "b9"})
			f.reply(http.MethodGet, fakeBucketsPath+"/b9", 400, errBody(400))
		}, "Bucket Get Error (HTTP 400)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			b := f.cluster().Buckets
			_, err := b.Create(DefaultCreateBucketSettings("app"))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if b.Bucket != nil {
				t.Errorf("Bucket = %+v after a failure", b.Bucket)
			}
		})
	}
}

func TestBucketsGetByName(t *testing.T) {
	t.Run("cached", func(t *testing.T) {
		f := newFakeAPI(t)
		b := f.cluster().Buckets
		b.Bucket = &CapellaBucketData{ID: "b1", Name: "app"}
		got, err := b.GetByName("app")
		if err != nil || got != b.Bucket {
			t.Fatalf("GetByName = %+v, %v", got, err)
		}
	})
	t.Run("listed", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{map[string]any{"id": "b1", "name": "app"}}})
		got, err := f.cluster().Buckets.GetByName("app")
		if err != nil || got.ID != "b1" {
			t.Fatalf("GetByName = %+v, %v", got, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 200, map[string]any{"data": []any{}})
		_, err := f.cluster().Buckets.GetByName("app")
		var nf *CapellaNotFoundError
		if !AsCapellaNotFound(err, &nf) || nf.Message != "Can not find bucket app" {
			t.Fatalf("err = %v, want not found", err)
		}
	})
	t.Run("list fails", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeBucketsPath, 403, errBody(403))
		if _, err := f.cluster().Buckets.GetByName("app"); err == nil || IsNotFound(err) {
			t.Fatalf("err = %v, want a non-404 error", err)
		}
	})
}

func TestBucketsDelete(t *testing.T) {
	t.Run("nothing selected", func(t *testing.T) {
		f := newFakeAPI(t)
		if err := f.cluster().Buckets.Delete(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("deletes", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodDelete, fakeBucketsPath+"/b1", 204, nil)
		b := f.cluster().Buckets
		b.Bucket = &CapellaBucketData{ID: "b1"}
		if err := b.Delete(); err != nil {
			t.Fatal(err)
		}
		if b.Bucket != nil || f.countCalls(http.MethodDelete, fakeBucketsPath+"/b1") != 1 {
			t.Error("bucket not deleted")
		}
	})
	t.Run("failure", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodDelete, fakeBucketsPath+"/b1", 409, errBody(409))
		b := f.cluster().Buckets
		b.Bucket = &CapellaBucketData{ID: "b1"}
		if err := b.Delete(); err == nil || !strings.Contains(err.Error(), "Bucket Delete Error (HTTP 409)") {
			t.Fatalf("err = %v", err)
		}
		if b.Bucket == nil {
			t.Error("Bucket cleared after a failed delete")
		}
	})
}
