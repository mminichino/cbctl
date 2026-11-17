package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/spf13/cobra"
)

type renderers struct {
	out, err bytes.Buffer
	r        Renderer
}

func newTestRenderer(jsonMode bool) *renderers {
	t := &renderers{}
	t.r = newRenderer(&t.out, &t.err, jsonMode, false)
	return t
}

func TestTextTable(t *testing.T) {
	rr := newTestRenderer(false)
	rr.r.Table([]string{"NAME", "ID", "DESCRIPTION"}, [][]string{
		{"short", "1", "first"},
		{"a-much-longer-name", "22", "multi\nline\tcell"},
		{"x", "333", ""},
	})
	want := "NAME                ID   DESCRIPTION\n" +
		"short               1    first\n" +
		"a-much-longer-name  22   multi line cell\n" +
		"x                   333  \n"
	if got := rr.out.String(); got != want {
		t.Fatalf("table:\n%q\nwant:\n%q", got, want)
	}
}

func TestTextTableEmptyKeepsHeader(t *testing.T) {
	rr := newTestRenderer(false)
	rr.r.Table([]string{"A", "B"}, nil)
	if got := rr.out.String(); got != "A  B\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTextObjectOrderAndNesting(t *testing.T) {
	type cloud struct {
		Type   string `json:"type"`
		Region string `json:"region"`
	}
	type obj struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Cloud   cloud    `json:"cloud"`
		Nodes   int      `json:"nodes"`
		On      bool     `json:"on"`
		Tags    []string `json:"tags"`
		Empty   []string `json:"empty"`
		Missing *string  `json:"missing"`
		Z       string   `json:"z"`
	}
	rr := newTestRenderer(false)
	rr.r.Object(obj{ID: "1", Name: "n", Cloud: cloud{"aws", "us-east-1"}, Nodes: 3, On: true, Tags: []string{"a", "b"}, Empty: []string{}, Z: "last"})
	want := "id: 1\nname: n\ncloud.type: aws\ncloud.region: us-east-1\nnodes: 3\non: true\ntags[0]: a\ntags[1]: b\nempty: []\nmissing: null\nz: last\n"
	if got := rr.out.String(); got != want {
		t.Fatalf("object:\n%s\nwant:\n%s", got, want)
	}
}

func TestTextObjectKeepsSpecialCharacters(t *testing.T) {
	rr := newTestRenderer(false)
	rr.r.Object(map[string]string{"q": "a&b<c>"})
	if got := rr.out.String(); got != "q: a&b<c>\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTextLineAndList(t *testing.T) {
	rr := newTestRenderer(false)
	rr.r.Line("created project %s (%s)", "p", "id-1")
	rr.r.List([]string{"ignored"})
	if got := rr.out.String(); got != "created project p (id-1)\n" {
		t.Fatalf("got %q", got)
	}
}

func TestJSONRenderer(t *testing.T) {
	rr := newTestRenderer(true)
	rr.r.Table([]string{"A"}, [][]string{{"x"}})
	rr.r.Line("hidden %d", 1)
	if rr.out.Len() != 0 {
		t.Fatalf("Table/Line wrote in JSON mode: %q", rr.out.String())
	}

	rr.r.Object(map[string]any{"name": "p", "n": 12345678901234, "nested": map[string]any{"ok": true}})
	var got map[string]any
	if err := json.Unmarshal(rr.out.Bytes(), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, rr.out.String())
	}
	if got["name"] != "p" || got["n"] != float64(12345678901234) {
		t.Errorf("got %v", got)
	}
	if !strings.Contains(rr.out.String(), "\n  \"name\"") {
		t.Errorf("not indented:\n%s", rr.out.String())
	}
}

