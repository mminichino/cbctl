package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// dataDoc is capellactl's MINIMAL fixture (project nova-dev, cluster nova-dev-01) in cbctl form.
const dataDoc = `apiVersion: cbctl/v1
project:
  name: nova-dev
clusters:
  - name: nova-dev-01
    cloudProvider: {type: aws, region: us-east-1, cidr: 10.0.0.0/20}
    availability: {type: multi}
    support: {plan: basic, timezone: ET}
    serviceGroups:
      - node:
          compute: {cpu: 4, ram: 16}
          disk: {type: gp3, storage: 50, iops: 3000}
        numOfNodes: 3
        services: [data, index, query]
`

const dataOrders = `    buckets:
      - name: orders
        memoryAllocationInMb: 100
        scopes:
          - name: inventory
            collections:
              - name: items
              - name: suppliers
                maxTTL: -1
          - name: billing
            collections:
              - name: invoices
`

const dataSuppliers = `    buckets:
      - name: orders
        memoryAllocationInMb: 100
        scopes:
          - name: inventory
            collections:
              - name: suppliers
                maxTTL: -1
`

const dataOrdersBucket = `    buckets:
      - name: orders
        memoryAllocationInMb: 100
`

// dataOrdersApp is capellactl's ORDERS_BUCKET + ORDERS_APP fixture.
const dataOrdersApp = `    buckets:
      - name: orders
        memoryAllocationInMb: 100
        scopes:
          - name: inventory
            collections:
              - name: items
              - name: suppliers
    databaseCredentials:
      - name: orders_app
        password: ${ORDERS_APP_PASSWORD}
        access:
          - privileges: [data_reader, data_writer]
            resources:
              buckets:
                - name: orders
                  scopes:
                    - name: inventory
                      collections: [items, suppliers]
`

const dataPW = "S3cret!Orders-app-pw"

// dataWrite is one write on the fake with the path relative to the cluster.
type dataWrite struct {
	Method string
	Path   string
	Body   any
}

// dataScenario seeds project nova-dev with cluster nova-dev-01 running the given server version.
func dataScenario(t *testing.T, version string) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s = newScenario(t)
	pid = s.Fake.SeedProject("nova-dev")
	cid = s.Fake.SeedCluster(pid, "nova-dev-01", "healthy",
		capellafake.WithField("couchbaseServer", map[string]any{"version": version}))
	return s, pid, cid
}

// dataOrdersScenario adds the orders bucket (100 MB) to dataScenario.
func dataOrdersScenario(t *testing.T, version string) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s, pid, cid = dataScenario(t, version)
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 100))
	return s, pid, cid
}

// dataWrites lists the writes with the cluster prefix (and "/buckets/b3JkZXJz" for orders) cut.
func dataWrites(s *clitest.Scenario, pid, cid string) []dataWrite {
	base := "/v4/organizations/" + s.Fake.OrgID + "/projects/" + pid + "/clusters/" + cid
	var out []dataWrite
	for _, c := range s.Fake.Writes() {
		p := strings.TrimPrefix(c.Path, base)
		p = strings.TrimPrefix(p, "/buckets/b3JkZXJz")
		out = append(out, dataWrite{c.Method, p, normJSON(c.Body)})
	}
	return out
}

// lastSegments is the last path segment of every write.
func lastSegments(s *clitest.Scenario) []string {
	var out []string
	for _, c := range s.Fake.Writes() {
		out = append(out, c.Path[strings.LastIndex(c.Path, "/")+1:])
	}
	return out
}

// normJSON round-trips v through JSON so literal maps compare with decoded bodies.
func normJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}

func wantWrites(t *testing.T, got []dataWrite, want ...dataWrite) {
	t.Helper()
	for i := range want {
		want[i].Body = normJSON(want[i].Body)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("writes:\n got %+v\nwant %+v", got, want)
	}
}

func dataApply(t *testing.T, s *clitest.Scenario, args ...string) clitest.Result {
	t.Helper()
	args = append([]string{"capella", "apply", "-y"}, args...)
	return mustScenario(t, s.Run(args...), args...)
}

func dataOut(r clitest.Result) string { return r.Stdout + r.Stderr }

func header(c capellafake.Call, name string) string { return c.Header.Get(name) }

func TestScopes_CAP025_AC1_CreateOrderAndBodies(t *testing.T) {
	s, pid, cid := dataOrdersScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc+dataOrders)
	r := dataApply(t, s)
	wantContains(t, r.Stdout, "  + scope nova-dev-01/orders/inventory", "  + collection nova-dev-01/orders/inventory/items",
		"  + collection nova-dev-01/orders/inventory/suppliers  maxTTL -1",
		"  + scope nova-dev-01/orders/billing", "  + collection nova-dev-01/orders/billing/invoices")
	wantWrites(t, dataWrites(s, pid, cid),
		dataWrite{"POST", "/scopes", map[string]any{"name": "inventory"}},
		dataWrite{"POST", "/scopes", map[string]any{"name": "billing"}},
		dataWrite{"POST", "/scopes/inventory/collections", map[string]any{"name": "items"}},
		dataWrite{"POST", "/scopes/inventory/collections", map[string]any{"name": "suppliers", "maxTTL": -1}},
		dataWrite{"POST", "/scopes/billing/collections", map[string]any{"name": "invoices"}},
	)
}

func TestScopes_CAP025_AC1_NewBucketChildrenAfterBucket(t *testing.T) {
	s, _, _ := dataScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc+dataOrders)
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "  + scope nova-dev-01/orders/inventory  (after bucket is created)",
		"  + collection nova-dev-01/orders/inventory/items  (after scope is created)")
	dataApply(t, s)
	if got, want := lastSegments(s), []string{"buckets", "scopes", "scopes", "collections", "collections", "collections"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("write order %v, want %v", got, want)
	}
	r = s.Run("capella", "plan", "--detailed-exitcode")
	wantExit(t, r, 0)
}

func TestCollections_CAP025_AC2_MaxttlUpdate(t *testing.T) {
	s, pid, cid := dataOrdersScenario(t, "7.6.2")
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"suppliers": 0})
	writeCapellaYAML(t, s, dataDoc+dataSuppliers)
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "  ~ collection nova-dev-01/orders/inventory/suppliers", "maxTTL", "-1")
	dataApply(t, s)
	puts := s.Fake.Calls("PUT", "*")
	wantWrites(t, dataWrites(s, pid, cid), dataWrite{"PUT", "/scopes/inventory/collections/suppliers", map[string]any{"maxTTL": -1}})
	if h := header(puts[0], "If-Match"); h == "" {
		t.Fatal("PUT without If-Match")
	}
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
}

func TestCollections_CAP025_AC2_StaleEtagReplans(t *testing.T) {
	s, _, cid := dataOrdersScenario(t, "7.6.2")
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"suppliers": 0})
	writeCapellaYAML(t, s, dataDoc+dataSuppliers)
	s.Fake.FailNext("PUT", ".*/collections/suppliers$", 412, 1)
	dataApply(t, s)
	if n := len(s.Fake.Calls("PUT", "*")); n != 2 {
		t.Fatalf("PUTs = %d, want 2 (re-observed and retried once)", n)
	}
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
}

func TestCollections_CAP025_AC3_OldServerBlocked(t *testing.T) {
	s, _, cid := dataOrdersScenario(t, "7.2.4")
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"suppliers": 0})
	writeCapellaYAML(t, s, dataDoc+dataSuppliers)
	r := s.Run("capella", "apply", "-y")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, dataOut(r), "! collection nova-dev-01/orders/inventory/suppliers",
		"maxTTL can only be changed on Couchbase Server 7.6.0 or later (nova-dev-01 runs 7.2.4)")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestCollections_CAP025_AC3_OldServerStillCreates(t *testing.T) {
	s, pid, cid := dataOrdersScenario(t, "7.2.4")
	writeCapellaYAML(t, s, dataDoc+dataSuppliers)
	dataApply(t, s)
	wantWrites(t, dataWrites(s, pid, cid),
		dataWrite{"POST", "/scopes", map[string]any{"name": "inventory"}},
		dataWrite{"POST", "/scopes/inventory/collections", map[string]any{"name": "suppliers", "maxTTL": -1}})
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
}

