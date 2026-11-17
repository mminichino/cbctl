package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFormatRev(t *testing.T) {
	var nilPtr *int
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "string trimmed", in: "  42 ", want: "42"},
		{name: "float", in: float64(93706877), want: "93706877"},
		{name: "int via Sprint", in: 7, want: "7"},
		{name: "typed nil pointer", in: nilPtr, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRev(tt.in); got != tt.want {
				t.Fatalf("formatRev(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestServerGroupRevisionAndPathEdges(t *testing.T) {
	if got := serverGroupRevision(nil, "%zz"); got != "" {
		t.Fatalf("bad uri rev = %q", got)
	}
	if got := serverGroupRevision("5", "/pools/default/serverGroups?rev=9"); got != "5" {
		t.Fatalf("rev field should win, got %q", got)
	}
	if got := serverGroupsUpdatePath("?rev=3", ""); got != "/pools/default/serverGroups?rev=3" {
		t.Fatalf("query-only uri path = %q", got)
	}
	if got := serverGroupsUpdatePath("pools/default/serverGroups?rev=4", ""); got != "/pools/default/serverGroups?rev=4" {
		t.Fatalf("relative uri path = %q", got)
	}
	if got := serverGroupsUpdatePath("", " "); got != "" {
		t.Fatalf("empty rev path = %q", got)
	}
}

func TestGroupNameByURI(t *testing.T) {
	groups := []ServerGroup{{Name: "a", URI: "/g/0"}, {Name: "b", URI: "/g/1"}}
	tests := []struct{ uri, want string }{{"/g/1", "b"}, {"/g/0", "a"}, {"/g/9", ""}}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			if got := groupNameByURI(groups, tt.uri); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestNodeMatchesHostEmpty(t *testing.T) {
	if nodeMatchesHost(ServerGroupNode{OTPNode: "ns_1@10.0.0.1"}, "") {
		t.Fatal("empty want must not match")
	}
}

func TestGetServerGroups(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv"}})
	got, err := NewClient().GetServerGroups(f.endpoint(), "Administrator", "password")
	if err != nil {
		t.Fatal(err)
	}
	if got.Rev != "1" || got.URI != "/pools/default/serverGroups?rev=1" {
		t.Fatalf("rev=%q uri=%q", got.Rev, got.URI)
	}
	if len(got.Groups) != 1 || got.Groups[0].Name != "Group 1" || got.Groups[0].Nodes[0].OTPNode != "ns_1@10.0.0.1" {
		t.Fatalf("groups = %+v", got.Groups)
	}
	r := f.requests(http.MethodGet, "/pools/default/serverGroups")
	if len(r) != 1 || r[0].User != "Administrator" || r[0].Pass != "password" {
		t.Fatalf("requests = %+v", r)
	}

	f.setFail("GET /pools/default/serverGroups", http.StatusInternalServerError)
	if _, err := NewClient().GetServerGroups(f.endpoint(), "u", "p"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateServerGroup(t *testing.T) {
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "10.0.0.1"})
	c := NewClient()
	if err := c.CreateServerGroup(f.endpoint(), "u", "p", "   "); err != nil {
		t.Fatal(err)
	}
	if n := f.count(http.MethodPost, "/pools/default/serverGroups"); n != 0 {
		t.Fatalf("blank name posted %d times", n)
	}
	if err := c.CreateServerGroup(f.endpoint(), "u", "p", " rack-a "); err != nil {
		t.Fatal(err)
	}
	r := f.requests(http.MethodPost, "/pools/default/serverGroups")
	if len(r) != 1 || r[0].Form.Get("name") != "rack-a" {
		t.Fatalf("requests = %+v", r)
	}
}

func TestEnsureServerGroup(t *testing.T) {
	tests := []struct {
		name      string
		group     string
		setup     func(f *fakeCB)
		wantPosts int
		wantErr   bool
	}{
		{name: "blank is no-op", group: "", wantPosts: 0},
		{name: "existing group", group: "Group 1", wantPosts: 0},
		{name: "creates missing group", group: "rack-b", wantPosts: 1},
		{
			name: "create failure", group: "rack-d", wantPosts: 1, wantErr: true,
			setup: func(f *fakeCB) { f.setFail("POST /pools/default/serverGroups", http.StatusInternalServerError) },
		},
		{
			name: "get failure", group: "rack-e", wantPosts: 0, wantErr: true,
			setup: func(f *fakeCB) { f.setFail("GET /pools/default/serverGroups", http.StatusInternalServerError) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCB(t)
			f.seed(fakeNode{Host: "10.0.0.1"})
			if tt.setup != nil {
				tt.setup(f)
			}
			err := NewClient().EnsureServerGroup(f.endpoint(), "u", "p", tt.group)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if n := f.count(http.MethodPost, "/pools/default/serverGroups"); n != tt.wantPosts {
				t.Fatalf("posts = %d, want %d", n, tt.wantPosts)
			}
		})
	}
}

func TestEnsureServerGroupConcurrentCreate(t *testing.T) {
	// A group created between GET and POST answers "already exists"; Ensure treats it as success.
	ep := handlerEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"groups":[{"name":"Group 1","uri":"/g/0","nodes":[]}],"uri":"/pools/default/serverGroups?rev=1"}`)) // test response
		case http.MethodPost:
			http.Error(w, `{"name":"Name is Already taken"}`, http.StatusBadRequest)
		}
	})
	if err := NewClient().EnsureServerGroup(ep, "u", "p", "rack-a"); err != nil {
		t.Fatalf("already-exists should be tolerated: %v", err)
	}
}

func TestAssignNodeServerGroup(t *testing.T) {
	t.Run("blank group is no-op", func(t *testing.T) {
		f := newFakeCB(t)
		if err := NewClient().AssignNodeServerGroup(f.endpoint(), "u", "p", "10.0.0.1", " ", 0); err != nil {
			t.Fatal(err)
		}
		if f.count(http.MethodGet, "/pools/default/serverGroups") != 0 {
			t.Fatal("unexpected request")
		}
	})

	t.Run("already in target group", func(t *testing.T) {
		f := newFakeCB(t)
		f.seed(fakeNode{Host: "10.0.0.1"})
		if err := NewClient().AssignNodeServerGroup(f.endpoint(), "u", "p", "10.0.0.1:8091", "Group 1", 0); err != nil {
			t.Fatal(err)
		}
		if n := f.count(http.MethodPut, "/pools/default/serverGroups"); n != 0 {
			t.Fatalf("puts = %d", n)
		}
	})

	t.Run("creates group and moves node", func(t *testing.T) {
		f := newFakeCB(t)
		f.seed(fakeNode{Host: "10.0.0.1"}, fakeNode{Host: "10.0.0.2"})
		if err := NewClient().AssignNodeServerGroup(f.endpoint(), "Administrator", "password", "10.0.0.2", "rack-b", 3); err != nil {
			t.Fatal(err)
		}
		if got := f.groupOf("ns_1@10.0.0.2"); got != "rack-b" {
			t.Fatalf("node group = %q", got)
		}
		if got := f.groupOf("ns_1@10.0.0.1"); got != "Group 1" {
			t.Fatalf("other node group = %q", got)
		}
		posts := f.requests(http.MethodPost, "/pools/default/serverGroups")
		if len(posts) != 1 || posts[0].Form.Get("name") != "rack-b" {
			t.Fatalf("posts = %+v", posts)
		}
		puts := f.requests(http.MethodPut, "/pools/default/serverGroups")
		if len(puts) != 1 {
			t.Fatalf("puts = %d", len(puts))
		}
		// Creating the group bumped rev 1 -> 2; the PUT must carry the fresh rev.
		if puts[0].RawQuery != "rev=2" || puts[0].ContentType != "application/json" {
			t.Fatalf("put = %+v", puts[0])
		}
		var payload struct {
			Groups []struct {
				Name  string              `json:"name"`
				URI   string              `json:"uri"`
				Nodes []map[string]string `json:"nodes"`
			} `json:"groups"`
		}
		if err := json.Unmarshal([]byte(puts[0].Body), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Groups) != 2 || payload.Groups[1].Name != "rack-b" ||
			len(payload.Groups[1].Nodes) != 1 || payload.Groups[1].Nodes[0]["otpNode"] != "ns_1@10.0.0.2" ||
			len(payload.Groups[0].Nodes) != 1 {
			t.Fatalf("payload = %s", puts[0].Body)
		}
		// GET for ensure, GET before move, GET to confirm.
		if n := f.count(http.MethodGet, "/pools/default/serverGroups"); n != 3 {
			t.Fatalf("gets = %d", n)
		}
	})

	t.Run("ensure failure", func(t *testing.T) {
		f := newFakeCB(t)
		f.seed(fakeNode{Host: "10.0.0.1"})
		f.setFail("POST /pools/default/serverGroups", http.StatusForbidden)
		err := NewClient().AssignNodeServerGroup(f.endpoint(), "u", "p", "10.0.0.1", "rack-x", 1)
		if err == nil || !strings.Contains(err.Error(), `ensure server group "rack-x"`) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("second groups read fails", func(t *testing.T) {
		var calls atomic.Int32
		ep := handlerEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) > 1 {
				http.Error(w, "gone", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"groups":[{"name":"rack-a","uri":"/g/1","nodes":[]}],"uri":"/pools/default/serverGroups?rev=1"}`)) // test response
		})
		err := NewClient().AssignNodeServerGroup(ep, "u", "p", "10.0.0.1", "rack-a", 1)
		if err == nil || !strings.Contains(err.Error(), "HTTP 500: gone") {
			t.Fatalf("err = %v", err)
		}
	})
}
