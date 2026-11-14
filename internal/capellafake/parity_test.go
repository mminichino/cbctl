package capellafake_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Phase 4 §3.7: fake responses match docs/backlog/api/management-api-v4.0-responses.json.

type parityOp struct {
	Method     string                     `json:"method"`
	Path       string                     `json:"path"`
	Documented bool                       `json:"documented"`
	Responses  map[string]json.RawMessage `json:"responses"`
}

type paritySnapshot struct {
	Operations []parityOp `json:"operations"`
}

func responsesPaths(t *testing.T) (docs, testdata string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	pkg := filepath.Dir(file)
	root := filepath.Clean(filepath.Join(pkg, "../.."))
	return filepath.Join(root, "docs/backlog/api/management-api-v4.0-responses.json"),
		filepath.Join(pkg, "testdata/management-api-v4.0-responses.json")
}

func TestResponsesTestdataMatchesDocs(t *testing.T) {
	docs, testdata := responsesPaths(t)
	want, err := os.ReadFile(docs)
	if err != nil {
		t.Fatalf("docs responses: %v", err)
	}
	got, err := os.ReadFile(testdata)
	if err != nil {
		t.Fatalf("testdata responses: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("testdata/management-api-v4.0-responses.json is not byte-identical to docs/backlog/api/…; copy from docs (make docs)")
	}
}

func loadParitySnapshot(t *testing.T) paritySnapshot {
	t.Helper()
	_, testdata := responsesPaths(t)
	raw, err := os.ReadFile(testdata)
	if err != nil {
		t.Fatal(err)
	}
	var snap paritySnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Operations) == 0 {
		t.Fatal("responses snapshot has no operations")
	}
	return snap
}

var (
	parityGroups = []any{map[string]any{
		"node": map[string]any{
			"compute": map[string]any{"cpu": 4, "ram": 16},
			"disk":    map[string]any{"type": "gp3", "storage": 50, "iops": 3000},
		},
		"numOfNodes": 3, "services": []any{"data", "index", "query"},
	}}
	parityCluster = map[string]any{
		"name": "made", "cloudProvider": map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.99.0.0/24"},
		"serviceGroups": parityGroups, "availability": map[string]any{"type": "multi"},
		"support": map[string]any{"plan": "basic", "timezone": "ET"},
	}
	paritySchedule = map[string]any{
		"timezone": "US/Pacific",
		"days": []any{
			map[string]any{"day": "monday", "state": "on"}, map[string]any{"day": "tuesday", "state": "on"},
			map[string]any{"day": "wednesday", "state": "on"}, map[string]any{"day": "thursday", "state": "on"},
			map[string]any{"day": "friday", "state": "on"}, map[string]any{"day": "saturday", "state": "on"},
			map[string]any{"day": "sunday", "state": "on"},
		},
	}
	parityAccess = []any{map[string]any{
		"privileges": []any{"data_reader"},
		"resources":  map[string]any{"buckets": []any{map[string]any{"name": "orders"}}},
	}}
	parityBucketSchedule = map[string]any{
		"type": "weekly",
		"weeklySchedule": map[string]any{
			"dayOfWeek": "sunday", "startAt": 2, "incrementalEvery": 24,
			"retentionTime": "30days", "costOptimizedRetention": false,
		},
	}
)

type parityWorld struct {
	ids map[string]string
}

func seedParityWorld(t *testing.T, f *capellafake.Fake) parityWorld {
	t.Helper()
	c := f.Client()
	pid := f.SeedProject("nova-dev", capellafake.WithField("description", "Nova"))
	cid := f.SeedCluster(pid, "nova-dev-01", "healthy", capellafake.WithField("serviceGroups", parityGroups))
	// Free-tier cluster must share the project: inventory paths keep {projectId} and only swap the cluster id.
	fid := f.SeedFreeCluster(pid, "try-01", "healthy")
	bid := f.SeedBucket(cid, "orders", capellafake.WithField("memoryAllocationInMb", 512))
	f.SeedScope(cid, "orders", "inventory", map[string]int{"items": 0})
	freeBid := f.SeedBucket(fid, "free-b")
	uid := f.SeedCredential(cid, "app", []capellaapi.AccessEntry{{
		Privileges: []string{"data_reader"},
		Resources:  capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{{Name: "orders"}}},
	}}, false)
	snap := f.SeedSnapshot(cid, "", 168, "complete")
	backup := f.SeedBucketBackup(cid, "orders", "full", "manual")

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.CreateSchedule(t.Context(), pid, cid, paritySchedule))
	must(c.UpsertSnapshotSchedule(t.Context(), pid, cid, map[string]any{
		"interval": 24, "retention": 720, "startTime": "2026-10-30T02:00:00Z", "copyToRegions": []any{"us-west-2"},
	}))
	cidrID, err := c.CreateAllowedCIDR(t.Context(), pid, cid, "203.0.113.10/32", "VPN", "")
	must(err)
	must(c.CreateBucketBackupSchedule(t.Context(), pid, cid, bid, parityBucketSchedule))
	exportID := f.SeedExport(cid, backup, "failed")

	// cycleID from the seeded backup (needed for GET …/cycles/{cycleId}).
	bk, err := c.GetBackup(t.Context(), pid, cid, backup)
	must(err)
	cycleID, _ := bk["cycleID"].(string)
	if cycleID == "" {
		t.Fatal("seeded backup missing cycleID")
	}

	return parityWorld{ids: map[string]string{
		"{organizationId}": f.OrgID, "{projectId}": pid, "{clusterId}": cid, "{bucketId}": bid,
		"{scopeName}": "inventory", "{collectionName}": "items", "{allowedCidrId}": cidrID, "{userId}": uid,
		"free": fid, "free-bucket": freeBid, "snapshot": snap,
		"{backupId}": backup, "{cycleId}": cycleID, "{exportId}": exportID,
	}}
}

