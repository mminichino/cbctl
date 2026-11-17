package resources

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/secrets"
)

const backupYAML = baseYAML + `    backupSchedule:
      interval: 24
      retention: 720
      startTime: "02:00"
    buckets:
      - name: orders
        memoryAllocationInMb: 256
        backupSchedule:
          dayOfWeek: monday
          startAt: 3
          incrementalEvery: 12
          retentionTime: 90days
`

func TestBackupSchedulesCreateUpdateDelete(t *testing.T) {
	e := newEnv(t, backupYAML)
	e.apply(e.plan())
	requireAllNoop(t, e.plan())
	if requests(e.f, "PUT", `cloudsnapshotbackupschedule`) == 0 && requests(e.f, "POST", `cloudsnapshotbackupschedule`) == 0 {
		t.Fatalf("no cluster snapshot schedule write: %v", e.f.RequestLog())
	}
	if requests(e.f, "POST", `backup/schedules`)+requests(e.f, "PUT", `backup/schedules`) == 0 {
		t.Fatalf("no bucket backup schedule write: %v", e.f.RequestLog())
	}

	changed := strings.Replace(backupYAML, "retention: 720", "retention: 1440", 1)
	changed = strings.Replace(changed, "startAt: 3", "startAt: 5", 1)
	e.load(changed)
	p := e.plan()
	requireType(t, p, "backup-schedule", "one", plan.Update)
	requireType(t, p, "bucket-backup-schedule", "one/orders", plan.Update)
	e.apply(p)
	requireAllNoop(t, e.plan())

	absent := strings.Replace(changed, "    backupSchedule:\n      interval: 24\n", "    backupSchedule:\n      ensure: absent\n      interval: 24\n", 1)
	e.load(absent)
	p = e.plan()
	requireType(t, p, "backup-schedule", "one", plan.Delete)
	e.apply(p)
	requireAllNoop(t, e.plan())
	if requests(e.f, "DELETE", `cloudsnapshotbackupschedule`) != 1 {
		t.Fatalf("expected one snapshot schedule delete")
	}
}

func TestBucketBackupScheduleRemovedWithPrune(t *testing.T) {
	e := newEnv(t, backupYAML)
	e.apply(e.plan())
	e.load(strings.Replace(backupYAML, `        backupSchedule:
          dayOfWeek: monday
          startAt: 3
          incrementalEvery: 12
          retentionTime: 90days
`, "", 1))
	p := e.plan()
	if len(p.Unmanaged) == 0 {
		t.Fatalf("an undeclared bucket backup schedule is reported: %v", types(p))
	}
	pp := e.plan(plan.Options{Prune: true})
	requireType(t, pp, "bucket-backup-schedule", "one/orders", plan.Delete)
	e.apply(pp)
	requireAllNoop(t, e.plan())
}

func TestScheduleUpdateAndEnsureAbsent(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	requireAllNoop(t, e.plan())

	e.load(strings.Replace(fullYAML, "weekend: false", "weekend: true", 1))
	p := e.plan()
	requireType(t, p, "schedule", "one", plan.Update)
	e.apply(p)
	requireAllNoop(t, e.plan())

	e.load(strings.Replace(fullYAML, "      weekend: false\n", "      weekend: false\n      ensure: absent\n", 1))
	p = e.plan()
	requireType(t, p, "schedule", "one", plan.Delete)
	e.apply(p)
	requireAllNoop(t, e.plan())
	if requests(e.f, "DELETE", `onOffSchedule$`) != 1 {
		t.Fatalf("expected one schedule delete: %v", e.f.RequestLog())
	}
}

func TestAllowedCIDRReplaceAndPrune(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())

	e.load(strings.Replace(fullYAML, "comment: app", "comment: other", 1))
	p := e.plan()
	a := requireType(t, p, "allowed-cidr", "one/198.51.100.0/24", plan.Replace)
	if len(a.Notes) == 0 && len(a.Remarks) == 0 && a.Reason == "" {
		t.Fatalf("replace without explanation")
	}
	e.apply(p)
	requireAllNoop(t, e.plan())

	e.load(strings.Replace(fullYAML, "198.51.100.0/24", "203.0.113.0/24", 1))
	pp := e.plan(plan.Options{Prune: true})
	requireType(t, pp, "allowed-cidr", "one/203.0.113.0/24", plan.Create)
	requireType(t, pp, "allowed-cidr", "one/198.51.100.0/24", plan.Delete)
	e.apply(pp)
	requireAllNoop(t, e.plan(plan.Options{Prune: true}))
}