func TestJSONList(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"items", []map[string]string{{"a": "1"}, {"a": "2"}}, `[{"a":"1"},{"a":"2"}]`},
		{"nil slice is an empty array", []string(nil), `[]`},
		{"nil is an empty array", nil, `[]`},
		{"empty", []int{}, `[]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := newTestRenderer(true)
			rr.r.List(tc.in)
			var gotV, wantV any
			if err := json.Unmarshal(rr.out.Bytes(), &gotV); err != nil {
				t.Fatalf("not valid JSON: %v\n%s", err, rr.out.String())
			}
			_ = json.Unmarshal([]byte(tc.want), &wantV)
			g, _ := json.Marshal(gotV)
			w, _ := json.Marshal(wantV)
			if string(g) != string(w) {
				t.Errorf("got %s, want %s", g, w)
			}
		})
	}
}

func TestRenderersRedact(t *testing.T) {
	const secret = "sk-test-SECRET-1234"
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
			rr := newTestRenderer(tc.json)
			tc.call(rr.r)
			out := rr.out.String()
			if out == "" {
				t.Fatal("nothing rendered")
			}
			if strings.Contains(out, secret) || strings.Contains(out, "hunter2") {
				t.Fatalf("secret leaked: %s", out)
			}
			if !strings.Contains(out, "***") {
				t.Fatalf("no mask in output: %s", out)
			}
		})
	}
}

func TestPrintSecretOnce(t *testing.T) {
	const pw = "Gen3rated-Pa55word!"
	redact.Register(pw) // as CreateCredential does
	t.Cleanup(redact.Reset)

	type doc struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	t.Run("text", func(t *testing.T) {
		rr := newTestRenderer(false)
		var logOut, logErr bytes.Buffer
		logging.SetOutputs(&logOut, &logErr)
		t.Cleanup(func() { logging.SetOutputs(nil, nil) })
		printSecretOnce(&runtime{Out: rr.r}, doc{"id-1", "app"}, "password", pw)
		if got, want := rr.out.String(), "id: id-1\nname: app\npassword: "+pw+"\n"; got != want {
			t.Fatalf("stdout:\n%q\nwant:\n%q", got, want)
		}
		if !strings.Contains(logErr.String(), "this password is shown only once") {
			t.Errorf("warning missing from stderr: %q", logErr.String())
		}
		if strings.Contains(logErr.String(), pw) || strings.Contains(logOut.String(), pw) {
			t.Error("password leaked into the log")
		}
	})
	t.Run("json", func(t *testing.T) {
		rr := newTestRenderer(true)
		var logOut, logErr bytes.Buffer
		logging.SetOutputs(&logOut, &logErr)
		t.Cleanup(func() { logging.SetOutputs(nil, nil) })
		printSecretOnce(&runtime{Out: rr.r}, doc{"id-1", "app"}, "password", pw)
		var got map[string]any
		if err := json.Unmarshal(rr.out.Bytes(), &got); err != nil {
			t.Fatalf("not valid JSON: %v\n%s", err, rr.out.String())
		}
		if got["password"] != pw || got["id"] != "id-1" || got["name"] != "app" {
			t.Errorf("got %v", got)
		}
		if strings.Count(rr.out.String(), pw) != 1 {
			t.Errorf("password must appear exactly once:\n%s", rr.out.String())
		}
		if strings.Contains(logErr.String(), pw) {
			t.Error("password leaked into stderr")
		}
	})
	t.Run("json without a document", func(t *testing.T) {
		rr := newTestRenderer(true)
		logging.SetOutputs(io.Discard, io.Discard)
		t.Cleanup(func() { logging.SetOutputs(nil, nil) })
		printSecretOnce(&runtime{Out: rr.r}, nil, "password", pw)
		var got map[string]string
		if err := json.Unmarshal(rr.out.Bytes(), &got); err != nil || got["password"] != pw || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

func TestProgress(t *testing.T) {
	tests := []struct {
		name  string
		tty   bool
		quiet bool
		ci    string
		want  string
	}{
		{"tty", true, false, "", "."},
		{"no tty", false, false, "", ""},
		{"quiet", true, true, "", ""},
		{"CI set", true, false, "true", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			old := terminalCheck
			terminalCheck = func(any) bool { return tc.tty }
			t.Cleanup(func() { terminalCheck = old })
			var out, errW bytes.Buffer
			for _, jsonMode := range []bool{false, true} {
				errW.Reset()
				newRenderer(&out, &errW, jsonMode, tc.quiet).Progress(".")
				if errW.String() != tc.want {
					t.Errorf("json=%v: stderr = %q, want %q", jsonMode, errW.String(), tc.want)
				}
			}
			if out.Len() != 0 {
				t.Errorf("Progress wrote to stdout: %q", out.String())
			}
		})
	}
}

func confirmRuntime(yes bool, stdin string) (*runtime, *bytes.Buffer) {
	cmd := &cobra.Command{}
	var errW bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&errW)
	return &runtime{Yes: yes, cmd: cmd}, &errW
}

func TestConfirm(t *testing.T) {
	// confirmRuntime uses DefaultDeps, which reads the process env. Clear CI so the blocked
	// case asserts the non-CI action; CI-specific wording is covered by scenario_ci_test.go.
	t.Setenv("CI", "")
	tests := []struct {
		name       string
		yes, tty   bool
		stdin      string
		want       bool
		wantCode   ExitCode
		wantPrompt bool
	}{
		{"yes skips the prompt without a terminal", true, false, "", true, 0, false},
		{"yes skips the prompt with a terminal", true, true, "", true, 0, false},
		{"no terminal and no --yes is blocked", false, false, "y\n", false, ExitBlocked, false},
		{"y", false, true, "y\n", true, 0, true},
		{"yes", false, true, "yes\n", true, 0, true},
		{"uppercase", false, true, "Y\n", true, 0, true},
		{"padded", false, true, "  yes  \n", true, 0, true},
		{"n", false, true, "n\n", false, 0, true},
		{"empty answer defaults to no", false, true, "\n", false, 0, true},
		{"other text is no", false, true, "maybe\n", false, 0, true},
		{"EOF is no", false, true, "", false, 0, true},
		{"y without newline", false, true, "y", true, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := ttyCheck
			ttyCheck = func(any) bool { return tc.tty }
			t.Cleanup(func() { ttyCheck = old })
			rt, errW := confirmRuntime(tc.yes, tc.stdin)
			got, err := confirm(rt, "Delete it?")
			if tc.wantCode != 0 {
				wantCLIError(t, err, tc.wantCode, "confirmation required but no terminal is attached", "re-run with --yes")
			} else if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Errorf("confirm = %v, want %v", got, tc.want)
			}
			if prompted := strings.Contains(errW.String(), "Delete it? [y/N]: "); prompted != tc.wantPrompt {
				t.Errorf("prompted = %v, want %v (stderr %q)", prompted, tc.wantPrompt, errW.String())
			}
		})
	}
}

func TestConfirmThroughRealCommand(t *testing.T) {
	f := capellafake.New(t)
	capellaEnv(t, f)
	// Under `go test` stdin is not a terminal, so without --yes the command is blocked.
	res := runProbe(t, nil, func(rt *runtime) error {
		_, err := confirm(rt, "Proceed?")
		return err
	})
	var ce *CLIError
	if !errors.As(res.Err, &ce) || ce.Code != ExitBlocked {
		t.Fatalf("err = %v, want ExitBlocked", res.Err)
	}
	res = runProbe(t, []string{"--yes"}, func(rt *runtime) error {
		ok, err := confirm(rt, "Proceed?")
		if err != nil || !ok {
			t.Errorf("confirm = %v, %v", ok, err)
		}
		return nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	res = runProbe(t, []string{"--auto-approve"}, func(rt *runtime) error {
		if ok, _ := confirm(rt, "Proceed?"); !ok {
			t.Error("--auto-approve did not confirm")
		}
		return nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
}
