package yamlio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"
)

type spec struct {
	Name    string            `json:"name"`
	Nodes   []node            `json:"nodes"`
	Labels  map[string]string `json:"labels"`
	Extra   json.RawMessage   `json:"extra"`
	Replica int               `json:"replicas"`
	Weekend string            `json:"weekend"`
}

type node struct {
	IPAddress string `json:"ipAddress"`
	Port      int    `json:"port,omitempty"`
}

func TestYAMLToJSON(t *testing.T) {
	tests := []struct {
		name, in, want string
		opts           []Option
	}{
		{"weekend off stays a string", "weekend: off\n", `{"weekend":"off"}`, nil},
		{"yes no on y stay strings", "a: yes\nb: no\nc: on\nd: y\n", `{"a":"yes","b":"no","c":"on","d":"y"}`, nil},
		{"true false are booleans", "a: true\nb: False\n", `{"a":true,"b":false}`, nil},
		{"key order is kept", "z: 1\na: 2\nm: 3\n", `{"z":1,"a":2,"m":3}`, nil},
		{"big int keeps digits", "n: 12345678901234567890\n", `{"n":12345678901234567890}`, nil},
		{"huge int keeps digits", "n: 123456789012345678901234567890\n", `{"n":123456789012345678901234567890}`, nil},
		{"whole float is an int", "n: 2.0\n", `{"n":2}`, nil},
		{"nan stays text", "n: .nan\n", `{"n":".nan"}`, nil},
		{"timestamp keeps text", "t: 2026-01-01\n", `{"t":"2026-01-01"}`, nil},
		{"timestamp rfc3339", "t: 2026-01-01\n", `{"t":"2026-01-01T00:00:00Z"}`, []Option{WithTimestamps(RFC3339)}},
		{"reference untouched", "p: ${file:./a b}\n", `{"p":"${file:./a b}"}`, nil},
		{"merge", "a: &a {x: 1}\nb:\n  <<: *a\n  y: 2\n", `{"a":{"x":1},"b":{"y":2,"x":1}}`, nil},
		{"explicit key beats merge", "a: &a {x: 1}\nb:\n  <<: *a\n  x: 2\n", `{"a":{"x":1},"b":{"x":2}}`, nil},
		{"html is not escaped", "a: \"<&>\"\n", `{"a":"<&>"}`, nil},
		{"bom stripped", "\ufeffa: 1\n", `{"a":1}`, nil},
		{"empty is null", "", `null`, nil},
		{"comment only is null", "# nothing\n", `null`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := YAMLToJSON([]byte(tt.in), tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestYAMLToJSONErrors(t *testing.T) {
	tests := []struct {
		name, in, want string
		line           int
	}{
		{"duplicate key", "a: 1\nb: 2\na: 3\n", `mapping key "a" already defined at line 1`, 3},
		{"non-string key", "1: a\n", "mapping keys must be strings", 1},
		{"binary", "a: !!binary aGVsbG8=\n", "!!binary", 1},
		{"tab", "a:\n\t- b\n", "cannot start any token", 2},
		{"unclosed flow", "a: [1, 2\nb: 1\n", "", 0},
		{"two documents", "a: 1\n---\nb: 2\n", "several YAML documents", 0},
		{"bad merge", "a: 1\nb:\n  <<: 3\n", "merge key", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := YAMLToJSON([]byte(tt.in), WithSource("f.yaml"))
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("want *SyntaxError, got %T %v", err, err)
			}
			if !strings.Contains(se.Msg, tt.want) {
				t.Errorf("Msg = %q, want %q", se.Msg, tt.want)
			}
			if tt.line > 0 && se.Line != tt.line {
				t.Errorf("Line = %d, want %d", se.Line, tt.line)
			}
			if se.Source != "f.yaml" {
				t.Errorf("Source = %q", se.Source)
			}
		})
	}
}

func TestAliasExpansionIsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 8; i++ {
		b.WriteString("a" + string(rune('0'+i)) + ": &a" + string(rune('0'+i)) + " [")
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("*a" + string(rune('0'+i-1)))
		}
		b.WriteString("]\n")
	}
	_, err := YAMLToJSON([]byte(b.String()))
	var se *SyntaxError
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "alias expansion limit") {
		t.Fatalf("want alias limit error, got %v", err)
	}
}

func TestInputSizeLimit(t *testing.T) {
	big := bytes.Repeat([]byte("# c\n"), MaxInputBytes/4+1)
	if _, err := YAMLToJSON(big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v", err)
	}
}

