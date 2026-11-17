package capella

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// userBody is a Capella org user with project p1 in its resources.
func userBody(projects ...string) map[string]any {
	res := []any{map[string]any{"type": "organization", "id": "org-1"}}
	for _, p := range projects {
		res = append(res, map[string]any{"type": "project", "id": p})
	}
	return map[string]any{"id": "u1", "email": "dev@example.com", "resources": res}
}

func TestNewOrganization(t *testing.T) {
	t.Run("by id", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeOrgPath, 200, map[string]any{"id": "org-1", "name": "acme"})
		o, err := NewOrganization(f.client())
		if err != nil || o.Organization.Name != "acme" {
			t.Fatalf("NewOrganization = %+v, %v", o, err)
		}
		if got, err := o.GetByID("org-1"); err != nil || got != o.Organization {
			t.Errorf("cached GetByID = %+v, %v", got, err)
		}
		if got, err := o.GetByName("acme"); err != nil || got != o.Organization {
			t.Errorf("cached GetByName = %+v, %v", got, err)
		}
	})
	orgList := map[string]any{"data": []any{map[string]any{"id": "o1", "name": "first"}, map[string]any{"id": "o2", "name": "second"}}}
	tests := []struct {
		name    string
		orgID   string
		orgName string
		setup   func(f *fakeAPI)
		wantID  string
		wantErr string
	}{
		{"id missing", "org-1", "", func(f *fakeAPI) { f.reply(http.MethodGet, fakeOrgPath, 404, errBody(404)) }, "", "Organization ID not found"},
		{"id refused", "org-1", "", func(f *fakeAPI) { f.reply(http.MethodGet, fakeOrgPath, 403, errBody(403)) }, "", "Organization Get Error (HTTP 403)"},
		{"by name", "", "second", func(f *fakeAPI) { f.reply(http.MethodGet, organizationsPath, 200, orgList) }, "o2", ""},
		{"name missing", "", "third", func(f *fakeAPI) { f.reply(http.MethodGet, organizationsPath, 200, orgList) }, "", "Can not find organization third"},
		{"name list fails", "", "x", func(f *fakeAPI) { f.reply(http.MethodGet, organizationsPath, 401, errBody(401)) }, "", "Organization List Error (HTTP 401)"},
		{"first of list", "", "", func(f *fakeAPI) { f.reply(http.MethodGet, organizationsPath, 200, orgList) }, "o1", ""},
		{"empty list", "", "", func(f *fakeAPI) {
			f.reply(http.MethodGet, organizationsPath, 200, map[string]any{"data": []any{}})
		}, "", "cannot find the Capella organization"},
		{"list fails", "", "", func(f *fakeAPI) { f.reply(http.MethodGet, organizationsPath, 401, errBody(401)) }, "", "cannot find the Capella organization: Organization List Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			c := f.client()
			c.OrganizationID = tt.orgID
			c.OrganizationName = tt.orgName
			o, err := NewOrganization(c)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || o.Organization.ID != tt.wantID {
				t.Fatalf("NewOrganization = %+v, %v; want %s", o, err, tt.wantID)
			}
		})
	}
}

func TestOrganizationProjects(t *testing.T) {
	t.Run("GetProjectByID fetches once", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeProjectPath, 200, map[string]any{"id": "p1", "name": "proj"})
		o := f.organization()
		p, err := o.GetProjectByID("p1")
		if err != nil || p.Name() != "proj" || p.ID() != "p1" {
			t.Fatalf("GetProjectByID = %+v, %v", p, err)
		}
		again, err := o.GetProjectByID("p1")
		if err != nil || again != p || o.FindProjectByName("proj") != p {
			t.Fatalf("cache miss: %+v, %v", again, err)
		}
		if n := f.countCalls(http.MethodGet, fakeProjectPath); n != 1 {
			t.Errorf("GETs = %d, want 1", n)
		}
	})
	t.Run("GetProjectByID missing", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeProjectPath, 404, errBody(404))
		if _, err := f.organization().GetProjectByID("p1"); err == nil || !strings.Contains(err.Error(), "Project ID not found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("FetchProject refused", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodGet, fakeProjectPath, 400, errBody(400))
		if _, err := f.organization().FetchProject("p1"); err == nil || !strings.Contains(err.Error(), "Project Get Error (HTTP 400)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("RegisterProject ignores unresolved", func(t *testing.T) {
		f := newFakeAPI(t)
		o := f.organization()
		o.RegisterProject(nil)
		o.RegisterProject(NewProject(o, "x", nil))
		if len(o.byID) != 0 || len(o.byName) != 0 {
			t.Errorf("cache = %v / %v", o.byID, o.byName)
		}
	})
	t.Run("configDefaultProject", func(t *testing.T) {
		if got := configDefaultProject(nil); got != "default" {
			t.Errorf("nil = %q", got)
		}
		if got := configDefaultProject(&Client{ProjectName: "p"}); got != "p" {
			t.Errorf("named = %q", got)
		}
	})
}