func parityBody(method, template string, ids map[string]string) any {
	tail := template[strings.LastIndex(template, "/")+1:]
	if method != http.MethodPost && method != http.MethodPut || tail == "activationState" {
		return nil
	}
	switch {
	case tail == "cloudsnapshotbackupschedule":
		return map[string]any{"interval": 12, "retention": 168, "startTime": "2026-10-30T02:00:00Z", "copyToRegions": []any{}}
	case tail == "cloudsnapshotbackups":
		return map[string]any{"retention": 168, "regionsToCopy": []any{"us-west-2"}}
	case tail == "{backupId}" && strings.Contains(template, "cloudsnapshotbackups"):
		return map[string]any{"retention": 2160}
	case tail == "restore" && strings.Contains(template, "cloudsnapshotbackups"):
		return map[string]any{}
	case tail == "restore":
		return map[string]any{
			"targetClusterID": ids["{clusterId}"], "sourceClusterID": ids["{clusterId}"],
			"backupID": ids["{backupId}"], "services": []any{"data"},
		}
	case tail == "schedules":
		return parityBucketSchedule
	case tail == "backups" || tail == "export":
		return map[string]any{}
	case tail == "projects":
		return map[string]any{"name": "made", "description": "d"}
	case tail == "{projectId}":
		return map[string]any{"name": "nova-dev", "description": "changed"}
	case tail == "clusters":
		return parityCluster
	case tail == "freeTier" && !strings.Contains(template, "/buckets/"):
		return map[string]any{
			"name": "free-made", "cloudProvider": map[string]any{"type": "aws", "region": "us-east-2", "cidr": "10.98.0.0/24"},
		}
	case tail == "{clusterId}" && strings.Contains(template, "freeTier"):
		return map[string]any{"name": "try-01", "description": "changed"}
	case tail == "{clusterId}":
		return map[string]any{"name": "nova-dev-01", "description": "changed", "support": map[string]any{"plan": "basic", "timezone": "ET"}, "serviceGroups": parityGroups}
	case tail == "deletionProtection":
		return map[string]any{"deletionProtection": true}
	case tail == "onOffSchedule":
		return paritySchedule
	case tail == "allowedcidrs":
		return map[string]any{"cidr": "198.51.100.0/24", "comment": "made"}
	case tail == "buckets" || (tail == "freeTier" && strings.Contains(template, "/buckets/")):
		return map[string]any{"name": "made", "memoryAllocationInMb": 128}
	case tail == "{bucketId}" && strings.Contains(template, "freeTier"):
		return map[string]any{"memoryAllocationInMb": 200}
	case tail == "{bucketId}":
		return map[string]any{"memoryAllocationInMb": 1024, "durabilityLevel": "none", "replicas": 1, "flush": false, "timeToLiveInSeconds": 0}
	case tail == "scopes":
		return map[string]any{"name": "made"}
	case tail == "collections":
		return map[string]any{"name": "made", "maxTTL": 0}
	case tail == "{collectionName}":
		return map[string]any{"maxTTL": 60}
	case tail == "users":
		// Omit password so the fake generates one and returns it (schema requires password).
		return map[string]any{"name": "made", "access": parityAccess}
	case tail == "{userId}":
		return map[string]any{"access": parityAccess}
	default:
		return map[string]any{}
	}
}

