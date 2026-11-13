package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseCIDR(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string
	}{
		{"203.0.113.10/32", "203.0.113.10/32", ""},
		{"203.0.113.10", "203.0.113.10/32", ""},
		{" 198.51.100.0/24 ", "198.51.100.0/24", ""},
		{"2001:db8::1", "2001:db8::1/128", ""},
		{"2001:db8::/32", "2001:db8::/32", ""},
		{"0.0.0.0/0", "0.0.0.0/0", ""},
		{"10.0.0.5/24", "", "not a network address (did you mean 10.0.0.0/24?)"},
		{"not-an-ip", "", "not a valid CIDR"},
		{"10.0.0.0/33", "", "not a valid CIDR"},
		{"300.1.1.1", "", "not a valid CIDR"},
		{"", "", "must not be empty"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseCIDR(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if code := exitCodeFor(err); code != ExitConfigInvalid {
					t.Errorf("exit code = %d, want %d", code, ExitConfigInvalid)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseCIDR(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestCIDRList(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	if _, err := fx.Client.CreateAllowedCIDR(ctx, fx.ProjectID, fx.ClusterID, "203.0.113.10/32", "Office VPN", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Client.CreateAllowedCIDR(ctx, fx.ProjectID, fx.ClusterID, "198.51.100.0/24", "", "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "cidr", "list", "clu")
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) != 3 {
			t.Fatalf("want header and 2 rows:\n%s", res.Stdout)
		}
		if got := strings.Join(strings.Fields(lines[0]), " "); got != "CIDR TYPE STATUS EXPIRES COMMENT ID" {
			t.Errorf("header = %q", got)
		}
		wantContains(t, res.Stdout, "203.0.113.10/32", "permanent", "never", "Office VPN", "198.51.100.0/24", "temporary", "2099-01-01T00:00:00Z")
	})
	t.Run("json", func(t *testing.T) {
		items := jsonArray(t, mustRun(t, "cidr", "list", "clu", "-o", "json"))
		if len(items) != 2 || items[0].(map[string]any)["cidr"] != "203.0.113.10/32" {
			t.Errorf("items = %v", items)
		}
	})
	t.Run("unknown cluster", func(t *testing.T) {
		wantExit(t, runResource(t, "cidr", "list", "nope"), ExitConfigInvalid, "cluster nope not found")
	})
	t.Run("--project", func(t *testing.T) {
		mustRun(t, "cidr", "list", "clu", "--project", "proj")
		wantExit(t, runResource(t, "cidr", "list", "clu", "--project", "other"), ExitConfigInvalid, "project other not found")
	})
}

func TestCIDRCreate(t *testing.T) {
	fx := newFixture(t)
	list := func() int {
		items, err := fx.Client.ListAllowedCIDRs(context.Background(), fx.ProjectID, fx.ClusterID)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "cidr", "create", "clu", "203.0.113.10/32", "--comment", "Office VPN")
		wantContains(t, res.Stdout, "allowed 203.0.113.10/32 on clu")
		items, _ := fx.Client.ListAllowedCIDRs(context.Background(), fx.ProjectID, fx.ClusterID)
		if len(items) != 1 || items[0].Comment != "Office VPN" || items[0].Type != "permanent" {
			t.Fatalf("items = %+v", items)
		}
	})
	t.Run("bare address becomes /32, with expiry, as JSON", func(t *testing.T) {
		setClock(t, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
		obj := jsonObject(t, mustRun(t, "cidr", "create", "clu", "198.51.100.7", "--expires-at", "2027-01-01T00:00:00Z", "-o", "json"))
		if obj["cidr"] != "198.51.100.7/32" || obj["status"] != "created" || obj["id"] == "" {
			t.Errorf("object = %v", obj)
		}
		items, _ := fx.Client.ListAllowedCIDRs(context.Background(), fx.ProjectID, fx.ClusterID)
		if last := items[len(items)-1]; last.ExpiresAt != "2027-01-01T00:00:00Z" || last.Type != "temporary" {
			t.Errorf("last entry = %+v", last)
		}
	})
	t.Run("twice is a no-op", func(t *testing.T) {
		before := list()
		fx.Fake.ResetLog()
		res := mustRun(t, "cidr", "create", "clu", "203.0.113.10")
		wantContains(t, res.Stdout, "already allowed")
		if list() != before || fx.Fake.Count("POST", `/allowedcidrs`) != 0 {
			t.Error("a duplicate entry was created or posted")
		}
		obj := jsonObject(t, mustRun(t, "cidr", "create", "clu", "203.0.113.10/32", "-o", "json"))
		if obj["status"] != "already allowed" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("conflict from Capella is also a no-op", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/allowedcidrs$`, 409, 1)
		res := mustRun(t, "cidr", "create", "clu", "192.0.2.1")
		wantContains(t, res.Stdout, "already allowed")
	})

	invalid := []struct {
		name string
		args []string
		msg  string
	}{
		{"bad CIDR", []string{"nonsense"}, "not a valid CIDR"},
		{"host bits set", []string{"10.1.2.3/24"}, "not a network address"},
		{"any IP needs the flag", []string{"0.0.0.0/0"}, "would allow every IP address"},
		{"bad expiry", []string{"192.0.2.9", "--expires-at", "tomorrow"}, `invalid --expires-at "tomorrow"`},
		{"expiry in the past", []string{"192.0.2.9", "--expires-at", "2020-01-01T00:00:00Z"}, "not in the future"},
	}
	for _, tc := range invalid {
		t.Run("invalid: "+tc.name, func(t *testing.T) {
			setClock(t, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
			before := list()
			res := runResource(t, append([]string{"cidr", "create", "clu"}, tc.args...)...)
			wantExit(t, res, ExitConfigInvalid, tc.msg)
			if list() != before {
				t.Error("an entry was created")
			}
		})
	}
	t.Run("any IP with --allow-any-ip", func(t *testing.T) {
		mustRun(t, "cidr", "create", "clu", "0.0.0.0/0", "--allow-any-ip")
		items, _ := fx.Client.ListAllowedCIDRs(context.Background(), fx.ProjectID, fx.ClusterID)
		found := false
		for _, e := range items {
			found = found || e.CIDR == "0.0.0.0/0"
		}
		if !found {
			t.Error("0.0.0.0/0 was not created")
		}
	})
}

func TestCIDRCreateLimit(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	for i := 0; i < maxAllowedCIDRs; i++ {
		if _, err := fx.Client.CreateAllowedCIDR(ctx, fx.ProjectID, fx.ClusterID, fmt.Sprintf("10.9.%d.0/24", i), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	ce := wantExit(t, runResource(t, "cidr", "create", "clu", "192.0.2.1"), ExitBlocked, "maximum of 75 allowed CIDRs")
	if ce.Action == "" {
		t.Error("no action hint")
	}
	// An entry that is already there is still reported as allowed, even when the list is full.
	res := mustRun(t, "cidr", "create", "clu", "10.9.0.0/24")
	wantContains(t, res.Stdout, "already allowed")
}

func TestCIDRDelete(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	seed := func(cidr string) {
		if _, err := fx.Client.CreateAllowedCIDR(ctx, fx.ProjectID, fx.ClusterID, cidr, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		items, _ := fx.Client.ListAllowedCIDRs(ctx, fx.ProjectID, fx.ClusterID)
		return len(items)
	}
	seed("203.0.113.10/32")
	seed("198.51.100.0/24")

	t.Run("no terminal and no --yes is blocked", func(t *testing.T) {
		wantExit(t, runResource(t, "cidr", "delete", "clu", "203.0.113.10"), ExitBlocked, "no terminal")
		if count() != 2 {
			t.Error("an entry was deleted")
		}
	})
	t.Run("declined", func(t *testing.T) {
		fakeTTY(t)
		res := runResourceIn(t, "no\n", "cidr", "delete", "clu", "203.0.113.10")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stdout, "cancelled")
		if count() != 2 {
			t.Error("an entry was deleted")
		}
	})
	t.Run("matches a bare address by its normalized form", func(t *testing.T) {
		res := mustRun(t, "cidr", "delete", "clu", "203.0.113.10", "--yes")
		wantContains(t, res.Stdout, "deleted allowed CIDR 203.0.113.10/32 from clu")
		if count() != 1 {
			t.Errorf("%d entries left, want 1", count())
		}
	})
	t.Run("json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "cidr", "delete", "clu", "198.51.100.0/24", "-y", "-o", "json"))
		if obj["status"] != "deleted" || obj["cidr"] != "198.51.100.0/24" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("not found is a warning, not an error", func(t *testing.T) {
		res := mustRun(t, "cidr", "delete", "clu", "192.0.2.77", "--yes")
		wantContains(t, res.Stdout, "not found")
		wantContains(t, res.Stderr, "192.0.2.77/32")
		obj := jsonObject(t, mustRun(t, "cidr", "delete", "clu", "192.0.2.77", "-y", "-o", "json"))
		if obj["status"] != "not found" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("invalid CIDR", func(t *testing.T) {
		wantExit(t, runResource(t, "cidr", "delete", "clu", "garbage", "-y"), ExitConfigInvalid, "not a valid CIDR")
	})
	t.Run("API failure", func(t *testing.T) {
		seed("192.0.2.5/32")
		fx.Fake.FailNext("DELETE", `/allowedcidrs/`, 500, 10)
		res := runResource(t, "cidr", "delete", "clu", "192.0.2.5", "-y")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "deleting allowed CIDR 192.0.2.5/32") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}
