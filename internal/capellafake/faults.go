package capellafake

import (
	"net/http"
	"strings"
	"time"
)

// FaultOptions configures an injected failure (Phase 4 §3.5).
type FaultOptions struct {
	Status  int
	Code    int
	Message string
	Times   int // default 1
	Headers http.Header
	// DropAfterRead closes the connection after reading the request and applies nothing.
	DropAfterRead bool
	// ApplyThenDrop runs the real handler (mutating state) then drops the connection
	// without delivering the response (ambiguous-write case).
	ApplyThenDrop bool
	Delay         time.Duration
	Before        func()
}

// FailNextOpts registers a fault with Capella error fields and optional connection drop.
// pathGlob uses path.Match semantics with "{param}" segments treated as "*".
// Failures are consumed in registration order.
func (f *Fake) FailNextOpts(method, pathGlob string, o FaultOptions) {
	times := o.Times
	if times <= 0 {
		times = 1
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, &failure{
		method:        strings.ToUpper(method),
		glob:          pathGlob,
		status:        o.Status,
		code:          o.Code,
		message:       o.Message,
		headers:       o.Headers.Clone(),
		left:          times,
		dropAfterRead: o.DropAfterRead,
		applyThenDrop: o.ApplyThenDrop,
		delay:         o.Delay,
		before:        o.Before,
	})
}

// hook runs fn before the fake serves a matching request normally.
type hook struct {
	method string
	glob   string
	n      int
	fn     func(n int)
}

// OnRequest calls fn (with the 1-based count of matching requests so far) before each request
// matching method and pathGlob is served normally. Tests use it to cancel a context mid-command
// (Ctrl-C) or to change state between polls. fn runs with the fake locked: it must not call
// the fake's methods.
func (f *Fake) OnRequest(method, pathGlob string, fn func(n int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks = append(f.hooks, &hook{method: strings.ToUpper(method), glob: pathGlob, fn: fn})
}

func (f *Fake) runHooks(method, path string) {
	for _, h := range f.hooks {
		if (h.method == "" || h.method == "*" || h.method == method) && matchPathGlob(h.glob, path) {
			h.n++
			h.fn(h.n)
		}
	}
}
