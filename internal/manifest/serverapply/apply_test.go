package serverapply_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/serverapply"
	"github.com/mminichino/cbctl/internal/rest"
)

const twoNodeYAML = `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: demo
spec:
  credentials:
    password: ${SA_PW}
  nodes:
    - ipAddress: 10.0.0.1
    - ipAddress: 10.0.0.2
  buckets:
    - name: orders
      quotaMiB: 256
      scopes:
        - name: sales
          collections: [items, gates]
      import:
        - scope: sales
          collection: items
          file: items.jsonl
          keyField: id
        - scope: sales
          collection: gates
          keyField: gid
          data:
            - gid: g1
            - gid: g2
`

const singleYAML = `apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: solo
spec:
  credentials:
    password: ${SA_PW}
  nodes:
    - ipAddress: 10.0.0.1
`

func writeItems(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "{\"id\": 1, \"v\": \"a\"}\n\n{\"id\": 2, \"v\": \"b\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "items.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func statuses(r *manifest.Report) map[string]string {
	m := map[string]string{}
	for _, s := range r.Steps {
		m[s.Name+" "+s.Target] = s.Status
	}
	return m
}

func TestApplyFreshAndRepeat(t *testing.T) {
	dir := writeItems(t)
	d := doc(t, dir, twoNodeYAML)
	prov := &fakeProv{members: []rest.NodeInfo{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}}, pending: []string{"10.0.0.2"}}
	data := newFakeData()

	rep, pr, err := run(t, context.Background(), dir, d, prov, data, false)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	rep.Finish()
	if rep.Status != manifest.ReportApplied {
		t.Errorf("status = %q, want applied", rep.Status)
	}
	st := statuses(rep)
	for k, want := range map[string]string{
		"bootstrap 10.0.0.1":            manifest.StepDone,
		"join 10.0.0.2":                 manifest.StepDone,
		"rebalance 10.0.0.1":            manifest.StepDone,
		"bucket orders":                 manifest.StepDone,
		"scope orders.sales":            manifest.StepDone,
		"collection orders.sales.items": manifest.StepDone,
		"import orders.sales.items":     manifest.StepDone,
		"import orders.sales.gates":     manifest.StepDone,
	} {
		if st[k] != want {
			t.Errorf("step %q = %q, want %q", k, st[k], want)
		}
	}
	if got := data.colls["orders.sales.items"]; strings.Join(got, ",") != "1,2" {
		t.Errorf("items keys = %v, want [1 2]", got)
	}
	if got := data.colls["orders.sales.gates"]; strings.Join(got, ",") != "g1,g2" {
		t.Errorf("gates keys = %v, want [g1 g2]", got)
	}
	if !pr.has("ok:Cluster created on 10.0.0.1") || !pr.has("ok:Cluster rebalanced") {
		t.Errorf("missing ok lines: %v", pr.lines)
	}
	if rep.Summary != "2 nodes, 1 bucket, 2 collections, 2 imports" {
		t.Errorf("summary = %q", rep.Summary)
	}

	// Second run: nothing changes, nothing is imported again.
	creates, before := data.creates, len(prov.calls)
	rep, pr, err = run(t, context.Background(), dir, d, prov, data, false)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	rep.Finish()
	if rep.Status != manifest.ReportUnchanged {
		t.Errorf("second status = %q, want unchanged: %+v", rep.Status, rep.Steps)
	}
	if data.creates != creates {
		t.Errorf("second run created %d resources", data.creates-creates)
	}
	if len(data.colls["orders.sales.items"]) != 2 {
		t.Errorf("items were imported again")
	}
	if !pr.has("warn:Collection orders.sales.items not empty; import skipped") {
		t.Errorf("no not-empty warning: %v", pr.lines)
	}
	if len(prov.calls) == before {
		t.Errorf("second run made no REST calls at all")
	}
	for _, c := range prov.calls[before:] {
		if c == "rebalance" {
			t.Errorf("second run rebalanced")
		}
	}
}

func TestApplySingleNodeSkips(t *testing.T) {
	d := doc(t, t.TempDir(), singleYAML)
	prov := &fakeProv{members: []rest.NodeInfo{{Host: "10.0.0.1"}}}
	data := newFakeData()
	rep, _, err := run(t, context.Background(), "", d, prov, data, false)
	if err != nil {
		t.Fatal(err)
	}
	rep.Finish()
	st := statuses(rep)
	if st["rebalance 10.0.0.1"] != manifest.StepSkipped {
		t.Errorf("rebalance = %q, want skipped", st["rebalance 10.0.0.1"])
	}
	if len(data.buckets) != 0 || data.creates != 0 {
		t.Errorf("a manifest without buckets touched the data model")
	}
}

