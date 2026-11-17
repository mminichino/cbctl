package output

import (
	"bytes"
	"io"
	"testing"
)

// fakeTerm is a Decider.IsTerm that knows which writers are terminals.
func fakeTerm(terms ...io.Writer) func(any) bool {
	return func(w any) bool {
		for _, t := range terms {
			if w == any(t) {
				return true
			}
		}
		return false
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseColorMode(t *testing.T) {
	tests := []struct {
		in      string
		want    ColorMode
		wantErr bool
	}{
		{"auto", ColorAuto, false},
		{"", ColorAuto, false},
		{"always", ColorAlways, false},
		{"never", ColorNever, false},
		{" ALWAYS ", ColorAlways, false},
		{"Never", ColorNever, false},
		{"bogus", ColorAuto, true},
		{"yes", ColorAuto, true},
		{"1", ColorAuto, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseColorMode(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseColorMode(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("ParseColorMode(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if err != nil && err.Error() != `--color must be auto, always or never (got "`+tc.in+`")` {
				t.Fatalf("error text = %q", err)
			}
		})
	}
}

func TestColorModeString(t *testing.T) {
	for m, want := range map[ColorMode]string{ColorAuto: "auto", ColorAlways: "always", ColorNever: "never"} {
		if got := m.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", m, got, want)
		}
	}
}

// TestDeciderPrecedence covers every row of the precedence table: flag, NO_COLOR, TERM=dumb,
// CLICOLOR_FORCE / FORCE_COLOR, CLICOLOR and the terminal check.
func TestDeciderPrecedence(t *testing.T) {
	tests := []struct {
		name string
		mode ColorMode
		env  map[string]string
		tty  bool
		want bool
	}{
		{"auto tty", ColorAuto, nil, true, true},
		{"auto pipe", ColorAuto, nil, false, false},
		{"never tty", ColorNever, nil, true, false},
		{"never beats CLICOLOR_FORCE", ColorNever, map[string]string{"CLICOLOR_FORCE": "1"}, true, false},
		{"never beats FORCE_COLOR", ColorNever, map[string]string{"FORCE_COLOR": "1"}, false, false},
		{"always pipe", ColorAlways, nil, false, true},
		{"always beats NO_COLOR", ColorAlways, map[string]string{"NO_COLOR": "1"}, false, true},
		{"always beats TERM=dumb", ColorAlways, map[string]string{"TERM": "dumb"}, false, true},
		{"always beats CLICOLOR=0", ColorAlways, map[string]string{"CLICOLOR": "0"}, false, true},
		{"NO_COLOR tty", ColorAuto, map[string]string{"NO_COLOR": "1"}, true, false},
		{"NO_COLOR beats CLICOLOR_FORCE", ColorAuto, map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, true, false},
		{"NO_COLOR beats FORCE_COLOR", ColorAuto, map[string]string{"NO_COLOR": "yes", "FORCE_COLOR": "1"}, false, false},
		{"empty NO_COLOR does not disable", ColorAuto, map[string]string{"NO_COLOR": ""}, true, true},
		{"TERM=dumb tty", ColorAuto, map[string]string{"TERM": "dumb"}, true, false},
		{"TERM=dumb beats CLICOLOR_FORCE", ColorAuto, map[string]string{"TERM": "dumb", "CLICOLOR_FORCE": "1"}, true, false},
		{"TERM=xterm tty", ColorAuto, map[string]string{"TERM": "xterm-256color"}, true, true},
		{"CLICOLOR_FORCE pipe", ColorAuto, map[string]string{"CLICOLOR_FORCE": "1"}, false, true},
		{"CLICOLOR_FORCE=0 pipe", ColorAuto, map[string]string{"CLICOLOR_FORCE": "0"}, false, false},
		{"CLICOLOR_FORCE=0 tty", ColorAuto, map[string]string{"CLICOLOR_FORCE": "0"}, true, true},
		{"CLICOLOR_FORCE beats CLICOLOR=0", ColorAuto, map[string]string{"CLICOLOR_FORCE": "1", "CLICOLOR": "0"}, false, true},
		{"FORCE_COLOR pipe", ColorAuto, map[string]string{"FORCE_COLOR": "1"}, false, true},
		{"FORCE_COLOR=true pipe", ColorAuto, map[string]string{"FORCE_COLOR": "true"}, false, true},
		{"FORCE_COLOR=0 tty is off", ColorAuto, map[string]string{"FORCE_COLOR": "0"}, true, false},
		{"FORCE_COLOR=0 pipe", ColorAuto, map[string]string{"FORCE_COLOR": "0"}, false, false},
		{"FORCE_COLOR beats CLICOLOR=0", ColorAuto, map[string]string{"FORCE_COLOR": "1", "CLICOLOR": "0"}, false, true},
		{"CLICOLOR=0 tty", ColorAuto, map[string]string{"CLICOLOR": "0"}, true, false},
		{"CLICOLOR=1 tty", ColorAuto, map[string]string{"CLICOLOR": "1"}, true, true},
		{"CLICOLOR=1 pipe", ColorAuto, map[string]string{"CLICOLOR": "1"}, false, false},
		{"CI does not matter", ColorAuto, map[string]string{"CI": "true"}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &bytes.Buffer{}
			d := Decider{Mode: tc.mode, Env: envOf(tc.env)}
			if tc.tty {
				d.IsTerm = fakeTerm(w)
			}
			if got := d.Color(w); got != tc.want {
				t.Errorf("Color = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeciderZeroValue(t *testing.T) {
	if (Decider{}).Color(&bytes.Buffer{}) {
		t.Error("zero Decider colored a buffer")
	}
	if !(Decider{Mode: ColorAlways}).Color(&bytes.Buffer{}) {
		t.Error("ColorAlways without Env did not color")
	}
}

// TestDeciderStreamsIndependent: stdout and stderr are decided separately, in both directions.
func TestDeciderStreamsIndependent(t *testing.T) {
	tests := []struct {
		name             string
		outTTY, errTTY   bool
		wantOut, wantErr bool
	}{
		{"stdout piped, stderr tty", false, true, false, true},
		{"stdout tty, stderr piped", true, false, true, false},
		{"both tty", true, true, true, true},
		{"neither", false, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errW bytes.Buffer
			var terms []io.Writer
			if tc.outTTY {
				terms = append(terms, &out)
			}
			if tc.errTTY {
				terms = append(terms, &errW)
			}
			d := Decider{Env: envOf(nil), IsTerm: fakeTerm(terms...)}
			if got := d.Color(&out); got != tc.wantOut {
				t.Errorf("stdout color = %v, want %v", got, tc.wantOut)
			}
			if got := d.Color(&errW); got != tc.wantErr {
				t.Errorf("stderr color = %v, want %v", got, tc.wantErr)
			}

			// The same through a Printer: each label follows its own stream.
			p := New(&out, &errW, d)
			p.OK("a")
			p.Error("b")
			wantOK, wantErrLine := "[ok] - a\n", "[error] - b\n"
			if tc.wantOut {
				wantOK = "\x1b[1;32m[ok]\x1b[0m - a\n"
			}
			if tc.wantErr {
				wantErrLine = "\x1b[1;31m[error]\x1b[0m - b\n"
			}
			if out.String() != wantOK || errW.String() != wantErrLine {
				t.Errorf("stdout=%q stderr=%q", out.String(), errW.String())
			}
		})
	}
}

func TestIsTerminalNonFile(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) || IsTerminal(nil) {
		t.Error("a buffer or nil is not a terminal")
	}
}
