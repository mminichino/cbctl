package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/secrets"
)

var passwordLine = regexp.MustCompile(`(?m)^password: (\S+)$`)

// textPassword extracts the one password line from text output.
func textPassword(t *testing.T, out string) string {
	t.Helper()
	m := passwordLine.FindAllStringSubmatch(out, -1)
	if len(m) != 1 {
		t.Fatalf("want exactly one password line, found %d in:\n%s", len(m), out)
	}
	return m[0][1]
}

func credentialByName(t *testing.T, fx *resFixture, name string) *capellaapi.Credential {
	t.Helper()
	items, err := fx.Client.ListCredentials(context.Background(), fx.ProjectID, fx.ClusterID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func TestAccessGrant(t *testing.T) {
	tests := []struct {
		name    string
		flags   accessFlags
		want    capellaapi.AccessEntry
		wantErr string
	}{
		{"read bucket", accessFlags{privileges: "read", bucket: "b"},
			capellaapi.AccessEntry{Privileges: []string{"data_reader"}, Resources: capellaapi.Resources{
				Buckets: []capellaapi.ResourceBucket{{Name: "b"}}}}, ""},
		{"read+write scope", accessFlags{privileges: "read+write", bucket: "b", scope: "s"},
			capellaapi.AccessEntry{Privileges: []string{"data_reader", "data_writer"}, Resources: capellaapi.Resources{
				Buckets: []capellaapi.ResourceBucket{{Name: "b", Scopes: []capellaapi.ResourceScope{{Name: "s"}}}}}}, ""},
		{"write collections", accessFlags{privileges: "write", bucket: "b", scope: "s", collections: []string{"c1", "c2"}},
			capellaapi.AccessEntry{Privileges: []string{"data_writer"}, Resources: capellaapi.Resources{
				Buckets: []capellaapi.ResourceBucket{{Name: "b", Scopes: []capellaapi.ResourceScope{{Name: "s", Collections: []string{"c1", "c2"}}}}}}}, ""},
		{"all buckets", accessFlags{privileges: "read", allBuckets: true},
			capellaapi.AccessEntry{Privileges: []string{"data_reader"}}, ""},
		{"no privileges", accessFlags{bucket: "b"}, capellaapi.AccessEntry{}, "--privileges is required"},
		{"bad privileges", accessFlags{privileges: "admin", bucket: "b"}, capellaapi.AccessEntry{}, `invalid --privileges "admin"`},
		{"scope without bucket", accessFlags{privileges: "read", scope: "s", allBuckets: true}, capellaapi.AccessEntry{}, "--scope was given without --bucket"},
		{"collection without scope", accessFlags{privileges: "read", bucket: "b", collections: []string{"c"}}, capellaapi.AccessEntry{}, "--collection was given without --scope"},
		{"bucket and all buckets", accessFlags{privileges: "read", bucket: "b", allBuckets: true}, capellaapi.AccessEntry{}, "both --bucket and --all-buckets"},
		{"no bucket", accessFlags{privileges: "read"}, capellaapi.AccessEntry{}, "no bucket was given"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.flags.grant()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if exitCodeFor(err) != ExitConfigInvalid {
					t.Errorf("exit code = %d, want %d", exitCodeFor(err), ExitConfigInvalid)
				}
				return
			}
			if err != nil || accessKey(got) != accessKey(tc.want) {
				t.Fatalf("grant = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestAccessSummary(t *testing.T) {
	access := []capellaapi.AccessEntry{
		{Privileges: []string{"data_reader"}, Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "travel-sample"}}}},
		{Privileges: []string{"data_writer", "data_reader"}, Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{
			{Name: "orders", Scopes: []capellaapi.ResourceScope{{Name: "inv", Collections: []string{"a", "b"}}, {Name: "x"}}}}}},
		{Privileges: []string{"data_writer"}},
	}
	want := "read:travel-sample.*; read+write:orders.inv.a,orders.inv.b,orders.x.*; write:*"
	if got := accessSummary(access); got != want {
		t.Errorf("accessSummary = %q, want %q", got, want)
	}
	if !sameAccess(access, []capellaapi.AccessEntry{access[2], access[0], {Privileges: []string{"data_reader", "data_writer"}, Resources: access[1].Resources}}) {
		t.Error("sameAccess should ignore order of entries and privileges")
	}
	if sameAccess(access, access[:2]) {
		t.Error("sameAccess ignored a missing entry")
	}
}

func TestCredentialCreate(t *testing.T) {
	fx := newFixture(t)

	t.Run("generated password is printed once", func(t *testing.T) {
		res := mustRun(t, "credential", "create", "clu", "app_ro", "--privileges", "read", "--bucket", "travel-sample")
		wantContains(t, res.Stdout, "created credential app_ro (")
		pw := textPassword(t, res.Stdout)
		if len(pw) != 32 || !secrets.MeetsRules(pw) {
			t.Errorf("generated password %q does not meet the rules", pw)
		}
		if strings.Contains(res.Stderr, pw) {
			t.Error("the password leaked to stderr")
		}
		wantContains(t, res.Stderr, "shown only once")
		if strings.Count(res.Stdout, pw) != 1 {
			t.Errorf("password appears %d times on stdout", strings.Count(res.Stdout, pw))
		}
		c := credentialByName(t, fx, "app_ro")
		if c == nil || accessSummary(c.Access) != "read:travel-sample.*" {
			t.Fatalf("credential = %+v", c)
		}
	})
	t.Run("json", func(t *testing.T) {
		res := mustRun(t, "credential", "create", "clu", "app_rw", "--privileges", "read+write", "--bucket", "orders",
			"--scope", "inv", "--collection", "a", "--collection", "b", "-o", "json")
		obj := jsonObject(t, res)
		pw, _ := obj["password"].(string)
		if !secrets.MeetsRules(pw) || obj["name"] != "app_rw" || obj["status"] != "created" || obj["id"] == "" {
			t.Errorf("object = %v", obj)
		}
		if strings.Contains(res.Stderr, pw) {
			t.Error("the password leaked to stderr")
		}
		c := credentialByName(t, fx, "app_rw")
		if got := accessSummary(c.Access); got != "read+write:orders.inv.a,orders.inv.b" {
			t.Errorf("access = %q", got)
		}
	})
	t.Run("all buckets", func(t *testing.T) {
		mustRun(t, "credential", "create", "clu", "dba", "--privileges", "write", "--all-buckets")
		if got := accessSummary(credentialByName(t, fx, "dba").Access); got != "write:*" {
			t.Errorf("access = %q", got)
		}
	})
	t.Run("password from the environment is not printed", func(t *testing.T) {
		const pw = "S3cure-Passw0rd!x"
		t.Setenv("APP_PW", pw)
		res := mustRun(t, "credential", "create", "clu", "env_user", "--privileges", "read", "--bucket", "b", "--password-env", "APP_PW")
		if strings.Contains(res.Stdout+res.Stderr, pw) || strings.Contains(res.Stdout, "password:") {
			t.Errorf("a supplied password was printed:\n%s\n%s", res.Stdout, res.Stderr)
		}
		wantContains(t, res.Stdout, "created credential env_user")
	})
	t.Run("password from a file with a trailing newline", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pw")
		if err := os.WriteFile(path, []byte("S3cure-Passw0rd!y\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := mustRun(t, "credential", "create", "clu", "file_user", "--privileges", "read", "--bucket", "b", "--password-file", path, "-o", "json")
		if _, has := jsonObject(t, res)["password"]; has {
			t.Error("a supplied password was printed")
		}
		if strings.Contains(res.Stderr, "readable by other users") {
			t.Error("unexpected permission warning for a 0600 file")
		}
	})
	t.Run("a world-readable password file only warns", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("POSIX permission bits; the check is skipped on Windows by design")
		}
		path := filepath.Join(t.TempDir(), "pw")
		if err := os.WriteFile(path, []byte("S3cure-Passw0rd!z"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := mustRun(t, "credential", "create", "clu", "loose_user", "--privileges", "read", "--bucket", "b", "--password-file", path)
		wantContains(t, res.Stderr, "readable by other users")
	})
	t.Run("same name and access is a no-op", func(t *testing.T) {
		fx.Fake.ResetLog()
		res := mustRun(t, "credential", "create", "clu", "app_ro", "--privileges", "read", "--bucket", "travel-sample")
		wantContains(t, res.Stdout, "credential app_ro already exists")
		if strings.Contains(res.Stdout, "password:") || fx.Fake.Count("POST", `/users`) != 0 {
			t.Error("a password was printed or a credential posted")
		}
	})
	t.Run("same name with other access is an error", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "credential", "create", "clu", "app_ro", "--privileges", "write", "--bucket", "travel-sample"),
			ExitError, "already exists with different access")
		if !strings.Contains(ce.Action, "credential update clu app_ro") {
			t.Errorf("action = %q", ce.Action)
		}
	})

	invalid := []struct {
		name string
		args []string
		env  string
		msg  string
	}{
		{"no privileges", []string{"--bucket", "b"}, "", "--privileges is required"},
		{"no bucket", []string{"--privileges", "read"}, "", "no bucket was given"},
		{"scope without bucket", []string{"--privileges", "read", "--scope", "s"}, "", "--scope was given without --bucket"},
		{"both password sources", []string{"--privileges", "read", "--bucket", "b", "--password-env", "X", "--password-file", "/tmp/x"}, "", "both --password-env and --password-file"},
		{"empty env variable", []string{"--privileges", "read", "--bucket", "b", "--password-env", "EMPTY_PW"}, "", "environment variable EMPTY_PW is empty"},
		{"weak password", []string{"--privileges", "read", "--bucket", "b", "--password-env", "WEAK_PW"}, "weak", "does not meet Capella's rules"},
		{"forbidden character", []string{"--privileges", "read", "--bucket", "b", "--password-env", "WEAK_PW"}, "Valid-Passw0rd*", "does not meet Capella's rules"},
		{"missing file", []string{"--privileges", "read", "--bucket", "b", "--password-file", "/nonexistent/pw"}, "", "reading --password-file"},
	}
	for _, tc := range invalid {
		t.Run("invalid: "+tc.name, func(t *testing.T) {
			t.Setenv("EMPTY_PW", "")
			t.Setenv("WEAK_PW", tc.env)
			fx.Fake.ResetLog()
			res := runResource(t, append([]string{"credential", "create", "clu", "bad_user"}, tc.args...)...)
			wantExit(t, res, ExitConfigInvalid, tc.msg)
			if fx.Fake.Count("POST", `/users`) != 0 {
				t.Error("a credential was posted")
			}
			if tc.env != "" && strings.Contains(res.Stdout+res.Stderr+res.Err.Error(), tc.env) {
				t.Error("the rejected password was echoed")
			}
		})
	}
	t.Run("unknown cluster", func(t *testing.T) {
		wantExit(t, runResource(t, "credential", "create", "nope", "u", "--privileges", "read", "--bucket", "b"), ExitConfigInvalid, "cluster nope not found")
	})
	t.Run("API rejection", func(t *testing.T) {
		fx.Fake.FailNext("POST", `/users$`, 422, 1)
		res := runResource(t, "credential", "create", "clu", "rejected", "--privileges", "read", "--bucket", "b")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "creating credential rejected") {
			t.Fatalf("err = %v", res.Err)
		}
		if strings.Contains(res.Stdout, "password:") {
			t.Error("a password was printed for a failed create")
		}
	})
}