func TestUnmarshal(t *testing.T) {
	var s spec
	y := "name: demo\nweekend: off\nreplicas: 2\nnodes:\n  - ipAddress: 10.0.0.1\n    port: 8091\nlabels: {a: b}\nextra: {anything: [1, 2]}\nunknown: 1\n"
	if err := Unmarshal([]byte(y), &s); err != nil {
		t.Fatal(err)
	}
	want := spec{Name: "demo", Weekend: "off", Replica: 2, Nodes: []node{{"10.0.0.1", 8091}},
		Labels: map[string]string{"a": "b"}, Extra: json.RawMessage(`{"anything":[1,2]}`)}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("got %+v, want %+v", s, want)
	}
	var untouched spec
	untouched.Name = "keep"
	if err := Unmarshal([]byte("# empty\n"), &untouched); err != nil || untouched.Name != "keep" {
		t.Errorf("empty document: %v %+v", err, untouched)
	}
}

func TestUnmarshalStrictErrors(t *testing.T) {
	tests := []struct {
		name, in string
		want     []string // each is an exact Error() line
	}{
		{"unknown field with suggestion",
			"name: a\nnodes:\n  - ipadress: 1.2.3.4\n",
			[]string{"capella.yaml:3:5: nodes[0].ipadress: unknown field (did you mean ipAddress?)"}},
		{"unknown field without suggestion",
			"name: a\nzzzzzzzz: 1\n",
			[]string{"capella.yaml:2:1: zzzzzzzz: unknown field"}},
		{"type mismatch",
			"name: a\nreplicas: many\n",
			[]string{"capella.yaml:2:1: replicas: expected an integer, got a string"}},
		{"list expected",
			"nodes: x\n",
			[]string{"capella.yaml:1:1: nodes: expected a list, got a string"}},
		{"several are all reported in line order",
			"naem: a\nnodes:\n  - port: 1\n    prot: 2\n",
			[]string{"capella.yaml:1:1: naem: unknown field (did you mean name?)", "capella.yaml:4:5: nodes[0].prot: unknown field (did you mean port?)"}},
		{"duplicate key",
			"name: a\nname: b\n",
			[]string{`capella.yaml:2:1: mapping key "name" already defined at line 1`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s spec
			err := UnmarshalStrict([]byte(tt.in), &s, WithSource("capella.yaml"))
			if err == nil {
				t.Fatal("want an error")
			}
			got := strings.Split(err.Error(), "\n")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFieldErrorFields(t *testing.T) {
	var s spec
	err := UnmarshalStrict([]byte("nodes:\n  - ipadress: x\n"), &s, WithSource("m.yaml"))
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("got %T", err)
	}
	if fe.Source != "m.yaml" || fe.Pointer != "/nodes/0/ipadress" || fe.Line != 2 || fe.Column != 5 || fe.Suggest != "ipAddress" {
		t.Errorf("%+v", fe)
	}
}

func TestUnknownFieldsLenient(t *testing.T) {
	var s spec
	y := []byte("name: a\nfuture: 1\nnodes:\n  - ipAddress: x\n    tls: true\n")
	if err := Unmarshal(y, &s); err != nil {
		t.Fatal(err)
	}
	extra, err := UnknownFields(y, &s)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range extra {
		got = append(got, DottedPath(e.Pointer))
	}
	if want := []string{"future", "nodes[0].tls"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMarshal(t *testing.T) {
	got, err := Marshal(spec{Name: "on", Nodes: []node{{IPAddress: "1.2.3.4"}}, Weekend: "off", Replica: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := "name: \"on\"\nnodes:\n  - ipAddress: 1.2.3.4\nlabels: null\nextra: null\nreplicas: 1\nweekend: \"off\"\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	var back spec
	if err := Unmarshal(got, &back); err != nil || back.Name != "on" || back.Weekend != "off" {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

func TestMarshalOrdered(t *testing.T) {
	v := []any{map[string]any{"access": []any{}, "name": "x", "other": 1}}
	got, err := MarshalOrdered(v, []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "- name: x\n  access: []\n  other: 1\n"; string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestJSONToYAMLErrors(t *testing.T) {
	for _, in := range []string{"", "{", "[1,", `{"a":1} {"b":2}`} {
		if _, err := JSONToYAML([]byte(in)); err == nil {
			t.Errorf("JSONToYAML(%q): want an error", in)
		}
	}
}

func TestDecoder(t *testing.T) {
	in := "a: 1\n---\n# comment\n---\nb: [2]\n---\nc: 3\n"
	dec := NewDecoder(strings.NewReader(in), WithKeepNode())
	var idx []int
	var js []string
	for {
		d, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if d.Node == nil {
			t.Error("WithKeepNode: no node")
		}
		idx = append(idx, d.Index)
		js = append(js, string(d.JSON))
	}
	if !reflect.DeepEqual(idx, []int{0, 2, 3}) || !reflect.DeepEqual(js, []string{`{"a":1}`, `{"b":[2]}`, `{"c":3}`}) {
		t.Errorf("idx %v js %v", idx, js)
	}
	if _, err := dec.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("after EOF: %v", err)
	}
}

func TestDecoderDocumentDecodeAndLines(t *testing.T) {
	dec := NewDecoder(strings.NewReader("a: 1\n---\nname: x\nnodes:\n  - ipadress: y\n"))
	if _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	d, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	var s spec
	if err := d.Decode(&s, false); err != nil || s.Name != "x" {
		t.Fatalf("lenient: %v %+v", err, s)
	}
	err = d.Decode(&s, true)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Line != 5 {
		t.Errorf("strict: %v", err)
	}
	if d.Lines.Of("/nodes/0/ipadress") != (Position{5, 5}) {
		t.Errorf("lines: %+v", d.Lines)
	}
}

func TestDecoderLimits(t *testing.T) {
	dec := NewDecoder(strings.NewReader("a: 1\n---\nb: 2\n---\nc: 3\n"), WithMaxDocuments(2))
	for i := 0; i < 2; i++ {
		if _, err := dec.Next(); err != nil {
			t.Fatal(err)
		}
	}
	_, err := dec.Next()
	var se *SyntaxError
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "more than 2 documents") {
		t.Errorf("got %v", err)
	}
	dec = NewDecoder(strings.NewReader("a: 1\n---\nb: 2: 3\n"))
	if _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); !errors.As(err, &se) {
		t.Errorf("syntax error in the second document: %v", err)
	}
}

func TestLineIndexOf(t *testing.T) {
	n, err := ParseNode([]byte("a:\n  b:\n    - x\n    - y: 1\nc: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	idx := BuildLineIndex(n)
	tests := []struct {
		ptr  string
		want int
	}{
		{"/a", 1}, {"/a/b", 2}, {"/a/b/0", 3}, {"/a/b/1/y", 4}, {"/c", 5},
		{"/a/b/1/missing", 4}, // nearest ancestor
		{"/zzz", 1},           // the root
	}
	for _, tt := range tests {
		if got := idx.Of(tt.ptr).Line; got != tt.want {
			t.Errorf("Of(%q) = %d, want %d", tt.ptr, got, tt.want)
		}
	}
	if (LineIndex{}).Of("/x") != (Position{}) {
		t.Error("empty index should know nothing")
	}
}

func TestDottedPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""}, {"/a", "a"}, {"/spec/nodes/0/ip", "spec.nodes[0].ip"}, {"/a~1b/c~0d", "a/b.c~d"}, {"/0", "0"},
	}
	for _, tt := range tests {
		if got := DottedPath(tt.in); got != tt.want {
			t.Errorf("DottedPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDeleteMappingKeysKeepsComments(t *testing.T) {
	in := "# header\nversion: 1\nprofiles:\n  a:\n    x: 1 # keep\n  b:\n    x: 2\n  c:\n    x: 3\n"
	n, err := ParseNode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := DeleteMappingKeys(n, "profiles", []string{"b", "zz"}); got != 1 {
		t.Fatalf("removed %d", got)
	}
	out, err := EncodeNode(n)
	if err != nil {
		t.Fatal(err)
	}
	want := "# header\nversion: 1\nprofiles:\n  a:\n    x: 1 # keep\n  c:\n    x: 3\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	DeleteMappingKeys(n, "profiles", []string{"a", "c"})
	out, _ = EncodeNode(n)
	if !strings.Contains(string(out), "profiles: {}") {
		t.Errorf("empty mapping:\n%s", out)
	}
}

func TestMappingKeys(t *testing.T) {
	n, err := ParseNode([]byte("z: 1\na: 2\nm: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := MappingKeys(n.Content[0]); !reflect.DeepEqual(got, []string{"z", "a", "m"}) {
		t.Errorf("got %v", got)
	}
	if MappingKeys(&yaml.Node{Kind: yaml.ScalarNode}) != nil {
		t.Error("scalar has no keys")
	}
}

func TestConcurrentStrictDecode(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var s spec
			if err := UnmarshalStrict([]byte("name: a\nnodes:\n  - ipAddress: x\n"), &s); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestUnmarshalTargetMustBePointer(t *testing.T) {
	if err := UnmarshalStrict([]byte("a: 1\n"), spec{}); err == nil {
		t.Error("want an error for a non-pointer target")
	}
}
