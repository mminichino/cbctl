package output

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// golden compares got with testdata/golden/name, or rewrites the file with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run 'go test ./internal/output -update')", err)
	}
	if string(want) != got {
		t.Errorf("%s differs\n got: %q\nwant: %q", name, got, string(want))
	}
}

func newTestPrinter(mode ColorMode, opts ...Option) (p *Printer, out, errW *bytes.Buffer) {
	out, errW = &bytes.Buffer{}, &bytes.Buffer{}
	d := Decider{Mode: mode, Env: envOf(nil)}
	opts = append([]Option{WithLevel(LevelDebug)}, opts...)
	return New(out, errW, d, opts...), out, errW
}

// TestLevelGoldens: exact bytes for every level, color on and off, on the stream it belongs to.
func TestLevelGoldens(t *testing.T) {
	levels := []struct {
		name   string
		write  func(p *Printer)
		stream string // "out" or "err"
	}{
		{"ok", func(p *Printer) { p.OK("Cluster created on %s", "10.0.0.1") }, "out"},
		{"info", func(p *Printer) { p.Info("Cluster already configured") }, "out"},
		{"warning", func(p *Printer) { p.Warn("Collection not empty") }, "err"},
		{"error", func(p *Printer) { p.Error("could not connect to %s", "10.0.0.1") }, "err"},
		{"debug", func(p *Printer) { p.Debug("GET /v4/organizations -> 200") }, "err"},
		{"action", func(p *Printer) { p.Action("Run the command again.") }, "err"},
		{"notice", func(p *Printer) {
			p.Notice("cluster has no backup", "Run 'cbctl capella cluster backup schedule set'.")
		}, "err"},
		{"multiline", func(p *Printer) { p.Error("first\nsecond") }, "err"},
	}
	for _, lv := range levels {
		for _, variant := range []struct {
			suffix string
			mode   ColorMode
		}{{"color", ColorAlways}, {"plain", ColorNever}} {
			t.Run(lv.name+"_"+variant.suffix, func(t *testing.T) {
				p, out, errW := newTestPrinter(variant.mode)
				lv.write(p)
				got, other := out.String(), errW.String()
				if lv.stream == "err" {
					got, other = other, got
				}
				if other != "" {
					t.Errorf("unexpected output on the other stream: %q", other)
				}
				golden(t, lv.name+"_"+variant.suffix+".txt", got)
			})
		}
	}
}

// TestLabelBytes pins the escape sequence of every kind and that each styled label is reset.
func TestLabelBytes(t *testing.T) {
	d := Decider{Mode: ColorAlways}
	tests := []struct{ kind, want string }{
		{KindOK, "\x1b[1;32m[ok]\x1b[0m"},
		{KindInfo, "[info]"},
		{KindWarning, "\x1b[1;33m[warning]\x1b[0m"},
		{KindError, "\x1b[1;31m[error]\x1b[0m"},
		{KindDebug, "\x1b[2m[debug]\x1b[0m"},
		{"custom", "[custom]"},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			if got := Label(d, &bytes.Buffer{}, tc.kind); got != tc.want {
				t.Errorf("Label = %q, want %q", got, tc.want)
			}
			plain := Label(Decider{Mode: ColorNever}, &bytes.Buffer{}, tc.kind)
			if plain != "["+tc.kind+"]" {
				t.Errorf("plain Label = %q", plain)
			}
		})
	}
}

func TestPrinterStreamsAndLevels(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantOut string
		wantErr string
	}{
		{"default info", []Option{WithLevel(LevelInfo)}, "[ok] - o\n[info] - i\nd1\n", "[warning] - w\n[error] - e\n  -> a\n"},
		{"json moves ok/info to stderr", []Option{WithJSON(true)}, "d1\n", "[ok] - o\n[info] - i\n[debug] - dbg\n[warning] - w\n[error] - e\n  -> a\n"},
		{"quiet hides ok/info/debug", []Option{WithQuiet(true)}, "d1\n", "[warning] - w\n[error] - e\n  -> a\n"},
		{"error level keeps only errors", []Option{WithLevel(LevelError)}, "d1\n", "[error] - e\n  -> a\n"},
		{"warn level", []Option{WithLevel(LevelWarn)}, "d1\n", "[warning] - w\n[error] - e\n  -> a\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, out, errW := newTestPrinter(ColorNever, tc.opts...)
			p.OK("o")
			p.Info("i")
			p.Data("d%d", 1)
			p.Debug("dbg")
			p.Warn("w")
			p.Error("e")
			p.Action("a")
			if out.String() != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out.String(), tc.wantOut)
			}
			// The ASCII arrow is used because color is off (GlyphsByColor).
			if errW.String() != tc.wantErr {
				t.Errorf("stderr = %q, want %q", errW.String(), tc.wantErr)
			}
		})
	}
}

func TestPrinterRedactsBeforeLabel(t *testing.T) {
	const secret = "s3cr3t-output-test-value"
	redact.Register(secret)
	t.Cleanup(redact.Reset)
	p, out, errW := newTestPrinter(ColorAlways)
	p.OK("token %s", secret)
	p.Error("token %s", secret)
	p.Action("use " + secret)
	p.Data("%s", secret)
	for _, s := range []string{out.String(), errW.String()} {
		if strings.Contains(s, secret) {
			t.Errorf("secret leaked: %q", s)
		}
	}
}

func TestSettersAndAccessors(t *testing.T) {
	p, _, _ := newTestPrinter(ColorNever)
	var a, b bytes.Buffer
	p.SetOutputs(&a, &b)
	p.SetOutputs(nil, nil) // nil keeps the writers
	gotOut, gotErr := p.Outputs()
	if gotOut != io.Writer(&a) || gotErr != io.Writer(&b) {
		t.Error("Outputs did not return the writers set")
	}
	p.SetLevel(LevelWarn)
	if p.Level() != LevelWarn {
		t.Error("Level not kept")
	}
	p.SetDecider(Decider{Mode: ColorAlways})
	if p.Decider().Mode != ColorAlways {
		t.Error("Decider not kept")
	}
	p.SetQuiet(true)
	p.SetJSON(true)
	p.Warn("w")
	if b.String() != "\x1b[1;33m[warning]\x1b[0m - w\n" {
		t.Errorf("stderr = %q", b.String())
	}
}

// TestConcurrentLinesStayWhole: many goroutines write; every line arrives whole (run with -race).
func TestConcurrentLinesStayWhole(t *testing.T) {
	p, out, errW := newTestPrinter(ColorAlways)
	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.OK("line from goroutine %03d", i)
			p.Error("error from goroutine %03d", i)
			p.Progress().Line("progress line")
		}()
	}
	wg.Wait()
	check := func(name, s string, want int, prefix string) {
		lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
		if len(lines) != want {
			t.Errorf("%s: %d lines, want %d", name, len(lines), want)
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, prefix) && l != "progress line" {
				t.Errorf("%s: torn line %q", name, l)
			}
			if strings.HasPrefix(l, "\x1b") && !strings.Contains(l, "\x1b[0m - ") {
				t.Errorf("%s: unterminated label %q", name, l)
			}
		}
	}
	check("stdout", out.String(), n, "\x1b[1;32m[ok]\x1b[0m - line from goroutine ")
	check("stderr", errW.String(), 2*n, "\x1b[1;31m[error]\x1b[0m - error from goroutine ")
}