func TestCredentialListAndGet(t *testing.T) {
	fx := newFixture(t)
	mustRun(t, "credential", "create", "clu", "app_ro", "--privileges", "read", "--bucket", "travel-sample")
	mustRun(t, "credential", "create", "clu", "dba", "--privileges", "read+write", "--all-buckets")

	t.Run("list text", func(t *testing.T) {
		res := mustRun(t, "credential", "list", "clu")
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) != 3 {
			t.Fatalf("want header and 2 rows:\n%s", res.Stdout)
		}
		if got := strings.Join(strings.Fields(lines[0]), " "); got != "NAME ID ACCESS" {
			t.Errorf("header = %q", got)
		}
		wantContains(t, res.Stdout, "app_ro", "read:travel-sample.*", "dba", "read+write:*")
	})
	t.Run("list json", func(t *testing.T) {
		items := jsonArray(t, mustRun(t, "credential", "list", "clu", "-o", "json"))
		if len(items) != 2 {
			t.Fatalf("items = %v", items)
		}
		for _, it := range items {
			if _, has := it.(map[string]any)["password"]; has {
				t.Error("list shows a password")
			}
		}
	})
	t.Run("get", func(t *testing.T) {
		res := mustRun(t, "credential", "get", "clu", "app_ro")
		wantContains(t, res.Stdout, "name: app_ro", "access[0].privileges[0]: data_reader", "access[0].resources.buckets[0].name: travel-sample")
		obj := jsonObject(t, mustRun(t, "credential", "get", "clu", "app_ro", "-o", "json"))
		if obj["name"] != "app_ro" || obj["id"] != credentialByName(t, fx, "app_ro").ID {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("get unknown", func(t *testing.T) {
		ce := wantExitErr(t, runResource(t, "credential", "get", "clu", "ghost"), ExitConfigInvalid, "credential ghost not found")
		if !strings.Contains(ce.Action, "credential list clu") {
			t.Errorf("action = %q", ce.Action)
		}
	})
	t.Run("list API failure", func(t *testing.T) {
		fx.Fake.FailNext("GET", `/users$`, 403, 1)
		res := runResource(t, "credential", "list", "clu")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "listing credentials of clu") {
			t.Fatalf("err = %v", res.Err)
		}
	})
}

func TestCredentialUpdate(t *testing.T) {
	fx := newFixture(t)
	mustRun(t, "credential", "create", "clu", "app_ro", "--privileges", "read", "--bucket", "travel-sample")

	t.Run("change access", func(t *testing.T) {
		res := mustRun(t, "credential", "update", "clu", "app_ro", "--privileges", "read+write", "--bucket", "orders")
		wantContains(t, res.Stdout, "updated credential app_ro")
		if strings.Contains(res.Stdout, "password:") {
			t.Error("a password was printed although none changed")
		}
		if got := accessSummary(credentialByName(t, fx, "app_ro").Access); got != "read+write:orders.*" {
			t.Errorf("access = %q", got)
		}
	})
	t.Run("rotate prints the new password once", func(t *testing.T) {
		fx.Fake.ResetLog()
		res := mustRun(t, "credential", "update", "clu", "app_ro", "--rotate-password")
		pw := textPassword(t, res.Stdout)
		if !secrets.MeetsRules(pw) || len(pw) != 32 {
			t.Errorf("password %q", pw)
		}
		if strings.Contains(res.Stderr, pw) {
			t.Error("the password leaked to stderr")
		}
		var body string
		for _, r := range fx.Fake.RequestLog() {
			if r.Method == "PUT" {
				body = r.Body
			}
		}
		if !strings.Contains(body, pw) || !strings.Contains(body, "data_reader") {
			t.Errorf("PUT body should carry the new password and the unchanged access: %s", body)
		}
		if got := accessSummary(credentialByName(t, fx, "app_ro").Access); got != "read+write:orders.*" {
			t.Errorf("access changed by a password rotation: %q", got)
		}
	})
	t.Run("rotate as json", func(t *testing.T) {
		obj := jsonObject(t, mustRun(t, "credential", "update", "clu", "app_ro", "--rotate-password", "-o", "json"))
		if pw, _ := obj["password"].(string); !secrets.MeetsRules(pw) || obj["status"] != "updated" {
			t.Errorf("object = %v", obj)
		}
	})
	t.Run("password from the environment", func(t *testing.T) {
		const pw = "N3w-Passw0rd!aa"
		t.Setenv("NEW_PW", pw)
		res := mustRun(t, "credential", "update", "clu", "app_ro", "--password-env", "NEW_PW")
		if strings.Contains(res.Stdout+res.Stderr, pw) || strings.Contains(res.Stdout, "password:") {
			t.Errorf("a supplied password was printed:\n%s\n%s", res.Stdout, res.Stderr)
		}
	})
	t.Run("nothing to update", func(t *testing.T) {
		fx.Fake.ResetLog()
		wantExit(t, runResource(t, "credential", "update", "clu", "app_ro"), ExitConfigInvalid, "nothing to update")
		if len(fx.Fake.RequestLog()) != 0 {
			t.Error("requests were sent")
		}
	})
	t.Run("invalid access flags", func(t *testing.T) {
		wantExit(t, runResource(t, "credential", "update", "clu", "app_ro", "--bucket", "b"), ExitConfigInvalid, "--privileges is required")
	})
	t.Run("weak password", func(t *testing.T) {
		t.Setenv("NEW_PW", "weak")
		wantExit(t, runResource(t, "credential", "update", "clu", "app_ro", "--password-env", "NEW_PW"), ExitConfigInvalid, "does not meet")
	})
	t.Run("unknown credential", func(t *testing.T) {
		wantExit(t, runResource(t, "credential", "update", "clu", "ghost", "--rotate-password"), ExitConfigInvalid, "credential ghost not found")
	})
	t.Run("stale version is retried once", func(t *testing.T) {
		fx.Fake.ResetLog()
		fx.Fake.FailNext("PUT", `/users/`, 412, 1)
		mustRun(t, "credential", "update", "clu", "app_ro", "--privileges", "read", "--bucket", "retry")
		var seq []string
		for _, r := range fx.Fake.RequestLog() {
			if strings.Contains(r.Path, "/users/") {
				seq = append(seq, r.Method)
			}
		}
		if got := strings.Join(seq, " "); got != "GET PUT GET PUT" {
			t.Errorf("requests = %q, want GET PUT GET PUT", got)
		}
		if got := accessSummary(credentialByName(t, fx, "app_ro").Access); got != "read:retry.*" {
			t.Errorf("access = %q", got)
		}
	})
	t.Run("API failure", func(t *testing.T) {
		fx.Fake.FailNext("PUT", `/users/`, 422, 1)
		res := runResource(t, "credential", "update", "clu", "app_ro", "--rotate-password")
		if res.Err == nil || !strings.Contains(res.Err.Error(), "updating credential app_ro") {
			t.Fatalf("err = %v", res.Err)
		}
		if strings.Contains(res.Stdout, "password:") {
			t.Error("a password was printed for a failed update")
		}
	})
}

func TestCredentialDelete(t *testing.T) {
	fx := newFixture(t)
	mustRun(t, "credential", "create", "clu", "app_ro", "--privileges", "read", "--bucket", "b")

	t.Run("no terminal and no --yes is blocked", func(t *testing.T) {
		wantExit(t, runResource(t, "credential", "delete", "clu", "app_ro"), ExitBlocked, "no terminal")
		if credentialByName(t, fx, "app_ro") == nil {
			t.Error("credential was deleted")
		}
	})
	t.Run("declined", func(t *testing.T) {
		fakeTTY(t)
		res := runResourceIn(t, "n\n", "credential", "delete", "clu", "app_ro")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		wantContains(t, res.Stdout, "cancelled")
		if credentialByName(t, fx, "app_ro") == nil {
			t.Error("credential was deleted")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		wantContains(t, mustRun(t, "credential", "delete", "clu", "ghost", "-y").Stdout, "credential clu/ghost not found, nothing to do")
	})
	t.Run("delete", func(t *testing.T) {
		res := mustRun(t, "credential", "delete", "clu", "app_ro", "--yes")
		wantContains(t, res.Stdout, "deleted credential app_ro")
		if credentialByName(t, fx, "app_ro") != nil {
			t.Error("credential still exists")
		}
	})
	t.Run("json", func(t *testing.T) {
		mustRun(t, "credential", "create", "clu", "tmp", "--privileges", "read", "--bucket", "b")
		obj := jsonObject(t, mustRun(t, "credential", "delete", "clu", "tmp", "-y", "-o", "json"))
		if obj["status"] != "deleted" || obj["name"] != "tmp" {
			t.Errorf("object = %v", obj)
		}
	})
}

// TestCredentialSecretsNeverLeak runs the credential commands at debug level and checks that the
// API key never shows up and the password shows up exactly once on stdout and never on stderr.
func TestCredentialSecretsNeverLeak(t *testing.T) {
	fx := newFixture(t)
	res := mustRun(t, "credential", "create", "clu", "leaky", "--privileges", "read", "--bucket", "b", "--log-level", "debug")
	pw := textPassword(t, res.Stdout)
	for _, out := range []string{res.Stdout, res.Stderr} {
		if strings.Contains(out, fx.Fake.APIKey) {
			t.Errorf("API key leaked:\n%s", out)
		}
	}
	if strings.Contains(res.Stderr, pw) {
		t.Error("password leaked to stderr")
	}
	if strings.Count(res.Stdout, pw) != 1 {
		t.Errorf("password appears %d times on stdout", strings.Count(res.Stdout, pw))
	}
	list := mustRun(t, "credential", "list", "clu", "--log-level", "debug")
	if strings.Contains(list.Stdout+list.Stderr, pw) {
		t.Error("password shown by list")
	}
}
