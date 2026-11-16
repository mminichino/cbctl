package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

func (fx *resFixture) scopeNames(t *testing.T, bucket string) []string {
	t.Helper()
	scopes, err := fx.Client.ListScopes(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID(bucket))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(scopes))
	for i, s := range scopes {
		names[i] = s.Name
	}
	return names
}

func (fx *resFixture) addScope(t *testing.T, bucket, scope string) {
	t.Helper()
	if err := fx.Client.CreateScope(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID(bucket), scope); err != nil {
		t.Fatal(err)
	}
}

func (fx *resFixture) addCollection(t *testing.T, bucket, scope, name string) {
	t.Helper()
	if err := fx.Client.CreateCollection(context.Background(), fx.ProjectID, fx.ClusterID, capellaapi.BucketID(bucket), scope, name, nil); err != nil {
		t.Fatal(err)
	}
}

func TestScopeList(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")
	fx.addScope(t, "orders", "inv")
	fx.addCollection(t, "orders", "inv", "items")
	fx.addCollection(t, "orders", "inv", "suppliers")

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "scope", "list", "clu", "orders")
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if got := strings.Join(strings.Fields(lines[0]), " "); got != "NAME COLLECTIONS" {
			t.Errorf("header = %q", got)
		}
		wantContains(t, res.Stdout, "_default", "inv")
		for _, l := range lines {
			if strings.HasPrefix(l, "inv") && !strings.HasSuffix(strings.TrimSpace(l), "2") {
				t.Errorf("inv row = %q, want 2 collections", l)
			}
			if strings.HasPrefix(l, "_default") && !strings.HasSuffix(strings.TrimSpace(l), "0") {
				t.Errorf("_default row = %q, system collections are not counted", l)
			}
		}
	})
	t.Run("json", func(t *testing.T) {
		items := jsonArray(t, mustRun(t, "scope", "list", "clu", "orders", "-o", "json"))
		if len(items) != 2 {
			t.Fatalf("items = %v", items)
		}
	})
	t.Run("unknown bucket", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "scope", "list", "clu", "ghost"), ExitConfigInvalid, "bucket ghost not found")
		if !strings.Contains(ce.Action, "bucket list") {
			t.Errorf("action = %q", ce.Action)
		}
	})
	t.Run("unknown cluster", func(t *testing.T) {
		wantExit(t, runResource(t, "scope", "list", "nope", "orders"), ExitConfigInvalid, "cluster nope not found")
	})
}

func TestScopeAdd(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")

	t.Run("text", func(t *testing.T) {
		res := mustRun(t, "scope", "add", "clu", "orders", "billing")
		wantContains(t, res.Stdout, "created scope billing in clu/orders")
		if got := strings.Join(fx.scopeNames(t, "orders"), ","); got != "_default,billing" {
			t.Errorf("scopes = %s", got)
		}
	})
	t.Run("json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "scope", "add", "clu", "orders", "shipping", "-o", "json"))
		if obj["status"] != "created" || obj["scope"] != "shipping" || obj["bucket"] != "orders" || obj["cluster"] != "clu" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("twice is a no-op", func(t *testing.T) {
		fx.Fake.ResetLog()
		res := mustRun(t, "scope", "add", "clu", "orders", "billing")
		wantContains(t, res.Stdout, "scope billing already exists in clu/orders")
		if fx.Fake.Count("POST", `/scopes`) != 0 {
			t.Error("a POST was sent for an existing scope")
		}
		obj := jsonObject(t, mustRun(t, "scope", "add", "clu", "orders", "billing", "-o", "json"))
		if obj["status"] != "already exists" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("system scopes already exist", func(t *testing.T) {
		wantContains(t, mustRun(t, "scope", "add", "clu", "orders", "_default").Stdout, "already exists")
	})
	t.Run("conflict from Capella is a no-op", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/scopes$`, 409, 1)
		wantContains(t, mustRun(t, "scope", "add", "clu", "orders", "raced").Stdout, "already exists")
	})
	t.Run("unknown bucket", func(t *testing.T) {
		wantExit(t, runResource(t, "scope", "add", "clu", "ghost", "x"), ExitConfigInvalid, "bucket ghost not found")
	})
	t.Run("API failure", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/scopes$`, 422, 1)
		res := runResource(t, "scope", "add", "clu", "orders", "bad")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "creating scope bad") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestScopeDelete(t *testing.T) {
	fx := newFixture(t)
	fx.addBucket(t, "orders")
	fx.addScope(t, "orders", "billing")

	t.Run("system scopes are refused before any request", func(t *testing.T) {
		for _, name := range []string{"_default", "_system"} {
			fx.Fake.ResetLog()
			wantExit(t, runResource(t, "scope", "delete", "clu", "orders", name, "-y"), ExitConfigInvalid, "cannot be deleted")
			if n := len(fx.Fake.RequestLog()); n != 0 {
				t.Errorf("%s: %d requests sent", name, n)
			}
		}
	})
	t.Run("no terminal and no --yes is blocked", func(t *testing.T) {
		wantExit(t, runResource(t, "scope", "delete", "clu", "orders", "billing"), ExitBlocked, "no terminal")
		if len(fx.scopeNames(t, "orders")) != 2 {
			t.Error("scope was deleted")
		}
	})
	t.Run("declined", func(t *testing.T) {
		fakeTTY(t)
		res := runResourceIn(t, "n\n", "scope", "delete", "clu", "orders", "billing")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stdout, "cancelled")
		if len(fx.scopeNames(t, "orders")) != 2 {
			t.Error("scope was deleted")
		}
	})
	t.Run("unknown scope", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "scope", "delete", "clu", "orders", "ghost", "-y"), ExitConfigInvalid, "scope ghost not found")
		if !strings.Contains(ce.Action, "scope list clu orders") {
			t.Errorf("action = %q", ce.Action)
		}
	})
	t.Run("deletion protection blocks", func(t *testing.T) {
		if err := fx.Client.SetDeletionProtection(context.Background(), fx.ProjectID, fx.ClusterID, true); err != nil {
			t.Fatal(err)
		}
		wantExit(t, runResource(t, "scope", "delete", "clu", "orders", "billing", "-y"), ExitBlocked, "deletion protection")
		if err := fx.Client.SetDeletionProtection(context.Background(), fx.ProjectID, fx.ClusterID, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		res := mustRun(t, "scope", "delete", "clu", "orders", "billing", "--yes")
		wantContains(t, res.Stdout, "deleted scope billing from clu/orders")
		if got := strings.Join(fx.scopeNames(t, "orders"), ","); got != "_default" {
			t.Errorf("scopes = %s", got)
		}
	})
	t.Run("json", func(t *testing.T) {
		fx.addScope(t, "orders", "tmp")
		obj := jsonObject(t, mustRun(t, "scope", "delete", "clu", "orders", "tmp", "-y", "-o", "json"))
		if obj["status"] != "deleted" || obj["scope"] != "tmp" {
			t.Errorf("object = %v", obj)
		}
	})
}

func TestLegacyScopeLeavesStillRegistered(t *testing.T) {
	scope, _, err := newCapellaCmd().Find([]string{"scope"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range scope.Commands() {
		names = append(names, c.Name())
	}
	for _, want := range []string{"create", "exists", "list", "add", "delete"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("scope has no %q subcommand (has %v)", want, names)
		}
	}
}