func TestScopes_CAP025_AC5_SystemScopeIgnored(t *testing.T) {
	s, pid, cid := dataOrdersScenario(t, "8.0.1")
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"items": 0, "suppliers": -1})
	s.Fake.SeedScope(cid, "orders", "legacy", nil)
	writeCapellaYAML(t, s, dataDoc+dataSuppliers)
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	_, unmanaged, ok := strings.Cut(r.Stdout, "Unmanaged")
	if !ok {
		t.Fatalf("no Unmanaged section:\n%s", r.Stdout)
	}
	wantContains(t, unmanaged, "scope nova-dev-01/orders/legacy", "collection nova-dev-01/orders/inventory/items")
	if strings.Contains(r.Stdout, "_system") || strings.Contains(r.Stdout, "_default") {
		t.Fatalf("system scopes listed:\n%s", r.Stdout)
	}
	dataApply(t, s, "--prune")
	got := dataWrites(s, pid, cid)
	var paths []string
	for _, w := range got {
		paths = append(paths, w.Method+" "+w.Path)
	}
	want := []string{"DELETE /scopes/inventory/collections/items", "DELETE /scopes/legacy"}
	if !reflect.DeepEqual(paths, want) && !reflect.DeepEqual(paths, []string{want[1], want[0]}) {
		t.Fatalf("prune writes %v, want %v", paths, want)
	}
}

func TestScopes_CAP025_AC7_BeforeCredentials(t *testing.T) {
	s, _, _ := dataOrdersScenario(t, "8.0.1")
	s.Env["ORDERS_APP_PASSWORD"] = dataPW
	t.Setenv("ORDERS_APP_PASSWORD", dataPW)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	r := dataApply(t, s)
	clitest.AssertNoSecret(t, r, dataPW)
	if got, want := lastSegments(s), []string{"scopes", "collections", "collections", "users"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("write order %v, want %v", got, want)
	}
}

func TestScope_CAP025_AC1_CreateRecordsAndApplies(t *testing.T) {
	s, pid, cid := dataOrdersScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucket)
	r := mustScenario(t, s.Run("capella", "scope", "add", "nova-dev-01", "orders", "billing", "-y"), "scope add")
	wantContains(t, r.Stdout, "  + scope nova-dev-01/orders/billing")
	wantContains(t, s.ReadFile("capella.yaml"), "            scopes:\n              - name: billing\n")
	wantWrites(t, dataWrites(s, pid, cid), dataWrite{"POST", "/scopes", map[string]any{"name": "billing"}})
	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "scope", "add", "nova-dev-01", "orders", "billing", "-y"), "scope add")
	wantContains(t, dataOut(r), "already")
	clitest.AssertNoWrites(t, s.Fake)
	r = mustScenario(t, s.Run("capella", "scope", "list", "nova-dev-01", "orders"), "scope list")
	wantContains(t, r.Stdout, "NAME", "COLLECTIONS", "billing", "_default")
}

func TestScope_CAP025_AC1_BucketNotInFile(t *testing.T) {
	s, _, _ := dataOrdersScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc)
	r := s.Run("capella", "scope", "add", "nova-dev-01", "orders", "billing", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "bucket orders is not in capella.yaml")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestScope_CAP025_AC5_SystemScopeNotCreated(t *testing.T) {
	s, _, _ := dataOrdersScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucket)
	// cbctl: a system scope always exists, so adding it is "already exists" (exit 0).
	r := mustScenario(t, s.Run("capella", "scope", "add", "nova-dev-01", "orders", "_default", "-y"), "scope add")
	wantContains(t, r.Stdout, "already exists")
	r = s.Run("capella", "collection", "add", "nova-dev-01", "orders", "_default", "_default", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "collection name '_default' is not valid")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestCollection_CAP025_AC1_CreateAddsScope(t *testing.T) {
	s, pid, cid := dataOrdersScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucket)
	args := []string{"capella", "collection", "add", "nova-dev-01", "orders", "inventory", "items", "--max-ttl", "3600", "-y"}
	mustScenario(t, s.Run(args...), args...)
	wantContains(t, s.ReadFile("capella.yaml"),
		"            scopes:\n              - name: inventory\n                collections:\n                  - name: items\n                    maxTTL: 3600\n")
	wantWrites(t, dataWrites(s, pid, cid),
		dataWrite{"POST", "/scopes", map[string]any{"name": "inventory"}},
		dataWrite{"POST", "/scopes/inventory/collections", map[string]any{"name": "items", "maxTTL": 3600}})
	s.Fake.ResetCalls()
	// cbctl: re-adding an existing collection is "already exists" (exit 0), not exit 4.
	r := mustScenario(t, s.Run("capella", "collection", "add", "nova-dev-01", "orders", "inventory", "items", "-y"), "collection add")
	wantContains(t, dataOut(r), "already")
	clitest.AssertNoWrites(t, s.Fake)
	r = mustScenario(t, s.Run("capella", "collection", "list", "nova-dev-01", "orders", "inventory"), "collection list")
	wantContains(t, r.Stdout, "items", "3600")
}

const dataBilling = `    buckets:
      - name: orders
        memoryAllocationInMb: 100
        scopes:
          - name: billing
            collections:
              - name: invoices
              - name: refunds
`

// billingScenario is capellactl's billing fixture: orders/billing with invoices and refunds in
// Capella and in capella.yaml.
func billingScenario(t *testing.T) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s, pid, cid = dataOrdersScenario(t, "8.0.1")
	s.Fake.SeedScope(cid, "orders", "billing", map[string]int{"invoices": 0, "refunds": 0})
	writeCapellaYAML(t, s, dataDoc+dataBilling)
	return s, pid, cid
}

