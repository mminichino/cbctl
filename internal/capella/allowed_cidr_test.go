package capella

import (
	"net/http"
	"strings"
	"testing"
)

const fakeCIDRsPath = fakeClusterPath + "/allowedcidrs"

func TestAllowedCIDRsList(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    any
		want    []string
		wantErr string
	}{
		{"entries", 200, listPage(map[string]any{"id": "a1", "cidr": "10.0.0.0/24"}, map[string]any{"id": "a2", "cidr": "1.2.3.4/32"}), []string{"10.0.0.0/24", "1.2.3.4/32"}, ""},
		{"empty", 200, listPage(), nil, ""},
		{"missing is empty", 404, errBody(404), nil, ""},
		{"server refuses", 400, errBody(400), nil, "Allowed CIDR List Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.reply(http.MethodGet, fakeCIDRsPath, tt.status, tt.body)
			list, err := f.cluster().AllowedCIDR.List()
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
				t.Fatalf("list = %+v, want %v", list, tt.want)
			}
			for i, w := range tt.want {
				if list[i].CIDR != w {
					t.Errorf("list[%d] = %q, want %q", i, list[i].CIDR, w)
				}
			}
		})
	}
}

func TestAllowedCIDRsFindByCIDR(t *testing.T) {
	f := newFakeAPI(t)
	f.reply(http.MethodGet, fakeCIDRsPath, 200, listPage(map[string]any{"id": "a1", "cidr": "10.0.0.0/24"}))
	a := f.cluster().AllowedCIDR
	got, err := a.FindByCIDR("10.0.0.0/24")
	if err != nil || got == nil || got.ID != "a1" {
		t.Fatalf("FindByCIDR = %+v, %v; want a1", got, err)
	}
	got, err = a.FindByCIDR("192.168.0.0/16")
	if err != nil || got != nil {
		t.Fatalf("FindByCIDR(missing) = %+v, %v; want nil, nil", got, err)
	}
}

func TestAllowedCIDRsCreate(t *testing.T) {
	t.Run("already allowed is reused", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCIDRsPath, 200, listPage(map[string]any{"id": "a1", "cidr": "10.0.0.0/24"}))
		a := f.cluster().AllowedCIDR
		got, err := a.CreateAllowedCIDR("10.0.0.0/24")
		if err != nil || got.ID != "a1" || a.CIDR != got {
			t.Fatalf("CreateAllowedCIDR = %+v, %v", got, err)
		}
		if n := f.countCalls(http.MethodPost, fakeCIDRsPath); n != 0 {
			t.Errorf("POSTs = %d, want 0", n)
		}
	})
	t.Run("posts and reads back", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCIDRsPath, 200, listPage())
		f.reply(http.MethodPost, fakeCIDRsPath, 201, map[string]any{"id": "new"})
		f.reply(http.MethodGet, fakeCIDRsPath+"/new", 200, map[string]any{"id": "new", "cidr": "1.2.3.4/32", "status": "active"})
		a := f.cluster().AllowedCIDR
		got, err := a.CreateAllowedCIDR("1.2.3.4/32")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "new" || got.Status != "active" || a.CIDR != got {
			t.Errorf("created = %+v", got)
		}
		posts := f.callsTo(http.MethodPost, fakeCIDRsPath)
		if len(posts) != 1 {
			t.Fatalf("POSTs = %d, want 1", len(posts))
		}
		body := decodeBody(t, posts[0])
		if body["cidr"] != "1.2.3.4/32" || body["comment"] != "Automatically Created Allowed CIDR Block" {
			t.Errorf("POST body = %v", body)
		}
		// A second GetByID for the same id is served from the cache.
		if _, err := a.GetByID("new"); err != nil {
			t.Fatal(err)
		}
		if n := f.countCalls(http.MethodGet, fakeCIDRsPath+"/new"); n != 1 {
			t.Errorf("GET by id = %d, want 1 (cached)", n)
		}
	})
	tests := []struct {
		name    string
		setup   func(f *fakeAPI)
		wantErr string
	}{
		{"list fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCIDRsPath, 403, errBody(403))
		}, "Allowed CIDR List Error"},
		{"post fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCIDRsPath, 200, listPage())
			f.reply(http.MethodPost, fakeCIDRsPath, 422, errBody(422))
		}, "Allowed CIDR Create Error (HTTP 422)"},
		{"read back missing", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCIDRsPath, 200, listPage())
			f.reply(http.MethodPost, fakeCIDRsPath, 201, map[string]any{"id": "new"})
			f.reply(http.MethodGet, fakeCIDRsPath+"/new", 404, errBody(404))
		}, "allowed CIDR creation failed: CIDR not found"},
		{"read back fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCIDRsPath, 200, listPage())
			f.reply(http.MethodPost, fakeCIDRsPath, 201, map[string]any{"id": "new"})
			f.reply(http.MethodGet, fakeCIDRsPath+"/new", 400, errBody(400))
		}, "Allowed CIDR Get Error (HTTP 400)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			a := f.cluster().AllowedCIDR
			_, err := a.CreateAllowedCIDR("1.2.3.4/32")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if a.CIDR != nil {
				t.Errorf("CIDR = %+v after a failure, want nil", a.CIDR)
			}
		})
	}
}

func TestAllowedCIDRsDelete(t *testing.T) {
	t.Run("nothing selected is a no-op", func(t *testing.T) {
		f := newFakeAPI(t)
		if err := f.cluster().AllowedCIDR.Delete(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("deletes the current entry", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodDelete, fakeCIDRsPath+"/a1", 204, nil)
		a := f.cluster().AllowedCIDR
		a.CIDR = &AllowedCIDRData{ID: "a1"}
		if err := a.Delete(); err != nil {
			t.Fatal(err)
		}
		if a.CIDR != nil || f.countCalls(http.MethodDelete, fakeCIDRsPath+"/a1") != 1 {
			t.Errorf("CIDR = %+v, DELETEs = %d", a.CIDR, f.countCalls(http.MethodDelete, fakeCIDRsPath+"/a1"))
		}
	})
	t.Run("failure keeps the entry", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodDelete, fakeCIDRsPath+"/a1", 409, errBody(409))
		a := f.cluster().AllowedCIDR
		a.CIDR = &AllowedCIDRData{ID: "a1"}
		err := a.Delete()
		if err == nil || !strings.Contains(err.Error(), "Allowed CIDR Delete Error (HTTP 409)") {
			t.Fatalf("err = %v", err)
		}
		if a.CIDR == nil {
			t.Error("CIDR cleared after a failed delete")
		}
	})
}