func expandParityPath(template string, ids map[string]string) string {
	p := template
	p = strings.ReplaceAll(p, "/cloudsnapshotbackups/{backupId}", "/cloudsnapshotbackups/"+ids["snapshot"])
	if strings.Contains(p, "/clusters/freeTier/") {
		p = strings.ReplaceAll(p, "/clusters/freeTier/{clusterId}", "/clusters/freeTier/"+ids["free"])
	}
	if strings.Contains(p, "/buckets/freeTier/") {
		p = strings.ReplaceAll(p, "/buckets/freeTier/{bucketId}", "/buckets/freeTier/"+ids["free-bucket"])
	}
	if strings.Contains(p, "/buckets/freeTier") {
		p = strings.ReplaceAll(p, "/clusters/{clusterId}", "/clusters/"+ids["free"])
	}
	for k, v := range ids {
		if strings.HasPrefix(k, "{") {
			p = strings.ReplaceAll(p, k, v)
		}
	}
	return p
}

func parityOrder(method, path string) (int, int) {
	if method == http.MethodDelete {
		return 1, -strings.Count(path, "/")
	}
	return 0, 0
}

func TestFakeResponseParity(t *testing.T) {
	snap := loadParitySnapshot(t)
	spec := map[string]parityOp{}
	for _, op := range snap.Operations {
		spec[op.Method+" "+op.Path] = op
	}
	for _, ep := range capellaapi.Inventory {
		if _, ok := spec[ep.Method+" "+ep.Path]; !ok {
			t.Fatalf("inventory op missing from responses snapshot: %s %s", ep.Method, ep.Path)
		}
	}
	if len(spec) != len(capellaapi.Inventory) {
		t.Fatalf("responses ops=%d inventory=%d", len(spec), len(capellaapi.Inventory))
	}

	clk := engine.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	f := capellafake.New(t, capellafake.WithClock(clk))
	world := seedParityWorld(t, f)

	ops := append([]capellaapi.Endpoint(nil), capellaapi.Inventory...)
	sort.SliceStable(ops, func(i, j int) bool {
		ai0, ai1 := parityOrder(ops[i].Method, ops[i].Path)
		bi0, bi1 := parityOrder(ops[j].Method, ops[j].Path)
		if ai0 != bi0 {
			return ai0 < bi0
		}
		return ai1 < bi1
	})

	var report []string
	covered := 0
	for _, ep := range ops {
		op := spec[ep.Method+" "+ep.Path]
		if !op.Documented {
			report = append(report, fmt.Sprintf("%s %s: not in the published spec", ep.Method, ep.Path))
			continue
		}
		restore := prepareParityOp(t, f, ep, world)

		status, body, err := doParityRequest(f, ep.Method, expandParityPath(ep.Path, world.ids), parityBody(ep.Method, ep.Path, world.ids))
		if restore != nil {
			if ep.Method == http.MethodPost && strings.HasSuffix(ep.Path, "/freeTier") && !strings.Contains(ep.Path, "/buckets/") {
				// Drop the newly created free-tier cluster, then put the seeded one back.
				var created struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(body, &created)
				if created.ID != "" && created.ID != world.ids["free"] {
					_ = f.DetachCluster(created.ID)
				}
			}
			restore()
		}
		if err != nil {
			report = append(report, fmt.Sprintf("%s %s: request error: %v", ep.Method, ep.Path, err))
			continue
		}
		documented := map[string]json.RawMessage{}
		for code, raw := range op.Responses {
			documented[code] = raw
		}
		codeKey := strconv.Itoa(status)
		rawSchema, ok := documented[codeKey]
		if !ok {
			keys := make([]string, 0, len(documented))
			for k := range documented {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			report = append(report, fmt.Sprintf("%s %s: status %d (%s); the spec documents %s",
				ep.Method, ep.Path, status, truncate(string(body), 120), strings.Join(keys, ", ")))
			continue
		}
		if status == http.StatusNoContent {
			if len(bytes.TrimSpace(body)) != 0 {
				report = append(report, fmt.Sprintf("%s %s: 204 with a body", ep.Method, ep.Path))
			}
			covered++
			continue
		}
		if isJSONNull(rawSchema) {
			if len(bytes.TrimSpace(body)) != 0 {
				report = append(report, fmt.Sprintf("%s %s: a body, but the spec documents none", ep.Method, ep.Path))
			}
			covered++
			continue
		}
		var schema any
		if err := json.Unmarshal(rawSchema, &schema); err != nil {
			report = append(report, fmt.Sprintf("%s %s: bad schema JSON: %v", ep.Method, ep.Path, err))
			continue
		}
		inst, err := decodeParityBody(body, schema)
		if err != nil {
			report = append(report, fmt.Sprintf("%s %s: %v", ep.Method, ep.Path, err))
			continue
		}
		for _, p := range schemaProblems(inst, schema, "body") {
			report = append(report, fmt.Sprintf("%s %s: %s", ep.Method, ep.Path, p))
		}
		if err := validateWithJSONSchema(schema, inst); err != nil {
			report = append(report, fmt.Sprintf("%s %s: jsonschema: %v", ep.Method, ep.Path, err))
		}
		covered++
	}
	if len(report) > 0 {
		t.Fatalf("the fake differs from the Management API spec (%d issues; covered %d/%d):\n%s",
			len(report), covered, len(ops), strings.Join(report, "\n"))
	}
	t.Logf("parity covered %d/%d inventory operations", covered, len(ops))
}

func prepareParityOp(t *testing.T, f *capellafake.Fake, ep capellaapi.Endpoint, world parityWorld) (restore func()) {
	t.Helper()
	c := f.Client()
	ids := world.ids
	ctx := t.Context()
	switch {
	case ep.Method == http.MethodPost && strings.HasSuffix(ep.Path, "/freeTier") && !strings.Contains(ep.Path, "/buckets/"):
		// Capella allows only one free-tier cluster: set the seed aside for the create.
		return f.DetachCluster(ids["free"])
	case ep.Method == http.MethodPost && strings.HasSuffix(ep.Path, "/activationState") && strings.Contains(ep.Path, "/clusters/"):
		// Go fake only accepts turn-on from turnedOff; restore healthy afterwards.
		target := ids["{clusterId}"]
		if strings.Contains(ep.Path, "freeTier") {
			target = ids["free"]
		}
		f.SetClusterState(target, "turnedOff")
		return func() { f.SetClusterState(target, "healthy") }
	case ep.Method == http.MethodPost && strings.HasSuffix(ep.Path, "/onOffSchedule"):
		_ = c.DeleteSchedule(ctx, ids["{projectId}"], ids["{clusterId}"])
	case ep.Method == http.MethodPost && strings.HasSuffix(ep.Path, "/backup/schedules"):
		_ = c.DeleteBucketBackupSchedule(ctx, ids["{projectId}"], ids["{clusterId}"], ids["{bucketId}"])
	case ep.Method == http.MethodDelete && ep.Path == "/v4/organizations/{organizationId}/projects/{projectId}/clusters/freeTier/{clusterId}":
		// Prior DELETE …/activationState may have left the free-tier cluster transitional.
		f.SetClusterState(ids["free"], "healthy")
	case ep.Method == http.MethodDelete && ep.Path == "/v4/organizations/{organizationId}/projects/{projectId}/clusters/{clusterId}":
		// Prior DELETE …/activationState / PUT deletionProtection may block destroy.
		f.SetClusterState(ids["{clusterId}"], "healthy")
		_ = c.SetDeletionProtection(ctx, ids["{projectId}"], ids["{clusterId}"], false)
	case ep.Method == http.MethodDelete && strings.HasSuffix(ep.Path, "/projects/{projectId}"):
		// Capella deletes a project only once its clusters are gone (incl. ones POST /clusters created).
		f.ClearProjectClusters(ids["{projectId}"])
	}
	return nil
}

func doParityRequest(f *capellafake.Fake, method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.URL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return res.StatusCode, b, err
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func decodeParityBody(body []byte, schema any) (any, error) {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		// Empty body with an object schema (e.g. DELETE snapshot 202 {}) → treat as {}.
		if m, ok := schema.(map[string]any); ok {
			if typ, _ := m["type"].(string); typ == "object" || m["properties"] != nil {
				return map[string]any{}, nil
			}
		}
		return nil, fmt.Errorf("empty body, but the spec documents a schema")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(trim))
	if err != nil {
		return nil, fmt.Errorf("body JSON: %w", err)
	}
	return inst, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// schemaProblems ports capellactl tests/unit/fakes/test_fake_parity.py::problems.
// Tolerance vs Python: additionalProperties / undeclared fields are ignored (Phase 4 §3.7).
func schemaProblems(value any, schema any, where string) []string {
	if schema == nil {
		return nil
	}
	sch, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if opts, ok := sch[key].([]any); ok && len(opts) > 0 {
			var best []string
			anyOK := false
			for _, o := range opts {
				p := schemaProblems(value, o, where)
				if len(p) == 0 {
					anyOK = true
					break
				}
				if best == nil || len(p) < len(best) {
					best = p
				}
			}
			if anyOK {
				return nil
			}
			return best
		}
	}
	if all, ok := sch["allOf"].([]any); ok && len(all) > 0 {
		merged := map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
		props := merged["properties"].(map[string]any)
		var req []any
		for _, part := range all {
			pm, _ := part.(map[string]any)
			for k, v := range asMap(pm["properties"]) {
				props[k] = v
			}
			req = append(req, asList(pm["required"])...)
		}
		merged["required"] = req
		for k, v := range sch {
			if k != "allOf" {
				merged[k] = v
			}
		}
		sch = merged
	}
	if value == nil {
		if nullable, _ := sch["nullable"].(bool); nullable {
			return nil
		}
		return []string{where + ": null, but the spec does not allow null"}
	}
	t, _ := sch["type"].(string)
	if t == "" && sch["properties"] != nil {
		t = "object"
	}
	switch t {
	case "string":
		if _, ok := value.(string); !ok {
			return []string{fmt.Sprintf("%s: %T, the spec says string", where, value)}
		}
	case "integer":
		if !isParityInt(value) {
			return []string{fmt.Sprintf("%s: %T, the spec says integer", where, value)}
		}
	case "number":
		if !isParityNumber(value) {
			return []string{fmt.Sprintf("%s: %T, the spec says number", where, value)}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return []string{fmt.Sprintf("%s: %T, the spec says boolean", where, value)}
		}
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: %T, the spec says object", where, value)}
		}
		props := asMap(sch["properties"])
		var found []string
		for _, req := range asList(sch["required"]) {
			r, _ := req.(string)
			if r != "" {
				if _, ok := obj[r]; !ok {
					found = append(found, fmt.Sprintf("%s.%s: required by the spec, missing in the fake", where, r))
				}
			}
		}
		// additionalProperties ignored for fake-added fields (§3.7).
		for k, v := range obj {
			if ps, ok := props[k]; ok {
				found = append(found, schemaProblems(v, ps, where+"."+k)...)
			}
		}
		return found
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: %T, the spec says array", where, value)}
		}
		items := sch["items"]
		var found []string
		n := len(arr)
		if n > 3 {
			n = 3
		}
		for i := 0; i < n; i++ {
			found = append(found, schemaProblems(arr[i], items, fmt.Sprintf("%s[%d]", where, i))...)
		}
		return found
	}
	return nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func asList(v any) []any {
	a, _ := v.([]any)
	return a
}