func billingGrant(scope string, collections ...string) []capellaapi.AccessEntry {
	sc := capellaapi.ResourceScope{Name: scope, Collections: collections}
	return []capellaapi.AccessEntry{{Privileges: []string{"data_reader"},
		Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders", Scopes: []capellaapi.ResourceScope{sc}}}}}}
}

func deletes(s *clitest.Scenario, pid, cid string) []string {
	var out []string
	for _, w := range dataWrites(s, pid, cid) {
		if w.Method == "DELETE" {
			out = append(out, w.Path)
		}
	}
	return out
}

func TestCollectionDelete_CAP026_AC1_WrongNameCancels(t *testing.T) {
	s, _, _ := billingScenario(t)
	s.TTY(true)
	s.Answers("refunds")
	r := mustScenario(t, s.Run("capella", "collection", "delete", "nova-dev-01", "orders", "billing", "invoices"), "collection delete")
	wantContains(t, r.Stdout, "cancelled; nothing was deleted")
	clitest.AssertNoWrites(t, s.Fake)
	s.TTY(false)
	r = s.Run("capella", "collection", "delete", "nova-dev-01", "orders", "billing", "invoices")
	wantExit(t, r, cli.ExitBlocked) // cbctl: no terminal and no -y exits 4
	wantContains(t, r.Stderr, "-y")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestScopeDelete_CAP026_AC2_ListsCollections(t *testing.T) {
	s, pid, cid := billingScenario(t)
	s.TTY(true)
	s.Answers("billing")
	r := mustScenario(t, s.Run("capella", "scope", "delete", "nova-dev-01", "orders", "billing"), "scope delete")
	wantContains(t, dataOut(r), "billing - 2 collections: invoices, refunds", "These collections and all their documents are deleted.")
	if got := deletes(s, pid, cid); !reflect.DeepEqual(got, []string{"/scopes/billing"}) {
		t.Fatalf("deletes %v: want one DELETE for the scope and its collections", got)
	}
	if strings.Contains(s.ReadFile("capella.yaml"), "scopes") {
		t.Fatalf("scope still in capella.yaml:\n%s", s.ReadFile("capella.yaml"))
	}
}

func TestCollectionDelete_CAP026_AC4_PruneOnly(t *testing.T) {
	s, pid, cid := billingScenario(t)
	writeCapellaYAML(t, s, dataDoc+strings.Replace(dataBilling, "              - name: refunds\n", "", 1))
	dataApply(t, s)
	clitest.AssertNoWrites(t, s.Fake)
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	_, unmanaged, _ := strings.Cut(r.Stdout, "Unmanaged")
	wantContains(t, unmanaged, "collection nova-dev-01/orders/billing/refunds")
	s.TTY(true)
	s.Answers("refunds", "y")
	r = mustScenario(t, s.Run("capella", "apply", "--prune"), "apply --prune")
	wantContains(t, dataOut(r), "  - collection nova-dev-01/orders/billing/refunds", "billing.refunds and all its documents are deleted.")
	if got := deletes(s, pid, cid); !reflect.DeepEqual(got, []string{"/scopes/billing/collections/refunds"}) {
		t.Fatalf("deletes %v", got)
	}
}

func TestScopeDelete_CAP026_AC4_PruneScope(t *testing.T) {
	s, pid, cid := billingScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucket)
	s.TTY(true)
	s.Answers("wrong")
	r := mustScenario(t, s.Run("capella", "apply", "--prune"), "apply --prune")
	wantContains(t, dataOut(r), "Apply cancelled; nothing was changed.")
	clitest.AssertNoWrites(t, s.Fake)
	s.Answers("billing", "y")
	r = mustScenario(t, s.Run("capella", "apply", "--prune"), "apply --prune")
	wantContains(t, dataOut(r), "billing - 2 collections: invoices, refunds")
	if got := deletes(s, pid, cid); !reflect.DeepEqual(got, []string{"/scopes/billing"}) {
		t.Fatalf("deletes %v", got)
	}
}

func TestScopeDelete_CAP026_AC5_ListsDependentCredentials(t *testing.T) {
	s, _, cid := billingScenario(t)
	s.Fake.SeedCredential(cid, "billing_app", billingGrant("billing", "invoices"), false)
	s.Fake.SeedCredential(cid, "billing_ro", billingGrant("billing"), false)
	s.Fake.SeedCredential(cid, "refunds_app", billingGrant("billing", "refunds"), false)
	s.Fake.SeedCredential(cid, "inventory_app", billingGrant("inventory"), false)
	s.TTY(true)
	s.Answers("no")
	r := mustScenario(t, s.Run("capella", "collection", "delete", "nova-dev-01", "orders", "billing", "invoices"), "collection delete")
	wantContains(t, r.Stderr, "  These credentials lose access to this collection:\n    billing_app\n")
	if strings.Contains(dataOut(r), "refunds_app") {
		t.Fatalf("refunds_app listed:\n%s", r.Stderr)
	}
	s.Answers("no")
	r = mustScenario(t, s.Run("capella", "scope", "delete", "nova-dev-01", "orders", "billing"), "scope delete")
	wantContains(t, r.Stderr, "  These credentials lose access to this scope:\n    billing_app\n    billing_ro\n    refunds_app\n")
	if strings.Contains(dataOut(r), "inventory_app") {
		t.Fatalf("inventory_app listed:\n%s", r.Stderr)
	}
	clitest.AssertNoWrites(t, s.Fake)
}

func TestCollectionDelete_CAP026_AC5_PruneListsDependentCredentials(t *testing.T) {
	s, _, cid := billingScenario(t)
	s.Fake.SeedCredential(cid, "refunds_app", billingGrant("billing", "refunds"), false)
	writeCapellaYAML(t, s, dataDoc+strings.Replace(dataBilling, "              - name: refunds\n", "", 1))
	s.TTY(true)
	s.Answers("refunds", "y")
	r := mustScenario(t, s.Run("capella", "apply", "--prune"), "apply --prune")
	wantContains(t, r.Stderr, "These credentials lose access to this collection:\n    refunds_app")
}

func TestCollectionDelete_CAP026_AC6_RerunNoop(t *testing.T) {
	s, _, _ := billingScenario(t)
	mustScenario(t, s.Run("capella", "collection", "delete", "nova-dev-01", "orders", "billing", "invoices", "-y"), "collection delete")
	mustScenario(t, s.Run("capella", "scope", "delete", "nova-dev-01", "orders", "billing", "-y"), "scope delete")
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "collection", "delete", "nova-dev-01", "orders", "billing", "invoices", "-y"), "collection delete")
	wantContains(t, r.Stdout, "collection nova-dev-01/orders/billing/invoices not found, nothing to do")
	r = mustScenario(t, s.Run("capella", "scope", "delete", "nova-dev-01", "orders", "billing", "-y"), "scope delete")
	wantContains(t, r.Stdout, "scope nova-dev-01/orders/billing not found, nothing to do")
	clitest.AssertNoWrites(t, s.Fake)
}

const dataBucketOrders = `    buckets:
      - name: orders
        storageBackend: magma
        memoryAllocationInMb: 1024
        durabilityLevel: majority
`

func seedMagmaOrders(s *clitest.Scenario, cid string, fields ...capellafake.Field) {
	base := []capellafake.Field{capellafake.WithField("storageBackend", "magma"),
		capellafake.WithField("memoryAllocationInMb", 1024), capellafake.WithField("durabilityLevel", "majority")}
	s.Fake.SeedBucket(cid, "orders", append(base, fields...)...)
}

func TestBucket_CAP023_AC1_CreateFinishesRecordedBucket(t *testing.T) {
	s, _, cid := dataScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc+dataBucketOrders)
	args := []string{"capella", "bucket", "add", "nova-dev-01", "orders", "--memory", "256", "--storage-backend", "couchstore", "-y"}
	r := mustScenario(t, s.Run(args...), args...)
	wantContains(t, r.Stderr, "not in Capella yet (an earlier run did not finish); creating it now")
	wantContains(t, s.ReadFile("capella.yaml"), "          - name: orders\n            storageBackend: couchstore\n            memoryAllocationInMb: 256\n            durabilityLevel: majority\n")
	posts := s.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters/"+cid+"/buckets")
	if len(posts) != 1 || posts[0].Body.(map[string]any)["storageBackend"] != "couchstore" {
		t.Fatalf("bucket POSTs: %v", posts)
	}
	r = mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked.")
}

func TestBucket_CAP023_AC4_ImmutableBlocked(t *testing.T) {
	for _, tc := range []struct {
		field, before, after string
		seed                 any
		extra                string
	}{
		{"type", "ephemeral", "couchbase", "ephemeral", ""},
		{"storageBackend", "couchstore", "magma", "couchstore", ""},
		{"vbuckets", "128", "1024", 128, "        vbuckets: 1024\n"},
		{"bucketConflictResolution", "lww", "seqno", "lww", ""},
		{"evictionPolicy", "valueOnly", "fullEviction", "valueOnly", "        evictionPolicy: fullEviction\n"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			s, _, cid := dataScenario(t, "8.0.1")
			seedMagmaOrders(s, cid, capellafake.WithField(tc.field, tc.seed))
			writeCapellaYAML(t, s, dataDoc+dataBucketOrders+tc.extra)
			r := s.Run("capella", "apply", "-y")
			wantExit(t, r, cli.ExitBlocked)
			wantContains(t, r.Stdout, "  ! bucket nova-dev-01/orders  "+tc.field+": "+tc.before+" -> "+tc.after+
				"  BLOCKED (immutable; create a new bucket and migrate data)")
			clitest.AssertNoWrites(t, s.Fake)
		})
	}
}

func TestBucket_CAP023_AC7_ReplicaChangeNote(t *testing.T) {
	s, _, cid := dataScenario(t, "8.0.1")
	seedMagmaOrders(s, cid)
	writeCapellaYAML(t, s, dataDoc+dataBucketOrders+"        replicas: 2\n")
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "  ~ bucket nova-dev-01/orders  note: the new replica count takes full effect after a rebalance",
		"      replicas:  1 -> 2")
}

func TestBucket_ListAndGet(t *testing.T) {
	s, _, cid := dataScenario(t, "8.0.1")
	seedMagmaOrders(s, cid, capellafake.WithField("stats", map[string]any{"itemCount": 1204, "memoryUsedInMib": 412, "diskUsedInMib": 900}))
	s.Fake.SeedBucket(cid, "events")
	writeCapellaYAML(t, s, dataDoc+dataBucketOrders)
	r := mustScenario(t, s.Run("capella", "bucket", "list", "nova-dev-01"), "bucket list")
	lines := r.Lines()
	// cbctl's table has no S.NO, ITEMS or IN FILE columns (see docs/capella/migrating-from-capellactl.md).
	if got := strings.Fields(lines[0]); !reflect.DeepEqual(got, []string{"NAME", "TYPE", "STORAGE", "MEMORY_MB", "REPLICAS", "DURABILITY", "TTL", "FLUSH"}) {
		t.Fatalf("header %v", got)
	}
	if got := strings.Fields(lines[1]); !reflect.DeepEqual(got, []string{"orders", "couchbase", "magma", "1024", "1", "majority", "0", "false"}) {
		t.Fatalf("row %v", got)
	}
	r = mustScenario(t, s.Run("capella", "bucket", "get", "nova-dev-01", "orders"), "bucket get")
	wantContains(t, r.Stdout, "storageBackend: magma", "stats.itemCount: 1204")
	wantExit(t, s.Run("capella", "bucket", "get", "nova-dev-01", "nope"), cli.ExitConfigInvalid)
	clitest.AssertNoWrites(t, s.Fake)
}

