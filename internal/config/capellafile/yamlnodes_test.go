package capellafile

import (
	"math"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeNumbers(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"int", 3, int64(3)},
		{"int32", int32(-4), int64(-4)},
		{"uint8", uint8(7), int64(7)},
		{"uint64 small", uint64(5), int64(5)},
		{"uint64 huge", uint64(math.MaxUint64), float64(math.MaxUint64)},
		{"integral float", float64(3), int64(3)},
		{"integral float exponent", 1e3, int64(1000)},
		{"fractional float", 1.5, 1.5},
		{"huge float stays float", 1e300, 1e300},
		{"NaN stays float", math.NaN(), math.NaN()},
		{"string", "3", "3"},
		{"bool", true, true},
		{"nil", nil, nil},
		{"nested", Obj{"a": []any{1, 2.0, 2.5, Obj{"b": uint(9)}}},
			Obj{"a": []any{int64(1), int64(2), 2.5, Obj{"b": int64(9)}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeNumbers(tt.in)
			if f, ok := tt.want.(float64); ok && math.IsNaN(f) {
				if g, ok := got.(float64); !ok || !math.IsNaN(g) {
					t.Errorf("got %v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeNumbers(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToObjScalarTyping(t *testing.T) {
	root, issues := parseYAML("i: 3\nf: 1.5\nw: 2.0\nb: true\nn: ~\ns: \"3\"\nt: 2026-01-01T00:00:00Z\nd: 2026-01-01\nbig: 12345678901234567890\nyes: on\nneg: -7\nhex: 0x10\nlist: [1, a]\nnan: .nan\n", "x")
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	got := toObj(root).(Obj)
	want := Obj{
		"i": int64(3), "f": 1.5, "w": int64(2), "b": true, "n": nil, "s": "3",
		"t": "2026-01-01T00:00:00Z", "d": "2026-01-01", "big": float64(12345678901234567890), "yes": "on", "neg": int64(-7), "hex": int64(16),
		"list": []any{int64(1), "a"}, "nan": ".nan",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toObj =\n%#v\nwant\n%#v", got, want)
	}
}

func TestToObjMergeKeys(t *testing.T) {
	root, issues := parseYAML("base: &b {a: 1, b: 2}\nchild:\n  <<: *b\n  b: 3\n", "x")
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	child := toObj(root).(Obj)["child"].(Obj)
	if child["a"] != int64(1) || child["b"] != int64(3) {
		t.Errorf("child = %v", child)
	}
}

func TestParseYAMLProblems(t *testing.T) {
	tests := []struct {
		name, text, want string
		line             int
	}{
		{"empty", "", "x.yaml is empty", 1},
		{"whitespace and comments", "\n# hi\n\n", "x.yaml is empty", 1},
		{"null document", "~\n", "x.yaml is empty", 1},
		{"list", "- 1\n", "x.yaml must be a mapping", 1},
		{"scalar", "hello\n", "x.yaml must be a mapping", 1},
		{"unclosed flow", "a: [1, 2\nb: 1\n", "invalid YAML", 0},
		{"duplicate key", "a: 1\nb: 2\na: 3\n", `mapping key "a" already defined at line 1`, 3},
		{"tab", "a:\n\tb: 1\n", "indent with spaces", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues := parseYAML(tt.text, "x.yaml")
			if len(issues) != 1 || !strings.Contains(issues[0].Message+" "+issues[0].Fix, tt.want) {
				t.Fatalf("issues = %+v", issues)
			}
			if tt.line != 0 && issues[0].Line != tt.line {
				t.Errorf("line = %d, want %d", issues[0].Line, tt.line)
			}
		})
	}
}

func TestIndexLines(t *testing.T) {
	root, _ := parseYAML("a: 1\nb:\n  - x\n  - y: 2\n    z: 3\n", "x")
	lines := indexLines(root)
	tests := []struct {
		path Path
		want int
	}{
		{Path{"a"}, 1}, {Path{"b"}, 2}, {Path{"b", 0}, 3}, {Path{"b", 1}, 4}, {Path{"b", 1, "z"}, 5},
		{Path{"b", 1, "missing"}, 4}, // nearest ancestor
	}
	for _, tt := range tests {
		if got := lines.of(tt.path); got != tt.want {
			t.Errorf("line of %s = %d, want %d", FormatPath(tt.path), got, tt.want)
		}
	}
	if (lineIndex{}).of(Path{"a"}) != 0 {
		t.Error("empty index must report 0")
	}
}

// TestImportRule enforces the layering rule: capellafile must not depend on plan, resources or
// capellaapi (it is the file format, the engine builds on it).
func TestImportRule(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Skipf("go list unavailable: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"/internal/plan", "/internal/resources", "/internal/capellaapi"} {
			if strings.Contains(dep, banned) {
				t.Errorf("capellafile must not import %s (found %s)", banned, dep)
			}
		}
	}
}
