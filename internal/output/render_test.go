package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
)

type sample struct {
	Name   string            `json:"name"`
	Memory int               `json:"memoryMB"`
	Tags   []string          `json:"tags"`
	Nested map[string]string `json:"nested"`
}

var sampleDoc = sample{Name: "orders", Memory: 1024, Tags: []string{"a", "b"}, Nested: map[string]string{"k": "v"}}

func newRender(jsonMode bool) (Renderer, *bytes.Buffer, *bytes.Buffer) {
	var out, errW bytes.Buffer
	return NewRenderer(&out, &errW, jsonMode, false, RenderOptions{}), &out, &errW
}

// TestRenderGoldens: Table, Object and the one-time secrets in text and JSON mode.
func TestRenderGoldens(t *testing.T) {
	tests := []struct {
		name string
		json bool
		call func(r Renderer)
	}{
		{"table", false, func(r Renderer) {
			r.Table([]string{"NAME", "NODES"}, [][]string{{"nova-dev-01", "3"}, {"a\tb\nc", "12"}})
		}},
		{"table", true, func(r Renderer) { r.Table([]string{"NAME"}, [][]string{{"x"}}) }},
		{"object", false, func(r Renderer) { r.Object(sampleDoc) }},
		{"object", true, func(r Renderer) { r.Object(sampleDoc) }},
		{"list", true, func(r Renderer) { r.List([]sample{sampleDoc}) }},
		{"list_empty", true, func(r Renderer) { r.List([]sample(nil)) }},
		{"list", false, func(r Renderer) { r.List([]sample{sampleDoc}) }},
		{"line", false, func(r Renderer) { r.Line("%d clusters", 2) }},
		{"line", true, func(r Renderer) { r.Line("%d clusters", 2) }},
		{"secret", false, func(r Renderer) { r.Secret(map[string]string{"name": "app"}, "password", "Pa55-once") }},
		{"secret", true, func(r Renderer) { r.Secret(map[string]string{"name": "app"}, "password", "Pa55-once") }},
		{"secret_nil_doc", false, func(r Renderer) { r.Secret(nil, "password", "Pa55-once") }},
		{"secrets", false, func(r Renderer) {
			r.Secrets(map[string]string{"cluster": "c2"}, "credentials", []NamedSecret{{"app", "p1"}, {"ro", "p2"}})
		}},
		{"secrets", true, func(r Renderer) {
			r.Secrets(map[string]string{"cluster": "c2"}, "credentials", []NamedSecret{{"app", "p1"}, {"ro", "p2"}})
		}},
	}
	for _, tc := range tests {
		mode := "text"
		if tc.json {
			mode = "json"
		}
		t.Run(tc.name+"_"+mode, func(t *testing.T) {
			r, out, errW := newRender(tc.json)
			tc.call(r)
			if errW.Len() != 0 {
				t.Errorf("stderr = %q", errW.String())
			}
			golden(t, "render_"+tc.name+"_"+mode+".txt", out.String())
		})
	}
}

// TestRendererNeverColored: no mode of the renderer writes an escape code, whatever the
// environment; JSON output parses as one document.
func TestRendererNeverColored(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		var out, errW bytes.Buffer
		r := NewRenderer(&out, &errW, jsonMode, false, RenderOptions{
			Env:    envOf(map[string]string{"CLICOLOR_FORCE": "1", "FORCE_COLOR": "1"}),
			IsTerm: func(any) bool { return true },
		})
		r.Table([]string{"A"}, [][]string{{"b"}})
		r.Object(sampleDoc)
		r.Line("x")
		r.Progress(".")
		r.Secret(nil, "password", "p")
		if strings.Contains(out.String()+errW.String(), "\x1b[") {
			t.Errorf("json=%v: escape code in %q %q", jsonMode, out.String(), errW.String())
		}
	}
	r, out, _ := newRender(true)
	r.Object(sampleDoc)
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["name"] != "orders" {
		t.Errorf("JSON output does not parse as one document: %v %q", err, out.String())
	}
}