func TestBucketDelete_CAP024_AC3_ListsDependentCredentials(t *testing.T) {
	s, _, cid := dataScenario(t, "8.0.1")
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 1024),
		capellafake.WithField("stats", map[string]any{"itemCount": 1204, "memoryUsedInMib": 412, "diskUsedInMib": 900}))
	writeCapellaYAML(t, s, dataDoc+"    buckets:\n      - name: orders\n        memoryAllocationInMb: 1024\n")
	grant := func(buckets ...string) []capellaapi.AccessEntry {
		var rb []capellaapi.ResourceBucket
		for _, b := range buckets {
			rb = append(rb, capellaapi.ResourceBucket{Name: b})
		}
		return []capellaapi.AccessEntry{{Privileges: []string{"data_reader"}, Resources: capellaapi.Resources{Buckets: rb}}}
	}
	s.Fake.SeedCredential(cid, "reporting_ro", grant("orders"), false)
	s.Fake.SeedCredential(cid, "orders_app", grant("orders", "events"), false)
	s.Fake.SeedCredential(cid, "events_app", grant("events"), false)
	s.TTY(true)
	s.Answers("orders")
	r := mustScenario(t, s.Run("capella", "bucket", "delete", "nova-dev-01", "orders"), "bucket delete")
	wantContains(t, r.Stderr, "  These credentials lose access to this bucket:\n    orders_app\n    reporting_ro\n")
	if strings.Contains(dataOut(r), "events_app") {
		t.Fatalf("events_app listed:\n%s", r.Stderr)
	}
	wantContains(t, r.Stdout, "deleted bucket orders from nova-dev-01")
}

// freeScenario is capellactl's free fixture: the synced free-tier cluster nova-dev of project demo.
func freeScenario(t *testing.T) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s = newScenario(t)
	pid = s.Fake.SeedProject("demo")
	cid = s.Fake.SeedFreeCluster(pid, "nova-dev", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-2", "cidr": "10.0.0.0/23"}))
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	return s, pid, cid
}

func freePath(s *clitest.Scenario, pid, cid, bid string) string {
	p := "/v4/organizations/" + s.Fake.OrgID + "/projects/" + pid + "/clusters/" + cid + "/buckets/freeTier"
	if bid != "" {
		p += "/" + bid
	}
	return p
}

func TestBucketFree_CAP023_AC1_CreateUsesFreeTierEndpoint(t *testing.T) {
	s, pid, cid := freeScenario(t)
	r := mustScenario(t, s.Run("capella", "bucket", "add", "nova-dev", "orders", "--memory", "100", "-y"), "bucket add")
	wantContains(t, r.Stdout, "  + bucket nova-dev/orders  free tier - 100 MB")
	w := s.Fake.Writes()
	if len(w) != 1 || w[0].Method != "POST" || w[0].Path != freePath(s, pid, cid, "") ||
		!reflect.DeepEqual(normJSON(w[0].Body), normJSON(map[string]any{"name": "orders", "memoryAllocationInMb": 100})) {
		t.Fatalf("writes %v", w)
	}
	wantContains(t, s.ReadFile("capella.yaml"), "        buckets:\n          - name: orders\n            memoryAllocationInMb: 100\n")
	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	wantContains(t, r.Stdout, "No changes")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestBucketFree_CAP023_AC5_InteractiveUpdateAsksMemoryOnly(t *testing.T) {
	s, _, _ := freeScenario(t)
	mustScenario(t, s.Run("capella", "bucket", "add", "nova-dev", "orders", "--memory", "100", "-y"), "bucket add")
	s.TTY(true)
	r := mustScenario(t, s.RunWith("200\n", "capella", "bucket", "update", "nova-dev", "orders", "-y"), "bucket update")
	out := dataOut(r)
	wantContains(t, out, "Memory (MB)")
	for _, q := range []string{"Replicas", "Durability"} {
		if strings.Contains(out, q) {
			t.Fatalf("asked %s on the free tier:\n%s", q, out)
		}
	}
	wantContains(t, s.ReadFile("capella.yaml"), "memoryAllocationInMb: 200")
	if n := len(s.Fake.Calls("PUT", "*")); n != 1 {
		t.Fatalf("PUTs = %d, want 1", n)
	}
}

func TestBucketFree_CAP024_AC1_DeleteAndList(t *testing.T) {
	s, pid, cid := freeScenario(t)
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("stats", map[string]any{"itemCount": 7, "memoryUsedInMib": 12}))
	r := mustScenario(t, s.Run("capella", "bucket", "list", "nova-dev"), "bucket list")
	if f := strings.Fields(r.Lines()[1]); f[0] != "orders" {
		t.Fatalf("list row %v", f)
	}
	if len(s.Fake.Calls("GET", freePath(s, pid, cid, ""))) == 0 {
		t.Fatal("list did not use the free-tier endpoint")
	}
	r = mustScenario(t, s.Run("capella", "bucket", "delete", "nova-dev", "orders", "-y"), "bucket delete")
	wantContains(t, r.Stderr, "orders - 7 items - 12 MB used")
	w := s.Fake.Writes()
	if len(w) != 1 || w[0].Method != "DELETE" || w[0].Path != freePath(s, pid, cid, "b3JkZXJz") {
		t.Fatalf("writes %v", w)
	}
}

func TestImport_FreeTierBucketsRoundTrip(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("demo")
	cid := s.Fake.SeedFreeCluster(pid, "nova-dev", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-2", "cidr": "10.0.0.0/23"}))
	s.Fake.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 200))
	mustScenario(t, s.Run("capella", "sync"), "sync")
	wantContains(t, s.ReadFile("capella.yaml"), "        buckets:\n          - name: orders\n            memoryAllocationInMb: 200\n")
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
}

func TestFreeTier_CAP041_AC2_CreateAndRecord(t *testing.T) {
	s, _, _ := dataScenario(t, "8.0.1")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	args := []string{"capella", "cluster", "create", "try-01", "--free-tier", "--provider", "aws", "--region", "us-east-2", "-y"}
	r := mustScenario(t, s.Run(args...), args...)
	posts := s.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters/freeTier")
	if len(posts) != 1 {
		t.Fatalf("free-tier POSTs: %v", posts)
	}
	body := posts[0].Body.(map[string]any)
	cp, _ := body["cloudProvider"].(map[string]any)
	if body["name"] != "try-01" || cp["type"] != "aws" || cp["region"] != "us-east-2" || cp["cidr"] == "" || len(body) != 2 {
		t.Fatalf("POST body %v", body)
	}
	wantContains(t, dataOut(r), "cluster try-01: healthy")
	wantContains(t, s.ReadFile("capella.yaml"), "name: try-01\n        tier: free\n")
	s.Fake.ResetCalls()
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
	clitest.AssertNoWrites(t, s.Fake)
}

func TestFreeTier_CAP041_AC3_InteractiveChoices(t *testing.T) {
	s, _, _ := dataScenario(t, "8.0.1")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.TTY(true)
	s.Answers("y")
	// As capellactl's test: NAME given, the wizard asks the type, provider and region.
	r := mustScenario(t, s.RunWith("free\ngcp\n\n", "capella", "cluster", "create", "try-01"), "cluster create")
	out := dataOut(r)
	wantContains(t, out, "Cluster type (free, provisioned) [provisioned]",
		"Region (free-tier regions) (us-central1, europe-west1, asia-east1) [us-central1]")
	if strings.Contains(out, "Size") || strings.Contains(out, "Support plan") {
		t.Fatalf("asked a size or plan for a free-tier cluster:\n%s", out)
	}
	wantContains(t, s.ReadFile("capella.yaml"), "name: try-01\n        tier: free\n        cloudProvider: {type: gcp, region: us-central1")
}

func TestFreeTier_CAP041_AC3_NotOfferedWhenUsed(t *testing.T) {
	s, pid, _ := dataScenario(t, "8.0.1")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.SeedFreeCluster(pid, "already-free", "healthy")
	s.Fake.ResetCalls()
	r := s.Run("capella", "cluster", "create", "try-02", "--free-tier", "-y")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "the free tier is already used by already-free in project nova-dev")
	clitest.AssertNoWrites(t, s.Fake)
	s.TTY(true)
	s.Answers("y")
	r = mustScenario(t, s.RunWith("\n\n\n\n\n", "capella", "cluster", "create", "prov-01"), "cluster create")
	if strings.Contains(dataOut(r), "Cluster type") {
		t.Fatalf("free tier offered while used:\n%s", dataOut(r))
	}
}

func TestFreeTier_CAP041_AC6_ImportRoundTrip(t *testing.T) {
	s, pid, _ := dataScenario(t, "8.0.1")
	fid := s.Fake.SeedFreeCluster(pid, "try-01", "healthy",
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "eu-west-1", "cidr": "10.4.0.0/23"}))
	mustScenario(t, s.Run("capella", "sync"), "sync")
	wantContains(t, s.ReadFile("capella.yaml"), "      - id: "+fid+"\n        name: try-01\n        tier: free\n        cloudProvider: {type: aws, region: eu-west-1, cidr: 10.4.0.0/23}\n")
	s.Fake.ResetCalls()
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
	mustScenario(t, s.Run("capella", "sync", "--dry-run"), "sync --dry-run") // exit 0: already in sync
	clitest.AssertNoWrites(t, s.Fake)
}

