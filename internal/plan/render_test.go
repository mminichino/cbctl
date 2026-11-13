package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mminichino/cbctl/internal/redact"
)

func TestFmtValue(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "(none)"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"string", "x", `"x"`},
		{"int", int64(5), "5"},
		{"plain int", 7, "7"},
		{"integral float", 3.0, "3.0"},
		{"float", 1.5, "1.5"},
		{"list", []any{"a", "b"}, "[a, b]"},
		{"string slice", []string{"a", "b"}, "[a, b]"},
		{"mixed list", []any{"a", int64(2), true, nil}, "[a, 2, true, (none)]"},
		{"empty list", []any{}, "[]"},
		{"map sorted", Obj{"b": int64(1), "a": "x"}, `{"a": "x", "b": 1}`},
		{"nested map", Obj{"a": Obj{"c": []any{int64(1), "z"}}, "n": nil}, `{"a": {"c": [1, "z"]}, "n": null}`},
		{"escapes", Obj{"k": "q\"\n\\é😀"}, `{"k": "q\"\n\\\u00e9\ud83d\ude00"}`},
		{"float in map", Obj{"v": 2.0, "w": 0.25}, `{"v": 2.0, "w": 0.25}`},
		{"big float", 1e20, "1e+20"},
		{"other", struct{ A int }{1}, "{1}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FmtValue(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{1200 * time.Millisecond, "1.2s"},
		{3 * time.Second, "3s"},
		{9500 * time.Millisecond, "9.5s"},
		{12 * time.Second, "12s"},
		{10500 * time.Millisecond, "10s"},
		{4 * time.Minute, "4m0s"},
		{62*time.Minute + 3*time.Second, "1h2m3s"},
	}
	for _, tc := range tests {
		if got := FormatDuration(tc.d); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.d, got, tc.want)
		}
	}
}

func fixedPlan() *Plan {
	s := newStub("thing", "", Flags{})
	create := act(s, "cluster", "nova-dev-01", Create, nil)
	create.Summary = "aws us-east-1 · 3 nodes"
	create.Notes = []string{"(after project is created)"}
	create.Remarks = []string{"note: three groups"}

	update := act(s, "cluster", "nova-prod", Update, nil)
	update.Label = "scale out"
	update.Changes = []Change{
		{Path: "serviceGroups[data,index].numOfNodes", Before: int64(3), After: int64(4)},
		{Path: "serviceGroups[search]", Before: nil, After: "2 x 4 cpu / 16 GB"},
		{Path: "serviceGroups[eventing]", Before: "1 x 4 cpu / 16 GB", After: nil},
		{Path: "support.plan", Before: "basic", After: "developer pro"},
		{Path: "description", Before: nil, After: "hi"},
		{Path: "services", Before: []any{"data"}, After: []any{"data", "index"}},
		{Path: "flushEnabled", Before: false, After: true},
	}
	update.Remarks = []string{"checks: memory ok"}

	replace := act(s, "allowed-cidr", "10.0.0.0/24", Replace, nil)
	replace.Changes = []Change{{Path: "comment", Before: "a", After: "b"}}

	del := act(s, "bucket", "nova-prod/old", Delete, nil)

	blocked := act(s, "cluster", "nova-stg", Blocked, nil)
	blocked.Reason = "immutable: cloudProvider.region (requires a new cluster)"
	blocked.Topic = "idempotency"

	blockedNoTopic := act(s, "scope", "x", Blocked, nil)
	blockedNoTopic.Reason = "nope"

	deferred := act(s, "bucket", "nova-prod/new", Deferred, nil)
	deferred.Summary = "memory 100 MB"
	deferred2 := act(s, "bucket", "nova-prod/new2", Deferred, nil)
	deferred2.Reason = "waiting"

	noop := act(s, "project", "nova", Noop, nil)
	hidden := act(s, "project", "hidden", Update, nil)
	hidden.Hidden = true
	return &Plan{
		Actions:   []*Action{noop, hidden, create, update, replace, del, blocked, blockedNoTopic, deferred, deferred2},
		Unmanaged: []string{"scope nova-prod/stray", "cluster extra  (not pruned: clusters are never pruned)"},
	}
}

