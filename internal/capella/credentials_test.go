package capella

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const fakeCredsPath = fakeClusterPath + "/users"

func TestCredentialsList(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    any
		want    int
		wantErr string
	}{
		{"two", 200, listPage(map[string]any{"id": "u1", "name": "app"}, map[string]any{"id": "u2", "name": "ro"}), 2, ""},
		{"missing is empty", 404, errBody(404), 0, ""},
		{"server refuses", 400, errBody(400), 0, "Credentials List Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.reply(http.MethodGet, fakeCredsPath, tt.status, tt.body)
			list, err := f.cluster().Credentials.List()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(list) != tt.want {
				t.Fatalf("List = %+v, %v; want %d", list, err, tt.want)
			}
		})
	}
}

func TestCredentialsCreate(t *testing.T) {
	t.Run("existing user is reused", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCredsPath, 200, listPage(map[string]any{"id": "u1", "name": "app"}))
		c := f.cluster().Credentials
		resp, err := c.CreateCredential("app", "pw", nil)
		if err != nil || resp.ID != "u1" || c.User == nil || c.User.ID != "u1" {
			t.Fatalf("CreateCredential = %+v, %v", resp, err)
		}
		if c.Username != "app" || c.Password != "pw" {
			t.Errorf("username/password = %q/%q", c.Username, c.Password)
		}
		if n := f.countCalls(http.MethodPost, fakeCredsPath); n != 0 {
			t.Errorf("POSTs = %d, want 0", n)
		}
	})
	t.Run("default access", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCredsPath, 200, listPage())
		f.reply(http.MethodPost, fakeCredsPath, 201, map[string]any{"id": "u9"})
		f.reply(http.MethodGet, fakeCredsPath+"/u9", 200, map[string]any{"id": "u9", "name": "app"})
		c := f.cluster().Credentials
		resp, err := c.CreateCredential("app", "pw", nil)
		if err != nil || resp.ID != "u9" || c.User.Name != "app" {
			t.Fatalf("CreateCredential = %+v, %v", resp, err)
		}
		var body CreateDatabaseCredentialRequest
		if err := json.Unmarshal(f.callsTo(http.MethodPost, fakeCredsPath)[0].Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Name != "app" || body.Password != "pw" || len(body.Access) != 1 {
			t.Fatalf("body = %+v", body)
		}
		a := body.Access[0]
		if strings.Join(a.Privileges, ",") != "data_reader,data_writer" || len(a.Resources.Buckets) != 1 || a.Resources.Buckets[0].Name != "*" {
			t.Errorf("default access = %+v", a)
		}
	})
	t.Run("explicit access", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCredsPath, 200, listPage())
		f.reply(http.MethodPost, fakeCredsPath, 201, map[string]any{"id": "u9"})
		f.reply(http.MethodGet, fakeCredsPath+"/u9", 200, map[string]any{"id": "u9", "name": "ro"})
		access := []DatabaseAccessEntry{{Privileges: []string{"data_reader"}, Resources: DatabaseResourceData{
			Buckets: []DatabaseResourceBucketData{{Name: "b", Scopes: []DatabaseResourceScopeData{{Name: "s"}}}}}}}
		if _, err := f.cluster().Credentials.CreateCredential("ro", "pw", access); err != nil {
			t.Fatal(err)
		}
		var body CreateDatabaseCredentialRequest
		if err := json.Unmarshal(f.callsTo(http.MethodPost, fakeCredsPath)[0].Body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Access) != 1 || body.Access[0].Privileges[0] != "data_reader" || body.Access[0].Resources.Buckets[0].Scopes[0].Name != "s" {
			t.Errorf("access = %+v", body.Access)
		}
	})
	tests := []struct {
		name    string
		setup   func(f *fakeAPI)
		wantErr string
	}{
		{"list fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCredsPath, 403, errBody(403))
		}, "Credentials List Error"},
		{"post fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCredsPath, 200, listPage())
			f.reply(http.MethodPost, fakeCredsPath, 422, errBody(422))
		}, "Credentials Create Error (HTTP 422)"},
		{"read back missing", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCredsPath, 200, listPage())
			f.reply(http.MethodPost, fakeCredsPath, 201, map[string]any{"id": "u9"})
			f.reply(http.MethodGet, fakeCredsPath+"/u9", 404, errBody(404))
		}, "database credential creation failed: Database credential not found"},
		{"read back fails", func(f *fakeAPI) {
			f.reply(http.MethodGet, fakeCredsPath, 200, listPage())
			f.reply(http.MethodPost, fakeCredsPath, 201, map[string]any{"id": "u9"})
			f.reply(http.MethodGet, fakeCredsPath+"/u9", 400, errBody(400))
		}, "Credentials Get Error (HTTP 400)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			c := f.cluster().Credentials
			_, err := c.CreateCredential("app", "pw", nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if c.User != nil {
				t.Errorf("User = %+v after a failure", c.User)
			}
		})
	}
}

