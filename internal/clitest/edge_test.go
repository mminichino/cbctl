package clitest

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
)

func TestScriptedPrompter_EdgeAnswers(t *testing.T) {
	p := NewScriptedPrompter(t)
	p.Push("", "NO", "maybe", "", "0", "9", "zzz")
	if got := p.Remaining(); got != 7 {
		t.Fatalf("Remaining = %d, want 7", got)
	}

	if ok, err := p.Confirm("default?", true); err != nil || !ok {
		t.Fatalf("empty confirm = %v, %v; want default true", ok, err)
	}
	if ok, err := p.Confirm("no?", true); err != nil || ok {
		t.Fatalf("NO confirm = %v, %v; want false", ok, err)
	}
	if _, err := p.Confirm("bad?", false); err == nil || !strings.Contains(err.Error(), `"maybe"`) {
		t.Fatalf("bad confirm err = %v", err)
	}
	if got, err := p.Ask("name", "fallback"); err != nil || got != "fallback" {
		t.Fatalf("empty ask = %q, %v; want fallback", got, err)
	}
	opts := []string{"a", "b"}
	if idx, err := p.Select("zero", opts); err != nil || idx != 0 {
		t.Fatalf("select 0 = %d, %v; want 0 (0-based)", idx, err)
	}
	if _, err := p.Select("range", opts); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("select 9 err = %v", err)
	}
	if _, err := p.Select("unknown", opts); err == nil || !strings.Contains(err.Error(), "not in options") {
		t.Fatalf("select zzz err = %v", err)
	}
	if got := p.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}
	if ok, err := func() (bool, error) { p.Push("other"); return p.Typed("type it", "expected") }(); err != nil || ok {
		t.Fatalf("Typed mismatch = %v, %v; want false", ok, err)
	}
}

func TestResult_LinesAndJSON(t *testing.T) {
	if got := (Result{}).Lines(); got != nil {
		t.Fatalf("empty Lines = %q, want nil", got)
	}
	got := Result{Stdout: "a\nb\n"}.Lines()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Lines = %q", got)
	}
	v := Result{Stdout: `{"k":[1,2]}`}.JSON(t)
	if m, ok := v.(map[string]any); !ok || len(m["k"].([]any)) != 2 {
		t.Fatalf("JSON = %#v", v)
	}

	var msg string
	rt := &countingTB{TB: t, fatalf: func(format string, args ...any) {
		msg = fmt.Sprintf(format, args...)
		panic("fatal")
	}}
	func() {
		defer func() { _ = recover() }()
		Result{Stdout: "not json"}.JSON(rt)
	}()
	if !strings.Contains(msg, "stdout is not JSON") {
		t.Fatalf("JSON fatal = %q", msg)
	}
}

func TestForbiddenStdin(t *testing.T) {
	rt := &countingTB{TB: t}
	r := ForbiddenStdin(rt, "plan")
	n, err := r.Read(make([]byte, 4))
	if n != 0 || err != io.EOF {
		t.Fatalf("Read = %d, %v; want 0, EOF", n, err)
	}
	if rt.errs != 1 {
		t.Fatalf("errors = %d, want 1", rt.errs)
	}
}

func TestSameMultiset(t *testing.T) {
	cases := []struct {
		name      string
		got, want []string
		ok        bool
	}{
		{"both empty", nil, nil, true},
		{"length differs", []string{"PUT /a"}, nil, false},
		{"order free", []string{"PUT /a", "POST /b"}, []string{"post /b", "PUT /a"}, true},
		{"method differs", []string{"PUT /a"}, []string{"POST /a"}, false},
		{"glob", []string{"DELETE /v4/x/1"}, []string{"DELETE /v4/x/*"}, true},
		{"glob does not cross slash", []string{"DELETE /v4/x/1/y"}, []string{"DELETE /v4/x/*"}, false},
		{"bad glob", []string{"PUT /a"}, []string{"PUT /[a"}, false},
		{"want without path", []string{"PUT /a"}, []string{"PUT"}, false},
		{"duplicate needs two", []string{"PUT /a", "PUT /b"}, []string{"PUT /a", "PUT /a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameMultiset(tc.got, tc.want); got != tc.ok {
				t.Fatalf("sameMultiset(%q, %q) = %v, want %v", tc.got, tc.want, got, tc.ok)
			}
		})
	}
}

func TestFormatWrites_Empty(t *testing.T) {
	f := capellafake.New(t)
	if got := FormatWrites(f); got != "[]" {
		t.Fatalf("FormatWrites = %q, want []", got)
	}
}

func TestScenario_FilesAndTTY(t *testing.T) {
	s := NewScenario(t)
	path := s.WriteFile("nested/dir/file.txt", "hello")
	if !strings.HasSuffix(path, "file.txt") {
		t.Fatalf("WriteFile path = %q", path)
	}
	if got := s.ReadFile("nested/dir/file.txt"); got != "hello" {
		t.Fatalf("ReadFile = %q", got)
	}
	s.TTY(true)
	r := s.Run("--help")
	if r.Code != 0 || !strings.Contains(r.Stdout, "capella") {
		t.Fatalf("--help = %d %q %q", r.Code, r.Stdout, r.Stderr)
	}

	var msg string
	rt := &countingTB{TB: t, fatalf: func(format string, args ...any) {
		msg = fmt.Sprintf(format, args...)
		panic("fatal")
	}}
	s.T = rt
	func() {
		defer func() { _ = recover() }()
		s.ReadFile("absent.txt")
	}()
	if !strings.Contains(msg, "read absent.txt") {
		t.Fatalf("ReadFile fatal = %q", msg)
	}
}