func isParityInt(v any) bool {
	switch n := v.(type) {
	case float64:
		return n == float64(int64(n))
	case json.Number:
		_, err := n.Int64()
		return err == nil
	case int, int32, int64:
		return true
	default:
		return false
	}
}

func isParityNumber(v any) bool {
	switch v.(type) {
	case float64, json.Number, int, int32, int64, float32:
		return true
	default:
		return false
	}
}

// validateWithJSONSchema compiles the documented schema via jsonschema/v6 with
// additionalProperties forced true (fake-added fields) and format assertions off.
func validateWithJSONSchema(schema, instance any) error {
	relaxed := relaxSchema(schema)
	raw, err := json.Marshal(relaxed)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	const uri = "https://cbctl.test/capella/parity-response.json"
	if err := c.AddResource(uri, doc); err != nil {
		return err
	}
	sch, err := c.Compile(uri)
	if err != nil {
		return err
	}
	return sch.Validate(instance)
}

func relaxSchema(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x)+1)
		for k, val := range x {
			if k == "format" {
				continue
			}
			out[k] = relaxSchema(val)
		}
		typ, _ := out["type"].(string)
		if typ == "object" || out["properties"] != nil {
			out["additionalProperties"] = true
		}
		// Spurious required on non-objects (e.g. regions array) confuses the compiler.
		if typ == "array" {
			delete(out, "required")
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = relaxSchema(e)
		}
		return out
	default:
		return v
	}
}

func TestParityCheckerReportsEachKind(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []any{"id", "name"},
		"properties": map[string]any{
			"id":   map[string]any{"type": "string"},
			"name": map[string]any{"type": "string"},
			"n":    map[string]any{"type": "integer"},
		},
	}
	found := schemaProblems(map[string]any{"id": 1.0, "n": true, "extra": "x"}, schema, "body")
	join := strings.Join(found, "\n")
	for _, want := range []string{
		"body.name: required by the spec, missing in the fake",
		"body.id: float64, the spec says string",
		"body.n: bool, the spec says integer",
	} {
		if !strings.Contains(join, want) {
			t.Fatalf("missing %q in:\n%s", want, join)
		}
	}
	// Extras are ignored (Go §3.7 tolerance); Python flags them.
	if strings.Contains(join, "extra") {
		t.Fatalf("additionalProperties should be ignored, got:\n%s", join)
	}
}
