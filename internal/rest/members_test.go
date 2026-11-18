package rest

import (
	"reflect"
	"strings"
	"testing"
)

func TestClusterNodesAndPendingNodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		nodes       []fakeNode
		wantHosts   []string
		wantPending []string
	}{
		{"single active node", []fakeNode{{Host: "10.0.0.1", Services: []string{"kv", "n1ql"}}}, []string{"10.0.0.1"}, nil},
		{"one pending", []fakeNode{{Host: "10.0.0.1"}, {Host: "10.0.0.2", Membership: "inactiveAdded"}}, []string{"10.0.0.1", "10.0.0.2"}, []string{"10.0.0.2"}},
		{"failed over is not pending", []fakeNode{{Host: "10.0.0.1"}, {Host: "10.0.0.2", Membership: "inactiveFailed"}}, []string{"10.0.0.1", "10.0.0.2"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeCB(t)
			f.seed(tt.nodes...)
			c := NewClient()
			got, err := c.ClusterNodes(f.endpoint(), "Administrator", "pw")
			if err != nil {
				t.Fatal(err)
			}
			var hosts []string
			for _, n := range got {
				hosts = append(hosts, n.Host)
			}
			if !reflect.DeepEqual(hosts, tt.wantHosts) {
				t.Errorf("hosts = %v, want %v", hosts, tt.wantHosts)
			}
			pending, err := c.PendingNodes(f.endpoint(), "Administrator", "pw")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pending, tt.wantPending) {
				t.Errorf("pending = %v, want %v", pending, tt.wantPending)
			}
		})
	}
}

func TestClusterNodesServicesAndError(t *testing.T) {
	t.Parallel()
	f := newFakeCB(t)
	f.seed(fakeNode{Host: "10.0.0.1", Services: []string{"kv", "index"}})
	got, err := NewClient().ClusterNodes(f.endpoint(), "Administrator", "pw")
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].Services, []string{"kv", "index"}) || got[0].Membership != "active" {
		t.Fatalf("nodes = %+v, %v", got, err)
	}
	f.setFail("GET /pools/default", 500)
	if _, err := NewClient().PendingNodes(f.endpoint(), "Administrator", "pw"); err == nil || !strings.Contains(err.Error(), "reading cluster nodes") {
		t.Fatalf("error = %v, want wrapped read failure", err)
	}
}
