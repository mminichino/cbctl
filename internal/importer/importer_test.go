package importer

import (
	"reflect"
	"testing"
)

func TestPasswordEnvName(t *testing.T) {
	tests := []struct{ cluster, cred, want string }{
		{"prod-01", "app", "CBCTL_PWD_PROD_01_APP"},
		{"a.b", "x y", "CBCTL_PWD_A_B_X_Y"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := PasswordEnvName(tt.cluster, tt.cred); got != tt.want {
				t.Errorf("PasswordEnvName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPoweredOff(t *testing.T) {
	for state, want := range map[string]bool{"turnedOff": true, "turningOn": true, "healthy": false, "": false} {
		if got := PoweredOff(state); got != want {
			t.Errorf("PoweredOff(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestExportCIDRDropsEmptyFields(t *testing.T) {
	got := ExportCIDR(Obj{"cidr": "203.0.113.0/24", "comment": "", "expiresAt": "2030-01-01T00:00:00Z", "id": "x"})
	want := Obj{"cidr": "203.0.113.0/24", "expiresAt": "2030-01-01T00:00:00Z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExportCIDR = %v, want %v", got, want)
	}
}

func TestToProjectsForm(t *testing.T) {
	single := Obj{"apiVersion": "cbctl/v1", "project": Obj{"name": "p"}, "clusters": []any{Obj{"name": "c"}}}
	got := ToProjectsForm(single)
	projects, _ := got["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("projects = %v", got["projects"])
	}
	p := projects[0].(Obj)
	if p["name"] != "p" || len(p["clusters"].([]any)) != 1 {
		t.Errorf("project = %v", p)
	}
	if _, ok := got["clusters"]; ok {
		t.Error("top-level clusters must move under the project")
	}
	multi := Obj{"projects": []any{}}
	if !reflect.DeepEqual(ToProjectsForm(multi), multi) {
		t.Error("the projects form must be returned unchanged")
	}
}

func TestComparableDropsMarkers(t *testing.T) {
	in := Obj{"name": "c", "__comment": "x", "list": []any{Obj{"a": 1}, Obj{"__only": 1}}}
	want := Obj{"name": "c", "list": []any{Obj{"a": 1}}}
	if got := Comparable(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Comparable = %v, want %v", got, want)
	}
}
