package plan

import (
	"encoding/json"
	"reflect"
	"testing"
)

var (
	g1 = Obj{
		"node":       Obj{"compute": Obj{"cpu": int64(4), "ram": int64(16)}, "disk": Obj{"type": "gp3", "storage": int64(50), "iops": int64(3000)}},
		"numOfNodes": int64(3), "services": []any{"data", "index", "query"},
	}
	g2 = Obj{"numOfNodes": int64(2), "services": []any{"search"}}
)

func paths(cs []Change) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

func TestFieldChanges_UndeclaredFieldIgnored(t *testing.T) {
	observed := Obj{"name": "b", "memoryAllocationInMb": float64(100), "priority": float64(5), "replicas": float64(1), "type": "couchbase"}
	desired := Obj{"name": "b", "memoryAllocationInMb": int64(100), "replicas": int64(1)}
	if got := FieldChanges(desired, observed, "", nil); len(got) != 0 {
		t.Fatalf("expected no changes, got %+v", got)
	}
}

func TestFieldChanges_DefaultCompared(t *testing.T) {
	got := FieldChanges(Obj{"name": "b", "replicas": int64(1)}, Obj{"name": "b", "replicas": float64(2)}, "", nil)
	want := []Change{{Path: "replicas", Before: float64(2), After: int64(1)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestFieldChanges(t *testing.T) {
	tests := []struct {
		name              string
		desired, observed any
		prefix            string
		skip              map[string]bool
		want              []string
	}{
		{"nested path", Obj{"a": Obj{"b": int64(1)}}, Obj{"a": Obj{"b": int64(2)}}, "", nil, []string{"a.b"}},
		{"prefix", Obj{"b": int64(1)}, Obj{"b": int64(2)}, "a", nil, []string{"a.b"}},
		{"skip top level only", Obj{"x": int64(1), "n": Obj{"x": int64(1)}}, Obj{"x": int64(2), "n": Obj{"x": int64(2)}}, "", map[string]bool{"x": true}, []string{"n.x"}},
		{"ignored fields at every level", Obj{"id": "a", "n": Obj{"etag": "x", "stats": Obj{}}}, Obj{"id": "b"}, "", nil, []string{}},
		{"missing observed object", Obj{"n": Obj{"v": int64(1)}}, nil, "", nil, []string{"n.v"}},
		{"int vs float", Obj{"v": int64(3)}, Obj{"v": float64(3)}, "", nil, []string{}},
		{"float differs", Obj{"v": 1.5}, Obj{"v": float64(2)}, "", nil, []string{"v"}},
		{"support.plan case-insensitive", Obj{"support": Obj{"plan": "Developer Pro"}}, Obj{"support": Obj{"plan": "developer pro"}}, "", nil, []string{}},
		{"services order", Obj{"services": []any{"query", "data"}}, Obj{"services": []any{"data", "query"}}, "", nil, []string{}},
		{"services differ", Obj{"services": []any{"data"}}, Obj{"services": []any{"data", "query"}}, "", nil, []string{"services"}},
		{"privilege aliases", Obj{"access": Obj{"privileges": []any{"read", "write"}}}, Obj{"access": Obj{"privileges": []any{"data_writer", "data_reader"}}}, "", nil, []string{}},
		{"string slices", Obj{"privileges": []string{"read"}}, Obj{"privileges": []any{"data_reader"}}, "", nil, []string{}},
		{"explicit null vs missing", Obj{"v": nil}, Obj{}, "", nil, []string{}},
		{"bool differs", Obj{"v": true}, Obj{"v": false}, "", nil, []string{"v"}},
		{"list of objects", Obj{"l": []any{Obj{"a": int64(1)}}}, Obj{"l": []any{Obj{"a": float64(1)}}}, "", nil, []string{}},
		{"scalar desired", "x", "y", "p", nil, []string{"p"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := paths(FieldChanges(tc.desired, tc.observed, tc.prefix, tc.skip))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestFieldChanges_SortedKeys(t *testing.T) {
	got := paths(FieldChanges(Obj{"c": int64(1), "a": int64(1), "b": int64(1)}, Obj{}, "", nil))
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
}

func TestCanonFlush(t *testing.T) {
	tests := []struct {
		name string
		body Obj
		want bool
		ok   bool
	}{
		{"flush fallback", Obj{"flush": false}, false, true},
		{"flushEnabled wins", Obj{"flush": true, "flushEnabled": false}, false, true},
		{"flushEnabled true", Obj{"flushEnabled": true}, true, true},
		{"absent", Obj{}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CanonFlush(tc.body)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %v,%v", got, ok)
			}
		})
	}
}

func TestGroupKey(t *testing.T) {
	if got := GroupKey(Obj{"services": []any{"query", "data", "index"}}); got != "data,index,query" {
		t.Fatal(got)
	}
	if got := GroupKey(Obj{"services": []string{"search"}}); got != "search" {
		t.Fatal(got)
	}
	if got := GroupKey(Obj{}); got != "" {
		t.Fatal(got)
	}
	if GroupKey(g1) != "data,index,query" || GroupKey(g2) != "search" {
		t.Fatal("fixture groups")
	}
}

func TestCanonPrivileges(t *testing.T) {
	got := CanonPrivileges([]string{"write", "read", "data_reader", "x"})
	if !reflect.DeepEqual(got, []string{"data_reader", "data_writer", "x"}) {
		t.Fatalf("got %v", got)
	}
}

func TestCanonValue(t *testing.T) {
	if got := CanonValue("support.plan", "BASIC"); got != "basic" {
		t.Fatal(got)
	}
	if got := CanonValue("other", "BASIC"); got != "BASIC" {
		t.Fatal(got)
	}
	if got := CanonValue("serviceGroups[x].services", []any{"b", "a"}); !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Fatal(got)
	}
	in := []any{"b", "a"}
	CanonValue("services", in)
	if in[0] != "b" {
		t.Fatal("CanonValue must not mutate its input")
	}
	if got := CanonValue("services", []any{1, 2}); !reflect.DeepEqual(got, []any{1, 2}) {
		t.Fatal("non-string lists pass through", got)
	}
	if got := CanonValue("privileges", []any{"read", "read"}); !reflect.DeepEqual(got, []any{"data_reader"}) {
		t.Fatal(got)
	}
}

func TestDeepMerge(t *testing.T) {
	base := Obj{"name": "c", "support": Obj{"plan": "basic", "timezone": "PT"}, "keep": int64(1), "n": int64(2)}
	overlay := Obj{"support": Obj{"plan": "pro"}, "n": nil, "extra": nil, "new": Obj{"a": int64(1)}}
	got := DeepMerge(base, overlay)
	want := Obj{"name": "c", "support": Obj{"plan": "pro", "timezone": "PT"}, "keep": int64(1), "n": int64(2), "extra": nil, "new": Obj{"a": int64(1)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if base["support"].(Obj)["plan"] != "basic" {
		t.Fatal("base mutated")
	}
	if DeepMerge(Obj{"a": 1}, "x") != "x" || DeepMerge("x", nil) != "x" || DeepMerge(nil, nil) != nil {
		t.Fatal("scalar overlay rules")
	}
	if got := DeepMerge([]any{1}, []any{2}); !reflect.DeepEqual(got, []any{2}) {
		t.Fatal("lists are replaced", got)
	}
}

func TestAsHelpers(t *testing.T) {
	for _, v := range []any{3, int8(3), int16(3), int32(3), int64(3), uint(3), uint8(3), uint16(3), uint32(3), uint64(3), float32(3), 3.0, json.Number("3"), json.Number("3.0")} {
		if i, ok := AsInt(v); !ok || i != 3 {
			t.Errorf("AsInt(%T) = %d,%v", v, i, ok)
		}
	}
	for _, v := range []any{3.5, "3", nil, true, uint64(1 << 63), json.Number("x")} {
		if _, ok := AsInt(v); ok {
			t.Errorf("AsInt(%v) should fail", v)
		}
	}
	if s, ok := AsString("x"); !ok || s != "x" {
		t.Fatal("AsString")
	}
	if _, ok := AsString(1); ok {
		t.Fatal("AsString int")
	}
	if b, ok := AsBool(true); !ok || !b {
		t.Fatal("AsBool")
	}
	if _, ok := AsBool("true"); ok {
		t.Fatal("AsBool string")
	}
	if l, ok := AsList([]string{"a"}); !ok || len(l) != 1 {
		t.Fatal("AsList []string")
	}
	if l, ok := AsList([]Obj{{"a": 1}}); !ok || len(l) != 1 {
		t.Fatal("AsList []Obj")
	}
	if _, ok := AsList([]byte("x")); ok {
		t.Fatal("bytes are not a list")
	}
	if _, ok := AsList("x"); ok {
		t.Fatal("string is not a list")
	}
	if m, ok := AsObj(map[string]string{"a": "b"}); !ok || m["a"] != "b" {
		t.Fatal("AsObj map[string]string")
	}
	if _, ok := AsObj(map[int]string{}); ok {
		t.Fatal("AsObj int keys")
	}
	if _, ok := AsObj(nil); ok {
		t.Fatal("AsObj nil")
	}
}

func TestValuesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{"nil nil", nil, nil, true},
		{"nil vs value", nil, "x", false},
		{"nil vs empty list", nil, []any{}, false},
		{"int float", int64(2), 2.0, true},
		{"float float", 1.5, 1.5, true},
		{"float int", 1.5, int64(1), false},
		{"maps", Obj{"a": int64(1)}, Obj{"a": 1.0}, true},
		{"maps keys", Obj{"a": 1}, Obj{"b": 1}, false},
		{"maps len", Obj{"a": 1}, Obj{"a": 1, "b": 2}, false},
		{"map vs list", Obj{}, []any{}, false},
		{"list len", []any{1}, []any{1, 2}, false},
		{"list elem", []any{1}, []any{2}, false},
		{"string vs number", "1", int64(1), false},
		{"bool", true, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValuesEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