func TestRendererRedacts(t *testing.T) {
	const secret = "tok-render-test-9f8e7d"
	redact.Register(secret)
	t.Cleanup(redact.Reset)
	tests := []struct {
		name string
		json bool
		call func(r Renderer)
	}{
		{"text table", false, func(r Renderer) { r.Table([]string{"K"}, [][]string{{"key " + secret}}) }},
		{"text object", false, func(r Renderer) { r.Object(map[string]string{"k": "v " + secret}) }},
		{"text object secret key", false, func(r Renderer) { r.Object(map[string]string{"password": "hunter2-not-registered"}) }},
		{"text line", false, func(r Renderer) { r.Line("token %s", secret) }},
		{"json object", true, func(r Renderer) { r.Object(map[string]string{"k": "v " + secret}) }},
		{"json secret key", true, func(r Renderer) { r.Object(map[string]string{"password": "hunter2-not-registered"}) }},
		{"json list", true, func(r Renderer) { r.List([]string{secret}) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, out, _ := newRender(tc.json)
			tc.call(r)
			if strings.Contains(out.String(), secret) || strings.Contains(out.String(), "hunter2-not-registered") {
				t.Errorf("secret leaked: %q", out.String())
			}
		})
	}
}

// TestSecretIsNotRedacted: a one-time secret is printed as issued even when registered.
func TestSecretIsNotRedacted(t *testing.T) {
	redact.Register("Pa55-once")
	t.Cleanup(redact.Reset)
	r, out, _ := newRender(false)
	r.Secret(nil, "password", "Pa55-once")
	if out.String() != "password: Pa55-once\n" {
		t.Errorf("got %q", out.String())
	}
}

func TestRendererProgressDots(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		tty   bool
		quiet bool
		want  string
	}{
		{"terminal", nil, true, false, "."},
		{"not a terminal", nil, false, false, ""},
		{"quiet", nil, true, true, ""},
		{"CI", map[string]string{"CI": "true"}, true, false, ""},
		{"TERM=dumb", map[string]string{"TERM": "dumb"}, true, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errW bytes.Buffer
			r := NewRenderer(&out, &errW, false, tc.quiet, RenderOptions{Env: envOf(tc.env), IsTerm: func(any) bool { return tc.tty }})
			r.Progress(".")
			if errW.String() != tc.want {
				t.Errorf("dots = %q, want %q", errW.String(), tc.want)
			}
		})
	}
}

func TestWriteJSONAndToGeneric(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, map[string]any{"a": "<b>", "n": 1}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "{\n  \"a\": \"<b>\",\n  \"n\": 1\n}\n" {
		t.Errorf("WriteJSON = %q", buf.String())
	}
	if g, err := ToGeneric(nil); g != nil || err != nil {
		t.Errorf("ToGeneric(nil) = %v, %v", g, err)
	}
	if _, err := ToGeneric(make(chan int)); err == nil {
		t.Error("ToGeneric(chan) should fail")
	}
	// A rendering failure becomes an [error] line on stderr, not a panic.
	r, out, errW := newRender(true)
	r.Object(make(chan int))
	if out.Len() != 0 || !strings.HasPrefix(errW.String(), "[error] - rendering output:") {
		t.Errorf("stdout=%q stderr=%q", out.String(), errW.String())
	}
}

func TestFlattenShapes(t *testing.T) {
	r, out, _ := newRender(false)
	r.Object(map[string]any{"a": map[string]any{}, "b": []any{}, "c": nil, "d": true, "e": []any{1, map[string]any{"x": "y"}}})
	want := "a: {}\nb: []\nc: null\nd: true\ne[0]: 1\ne[1].x: y\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	r, out, _ = newRender(false)
	r.Object("scalar")
	if out.String() != "scalar\n" {
		t.Errorf("scalar = %q", out.String())
	}
}
