package clitest

import (
	"fmt"
	"strings"
	"testing"
)

func TestScriptedPrompter_ConfirmAskSelect(t *testing.T) {
	p := NewScriptedPrompter(t)
	p.Push("y", "hello", "2", "opt-c")

	ok, err := p.Confirm("continue?", false)
	if err != nil || !ok {
		t.Fatalf("Confirm = %v, %v; want true", ok, err)
	}
	got, err := p.Ask("name", "def")
	if err != nil || got != "hello" {
		t.Fatalf("Ask = %q, %v; want hello", got, err)
	}
	idx, err := p.Select("pick", []string{"a", "b", "c"})
	if err != nil || idx != 1 {
		t.Fatalf("Select 2 = %d, %v; want 1", idx, err)
	}
	idx, err = p.Select("pick", []string{"opt-a", "opt-b", "opt-c"})
	if err != nil || idx != 2 {
		t.Fatalf("Select opt-c = %d, %v; want 2", idx, err)
	}
}

func TestScriptedPrompter_TypedAndSecret(t *testing.T) {
	p := NewScriptedPrompter(t)
	p.Push("proj-1", "s3cret-value")

	ok, err := p.Typed("type project name", "proj-1")
	if err != nil || !ok {
		t.Fatalf("Typed match = %v, %v", ok, err)
	}
	sec, err := p.Secret("api key")
	if err != nil || sec != "s3cret-value" {
		t.Fatalf("Secret = %q, %v", sec, err)
	}
}

func TestScriptedPrompter_UnexpectedPromptFatals(t *testing.T) {
	var sawFatal bool
	var msg string
	fake := &recordingTB{TB: t, fatalf: func(format string, args ...any) {
		sawFatal = true
		msg = fmt.Sprintf(format, args...)
		panic("fatal")
	}}
	p := &ScriptedPrompter{t: fake}
	func() {
		defer func() { _ = recover() }()
		_, _ = p.Confirm("really delete?", false)
	}()
	if !sawFatal {
		t.Fatal("expected fatal on unexpected prompt")
	}
	if !strings.Contains(msg, "really delete?") {
		t.Fatalf("fatal message %q should include the question", msg)
	}
}

func TestScriptedPrompter_UnusedAnswersFailCleanup(t *testing.T) {
	var reported string
	fake := &recordingTB{TB: t, errorf: func(format string, args ...any) {
		reported = fmt.Sprintf(format, args...)
	}}
	p := &ScriptedPrompter{t: fake}
	p.Push("leftover-1", "leftover-2")
	p.checkUnused()
	if !strings.Contains(reported, "unused scripted answers") || !strings.Contains(reported, "leftover-1") {
		t.Fatalf("cleanup error = %q, want unused answers message", reported)
	}
}

// recordingTB intercepts Fatalf/Errorf for ScriptedPrompter behaviour tests.
type recordingTB struct {
	testing.TB
	fatalf func(format string, args ...any)
	errorf func(format string, args ...any)
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	if r.fatalf != nil {
		r.fatalf(format, args...)
		return
	}
	r.TB.Fatalf(format, args...)
}

func (r *recordingTB) Errorf(format string, args ...any) {
	if r.errorf != nil {
		r.errorf(format, args...)
		return
	}
	r.TB.Errorf(format, args...)
}