const wantFixedText = `Project nova · org-1

  = project nova
  + cluster nova-dev-01  aws us-east-1 · 3 nodes  (after project is created)
      note: three groups
  ~ cluster nova-prod   scale out
      serviceGroups[data,index].numOfNodes:  3 → 4
    + serviceGroups[search]  2 x 4 cpu / 16 GB
    - serviceGroups[eventing]  1 x 4 cpu / 16 GB
      support.plan:  "basic" → "developer pro"
      description:  (none) → "hi"
      services:  [data] → [data, index]
      flushEnabled:  false → true
      checks: memory ok
  ± allowed-cidr 10.0.0.0/24
      comment:  "a" → "b"
  - bucket nova-prod/old
  ! cluster nova-stg  immutable: cloudProvider.region (requires a new cluster)
      See: cbctl capella guide idempotency
  ! scope x  nope
  … bucket nova-prod/new  memory 100 MB — deferred
  … bucket nova-prod/new2   — waiting

Unmanaged (not in capella.yaml; use --prune to delete):
    scope nova-prod/stray
    cluster extra  (not pruned: clusters are never pruned)

Plan: 2 to add, 1 to change, 1 to delete, 2 blocked, 2 deferred.`

func TestRenderPlanText_Golden(t *testing.T) {
	got := RenderPlanText(fixedPlan(), "Project nova · org-1", false)
	if got != wantFixedText {
		t.Fatalf("got:\n%s\n\nwant:\n%s", got, wantFixedText)
	}
}

func TestRenderPlanText_QuietAndEmpty(t *testing.T) {
	p := &Plan{}
	if got := RenderPlanText(p, "", true); got != "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked." {
		t.Fatal(got)
	}
	if got := RenderPlanText(p, "", false); got != "\nPlan: 0 to add, 0 to change, 0 to delete, 0 blocked." {
		t.Fatalf("%q", got)
	}
	if got := RenderPlanText(fixedPlan(), "H", true); !strings.Contains(got, "! cluster nova-stg") || !strings.HasPrefix(got, "H\n\n") {
		t.Fatal("quiet with a blocked action prints the full plan")
	}
	noBlock := &Plan{Actions: []*Action{act(newStub("t", "", Flags{}), "bucket", "b", Create, nil)}}
	if got := RenderPlanText(noBlock, "H", true); got != "Plan: 1 to add, 0 to change, 0 to delete, 0 blocked." {
		t.Fatal(got)
	}
}

func TestRenderPlanText_Redacts(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	redact.Register("s3cr3t-value")
	a := act(newStub("t", "", Flags{}), "credential", "u", Update, nil)
	a.Changes = []Change{{Path: "note", Before: nil, After: "s3cr3t-value"}}
	a.Notes = []string{"s3cr3t-value"}
	p := &Plan{Actions: []*Action{a}}
	if text := RenderPlanText(p, "", false); strings.Contains(text, "s3cr3t-value") {
		t.Fatal(text)
	}
	b, _ := json.Marshal(RenderPlanJSON(p))
	if strings.Contains(string(b), "s3cr3t-value") {
		t.Fatal(string(b))
	}
	if table := RenderSummaryTable([]*Action{func() *Action { a.Key = "s3cr3t-value"; return a }()}); strings.Contains(table, "s3cr3t-value") {
		t.Fatal(table)
	}
}

func TestSummaryLine(t *testing.T) {
	s := newStub("t", "", Flags{})
	p := &Plan{Actions: []*Action{
		act(s, "a", "1", Create, nil), act(s, "a", "2", Replace, nil), act(s, "a", "3", Update, nil),
		act(s, "a", "4", Delete, nil), act(s, "a", "5", Blocked, nil), act(s, "a", "6", Noop, nil),
	}}
	hidden := act(s, "a", "7", Create, nil)
	hidden.Hidden = true
	p.Actions = append(p.Actions, hidden)
	if got := p.SummaryLine(); got != "Plan: 2 to add, 1 to change, 1 to delete, 1 blocked." {
		t.Fatal(got)
	}
	if len(p.Visible()) != 6 || p.Count(Create) != 1 || !p.HasChanges() || len(p.Blocked()) != 1 {
		t.Fatal("counts")
	}
	p.Actions = append(p.Actions, act(s, "a", "8", Deferred, nil))
	if got := p.SummaryLine(); got != "Plan: 2 to add, 1 to change, 1 to delete, 1 blocked, 1 deferred." {
		t.Fatal(got)
	}
	if (&Plan{Actions: []*Action{act(s, "a", "1", Noop, nil), act(s, "a", "2", Blocked, nil)}}).HasChanges() {
		t.Fatal("noop and blocked are not changes")
	}
}

func TestActionType(t *testing.T) {
	for _, tc := range []struct {
		t     ActionType
		write bool
		sym   string
	}{
		{Create, true, "+"}, {Update, true, "~"}, {Replace, true, "±"}, {Delete, true, "-"},
		{Noop, false, "="}, {Blocked, false, "!"}, {Deferred, false, "…"},
	} {
		if tc.t.IsWrite() != tc.write || Symbol[tc.t] != tc.sym {
			t.Errorf("%s", tc.t)
		}
	}
}