// dataOrdersBucketScoped is capellactl's ORDERS_BUCKET: orders with inventory/{items,suppliers}.
const dataOrdersBucketScoped = `    buckets:
      - name: orders
        memoryAllocationInMb: 100
        scopes:
          - name: inventory
            collections:
              - name: items
              - name: suppliers
`

// credScenario is capellactl's credential cluster fixture: orders/inventory/{items,suppliers}
// exist in Capella, and ORDERS_APP_PASSWORD is set.
func credScenario(t *testing.T) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s, pid, cid = dataOrdersScenario(t, "8.0.1")
	s.Fake.SeedScope(cid, "orders", "inventory", map[string]int{"items": 0, "suppliers": 0})
	s.Env["ORDERS_APP_PASSWORD"] = dataPW
	t.Setenv("ORDERS_APP_PASSWORD", dataPW)
	return s, pid, cid
}

// dataAccess is ORDERS_APP's access as the API carries it.
var dataAccess = []any{map[string]any{"privileges": []any{"data_reader", "data_writer"},
	"resources": map[string]any{"buckets": []any{map[string]any{"name": "orders",
		"scopes": []any{map[string]any{"name": "inventory", "collections": []any{"items", "suppliers"}}}}}}}}

func userWrites(s *clitest.Scenario, pid, cid string) []dataWrite {
	var out []dataWrite
	for _, w := range dataWrites(s, pid, cid) {
		if strings.HasPrefix(w.Path, "/users") {
			out = append(out, w)
		}
	}
	return out
}

func TestCredential_CAP027_AC1_CreateSuppliedPassword(t *testing.T) {
	s, pid, cid := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	p := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, p.Stdout, "  + credential nova-dev-01/orders_app  read+write - orders/inventory/{items,suppliers}")
	a := dataApply(t, s)
	w := userWrites(s, pid, cid)
	if len(w) != 1 || w[0].Method != "POST" || w[0].Path != "/users" {
		t.Fatalf("user writes %+v", w)
	}
	body := w[0].Body.(map[string]any)
	if body["name"] != "orders_app" || body["password"] != dataPW || !reflect.DeepEqual(body["access"], normJSON(dataAccess)) {
		t.Fatalf("POST body %v", redactedBody(body))
	}
	if ct, ok := body["credentialType"]; ok && ct != "basic" {
		t.Fatalf("credentialType %v", ct)
	}
	clitest.AssertNoSecret(t, p, dataPW)
	clitest.AssertNoSecret(t, a, dataPW)
}

func redactedBody(b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range b {
		if k == "password" {
			v = "***"
		}
		out[k] = v
	}
	return out
}

func TestCredential_CAP027_AC4_UpdateAccessWithoutPassword(t *testing.T) {
	s, pid, cid := credScenario(t)
	uid := s.Fake.SeedCredential(cid, "orders_app", billingGrantOn("orders", "inventory", "data_reader"), false)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped+strings.Replace(dataOrdersApp[strings.Index(dataOrdersApp, "    databaseCredentials"):],
		"                      collections: [items, suppliers]\n", "", 1))
	p := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, p.Stdout, "  ~ credential nova-dev-01/orders_app", `      orders/inventory:  "read" -> "read+write"`)
	dataApply(t, s)
	w := userWrites(s, pid, cid)
	want := map[string]any{"access": []any{map[string]any{"privileges": []any{"data_reader", "data_writer"},
		"resources": map[string]any{"buckets": []any{map[string]any{"name": "orders", "scopes": []any{map[string]any{"name": "inventory"}}}}}}}}
	if len(w) != 1 || w[0].Method != "PUT" || w[0].Path != "/users/"+uid {
		t.Fatalf("user writes %+v", w)
	}
	body := w[0].Body.(map[string]any)
	if _, ok := body["password"]; ok {
		t.Fatal("the access update sent a password")
	}
	if !reflect.DeepEqual(body["access"], normJSON(want["access"])) {
		t.Fatalf("PUT access %v", body["access"])
	}
	if header(s.Fake.Calls("PUT", "*")[0], "If-Match") == "" {
		t.Fatal("PUT without If-Match")
	}
}

// billingGrantOn grants privilege on bucket/scope.
func billingGrantOn(bucket, scope, privilege string) []capellaapi.AccessEntry {
	return []capellaapi.AccessEntry{{Privileges: []string{privilege}, Resources: capellaapi.Resources{
		Buckets: []capellaapi.ResourceBucket{{Name: bucket, Scopes: []capellaapi.ResourceScope{{Name: scope}}}}}}}
}

func TestCredential_CAP027_AC6_ExistingAdvancedBlocked(t *testing.T) {
	s, pid, cid := credScenario(t)
	s.Fake.SeedCredential(cid, "orders_app", nil, true)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	r := s.Run("capella", "plan")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "  ! credential nova-dev-01/orders_app", "advanced credential")
	wantExit(t, s.Run("capella", "apply", "-y"), cli.ExitBlocked)
	if w := userWrites(s, pid, cid); len(w) != 0 {
		t.Fatalf("user writes %+v", w)
	}
}

func TestCredential_CAP027_AC7_AfterBucketInRealTree(t *testing.T) {
	s, _, _ := dataScenario(t, "8.0.1")
	s.Env["ORDERS_APP_PASSWORD"] = dataPW
	t.Setenv("ORDERS_APP_PASSWORD", dataPW)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	dataApply(t, s)
	if got, want := lastSegments(s), []string{"buckets", "scopes", "collections", "collections", "users"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("write order %v, want %v", got, want)
	}
}

func TestCredential_CAP027_AC8_RerunZeroWrites(t *testing.T) {
	s, _, _ := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	dataApply(t, s)
	s.Fake.ResetCalls()
	t.Setenv("ORDERS_APP_PASSWORD", "Changed!Pw-123456")
	s.Env["ORDERS_APP_PASSWORD"] = "Changed!Pw-123456"
	r := dataApply(t, s)
	wantContains(t, r.Stdout, "No changes. Capella matches capella.yaml.")
	clitest.AssertNoWrites(t, s.Fake)
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
}

// rwOrders is RW_ORDERS: read+write on the whole orders bucket.
var rwOrders = []capellaapi.AccessEntry{{Privileges: []string{"data_reader", "data_writer"},
	Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders"}}}}}

const fileRWOrders = "    databaseCredentials:\n      - name: orders_app\n        access:\n" +
	"          - privileges: [read, write]\n            resources: { buckets: [ { name: orders } ] }\n"

func TestCredential_CAP046_AC1_List(t *testing.T) {
	s, _, cid := credScenario(t)
	s.Fake.SeedCredential(cid, "orders_app", rwOrders, false)
	s.Fake.SeedCredential(cid, "dba", nil, true)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped+fileRWOrders)
	r := mustScenario(t, s.Run("capella", "credential", "list", "nova-dev-01"), "credential list")
	// cbctl's columns are NAME ID ACCESS (no S.NO, TYPE or IN FILE; see the migration guide).
	if got := strings.Fields(r.Lines()[0]); !reflect.DeepEqual(got, []string{"NAME", "ID", "ACCESS"}) {
		t.Fatalf("header %v", got)
	}
	wantContains(t, r.Stdout, "orders_app", "read+write:orders.*", "dba")
	j := mustScenario(t, s.Run("capella", "credential", "list", "nova-dev-01", "-o", "json"), "credential list -o json")
	var names []string
	for _, u := range j.JSON(t).([]any) {
		names = append(names, u.(map[string]any)["name"].(string))
	}
	if !reflect.DeepEqual(names, []string{"orders_app", "dba"}) && !reflect.DeepEqual(names, []string{"dba", "orders_app"}) {
		t.Fatalf("names %v", names)
	}
	clitest.AssertNoSecret(t, r, "Old!Passw0rd-xyz")
	if strings.Contains(j.Stdout, "password") {
		t.Fatalf("JSON shows a password field:\n%s", j.Stdout)
	}
}

func TestCredential_CAP046_AC2_Get(t *testing.T) {
	s, _, cid := credScenario(t)
	uid := s.Fake.SeedCredential(cid, "orders_app", rwOrders, false)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped)
	s.WriteFile(".cbctl/credentials/nova-dev-01.orders_app.json", `{"password": "Gen3rated!Pw-local"}`)
	r := mustScenario(t, s.Run("capella", "credential", "get", "nova-dev-01", "orders_app"), "credential get")
	wantContains(t, r.Stdout, "id: "+uid, "name: orders_app", "access[0].privileges[0]: data_reader",
		"access[0].resources.buckets[0].name: orders", "generated password saved in ")
	clitest.AssertNoSecret(t, r, "Gen3rated!Pw-local")
	r = s.Run("capella", "credential", "get", "nova-dev-01", "nope")
	wantExit(t, r, cli.ExitConfigInvalid) // cbctl: a named resource that is not found exits 3
	wantContains(t, r.Stderr, "credential nope not found")
}

