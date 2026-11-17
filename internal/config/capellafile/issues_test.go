package capellafile

import (
	"reflect"
	"strings"
	"testing"
)

func TestFormatAndParsePath(t *testing.T) {
	tests := []struct {
		path Path
		text string
	}{
		{nil, "(root)"},
		{Path{"apiVersion"}, "apiVersion"},
		{Path{"projects", 0, "clusters", 1, "name"}, "projects[0].clusters[1].name"},
		{Path{"a", 2}, "a[2]"},
		{Path{"a", 1, 2}, "a[1][2]"},
	}
	for _, tt := range tests {
		if got := FormatPath(tt.path); got != tt.text {
			t.Errorf("FormatPath(%v) = %q, want %q", tt.path, got, tt.text)
		}
		if got := ParsePath(tt.text); !reflect.DeepEqual(got, tt.path) && !(len(got) == 0 && len(tt.path) == 0) {
			t.Errorf("ParsePath(%q) = %#v, want %#v", tt.text, got, tt.path)
		}
	}
	if got := ParsePath(""); got != nil {
		t.Errorf("ParsePath(\"\") = %v", got)
	}
}

func TestDescribe(t *testing.T) {
	root := Obj{
		"projects": []any{
			Obj{"name": "shop", "clusters": []any{
				Obj{
					"name": "web-01",
					"serviceGroups": []any{
						Obj{"services": []any{"query", "data"}},
					},
					"buckets":      []any{Obj{"name": strings.Repeat("x", 50), "scopes": []any{Obj{"name": "s", "collections": []any{Obj{"name": "c"}}}}}},
					"allowedCidrs": []any{Obj{"cidr": "10.0.0.0/8"}},
				},
				Obj{}, // unnamed
			}},
		},
	}
	tests := []struct {
		path Path
		want string
	}{
		{nil, "(root)"},
		{Path{"apiVersion"}, "apiVersion"},
		{Path{"projects", 0, "name"}, "project shop"},
		{Path{"projects", 0, "description"}, "project shop › description"},
		{Path{"projects", 0, "clusters", 0, "cloudProvider", "region"}, "project shop › cluster web-01 › cloudProvider.region"},
		{Path{"projects", 0, "clusters", 0, "serviceGroups", 0, "numOfNodes"}, "project shop › cluster web-01 › service group data,query › numOfNodes"},
		{Path{"projects", 0, "clusters", 1, "name"}, "project shop › cluster #2"},
		{Path{"projects", 0, "clusters", 0, "allowedCidrs", 0, "cidr"}, "project shop › cluster web-01 › allowed IP range 10.0.0.0/8"},
		{Path{"projects", 0, "clusters", 0, "buckets", 0, "scopes", 0, "collections", 0, "name"}, "project shop › cluster web-01 › bucket " + strings.Repeat("x", 40) + "… › scope s › collection c"},
		{Path{"projects", 5, "name"}, "project #6"},
	}
	for _, tt := range tests {
		if got := Describe(root, tt.path); got != tt.want {
			t.Errorf("Describe(%v)\n got %q\nwant %q", tt.path, got, tt.want)
		}
	}
	single := Obj{"project": Obj{"name": "solo"}, "clusters": []any{Obj{"name": "c1"}}}
	if got := Describe(single, Path{"clusters", 0, "tier"}); got != "project solo › cluster c1 › tier" {
		t.Errorf("single form: %q", got)
	}
}

func TestIssueStringAndRender(t *testing.T) {
	issue := Issue{Path: "a.b", Where: "project p › b", Message: "must be x", Line: 7, Fix: "do y"}
	if got, want := issue.String(), "project p › b (line 7)\n  must be x\n  action: do y"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got := (Issue{Path: "a", Message: "m"}).String(); got != "a\n  m" {
		t.Errorf("String() without line/fix = %q", got)
	}
	got := RenderIssues("f.yaml has 2 problems:", []Issue{issue, {Where: "w2", Message: "m2"}})
	want := "f.yaml has 2 problems:\n  project p › b (line 7)\n    must be x\n    action: do y\n  w2\n    m2"
	if got != want {
		t.Errorf("RenderIssues:\n got %q\nwant %q", got, want)
	}
}

func TestConfigErrorHeadingAndSorting(t *testing.T) {
	ce := NewConfigError("x.yaml", []Issue{{Path: "b", Line: 9, Message: "m"}, {Path: "z", Line: 2, Message: "m"}, {Path: "a", Line: 2, Message: "m"}})
	order := []string{ce.Issues[0].Path, ce.Issues[1].Path, ce.Issues[2].Path}
	if strings.Join(order, ",") != "a,z,b" {
		t.Errorf("order = %v", order)
	}
	if !strings.HasPrefix(ce.Error(), "x.yaml has 3 problems:\n") {
		t.Errorf("Error() = %q", ce.Error())
	}
	one := NewConfigError("", []Issue{{Message: "m"}})
	if !strings.HasPrefix(one.Error(), "capella.yaml has 1 problem:\n") {
		t.Errorf("Error() = %q", one.Error())
	}
}

func TestClosestKey(t *testing.T) {
	allowed := []string{"description", "name", "replicas", "flushEnabled"}
	tests := []struct{ in, want string }{
		{"descripton", "description"},
		{"replica", "replicas"},
		{"Name", "name"},
		{"flush", ""}, // too far
		{"zzzzzz", ""},
	}
	for _, tt := range tests {
		if got := closestKey(tt.in, allowed); got != tt.want {
			t.Errorf("closestKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if levenshtein("kitten", "sitting") != 3 || levenshtein("", "abc") != 3 || levenshtein("é", "e") != 1 {
		t.Error("levenshtein")
	}
}
