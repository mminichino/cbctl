package resources

import (
	"os"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/secrets"
)

const fullYAML = baseYAML + `    deletionProtection: true
    hibernation:
      timezone: US/Eastern
      weekdays: true
      weekend: false
    allowedCidrs:
      - cidr: 198.51.100.0/24
        comment: app
    buckets:
      - name: orders
        memoryAllocationInMb: 256
        replicas: 1
        scopes:
          - name: billing
            collections:
              - name: invoices
                maxTTL: 0
    databaseCredentials:
      - name: orders_app
        password: ${APP_PW}
        access:
          - privileges: [data_reader, data_writer]
            resources:
              buckets:
                - name: orders
      - name: generated
        access:
          - privileges: [data_reader]
`

func TestApplyEverythingThenConverges(t *testing.T) {
	e := newEnv(t, fullYAML)
	p := e.plan()
	for _, want := range []string{"project/sandbox", "cluster/one"} {
		if !strings.Contains(strings.Join(types(p), "\n"), want+":create") {
			t.Fatalf("plan lacks %s create: %v", want, types(p))
		}
	}
	e.apply(p)
	if requests(e.f, "POST", `/projects$`) != 1 || requests(e.f, "POST", `/clusters$`) != 1 {
		t.Fatalf("unexpected create counts: %v", e.f.RequestLog())
	}

	p2 := e.plan()
	requireAllNoop(t, p2)

	generated := e.cfg.CredentialsOutput()
	entries, _ := os.ReadDir(generated)
	if len(entries) == 0 {
		t.Fatalf("generated password was not saved under %s (lines: %v)", generated, e.lines)
	}
}

func TestApplyUpdatesDriftAndPrunes(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())

	changed := strings.Replace(fullYAML, "replicas: 1", "replicas: 2", 1)
	changed = strings.Replace(changed, "comment: app", "comment: changed", 1)
	changed = strings.Replace(changed, "privileges: [data_reader, data_writer]", "privileges: [data_reader]", 1)
	e.load(changed)
	p := e.plan()
	requireType(t, p, "bucket", "one/orders", plan.Update)
	requireType(t, p, "credential", "one/orders_app", plan.Update)
	cred := find(t, p, "credential", "one/orders_app")
	if !strings.Contains(strings.Join(cred.Remarks, "\n"), "passwords are not compared") {
		t.Fatalf("missing password note: %v", cred.Remarks)
	}
	e.apply(p)
	requireAllNoop(t, e.plan())

	// Dropping a credential and a bucket plans deletes only with --prune.
	trimmed := strings.Replace(changed, `      - name: generated
        access:
          - privileges: [data_reader]
`, "", 1)
	e.load(trimmed)
	if p := e.plan(); strings.Contains(strings.Join(types(p), "\n"), "credential/one/generated:delete") {
		t.Fatalf("delete planned without --prune")
	}
	pp := e.plan(plan.Options{Prune: true})
	del := requireType(t, pp, "credential", "one/generated", plan.Delete)
	if del.Summary == "" {
		t.Fatalf("delete lacks access summary")
	}
	e.apply(pp)
	if _, err := os.Stat(secrets.SecretPath(e.cfg.CredentialsOutput(), "one", "generated")); err != nil {
		t.Fatalf("secret directory was removed: %v", err)
	}
	if !strings.Contains(joined(e.lines), "local secret file kept") {
		t.Fatalf("secret file keep note missing: %v", e.lines)
	}
}

func TestDeletionProtectionBlocksPrune(t *testing.T) {
	e := newEnv(t, fullYAML)
	e.apply(e.plan())
	noBucket := strings.Replace(fullYAML, `    buckets:
      - name: orders
        memoryAllocationInMb: 256
        replicas: 1
        scopes:
          - name: billing
            collections:
              - name: invoices
                maxTTL: 0
`, "    buckets: []\n", 1)
	noBucket = strings.Replace(noBucket, `          - privileges: [data_reader, data_writer]
            resources:
              buckets:
                - name: orders
`, `          - privileges: [data_reader]
`, 1)
	e.load(noBucket)
	p := e.plan(plan.Options{Prune: true})
	b := requireType(t, p, "bucket", "one/orders", plan.Blocked)
	if !strings.Contains(b.Reason, "deletion protection is on for") {
		t.Fatalf("reason %q", b.Reason)
	}
	if res := plan.Execute(t.Context(), e.rc, p); res.OK() && requests(e.f, "DELETE", `/buckets/`) > 0 {
		t.Fatalf("bucket deleted despite protection")
	}
}

func TestProjectRenameHintAndPinnedID(t *testing.T) {
	e := newEnv(t, baseYAML)
	e.f.SeedProject("other")
	p := e.plan()
	requireType(t, p, "project", "sandbox", plan.Create)

	pid := e.f.SeedProject("renamed")
	pinned := strings.Replace(baseYAML, "name: sandbox", "name: sandbox2\n  id: "+pid, 1)
	e.load(pinned)
	p = e.plan()
	a := find(t, p, "project", "sandbox2")
	if a.Type != plan.Update {
		t.Fatalf("pinned id should update the renamed project, got %s %s", a.Type, a.Reason)
	}

	missing := strings.Replace(baseYAML, "name: sandbox", "name: sandbox3\n  id: 00000000-0000-0000-0000-000000000000", 1)
	e.load(missing)
	if _, err := plan.BuildPlan(t.Context(), e.rc, BuildTree(), plan.Options{}); err == nil {
		t.Fatalf("expected an error for an unknown pinned project id")
	}
}