func TestAllowedCIDRExpiredDesiredIsNoop(t *testing.T) {
	yaml := strings.Replace(fullYAML, "        comment: app\n", "        comment: app\n        expiresAt: \"2001-01-01T00:00:00Z\"\n", 1)
	e := newEnv(t, yaml)
	p := e.plan()
	a := find(t, p, "allowed-cidr", "one/198.51.100.0/24")
	if a.Type != plan.Noop || len(a.Notes) == 0 {
		t.Fatalf("%s %v", a.Type, a.Notes)
	}
}

func TestProjectDescriptionUpdate(t *testing.T) {
	e := newEnv(t, strings.Replace(baseYAML, "  name: sandbox\n", "  name: sandbox\n  description: hello\n", 1))
	e.f.SeedProject("sandbox")
	p := e.plan()
	a := requireType(t, p, "project", "sandbox", plan.Update)
	if len(a.Changes) != 1 {
		t.Fatalf("changes %+v", a.Changes)
	}
	e.apply(p)
	requireType(t, e.plan(), "project", "sandbox", plan.Noop)
}

func TestBucketImmutableAndUpdate(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())

	e.load(strings.Replace(fullYAML, "memoryAllocationInMb: 256", "memoryAllocationInMb: 512", 1))
	p := e.plan()
	requireType(t, p, "bucket", "one/orders", plan.Update)
	e.apply(p)
	requireAllNoop(t, e.plan())

	e.load(strings.Replace(fullYAML, "        replicas: 1\n", "        replicas: 1\n        type: ephemeral\n", 1))
	a := requireType(t, e.plan(), "bucket", "one/orders", plan.Blocked)
	if a.Topic != "idempotency" {
		t.Fatalf("topic %q reason %q", a.Topic, a.Reason)
	}
}

func TestBucketPruneBlockedByNothingWithoutProtection(t *testing.T) {
	e := newEnv(t, strings.Replace(fullYAML, "deletionProtection: true", "deletionProtection: false", 1))
	e.apply(e.plan())
	e.load(strings.Replace(strings.Replace(fullYAML, "deletionProtection: true", "deletionProtection: false", 1),
		"        scopes:\n          - name: billing\n            collections:\n              - name: invoices\n                maxTTL: 0\n", "", 1))
	p := e.plan(plan.Options{Prune: true})
	requireType(t, p, "scope", "one/orders/billing", plan.Delete)
	e.apply(p)
	if requests(e.f, "DELETE", `scopes/billing`) != 1 {
		t.Fatalf("scope delete missing: %v", e.f.RequestLog())
	}
	requireAllNoop(t, e.plan(plan.Options{Prune: true}))
}

func TestCollectionMaxTTLUpdateAndVersionGate(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	e.load(strings.Replace(fullYAML, "maxTTL: 0", "maxTTL: 300", 1))
	p := e.plan()
	requireType(t, p, "collection", "one/orders/billing/invoices", plan.Update)
	e.apply(p)
	requireAllNoop(t, e.plan())

	rec := newCollection()
	scope := &plan.Action{Kind: kindScope, Key: "one/orders/billing", Desired: Obj{"name": "billing"}}
	cluster := &plan.Action{Kind: kindCluster, Key: "one",
		Observed: &plan.Observed{Body: Obj{"couchbaseServer": Obj{"version": "7.2.4"}}}}
	bucket := &plan.Action{Kind: kindBucket, Key: "one/orders", Parent: cluster}
	scope.Parent = bucket
	obs := &plan.Observed{ID: "invoices", Body: Obj{"name": "invoices", "maxTTL": int64(0)}}
	a, err := rec.Diff(t.Context(), e.rc, Obj{"name": "invoices", "maxTTL": int64(5)}, obs, scope)
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != plan.Blocked || !strings.Contains(a.Reason, "7.6.0 or later (one runs 7.2.4)") {
		t.Fatalf("%s %q", a.Type, a.Reason)
	}
	cluster.Observed.Body["couchbaseServer"] = Obj{"version": "7.6.2"}
	if a, _ = rec.Diff(t.Context(), e.rc, Obj{"name": "invoices", "maxTTL": int64(5)}, obs, scope); a.Type != plan.Update {
		t.Fatalf("type %s", a.Type)
	}
	if a, _ = rec.Diff(t.Context(), e.rc, Obj{"name": "invoices"}, obs, scope); a.Type != plan.Noop {
		t.Fatalf("an undeclared maxTTL is not compared: %s", a.Type)
	}
}

