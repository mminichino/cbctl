package clitest

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ScriptedPrompter answers interactive prompts from a FIFO queue. An unexpected
// prompt (empty queue) fatals the test with the question text. Unused answers
// left at cleanup fail the test.
type ScriptedPrompter struct {
	t    testing.TB
	mu   sync.Mutex
	q    []string
	used int
}

// NewScriptedPrompter returns an empty prompter bound to t.
func NewScriptedPrompter(t testing.TB) *ScriptedPrompter {
	t.Helper()
	p := &ScriptedPrompter{t: t}
	t.Cleanup(p.checkUnused)
	return p
}

// Push appends answers to the queue.
func (p *ScriptedPrompter) Push(answers ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.q = append(p.q, answers...)
}

// Remaining reports how many answers have not been consumed.
func (p *ScriptedPrompter) Remaining() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.q)
}

func (p *ScriptedPrompter) next(kind, question string) string {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.q) == 0 {
		p.t.Fatalf("unexpected %s prompt: %s", kind, question)
	}
	a := p.q[0]
	p.q = p.q[1:]
	p.used++
	return a
}

func (p *ScriptedPrompter) checkUnused() {
	p.mu.Lock()
	left := append([]string(nil), p.q...)
	p.mu.Unlock()
	if len(left) > 0 {
		p.t.Errorf("unused scripted answers (%d): %q", len(left), left)
	}
}

// Confirm implements cli.Prompter. Answers "y"/"yes" are true; "n"/"no" are false
// (case-insensitive). Any other non-empty answer fatals.
func (p *ScriptedPrompter) Confirm(question string, def bool) (bool, error) {
	p.t.Helper()
	a := strings.TrimSpace(p.next("confirm", question))
	if a == "" {
		return def, nil
	}
	switch strings.ToLower(a) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("scripted confirm answer %q (want y/n)", a)
	}
}

// Typed implements cli.Prompter. Returns whether the scripted answer equals expected.
func (p *ScriptedPrompter) Typed(prompt, expected string) (bool, error) {
	p.t.Helper()
	a := p.next("typed", prompt)
	return a == expected, nil
}

// Ask implements cli.Prompter. An empty answer yields def.
func (p *ScriptedPrompter) Ask(question, def string) (string, error) {
	p.t.Helper()
	a := p.next("ask", question)
	if a == "" {
		return def, nil
	}
	return a, nil
}

// Select implements cli.Prompter. The answer is a 1-based index, a 0-based index,
// or an exact option string.
func (p *ScriptedPrompter) Select(question string, options []string) (int, error) {
	p.t.Helper()
	a := strings.TrimSpace(p.next("select", question))
	if n, err := strconv.Atoi(a); err == nil {
		if n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		if n >= 0 && n < len(options) {
			return n, nil
		}
		return 0, fmt.Errorf("scripted select index %d out of range for %d options", n, len(options))
	}
	for i, opt := range options {
		if opt == a {
			return i, nil
		}
	}
	return 0, fmt.Errorf("scripted select answer %q not in options %q", a, options)
}

// Secret implements cli.Prompter.
func (p *ScriptedPrompter) Secret(question string) (string, error) {
	p.t.Helper()
	return p.next("secret", question), nil
}
