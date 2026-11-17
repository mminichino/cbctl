package yamlio

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type shapeInner struct {
	Name string `json:"name"`
}

type shapeEmbedded struct {
	Shared string `json:"shared"`
}

type shapeText struct{ V string }

func (s *shapeText) UnmarshalText(b []byte) error { s.V = string(b); return nil }

type shapeDoc struct {
	shapeEmbedded
	Str     string                `json:"str"`
	Flag    bool                  `json:"flag"`
	Small   int8                  `json:"small"`
	Count   int                   `json:"count"`
	Unsig   uint16                `json:"unsig"`
	Ratio   float64               `json:"ratio"`
	List    []shapeInner          `json:"list"`
	Arr     [2]string             `json:"arr"`
	Bytes   []byte                `json:"bytes"`
	Map     map[string]shapeInner `json:"map"`
	Ptr     *shapeInner           `json:"ptr"`
	Any     any                   `json:"any"`
	Text    shapeText             `json:"text"`
	When    time.Time             `json:"when"`
	Quoted  int                   `json:"quoted,string"`
	Skipped string                `json:"-"`
	hidden  string
}

func TestUnmarshalStrictShapeErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown top-level key", "strr: x\n", "strr: unknown field (did you mean str?)"},
		{"embedded field is known", "shared: x\nstr: y\n", ""},
		{"skipped and hidden fields are unknown", "Skipped: x\n", "Skipped: unknown field"},
		{"string for object", "ptr: x\n", "expected an object, got a string"},
		{"object for string", "str: {a: 1}\n", "expected a string, got an object"},
		{"list for string", "str: [1]\n", "expected a string, got a list"},
		{"number for string", "str: 5\n", "expected a string, got a number"},
		{"string for bool", "flag: 'yes'\n", "expected a boolean, got a string"},
		{"bool for int", "count: true\n", "expected an integer, got a boolean"},
		{"fraction for int", "count: 1.5\n", "expected an integer"},
		{"int8 overflow", "small: 300\n", "an integer that fits int8"},
		{"string for uint", "unsig: x\n", "expected a non-negative integer, got a string"},
		{"negative uint", "unsig: -1\n", "a non-negative integer that fits uint16"},
		{"string for float", "ratio: x\n", "expected a number, got a string"},
		{"object for list", "list: {a: 1}\n", "expected a list, got an object"},
		{"unknown field in list item", "list:\n  - nmae: a\n", "list[0].nmae: unknown field (did you mean name?)"},
		{"map of structs", "map:\n  k:\n    nam: a\n", "map.k.nam: unknown field (did you mean name?)"},
		{"object for map", "map: [1]\n", "expected an object, got a list"},
		{"number for bytes", "bytes: 1\n", "expected a string, got a number"},
		{"null is fine", "str: null\nptr: null\nlist: null\n", ""},
		{"opaque any and text types", "any: {x: [1, 2]}\ntext: hello\nwhen: 2024-01-02T03:04:05Z\n", ""},
		{"quoted number is not walked", "quoted: '5'\n", ""},
		{"array items", "arr: [a, 1]\n", "arr[1]: expected a string, got a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d shapeDoc
			err := UnmarshalStrict([]byte(tt.yaml), &d, WithSource("f.yaml"))
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "f.yaml:") {
				t.Errorf("error %q has no file and line", err)
			}
		})
	}
}

func TestUnmarshalStrictManyErrors(t *testing.T) {
	var d shapeDoc
	err := UnmarshalStrict([]byte("str: 1\nflag: x\nzzz: 1\n"), &d, WithSource("f.yaml"))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"f.yaml:1:", "f.yaml:2:", "f.yaml:3:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not report %s", err, want)
		}
	}
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Errorf("errors.As cannot reach a *FieldError in %T", err)
	}
}