func TestSystemScopesAndCollections(t *testing.T) {
	for _, tc := range []struct {
		scope, coll any
		want        bool
	}{
		{"_system", "_query", true},
		{"_default", "_default", true},
		{"_default", "extra", false},
		{"billing", "_default", false},
	} {
		if got := IsSystemCollection(tc.scope, tc.coll); got != tc.want {
			t.Errorf("IsSystemCollection(%v,%v)=%v", tc.scope, tc.coll, got)
		}
	}
	if !IsSystemScope("_default") || !IsSystemScope("_system") || IsSystemScope("billing") {
		t.Fatal("IsSystemScope")
	}
	for text, want := range map[string]string{"7.6.2": "7.6.2", "7.2.4-1234": "7.2.4", "": "", "x": ""} {
		if got := versionText(ParseVersion(text)); got != want {
			t.Errorf("ParseVersion(%q)=%q", text, got)
		}
	}
	if !versionLess([]int{7, 2, 4}, MaxTTLMinVersion) || versionLess([]int{7, 6}, MaxTTLMinVersion) {
		t.Fatal("versionLess")
	}
}

func TestDefaultScopeIsNotCreatedOrPruned(t *testing.T) {
	yaml := strings.Replace(fullYAML, "        scopes:\n", "        scopes:\n          - name: _default\n            collections:\n              - name: extra\n", 1)
	e := newEnv(t, yaml)
	e.apply(e.plan())
	requireAllNoop(t, e.plan(plan.Options{Prune: true}))
	if requests(e.f, "POST", `scopes$`) != 1 {
		t.Fatalf("only billing should be created: %v", e.f.RequestLog())
	}
}

const credYAML = baseYAML + `    buckets:
      - name: orders
        memoryAllocationInMb: 256
    databaseCredentials:
      - name: app
        password: ${APP_PW}
        access:
          - privileges: [data_reader]
            resources:
              buckets:
                - name: orders
`

func TestCredentialRotateUsesAccessAndSavesSecret(t *testing.T) {
	yaml := strings.Replace(credYAML, "        password: ${APP_PW}\n", "", 1)
	e := newEnv(t, yaml)
	e.apply(e.plan())
	path := secrets.SecretPath(e.cfg.CredentialsOutput(), "one", "app")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("secret not written: %v", err)
	}
	if !strings.Contains(joined(e.lines), path) {
		t.Fatalf("saved message missing: %v", e.lines)
	}
	if len(e.warns) == 0 {
		t.Logf("no git warning (directory not in a repository)")
	}
	requireAllNoop(t, e.plan())

	e.rc.RotatePasswords = true
	p := e.plan()
	a := requireType(t, p, "credential", "one/app", plan.Update)
	if a.Label != "rotate password" {
		t.Fatalf("label %q", a.Label)
	}
	e.f.ResetLog()
	e.apply(p)
	second, _ := os.ReadFile(path)
	if string(first) == string(second) {
		t.Fatalf("secret file not rewritten")
	}
	for _, r := range e.f.RequestLog() {
		if r.Method == "PUT" && strings.Contains(r.Path, "/users/") && !strings.Contains(string(r.Body), `"access"`) {
			t.Fatalf("rotation sent no access: %s", r.Body)
		}
	}
	if requests(e.f, "PUT", `/users/`) != 1 {
		t.Fatalf("expected one credential PUT: %v", e.f.RequestLog())
	}
}

func TestCredentialCreateRefusesExistingSecretFile(t *testing.T) {
	yaml := strings.Replace(credYAML, "        password: ${APP_PW}\n", "", 1)
	e := newEnv(t, yaml)
	path := secrets.SecretPath(e.cfg.CredentialsOutput(), "one", "app")
	writeFile(t, path, "{}")
	pid := e.f.SeedProject("sandbox")
	e.f.SeedCluster(pid, "one", "healthy")
	p := e.plan()
	res := planExecute(e, p)
	if res.OK() {
		t.Fatalf("create must fail before calling Capella when the secret file exists")
	}
	if requests(e.f, "POST", `/users$`) != 0 {
		t.Fatalf("credential created despite the preflight: %v", e.f.RequestLog())
	}
}

func planExecute(e *env, p *plan.Plan) *plan.ApplyResult {
	return plan.Execute(e.t.Context(), e.rc, p)
}

func TestCredentialForceOverwriteSecret(t *testing.T) {
	yaml := strings.Replace(credYAML, "        password: ${APP_PW}\n", "", 1)
	e := newEnv(t, yaml)
	path := secrets.SecretPath(e.cfg.CredentialsOutput(), "one", "app")
	writeFile(t, path, "{}")
	pid := e.f.SeedProject("sandbox")
	e.f.SeedCluster(pid, "one", "healthy")
	e.apply(e.planWith(BuildTreeWith(Options{ForceOverwriteSecret: true})))
	if b, _ := os.ReadFile(path); string(b) == "{}" {
		t.Fatalf("secret was not overwritten")
	}
}

