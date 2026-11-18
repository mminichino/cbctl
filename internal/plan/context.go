package plan

import (
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/output"
)

// Prompter asks the user to confirm destructive steps during apply --prune. A nil Prompter
// means "refuse", which is what tests and non-interactive runs get.
type Prompter interface {
	// ConfirmTyped shows prompt and returns true only if the user types expected.
	ConfirmTyped(prompt, expected string) (bool, error)
}

// RunContext is what the planner, executor and reconcilers share for one command run.
type RunContext struct {
	Client *capellaapi.Client
	// Config is the loaded capella.yaml (a *capellafile.Config). It is held as any because this
	// package must not depend on the file package; BuildPlan hands it to the root reconciler as
	// the parent desired value, and reconcilers type-assert it.
	Config  any
	Clock   engine.Clock
	Timeout time.Duration
	NoWait  bool

	// Glyphs spells the symbols of progress lines. The zero value means Unicode.
	Glyphs output.Glyphs

	// Progress receives one line of progress text. Nil discards it.
	Progress func(string)
	// ProgressInline writes without ending the line (a state line plus a dot per 30 s of waiting).
	ProgressInline func(text string, start bool)
	// Warn reports (what happened, what to do). Nil discards it.
	Warn func(what, action string)

	RotatePasswords          bool
	PowerOn                  bool
	AllowServiceRemoval      bool
	AllowServiceGroupRemoval bool
	SkipCapacityCheck        bool

	// Ephemeral marks a run whose configuration exists only in memory (cbctl apply -f). Nothing
	// in the engine may depend on a file: reconcilers never rotate a credential password, and
	// callers skip id pinning and backup files.
	Ephemeral bool

	// OnGeneratedPassword, when set, receives the cluster name, credential name and password of
	// each credential Capella generated a password for, once the password is saved. The manifest
	// apply prints it once from here.
	OnGeneratedPassword func(cluster, name, password string)

	Env    func(string) string
	Prompt Prompter
}

// DefaultTimeout is the wait budget when none is configured.
const DefaultTimeout = 60 * time.Minute

// NewRunContext returns a context with the real clock and the default timeout.
func NewRunContext(client *capellaapi.Client, cfg any) *RunContext {
	return &RunContext{Client: client, Config: cfg, Clock: engine.RealClock{}, Timeout: DefaultTimeout}
}

// glyphs is rc.Glyphs, or the Unicode set when none was given.
func (rc *RunContext) glyphs() output.Glyphs {
	if rc.Glyphs.Arrow == "" {
		return output.UnicodeGlyphs
	}
	return rc.Glyphs
}

// Say sends one progress line.
func (rc *RunContext) Say(msg string) {
	if rc.Progress != nil {
		rc.Progress(msg)
	}
}

// Inline writes progress text without a newline when the caller supplied ProgressInline.
func (rc *RunContext) Inline(text string, start bool) {
	if rc.ProgressInline != nil {
		rc.ProgressInline(text, start)
	}
}

// Warning reports a notice with its next step.
func (rc *RunContext) Warning(what, action string) {
	if rc.Warn != nil {
		rc.Warn(what, action)
	}
}

// Getenv reads an environment variable through the injected lookup.
func (rc *RunContext) Getenv(name string) string {
	if rc.Env == nil {
		return ""
	}
	return rc.Env(name)
}
