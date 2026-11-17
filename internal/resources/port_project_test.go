package resources

import (
	"encoding/json"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/plan"
)

// TestAllowedCIDR_CAP022_AC1_CreateBodyOmitsEmptyFields: an empty comment (or expiresAt) is not
// sent, and a bare IP is created as /32.
func TestAllowedCIDR_CAP022_AC1_CreateBodyOmitsEmptyFields(t *testing.T) {
	cidr, comment, expires := CreateBody(Obj{"cidr": "203.0.113.10", "comment": ""})
	if cidr != "203.0.113.10/32" || comment != "" || expires != "" {
		t.Fatalf("CreateBody = %q %q %q", cidr, comment, expires)
	}
	raw, err := json.Marshal(capellaapi.CreateAllowedCIDRRequest{CIDR: cidr, Comment: comment, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"cidr":"203.0.113.10/32"}` {
		t.Errorf("request body = %s, want only the cidr", raw)
	}
}

func protectedCluster(protected *bool) *plan.Action {
	a := plan.NewAction(nil, "cluster", "nova-dev-01", plan.Noop)
	if protected != nil {
		a.Observed = &plan.Observed{ID: "c-1", Body: Obj{"deletionProtection": *protected}}
	}
	return a
}

// TestDeletionProtection_CAP013_AC3_OmittedUnchanged: without deletionProtection in the file
// nothing is planned, whatever Capella has; a declared value is planned.
func TestDeletionProtection_CAP013_AC3_OmittedUnchanged(t *testing.T) {
	r := newDeletionProtection()
	for _, current := range []bool{true, false} {
		t.Run(map[bool]string{true: "protected", false: "unprotected"}[current], func(t *testing.T) {
			if got := r.DesiredItems(Obj{"name": "nova-dev-01"}); len(got) != 0 {
				t.Errorf("omitted: desired items = %v, want none", got)
			}
			got := r.DesiredItems(Obj{"name": "nova-dev-01", "deletionProtection": current})
			if len(got) != 1 || got[0] != current {
				t.Errorf("declared %v: desired items = %v", current, got)
			}
		})
	}
}

// TestDeletionProtection_CAP013_AC2_NewCluster: a new cluster (not observed yet) with protection
// true plans a create after the cluster; false is a hidden no-op, since Capella creates clusters
// unprotected.
func TestDeletionProtection_CAP013_AC2_NewCluster(t *testing.T) {
	r := newDeletionProtection()
	parent := protectedCluster(nil)
	on, err := r.Diff(t.Context(), nil, true, nil, parent)
	if err != nil || on.Type != plan.Create || on.Summary != "deletionProtection: true" {
		t.Fatalf("true on a new cluster = %+v, %v; want a create", on, err)
	}
	off, err := r.Diff(t.Context(), nil, false, nil, parent)
	if err != nil || off.Type != plan.Noop || !off.Hidden {
		t.Fatalf("false on a new cluster = %+v, %v; want a hidden no-op", off, err)
	}
}