func TestApplyMembershipAndRebalanceFalse(t *testing.T) {
	yaml := strings.Replace(twoNodeYAML, "  buckets:", "  rebalance: false\n  buckets:", 1)
	dir := writeItems(t)
	d := doc(t, dir, yaml)
	prov := &fakeProv{members: []rest.NodeInfo{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}, {Host: "10.0.0.9"}}, pending: []string{"10.0.0.2"}}
	rep, pr, err := run(t, context.Background(), dir, d, prov, newFakeData(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.has("warn:node 10.0.0.9 is in the cluster but not in the manifest") {
		t.Errorf("no unlisted-node warning: %v", pr.lines)
	}
	if len(rep.Warnings) != 1 {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	if got := statuses(rep)["rebalance 10.0.0.1"]; got != manifest.StepSkipped {
		t.Errorf("rebalance = %q, want skipped", got)
	}
	for _, c := range prov.calls {
		if c == "rebalance" || c == "pending" {
			t.Errorf("rebalance: false still called %s", c)
		}
	}
}

func TestApplyNoPendingNodes(t *testing.T) {
	dir := writeItems(t)
	d := doc(t, dir, twoNodeYAML)
	prov := &fakeProv{initialized: true, joined: map[string]bool{"10.0.0.2": true}, members: []rest.NodeInfo{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}}}
	rep, pr, err := run(t, context.Background(), dir, d, prov, newFakeData(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.has("info:Cluster already configured") || !pr.has("info:Node 10.0.0.2 already configured") || !pr.has("info:Rebalance not needed") {
		t.Errorf("lines: %v", pr.lines)
	}
	if got := statuses(rep)["bootstrap 10.0.0.1"]; got != manifest.StepUnchanged {
		t.Errorf("bootstrap = %q", got)
	}
}

func TestApplyBucketQuotaWarning(t *testing.T) {
	dir := writeItems(t)
	d := doc(t, dir, twoNodeYAML)
	data := newFakeData()
	data.buckets["orders"] = 512
	rep, pr, err := run(t, context.Background(), dir, d, &fakeProv{}, data, false)
	if err != nil {
		t.Fatal(err)
	}
	if data.buckets["orders"] != 512 {
		t.Errorf("existing bucket was changed")
	}
	if !pr.has("bucket \"orders\" exists with a 512 MiB quota; the manifest says 256 MiB") || len(rep.Warnings) != 1 {
		t.Errorf("lines: %v warnings: %v", pr.lines, rep.Warnings)
	}
}

func TestApplyFailures(t *testing.T) {
	tests := []struct {
		name     string
		prov     *fakeProv
		data     *fakeData
		wantStep string
		notRun   string
	}{
		{"join fails", &fakeProv{failJoin: "10.0.0.2"}, newFakeData(), "join 10.0.0.2", "bucket orders"},
		{"primary belongs to another cluster", &fakeProv{initialized: true, clusterName: "other"}, newFakeData(), "bootstrap 10.0.0.1", "join 10.0.0.2"},
		{"sdk connect fails", &fakeProv{}, &fakeData{failConn: true}, "connect 10.0.0.1", "bucket orders"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeItems(t)
			d := doc(t, dir, twoNodeYAML)
			rep, _, err := run(t, context.Background(), dir, d, tc.prov, tc.data, false)
			if err == nil {
				t.Fatal("want an error")
			}
			if rep == nil || rep.Status != manifest.ReportFailed || rep.Error == "" {
				t.Fatalf("report = %+v", rep)
			}
			last := rep.Steps[len(rep.Steps)-1]
			if last.Name+" "+last.Target != tc.wantStep || last.Status != manifest.StepFailed {
				t.Errorf("last step = %+v, want failed %s", last, tc.wantStep)
			}
			if _, ran := statuses(rep)[tc.notRun]; ran {
				t.Errorf("step %q ran after the failure", tc.notRun)
			}
			if strings.Contains(rep.Error, "pw-canary-1") {
				t.Errorf("error leaks the password")
			}
		})
	}
}

func TestApplyOtherClusterChangesNothing(t *testing.T) {
	dir := writeItems(t)
	d := doc(t, dir, twoNodeYAML)
	prov := &fakeProv{initialized: true, clusterName: "other"}
	_, _, err := run(t, context.Background(), dir, d, prov, newFakeData(), false)
	if err == nil || !strings.Contains(err.Error(), `already belongs to another cluster ("other", the manifest is "demo")`) {
		t.Fatalf("error = %v, want the other-cluster refusal", err)
	}
	for _, c := range prov.calls {
		if strings.HasPrefix(c, "bootstrap") || strings.HasPrefix(c, "join") || c == "rebalance" {
			t.Errorf("a call changed the foreign cluster: %v", prov.calls)
		}
	}
}

func TestApplySameNameAndUnnamedClusterPass(t *testing.T) {
	for _, name := range []string{"demo", ""} {
		dir := writeItems(t)
		d := doc(t, dir, twoNodeYAML)
		if _, _, err := run(t, context.Background(), dir, d, &fakeProv{initialized: true, clusterName: name}, newFakeData(), false); err != nil {
			t.Errorf("cluster name %q: %v", name, err)
		}
	}
}

func TestApplyCancel(t *testing.T) {
	dir := writeItems(t)
	d := doc(t, dir, twoNodeYAML)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prov := &fakeProv{}
	rep, _, err := run(t, ctx, dir, d, prov, newFakeData(), false)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("err = %v, want cancellation", err)
	}
	if len(prov.calls) != 0 {
		t.Errorf("calls after cancel: %v", prov.calls)
	}
	if rep.Status != manifest.ReportFailed {
		t.Errorf("status = %q", rep.Status)
	}
}

func TestApplyDryRun(t *testing.T) {
	dir := writeItems(t)
	d := doc(t, dir, twoNodeYAML)
	prov, data := &fakeProv{}, newFakeData()
	rep, pr, err := run(t, context.Background(), dir, d, prov, data, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != manifest.ReportPlanned {
		t.Errorf("status = %q, want planned", rep.Status)
	}
	if len(prov.calls) != 0 || data.creates != 0 {
		t.Errorf("a dry run contacted the cluster: %v", prov.calls)
	}
	if !pr.has("info:Would create bucket \"orders\"") {
		t.Errorf("lines: %v", pr.lines)
	}
	for _, s := range rep.Steps {
		if s.Status != manifest.StepPlanned {
			t.Errorf("step %+v is not planned", s)
		}
	}
}

func TestApplyRejectsOtherKinds(t *testing.T) {
	if _, err := serverapply.Apply(context.Background(), serverapply.Env{}, nil); err == nil {
		t.Error("want an error for a nil document")
	}
}