func TestCredentialAdvancedBlockedAndNeverPruned(t *testing.T) {
	e := newEnv(t, credYAML)
	rec := newCredential(Options{})
	obs := &plan.Observed{ID: "u1", Body: Obj{"name": "app", "userRoles": []any{"admin"}}}
	desired := Obj{"name": "app", "access": []any{}}
	a, err := rec.Diff(t.Context(), e.rc, desired, obs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != plan.Blocked || a.Topic != "credentials" || !strings.Contains(a.Reason, "advanced credential") {
		t.Fatalf("%s %q %q", a.Type, a.Topic, a.Reason)
	}
	d, err := rec.DeleteAction(t.Context(), e.rc, *obs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != plan.Noop || len(d.Notes) == 0 {
		t.Fatalf("advanced credentials are never pruned: %s %v", d.Type, d.Notes)
	}
	basic := plan.Observed{ID: "u2", Body: Obj{"name": "basic", "access": []any{}}}
	if d, _ = rec.DeleteAction(t.Context(), e.rc, basic, nil); d.Type != plan.Delete {
		t.Fatalf("type %s", d.Type)
	}
}

func TestAccessHelpers(t *testing.T) {
	read := []any{Obj{"privileges": []any{"data_reader"}, "resources": Obj{"buckets": []any{Obj{"name": "orders"}}}}}
	rw := []any{Obj{"privileges": []any{"data_reader", "data_writer"}, "resources": Obj{"buckets": []any{Obj{"name": "orders"}}}}}
	if got := AccessSummary(rw); got != "read+write · orders" {
		t.Fatalf("summary %q", got)
	}
	changes := AccessChanges(rw, read)
	if len(changes) != 1 || changes[0].Path != "orders" || changes[0].Before != "read" || changes[0].After != "read+write" {
		t.Fatalf("changes %+v", changes)
	}
	if len(AccessChanges(read, read)) != 0 {
		t.Fatal("equal access reported as changed")
	}
	// Capella spells "every scope and collection" with "*"; the file leaves them out.
	api := []any{Obj{"privileges": []any{"data_reader"}, "resources": Obj{"buckets": []any{
		Obj{"name": "orders", "scopes": []any{Obj{"name": "*"}}}}}}}
	if len(AccessChanges(read, api)) != 0 {
		t.Fatalf("wildcard scopes should equal a whole-bucket grant: %+v", AccessChanges(read, api))
	}
	scoped := []any{Obj{"privileges": []any{"data_reader"}, "resources": Obj{"buckets": []any{
		Obj{"name": "orders", "scopes": []any{Obj{"name": "billing", "collections": []any{"b", "a"}}}}}}}}
	if got := AccessSummary(scoped); got != "read · orders/billing/{a,b}" {
		t.Fatalf("summary %q", got)
	}
	all := []any{Obj{"privileges": []any{"data_reader"}}}
	if got := AccessSummary(all); got != "read · (all buckets)" {
		t.Fatalf("summary %q", got)
	}
	if IsAdvanced(Obj{}) || !IsAdvanced(Obj{"userRoles": []any{"x"}}) || !IsAdvanced(Obj{"credentialType": "advanced"}) {
		t.Fatal("IsAdvanced")
	}
	if got := PrivilegeLabel(map[string]bool{"data_writer": true, "data_reader": true, "xattr_read": true}); got != "read+write+xattr_read" {
		t.Fatalf("label %v", got)
	}
}

func TestCredentialRotateFailureRestoresSecret(t *testing.T) {
	yaml := strings.Replace(credYAML, "        password: ${APP_PW}\n", "", 1)
	e := newEnv(t, yaml)
	e.apply(e.plan())
	path := secrets.SecretPath(e.cfg.CredentialsOutput(), "one", "app")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e.rc.RotatePasswords = true
	p := e.plan()
	e.f.FailNext("PUT", `/users/`, 400, 1)
	res := plan.Execute(context.Background(), e.rc, p)
	if res.OK() {
		t.Fatal("rotation should fail on the injected 400")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(first) {
		t.Fatalf("secret file not restored after a failed rotation:\nbefore %s\nafter  %s", first, after)
	}
}

func TestCredentialRotateRestoreFailureIsReported(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced")
	}
	yaml := strings.Replace(credYAML, "        password: ${APP_PW}\n", "", 1)
	e := newEnv(t, yaml)
	e.apply(e.plan())
	path := secrets.SecretPath(e.cfg.CredentialsOutput(), "one", "app")
	e.rc.RotatePasswords = true
	p := e.plan()
	// Make the restore fail: after the new secret is written, the file becomes read-only.
	e.f.FailNextOpts("PUT", "/v4/organizations/*/projects/*/clusters/*/users/*", capellafake.FaultOptions{
		Status: 400, Before: func() { _ = os.Chmod(path, 0o400) },
	})
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	res := plan.Execute(context.Background(), e.rc, p)
	if res.OK() {
		t.Fatal("rotation should fail")
	}
	var re *secrets.RestoreError
	if !errors.As(res.Err, &re) {
		t.Fatalf("failure is not a *secrets.RestoreError: %v", res.Err)
	}
}