func TestNewUser(t *testing.T) {
	users := listPage(map[string]any{"id": "u0", "email": "other@example.com"}, userBody("p1"))
	tests := []struct {
		name    string
		email   string
		id      string
		setup   func(f *fakeAPI)
		wantErr string
		unc     bool
	}{
		{"by email", "dev@example.com", "", func(f *fakeAPI) { f.reply(http.MethodGet, fakeUsersPath, 200, users) }, "", false},
		{"email unknown", "nobody@example.com", "", func(f *fakeAPI) { f.reply(http.MethodGet, fakeUsersPath, 200, users) }, "no user for email: nobody@example.com", false},
		{"email list fails", "dev@example.com", "", func(f *fakeAPI) { f.reply(http.MethodGet, fakeUsersPath, 403, errBody(403)) }, "User List Error", false},
		{"by id", "", "u1", func(f *fakeAPI) { f.reply(http.MethodGet, fakeUsersPath+"/u1", 200, userBody("p1")) }, "", false},
		{"id missing", "", "u1", func(f *fakeAPI) { f.reply(http.MethodGet, fakeUsersPath+"/u1", 404, errBody(404)) }, "User ID not found", false},
		{"id refused", "", "u1", func(f *fakeAPI) { f.reply(http.MethodGet, fakeUsersPath+"/u1", 400, errBody(400)) }, "User Get Error (HTTP 400)", false},
		{"not configured", "", "", func(*fakeAPI) {}, "Capella user not configured", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			o := f.organization()
			o.Client.AccountEmail = tt.email
			o.Client.AccountID = tt.id
			u, err := NewUser(o)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				var unc *UserNotConfiguredError
				if AsUserNotConfigured(err, &unc) != tt.unc {
					t.Errorf("AsUserNotConfigured = %v, want %v", !tt.unc, tt.unc)
				}
				return
			}
			if err != nil || u.User.ID != "u1" || u.Email != "dev@example.com" {
				t.Fatalf("NewUser = %+v, %v", u, err)
			}
			if ids := u.ProjectIDs(); len(ids) != 1 || ids[0] != "p1" {
				t.Errorf("ProjectIDs = %v", ids)
			}
		})
	}
}

func TestUserHelpers(t *testing.T) {
	f := newFakeAPI(t)
	u := &User{Org: f.organization()}
	if u.ProjectIDs() != nil {
		t.Error("ProjectIDs of an unresolved user is not nil")
	}
	if _, err := u.GetByEmail(""); err == nil || err.Error() != "no email configured" {
		t.Errorf("GetByEmail(\"\") = %v", err)
	}
	if err := u.SetProjectOwnership("p1"); err == nil || !strings.Contains(err.Error(), "User not resolved") {
		t.Errorf("SetProjectOwnership unresolved = %v", err)
	}
	u.User = &CapellaOrgUserData{ID: "u1", Email: "dev@example.com"}
	u.Email = "dev@example.com"
	if got, err := u.GetByEmail(""); err != nil || got != u.User {
		t.Errorf("cached GetByEmail = %+v, %v", got, err)
	}
	if got, err := u.GetByID("u1"); err != nil || got != u.User {
		t.Errorf("cached GetByID = %+v, %v", got, err)
	}
	if AsUserNotConfigured(nil, nil) {
		t.Error("AsUserNotConfigured(nil) = true")
	}
	var unc *UserNotConfiguredError
	if AsUserNotConfigured(apiError(1, "", "m", nil), &unc) {
		t.Error("AsUserNotConfigured matched an API error with a nil cause")
	}
}

func TestSetProjectOwnership(t *testing.T) {
	t.Run("patches", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodPatch, fakeUsersPath+"/u1", 200, nil)
		u := &User{Org: f.organization(), User: &CapellaOrgUserData{ID: "u1"}}
		if err := u.SetProjectOwnership("p9"); err != nil {
			t.Fatal(err)
		}
		var ops []JSONPatchOp
		if err := json.Unmarshal(f.callsTo(http.MethodPatch, fakeUsersPath+"/u1")[0].Body, &ops); err != nil {
			t.Fatal(err)
		}
		v, _ := ops[0].Value.(map[string]any)
		if len(ops) != 1 || ops[0].Op != "add" || ops[0].Path != "/resources/p9" || v["id"] != "p9" || v["type"] != "project" {
			t.Errorf("patch = %+v", ops)
		}
		if roles, _ := v["roles"].([]any); len(roles) != 1 || roles[0] != "projectOwner" {
			t.Errorf("roles = %v", v["roles"])
		}
	})
	t.Run("refused", func(t *testing.T) {
		f := newFakeAPI(t)
		f.reply(http.MethodPatch, fakeUsersPath+"/u1", 403, errBody(403))
		u := &User{Org: f.organization(), User: &CapellaOrgUserData{ID: "u1"}}
		if err := u.SetProjectOwnership("p9"); err == nil || !strings.Contains(err.Error(), "User Set Error (HTTP 403)") {
			t.Fatalf("err = %v", err)
		}
	})
}