func TestCredential_CAP046_AC3_CreateRecordsAndApplies(t *testing.T) {
	s, pid, cid := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped)
	args := []string{"capella", "credential", "create", "nova-dev-01", "orders_app", "--privileges", "read+write",
		"--bucket", "orders", "--scope", "inventory", "--password-env", "ORDERS_APP_PASSWORD", "-y"}
	r := mustScenario(t, s.Run(args...), args...)
	wantContains(t, s.ReadFile("capella.yaml"), "        databaseCredentials:\n          - name: orders_app\n",
		"            password: ${ORDERS_APP_PASSWORD}\n", "privileges: [data_reader, data_writer]", "name: inventory")
	w := userWrites(s, pid, cid)
	if len(w) != 1 || w[0].Method != "POST" || len(dataWrites(s, pid, cid)) != 1 {
		t.Fatalf("writes %+v", dataWrites(s, pid, cid))
	}
	body := w[0].Body.(map[string]any)
	want := normJSON([]any{map[string]any{"privileges": []any{"data_reader", "data_writer"},
		"resources": map[string]any{"buckets": []any{map[string]any{"name": "orders", "scopes": []any{map[string]any{"name": "inventory"}}}}}}})
	if body["name"] != "orders_app" || body["password"] != dataPW || !reflect.DeepEqual(body["access"], want) {
		t.Fatalf("POST body %v", redactedBody(body))
	}
	clitest.AssertNoSecret(t, r, dataPW)
	if strings.Contains(s.ReadFile("capella.yaml"), dataPW) {
		t.Fatal("password written to capella.yaml")
	}
}

func TestCredential_CAP046_AC3_CreateGuards(t *testing.T) {
	s, _, cid := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	r := s.Run("capella", "credential", "create", "nova-dev-01", "orders_app", "--privileges", "read", "--bucket", "orders", "-y")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stderr, "already in capella.yaml with different access")
	r = s.Run("capella", "credential", "create", "nova-dev-01", "other", "--privileges", "read", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "add --bucket NAME (least privilege) or --all-buckets")
	clitest.AssertNoWrites(t, s.Fake)
	// Exists in Capella with the same access: adopted into the file with zero writes.
	s.Fake.SeedCredential(cid, "adopted", []capellaapi.AccessEntry{{Privileges: []string{"data_reader"},
		Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders"}}}}}, false)
	s.Fake.ResetCalls()
	r = mustScenario(t, s.Run("capella", "credential", "create", "nova-dev-01", "adopted", "--privileges", "read", "--bucket", "orders", "-y"), "credential create")
	wantContains(t, r.Stdout, "credential adopted already matches; nothing to do")
	clitest.AssertNoWrites(t, s.Fake)
	wantContains(t, s.ReadFile("capella.yaml"), "      - name: adopted\n")
}

func TestCredential_CAP046_AC4_UpdateGuards(t *testing.T) {
	s, _, _ := credScenario(t)
	two := "    databaseCredentials:\n      - name: orders_app\n        access:\n" +
		"          - privileges: [read]\n            resources: { buckets: [ { name: orders } ] }\n" +
		"          - privileges: [write]\n            resources: { buckets: [ { name: orders } ] }\n"
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped+two)
	r := s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--bucket", "orders", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "has several access grants")
	r = s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "nothing to update")
	r = s.Run("capella", "credential", "update", "nova-dev-01", "nope", "--bucket", "orders", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "is not in capella.yaml")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestCredential_CAP046_AC5_NoApply(t *testing.T) {
	s, _, _ := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped)
	r := mustScenario(t, s.Run("capella", "credential", "create", "nova-dev-01", "orders_app", "--privileges", "read",
		"--bucket", "orders", "--no-apply", "-y"), "credential create --no-apply")
	wantContains(t, r.Stdout, "Not applied (--no-apply)")
	// Only resources: the privileges of the one grant in the file are kept, as in capellactl.
	r = mustScenario(t, s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--bucket", "orders",
		"--scope", "inventory", "--no-apply", "-y"), "credential update --no-apply")
	wantContains(t, r.Stdout, "Not applied (--no-apply)")
	wantContains(t, s.ReadFile("capella.yaml"), "privileges: [data_reader]", "name: inventory")
	r = mustScenario(t, s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--privileges", "write",
		"--bucket", "orders", "--no-apply", "-y"), "credential update --no-apply")
	wantContains(t, r.Stdout, "Not applied (--no-apply)")
	wantContains(t, s.ReadFile("capella.yaml"), "privileges: [data_writer]")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestCredential_CAP046_AC6_RerunZeroWrites(t *testing.T) {
	s, _, _ := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped)
	args := []string{"capella", "credential", "create", "nova-dev-01", "orders_app", "--privileges", "read+write",
		"--bucket", "orders", "--password-env", "ORDERS_APP_PASSWORD", "-y"}
	mustScenario(t, s.Run(args...), args...)
	s.Fake.ResetCalls()
	wantExit(t, s.Run("capella", "plan", "--detailed-exitcode"), 0)
	r := dataApply(t, s)
	wantContains(t, r.Stdout, "No changes")
	clitest.AssertNoWrites(t, s.Fake)
}

const airlineCred = `    databaseCredentials:
      - name: airline_app
        password: ${AIRLINE_PW}
        access:
          - privileges: [data_reader, data_writer]
            resources:
              buckets:
                - name: demo
                  scopes:
                    - name: inventory
                      collections: [airline]
`

// airlineScenario is capellactl's airline fixture after its access was removed in Capella (for
// example in the Capella UI): capella.yaml still grants airline_app read+write on
// demo/inventory/airline; Capella has the credential with no access.
func airlineScenario(t *testing.T) (s *clitest.Scenario, pid, cid string) {
	t.Helper()
	s, pid, cid = dataScenario(t, "8.0.1")
	s.Fake.SeedBucket(cid, "demo", capellafake.WithField("memoryAllocationInMb", 512))
	s.Fake.SeedScope(cid, "demo", "inventory", map[string]int{"airline": 0})
	s.Fake.SeedCredential(cid, "airline_app", nil, false)
	s.Env["AIRLINE_PW"] = "Demo-A1rline-2026!"
	t.Setenv("AIRLINE_PW", "Demo-A1rline-2026!")
	writeCapellaYAML(t, s, dataDoc+`    buckets:
      - name: demo
        memoryAllocationInMb: 512
        scopes:
          - name: inventory
            collections:
              - name: airline
`+airlineCred)
	return s, pid, cid
}

func TestCredential_CAP027_AC4_AccessRemovedInCapellaIsRestored(t *testing.T) {
	s, _, _ := airlineScenario(t)
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	if strings.Contains(r.Stdout, "advanced credential") {
		t.Fatalf("an access-less credential was taken for an advanced one:\n%s", r.Stdout)
	}
	wantContains(t, r.Stdout, "  ~ credential nova-dev-01/airline_app", `demo/inventory/airline:  (none) -> "read+write"`)
	dataApply(t, s)
	put := s.Fake.Calls("PUT", "/v4/organizations/*/projects/*/clusters/*/users/*")
	if len(put) != 1 || len(put[0].Body.(map[string]any)["access"].([]any)) == 0 {
		t.Fatalf("access not restored: %v", put)
	}
	r = dataApply(t, s)
	wantContains(t, r.Stdout, "No changes")
}

func TestSync_CAP038_AC9_KeepsAccessCapellaLost(t *testing.T) {
	s, _, _ := airlineScenario(t)
	r := mustScenario(t, s.Run("capella", "sync"), "sync")
	wantContains(t, r.Stderr, "[warning] - credential nova-dev-01/airline_app has no access in Capella; capella.yaml keeps the access it had\n"+
		"  -> Run 'cbctl capella apply'")
	wantContains(t, s.ReadFile("capella.yaml"), "      - name: airline_app\n            password: ${AIRLINE_PW}\n",
		"privileges: [data_reader, data_writer]", "- name: demo\n", "collections: [airline]")
	mustScenario(t, s.Run("capella", "validate"), "validate")
}