func TestUnmarshalLenientTypeErrorKeepsLine(t *testing.T) {
	var d shapeDoc
	err := Unmarshal([]byte("str: ok\ncount: [1]\n"), &d, WithSource("f.yaml"))
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want a *FieldError", err)
	}
	if fe.Line != 2 || fe.Pointer != "/count" {
		t.Errorf("FieldError = %+v, want line 2 at /count", fe)
	}
	// Without a source the generic name is used for failures that have no position.
	var s struct{ N Size }
	err = Unmarshal([]byte("N: x\n"), &s)
	if err == nil || !strings.Contains(err.Error(), "YAML") {
		t.Errorf("error = %v, want one naming YAML", err)
	}
}

// Size fails to decode on purpose.
type Size struct{}

func (*Size) UnmarshalJSON([]byte) error { return errors.New("bad size") }

func TestUnknownFields(t *testing.T) {
	var d shapeDoc
	errs, err := UnknownFields([]byte("str: 1\nnope: 1\nlist:\n  - nmae: a\n"), &d, WithSource("f.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Type mismatches (str: 1) are not reported; only unknown keys are.
	if len(errs) != 2 || errs[0].Pointer != "/list/0/nmae" || errs[1].Pointer != "/nope" {
		t.Errorf("errs = %+v", errs)
	}
	for _, in := range []string{"", "# only a comment\n"} {
		if errs, err := UnknownFields([]byte(in), &d); err != nil || errs != nil {
			t.Errorf("UnknownFields(%q) = %v, %v; want nothing", in, errs, err)
		}
	}
	if _, err := UnknownFields([]byte("a: [\n"), &d); err == nil {
		t.Error("a syntax error must be reported")
	}
	if _, err := UnknownFields([]byte("a: 1\n"), shapeDoc{}); err == nil {
		t.Error("a non-pointer target must be rejected")
	}
}

func TestOptionsAndNodes(t *testing.T) {
	t.Run("WithUseNumber keeps big integers", func(t *testing.T) {
		var v map[string]any
		if err := Unmarshal([]byte("n: 12345678901234567890\n"), &v, WithUseNumber()); err != nil {
			t.Fatal(err)
		}
		if got, ok := v["n"].(json.Number); !ok || got.String() != "12345678901234567890" {
			t.Errorf("n = %#v", v["n"])
		}
	})
	t.Run("WithIndent", func(t *testing.T) {
		out, err := Marshal(map[string]any{"a": map[string]any{"b": 1}}, WithIndent(4))
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != "a:\n    b: 1\n" {
			t.Errorf("out = %q", out)
		}
	})
	t.Run("MarshalOrdered hoists keys", func(t *testing.T) {
		out, err := MarshalOrdered(map[string]any{"a": 1, "name": "x", "b": 2}, []string{"name"})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != "name: x\na: 1\nb: 2\n" {
			t.Errorf("out = %q", out)
		}
	})
	t.Run("JSONToNode and NodeToJSON", func(t *testing.T) {
		n, err := JSONToNode([]byte(`{"z":1,"a":[true,null,"s"]}`))
		if err != nil {
			t.Fatal(err)
		}
		js, err := NodeToJSON(n)
		if err != nil {
			t.Fatal(err)
		}
		if string(js) != `{"z":1,"a":[true,null,"s"]}` {
			t.Errorf("js = %s", js)
		}
		if js, err := NodeToJSON(nil); err != nil || string(js) != "null" {
			t.Errorf("NodeToJSON(nil) = %s, %v", js, err)
		}
		if _, err := JSONToNode([]byte(`{"a":1} extra`)); err == nil {
			t.Error("trailing data must be rejected")
		}
		if _, err := JSONToNode([]byte(`{"a":`)); err == nil {
			t.Error("truncated JSON must be rejected")
		}
	})
	t.Run("Marshal rejects what JSON cannot hold", func(t *testing.T) {
		if _, err := Marshal(make(chan int)); err == nil {
			t.Error("want an error for a channel")
		}
		if _, err := MarshalOrdered(make(chan int), nil); err == nil {
			t.Error("want an error for a channel")
		}
	})
	t.Run("ParseNode", func(t *testing.T) {
		if n, err := ParseNode(nil); err != nil || n != nil {
			t.Errorf("ParseNode(nil) = %v, %v", n, err)
		}
		if _, err := ParseNode([]byte("a: [\n")); err == nil {
			t.Error("want a syntax error")
		}
		if _, err := ParseNode([]byte("a: 1\n---\nb: 2\n")); err == nil {
			t.Error("several documents must be rejected")
		}
	})
}

func TestConversionEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"merge list", "a: &a {x: 1}\nb: &b {y: 2}\nc:\n  <<: [*a, *b]\n  z: 3\n", `{"a":{"x":1},"b":{"y":2},"c":{"z":3,"x":1,"y":2}}`},
		{"explicit key wins over merge", "a: &a {x: 1}\nc:\n  <<: *a\n  x: 9\n", `{"a":{"x":1},"c":{"x":9}}`},
		{"nan stays text", "v: .nan\n", `{"v":".nan"}`},
		{"infinity stays text", "v: .inf\n", `{"v":".inf"}`},
		{"plus integer", "v: +12345678901234567890\n", `{"v":12345678901234567890}`},
		{"octal and hex", "a: 0o17\nb: 0x1f\n", `{"a":15,"b":31}`},
		{"float", "v: 1.5e3\n", `{"v":1500}`},
		{"uint64", "v: 18446744073709551615\n", `{"v":18446744073709551615}`},
		{"null forms", "a: ~\nb: null\nc:\n", `{"a":null,"b":null,"c":null}`},
		{"quoted timestamp-like", "v: '2024-01-02'\n", `{"v":"2024-01-02"}`},
		{"timestamp text", "v: 2024-01-02T03:04:05Z\n", `{"v":"2024-01-02T03:04:05Z"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := YAMLToJSON([]byte(tt.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
	t.Run("timestamps as RFC3339", func(t *testing.T) {
		got, err := YAMLToJSON([]byte("v: 2024-01-02t03:04:05.5+01:00\n"), WithTimestamps(RFC3339))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"v":"2024-01-02T03:04:05.5+01:00"}` {
			t.Errorf("got %s", got)
		}
	})
	errCases := []struct{ name, yaml, want string }{
		{"merge of a scalar", "a:\n  <<: 5\n", "merge key"},
		{"merge list with a scalar", "a:\n  <<: [1]\n", "merge key"},
		{"binary", "v: !!binary aGk=\n", "!!binary"},
		{"non-string key", "? [1]\n: 2\n", "mapping keys must be strings"},
		{"undefined alias", "a: *nope\n", ""},
	}
	for _, tt := range errCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := YAMLToJSON([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecoderStream(t *testing.T) {
	dec := NewDecoder(strings.NewReader("a: 1\n---\nb: [\n"), WithSource("s.yaml"))
	doc, err := dec.Next()
	if err != nil || string(doc.JSON) != `{"a":1}` || doc.Index != 0 {
		t.Fatalf("first = %+v, %v", doc, err)
	}
	if _, err := dec.Next(); err == nil {
		t.Error("the broken second document must fail")
	}
}

func TestErrorText(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&FieldError{Pointer: "/a/0/b", Msg: "bad"}, "a[0].b: bad"},
		{&FieldError{Source: "f", Pointer: "/a", Msg: "bad"}, "f: a: bad"},
		{&FieldError{Source: "f", Line: 3, Pointer: "/a", Msg: "bad"}, "f:3: a: bad"},
		{&FieldError{Line: 3, Column: 2, Pointer: "/a", Msg: "bad", Suggest: "ab"}, "line 3, column 2: a: bad (did you mean ab?)"},
		{&SyntaxError{Source: "f", Line: 2, Column: 1, Msg: "oops"}, "f:2:1"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); !strings.Contains(got, tt.want) {
			t.Errorf("Error() = %q, want it to contain %q", got, tt.want)
		}
	}
}