func TestCredentialsAddAndGet(t *testing.T) {
	t.Run("add existing", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCredsPath, 200, listPage(map[string]any{"id": "u1", "name": "app"}))
		c := f.cluster().Credentials
		u, err := c.AddCredentials("app", "pw")
		if err != nil || u.ID != "u1" || c.User != u || c.Password != "pw" {
			t.Fatalf("AddCredentials = %+v, %v", u, err)
		}
		// Cached on the second lookup.
		if again, err := c.GetByName("app"); err != nil || again != u {
			t.Fatalf("GetByName cached = %+v, %v", again, err)
		}
		if again, err := c.GetByID("u1"); err != nil || again != u {
			t.Fatalf("GetByID cached = %+v, %v", again, err)
		}
		if n := f.countCalls(http.MethodGet, fakeCredsPath); n != 1 {
			t.Errorf("list GETs = %d, want 1", n)
		}
	})
	t.Run("add missing", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCredsPath, 200, listPage())
		c := f.cluster().Credentials
		_, err := c.AddCredentials("app", "pw")
		var nf *CapellaNotFoundError
		if !AsCapellaNotFound(err, &nf) || nf.Message != "Can not find user app" {
			t.Fatalf("err = %v, want not found", err)
		}
		if c.User != nil {
			t.Error("User set for a missing credential")
		}
	})
	t.Run("get by name list error", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeCredsPath, 500, errBody(500))
		if _, err := f.cluster().Credentials.GetByName("app"); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestCredentialsDelete(t *testing.T) {
	t.Run("nothing selected", func(t *testing.T) {
		f := newFakeAPI(t)
		if err := f.cluster().Credentials.Delete(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("deletes", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodDelete, fakeCredsPath+"/u1", 204, nil)
		c := f.cluster().Credentials
		c.User = &CredentialData{ID: "u1"}
		if err := c.Delete(); err != nil {
			t.Fatal(err)
		}
		if c.User != nil || f.countCalls(http.MethodDelete, fakeCredsPath+"/u1") != 1 {
			t.Error("credential not deleted")
		}
	})
	t.Run("failure", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodDelete, fakeCredsPath+"/u1", 400, errBody(400))
		c := f.cluster().Credentials
		c.User = &CredentialData{ID: "u1"}
		if err := c.Delete(); err == nil || !strings.Contains(err.Error(), "Credentials Delete Error (HTTP 400)") {
			t.Fatalf("err = %v", err)
		}
		if c.User == nil {
			t.Error("User cleared after a failed delete")
		}
	})
}

func TestCertificate(t *testing.T) {
	const certPath = fakeClusterPath + "/certificates"
	t.Run("fetches PEM", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, certPath, 200, map[string]any{"certificate": "-----BEGIN CERTIFICATE-----\nX\n"})
		c := f.cluster().Certificate
		pem, err := c.GetClusterCertificate()
		if err != nil || !strings.HasPrefix(pem, "-----BEGIN") || c.CertificatePEM != pem {
			t.Fatalf("GetClusterCertificate = %q, %v", pem, err)
		}
	})
	t.Run("error", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, certPath, 403, errBody(403))
		c := f.cluster().Certificate
		_, err := c.GetClusterCertificate()
		if err == nil || !strings.Contains(err.Error(), "Cluster Certificate Error (HTTP 403)") {
			t.Fatalf("err = %v", err)
		}
	})
}