func TestImport_CAP036_AC1_CredentialWithoutAccessListedAsComment(t *testing.T) {
	s, _, _ := airlineScenario(t)
	mustScenario(t, s.Run("capella", "sync", "--full", "--force"), "sync --full --force")
	text := s.ReadFile("capella.yaml")
	wantContains(t, text, "airline_app has no access in Capella, so it is listed here for reference",
		"cbctl capella credential create nova-dev-01 airline_app --privileges read --bucket BUCKET --password-env VAR")
	mustScenario(t, s.Run("capella", "validate"), "validate")
}

const reportingCred = `    databaseCredentials:
      - name: reporting_ro
        access:
          - privileges: [read]
            resources: { buckets: [ { name: orders } ] }
`

var readOrders = []capellaapi.AccessEntry{{Privileges: []string{"data_reader"},
	Resources: capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders"}}}}}

func TestCredentialDelete_CAP029_AC2_SecretFileKept(t *testing.T) {
	s, _, cid := credScenario(t)
	s.Fake.SeedCredential(cid, "reporting_ro", readOrders, false)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped+reportingCred)
	secret := s.WriteFile(".cbctl/credentials/nova-dev-01.reporting_ro.json", "{}")
	r := mustScenario(t, s.Run("capella", "credential", "delete", "nova-dev-01", "reporting_ro", "-y"), "credential delete")
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("secret file removed: %v", err)
	}
	wantContains(t, r.Stdout, "local secret file kept: ", "nova-dev-01.reporting_ro.json (delete it if no longer needed)")
}

func TestCredentialDelete_CAP029_AC4_RerunNoop(t *testing.T) {
	s, _, cid := credScenario(t)
	s.Fake.SeedCredential(cid, "reporting_ro", readOrders, false)
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped+reportingCred)
	mustScenario(t, s.Run("capella", "credential", "delete", "nova-dev-01", "reporting_ro", "-y"), "credential delete")
	s.Fake.ResetCalls()
	r := mustScenario(t, s.Run("capella", "credential", "delete", "nova-dev-01", "reporting_ro", "-y"), "credential delete")
	wantContains(t, r.Stdout, "credential nova-dev-01/reporting_ro not found, nothing to do")
	clitest.AssertNoWrites(t, s.Fake)
}

// dataNoPW is ORDERS_APP without its password reference (a generated password).
var dataNoPW = strings.Replace(dataOrdersApp, "        password: ${ORDERS_APP_PASSWORD}\n", "", 1)

func TestRotation_CAP028_AC1_FromConfiguredSecret(t *testing.T) {
	s, pid, cid := credScenario(t)
	var access []capellaapi.AccessEntry
	if err := json.Unmarshal(mustJSON(t, dataAccess), &access); err != nil {
		t.Fatal(err)
	}
	uid := s.Fake.SeedCredential(cid, "orders_app", access, false)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	r := mustScenario(t, s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--rotate-password", "-y"), "credential update")
	w := userWrites(s, pid, cid)
	if len(w) != 1 || w[0].Method != "PUT" || w[0].Path != "/users/"+uid {
		t.Fatalf("user writes %+v", w)
	}
	body := w[0].Body.(map[string]any)
	if body["password"] != dataPW || len(body["access"].([]any)) == 0 {
		t.Fatalf("PUT body %v (access must always be sent: Capella clears it otherwise)", redactedBody(body))
	}
	if header(s.Fake.Calls("PUT", "*")[0], "If-Match") == "" {
		t.Fatal("PUT without If-Match")
	}
	wantContains(t, dataOut(r), "password rotated for orders_app")
	clitest.AssertNoSecret(t, r, dataPW)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func seedOrdersApp(t *testing.T, s *clitest.Scenario, cid, name string) string {
	t.Helper()
	var access []capellaapi.AccessEntry
	if err := json.Unmarshal(mustJSON(t, dataAccess), &access); err != nil {
		t.Fatal(err)
	}
	return s.Fake.SeedCredential(cid, name, access, false)
}

func TestRotation_CAP028_AC2_GeneratedSavedWithBackup(t *testing.T) {
	s, pid, cid := credScenario(t)
	seedOrdersApp(t, s, cid, "orders_app")
	old := s.WriteFile(".cbctl/credentials/nova-dev-01.orders_app.json", `{"password": "Old!Passw0rd-xyz"}`)
	writeCapellaYAML(t, s, dataDoc+dataNoPW)
	r := mustScenario(t, s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--rotate-password", "-y"), "credential update")
	w := userWrites(s, pid, cid)
	if len(w) != 1 {
		t.Fatalf("user writes %+v", w)
	}
	sent, _ := w[0].Body.(map[string]any)["password"].(string)
	if sent == "" || sent == "Old!Passw0rd-xyz" || len(sent) < 16 {
		t.Fatalf("generated password not sent (len %d)", len(sent))
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(s.ReadFile(".cbctl/credentials/nova-dev-01.orders_app.json")), &saved); err != nil || saved["password"] != sent {
		t.Fatalf("secret file does not hold the new password (%v)", err)
	}
	if got := s.ReadFile(".cbctl/credentials/nova-dev-01.orders_app.json.bak"); !strings.Contains(got, "Old!Passw0rd-xyz") {
		t.Fatalf(".bak = %q", got)
	}
	for _, p := range []string{old, old + ".bak"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(p), err)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 { // Windows has no Unix modes
			t.Fatalf("%s mode %v", filepath.Base(p), st.Mode().Perm())
		}
	}
	wantContains(t, dataOut(r), "password rotated for orders_app", "password saved to")
	clitest.AssertNoSecret(t, r, sent)
}

func TestRotation_CAP028_AC2_FailedSendRestoresFile(t *testing.T) {
	s, _, cid := credScenario(t)
	seedOrdersApp(t, s, cid, "orders_app")
	s.WriteFile(".cbctl/credentials/nova-dev-01.orders_app.json", `{"password": "Old!Passw0rd-xyz"}`)
	writeCapellaYAML(t, s, dataDoc+dataNoPW)
	s.Fake.FailNext("PUT", ".*/users/.*", 400, 1)
	r := s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--rotate-password", "-y")
	wantExit(t, r, cli.ExitError)
	if got := s.ReadFile(".cbctl/credentials/nova-dev-01.orders_app.json"); got != `{"password": "Old!Passw0rd-xyz"}` {
		t.Fatalf("secret file not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".cbctl/credentials/nova-dev-01.orders_app.json.bak")); !os.IsNotExist(err) {
		t.Fatalf(".bak left behind (%v)", err)
	}
}

func TestRotation_CAP028_AC4_NoImplicitRotation(t *testing.T) {
	s, _, cid := credScenario(t)
	seedOrdersApp(t, s, cid, "orders_app")
	seedOrdersApp(t, s, cid, "reporting")
	second := strings.ReplaceAll(strings.ReplaceAll(dataOrdersApp[strings.Index(dataOrdersApp, "      - name: orders_app"):],
		"orders_app", "reporting"), "ORDERS_APP", "REPORTING")
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp+second)
	t.Setenv("REPORTING_PASSWORD", "Rep0rting!Passw0rd")
	s.Env["REPORTING_PASSWORD"] = "Rep0rting!Passw0rd"
	dataApply(t, s)
	s.Fake.ResetCalls()
	for k, v := range map[string]string{"ORDERS_APP_PASSWORD": "Changed!Pw-123456", "REPORTING_PASSWORD": "Changed!Pw-654321"} {
		t.Setenv(k, v)
		s.Env[k] = v
	}
	r := dataApply(t, s)
	clitest.AssertNoWrites(t, s.Fake)
	if n := strings.Count(dataOut(r), "note: passwords are not compared; use --rotate-password to change them"); n > 1 {
		t.Fatalf("note printed %d times:\n%s", n, dataOut(r))
	}
}

func TestRotation_CAP028_AC5_BulkWithTarget(t *testing.T) {
	s, pid, cid := credScenario(t)
	names := []string{"app_a", "app_b", "app_c"}
	creds := "    databaseCredentials:\n"
	block := dataOrdersApp[strings.Index(dataOrdersApp, "      - name: orders_app"):]
	for _, n := range names {
		seedOrdersApp(t, s, cid, n)
		creds += strings.ReplaceAll(strings.ReplaceAll(block, "orders_app", n), "ORDERS_APP", strings.ToUpper(n))
		v := "Rotated!" + n + "-Pw1"
		t.Setenv(strings.ToUpper(n)+"_PASSWORD", v)
		s.Env[strings.ToUpper(n)+"_PASSWORD"] = v
	}
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucketScoped+creds)
	s.TTY(true)
	s.Answers("y")
	r := mustScenario(t, s.Run("capella", "apply", "--rotate-passwords"), "apply --rotate-passwords")
	_, after, ok := strings.Cut(dataOut(r), "Passwords to rotate:\n")
	if !ok {
		t.Fatalf("no Passwords to rotate list:\n%s", dataOut(r))
	}
	if got := strings.Split(after, "\n")[:3]; !reflect.DeepEqual(got, []string{"  nova-dev-01/app_a", "  nova-dev-01/app_b", "  nova-dev-01/app_c"}) {
		t.Fatalf("listed %q", got)
	}
	var sent []string
	for _, w := range userWrites(s, pid, cid) {
		b := w.Body.(map[string]any)
		if len(b["access"].([]any)) == 0 {
			t.Fatal("rotation PUT without access")
		}
		sent = append(sent, b["password"].(string))
	}
	if !reflect.DeepEqual(sent, []string{"Rotated!app_a-Pw1", "Rotated!app_b-Pw1", "Rotated!app_c-Pw1"}) {
		t.Fatalf("rotated %d passwords", len(sent))
	}
	s.Fake.ResetCalls()
	s.TTY(false)
	mustScenario(t, s.Run("capella", "apply", "-y", "--rotate-passwords", "--target", "credential/nova-dev-01/app_b"), "apply --target")
	w := userWrites(s, pid, cid)
	if len(w) != 1 || w[0].Body.(map[string]any)["password"] != "Rotated!app_b-Pw1" {
		t.Fatalf("targeted rotation wrote %d credentials", len(w))
	}
}

func TestRotation_CAP028_AC6_MissingCredentialCreated(t *testing.T) {
	s, pid, cid := credScenario(t)
	writeCapellaYAML(t, s, dataDoc+dataOrdersApp)
	mustScenario(t, s.Run("capella", "credential", "update", "nova-dev-01", "orders_app", "--rotate-password", "-y"), "credential update")
	w := userWrites(s, pid, cid)
	if len(w) != 1 || w[0].Method != "POST" || w[0].Body.(map[string]any)["password"] != dataPW {
		t.Fatalf("user writes %d, want one POST with the configured password", len(w))
	}
}

func TestGroups_ListCrudVerbsWithoutApply(t *testing.T) {
	// cbctl's verb sets: 'add' is the create verb of buckets, scopes and collections ('create' is
	// the capellactl spelling), and scope/collection keep 'exists'. No group offers 'apply'.
	for group, verbs := range map[string][]string{
		"project": {"create", "delete", "get", "list", "update"},
		"cluster": {"backup", "clone", "create", "delete", "disk-types", "get", "group", "list", "options", "power",
			"protect", "scale", "schedule", "update"},
		"cidr":       {"create", "delete", "list"},
		"bucket":     {"add", "backup", "create", "delete", "get", "list", "update"},
		"scope":      {"add", "create", "delete", "exists", "list"},
		"collection": {"add", "create", "delete", "exists", "list", "update"},
		"credential": {"create", "delete", "get", "list", "update"},
	} {
		t.Run(group, func(t *testing.T) {
			s := newScenario(t)
			r := mustScenario(t, s.Run("capella", group, "-h"), group, "-h")
			_, listed, _ := strings.Cut(r.Stdout, "Available Commands:\n")
			listed, _, _ = strings.Cut(listed, "\n\n")
			var got []string
			for _, line := range strings.Split(listed, "\n") {
				if f := strings.Fields(line); len(f) > 0 {
					got = append(got, f[0])
				}
			}
			if !reflect.DeepEqual(got, verbs) {
				t.Fatalf("verbs %v, want %v", got, verbs)
			}
		})
	}
}

func TestProjectUpdate_RecordsAndApplies(t *testing.T) {
	s, pid, _ := dataScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc)
	mustScenario(t, s.Run("capella", "project", "update", "nova-dev", "--description", "Nova dev, renamed", "-y"), "project update")
	wantContains(t, s.ReadFile("capella.yaml"), "description: Nova dev, renamed")
	w := s.Fake.Writes()
	if len(w) != 1 || w[0].Method != "PUT" || !strings.HasSuffix(w[0].Path, "/projects/"+pid) {
		t.Fatalf("writes %v: want one PUT of the project only", w)
	}
	r := mustScenario(t, s.Run("capella", "project", "get", "nova-dev"), "project get")
	wantContains(t, r.Stdout, "Nova dev, renamed")
}