func TestActionHelpers(t *testing.T) {
	s := newStub("t", "", Flags{})
	proj := act(s, "project", "p", Noop, nil)
	proj.Observed = &Observed{ID: "p-1"}
	cl := act(s, "cluster", "c", Create, proj)
	if cl.ID() != "" || cl.Exists() {
		t.Fatal("new cluster has no id")
	}
	cl.CreatedID = "c-1"
	if cl.ID() != "c-1" || !cl.Exists() {
		t.Fatal("created id")
	}
	cl.Type = Delete
	if cl.Exists() {
		t.Fatal("deleted does not exist")
	}
	if cl.Ancestor("project") != proj || cl.Ancestor("cluster") != nil || proj.Ancestor("project") != nil {
		t.Fatal("Ancestor")
	}
	if proj.ID() != "p-1" {
		t.Fatal("observed id")
	}
	var a Action
	a.Set("k", 1)
	if a.Data["k"] != 1 {
		t.Fatal("Set")
	}
}

func TestToJSON(t *testing.T) {
	p := fixedPlan()
	j := p.ToJSON()
	actions := j["actions"].([]any)
	if len(actions) != 9 {
		t.Fatalf("hidden dropped: %d", len(actions))
	}
	create := actions[1].(map[string]any)
	if create["summary"] != "aws us-east-1 · 3 nodes" {
		t.Fatal(create)
	}
	notes := create["notes"].([]any)
	if len(notes) != 2 || notes[0] != "(after project is created)" || notes[1] != "note: three groups" {
		t.Fatal(notes)
	}
	if blocked := actions[5].(map[string]any); blocked["reason"] == nil {
		t.Fatal(blocked)
	}
	if _, has := (&Plan{}).ToJSON()["unmanaged"]; has {
		t.Fatal("no unmanaged key when empty")
	}
}

func TestRenderSummaryTable(t *testing.T) {
	if RenderSummaryTable(nil) != "" {
		t.Fatal("empty")
	}
	s := newStub("t", "", Flags{})
	a := act(s, "project", "nova", Create, nil)
	a.Status, a.Duration = "done", 1500*time.Millisecond
	b := act(s, "cluster", "nova-dev-01-long", Update, nil)
	b.Status, b.Duration = "done", 4*time.Minute
	c := act(s, "bucket", "x", Delete, nil)
	c.Status = "skipped"
	noop := act(s, "bucket", "n", Noop, nil)
	hidden := act(s, "bucket", "h", Update, nil)
	hidden.Hidden = true
	got := RenderSummaryTable([]*Action{a, b, c, noop, hidden})
	want := "KIND     NAME              ACTION  STATUS   DURATION\n" +
		"project  nova              create  done     1.5s\n" +
		"cluster  nova-dev-01-long  update  done     4m0s\n" +
		"bucket   x                 delete  skipped  -"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if RenderSummaryTable([]*Action{noop, hidden}) != "" {
		t.Fatal("only noop/hidden → empty")
	}
}

func loadSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for _, n := range []string{"plan", "apply", "scale"} {
		raw, err := os.ReadFile("testdata/" + n + ".schema.json")
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("https://cbctl.test/schemas/"+n+".schema.json", doc); err != nil {
			t.Fatal(err)
		}
	}
	sch, err := c.Compile("https://cbctl.test/schemas/" + name + ".schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func validate(t *testing.T, sch *jsonschema.Schema, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema violation: %v\n%s", err, raw)
	}
}

func TestJSONOutputsMatchSchemas(t *testing.T) {
	planSchema := loadSchema(t, "plan")
	applySchema := loadSchema(t, "apply")
	loadSchema(t, "scale")

	p := fixedPlan()
	validate(t, planSchema, RenderPlanJSON(p))
	validate(t, planSchema, RenderPlanJSON(&Plan{}))

	for _, a := range p.Actions {
		a.Status = "done"
	}
	p.Actions[4].Status, p.Actions[4].Err = "failed", "boom"
	p.Actions[2].CreatedID = "id-1"
	res := &ApplyResult{Actions: p.Actions, Failed: p.Actions[4], Extra: map[string]any{"connectionStrings": []any{"couchbases://x"}}}
	validate(t, applySchema, RenderApplyJSON(p, res))
	validate(t, applySchema, RenderApplyJSON(&Plan{}, &ApplyResult{}))
}

func TestSchemaRejectsBadOutput(t *testing.T) {
	planSchema := loadSchema(t, "plan")
	raw := []byte(`{"plan":{"actions":[{"kind":"a","key":"b","action":"explode","changes":[]}]}}`)
	inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if planSchema.Validate(inst) == nil {
		t.Fatal("schema must reject unknown action")
	}
}