func TestProjectUpdate_CAP009_AC2_ClusterNameGivenPointsToProject(t *testing.T) {
	s, _, _ := dataScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc)
	before := s.ReadFile("capella.yaml")
	r := s.Run("capella", "project", "update", "nova-dev-01", "--description", "Live demo", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "nova-dev-01 is a cluster in project nova-dev, not a project\n"+
		`  -> Did you mean: cbctl capella project update nova-dev --description "Live demo"`)
	if strings.Contains(r.Stderr, "sync --project nova-dev-01") {
		t.Fatalf("suggested syncing the cluster as a project:\n%s", r.Stderr)
	}
	if s.ReadFile("capella.yaml") != before {
		t.Fatal("capella.yaml changed")
	}
	clitest.AssertNoWrites(t, s.Fake)
	r = s.Run("capella", "project", "get", "nova-dev-01")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "is a cluster in project nova-dev, not a project\n  -> Did you mean: --project nova-dev")
	r = s.Run("capella", "project", "update", "nope", "--description", "x", "-y")
	wantContains(t, r.Stderr, "run 'cbctl capella sync --project nope' to add it")
}

func TestProjectOption_CAP009_AC2_ClusterNameGivenPointsToProject(t *testing.T) {
	s, _, _ := dataScenario(t, "8.0.1")
	writeCapellaYAML(t, s, dataDoc)
	r := s.Run("capella", "cluster", "create", "nova-dev-02", "--project", "nova-dev-01", "-y")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, r.Stderr, "nova-dev-01 is a cluster in project nova-dev, not a project\n  -> Did you mean: --project nova-dev")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestRotation_CAP028_AC3_HelpListsRules(t *testing.T) {
	s := newScenario(t)
	for _, args := range [][]string{{"capella", "credential", "--help"}, {"capella", "credential", "create", "--help"},
		{"capella", "credential", "update", "--help"}} {
		r := mustScenario(t, s.Run(args...), args...)
		wantContains(t, r.Stdout, "Password rules (Capella; checked before anything is sent): at least 8 characters",
			"an upper-case letter, a lower-case letter, a digit and one special character", "< > ; . * & | £ =")
	}
}

func TestBucket_CAP023_AC6_SingleNodeUsesZeroReplicas(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	s.Fake.SeedCluster(pid, "nova-dev-01", "healthy", capellafake.WithField("configurationType", "singleNode"),
		capellafake.WithField("availability", map[string]any{"type": "single"}),
		capellafake.WithField("serviceGroups", []any{map[string]any{"numOfNodes": 1, "services": []any{"data", "index", "query"},
			"node": map[string]any{"compute": map[string]any{"cpu": 4, "ram": 16}, "disk": map[string]any{"type": "gp3", "storage": 50, "iops": 3000}}}}))
	single := strings.NewReplacer("availability: {type: multi}", "availability: {type: single}\n    configurationType: singleNode",
		"numOfNodes: 3", "numOfNodes: 1").Replace(dataDoc)
	writeCapellaYAML(t, s, single+dataOrdersBucket)
	r := dataApply(t, s)
	wantContains(t, r.Stdout, "+ bucket nova-dev-01/orders  couchbase - 100 MB - 0 replicas - durability none")
	if strings.Contains(r.Stdout, "note:") {
		t.Fatalf("unexpected note:\n%s", r.Stdout)
	}
	posts := s.Fake.Calls("POST", "/v4/organizations/*/projects/*/clusters/*/buckets")
	if len(posts) != 1 || posts[0].Body.(map[string]any)["replicas"] != float64(0) {
		t.Fatalf("bucket POST %v", posts)
	}
	writeCapellaYAML(t, s, single+dataOrdersBucket+"        replicas: 1\n")
	r = s.Run("capella", "plan")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, dataOut(r), "single-node clusters only support replicas: 0")
	writeCapellaYAML(t, s, dataDoc+dataOrdersBucket+"        replicas: 0\n")
	r = s.Run("capella", "plan")
	wantExit(t, r, cli.ExitConfigInvalid)
	wantContains(t, dataOut(r), "replicas: 0 is only for single-node clusters; use 1, 2 or 3")
}
