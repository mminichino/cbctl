package plan

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
)

func TestRunContextHelpers(t *testing.T) {
	rc := NewRunContext(nil, "cfg")
	if rc.Config != "cfg" || rc.Timeout != DefaultTimeout || rc.Clock == nil {
		t.Fatalf("%+v", rc)
	}
	if _, ok := rc.Clock.(engine.RealClock); !ok {
		t.Fatal("default clock is the real clock")
	}
	// Nil callbacks are safe.
	rc.Say("x")
	rc.Inline("x", true)
	rc.Warning("a", "b")
	if rc.Getenv("X") != "" {
		t.Fatal("nil Env")
	}

	var said, inline, what, action string
	var started bool
	rc.Progress = func(s string) { said = s }
	rc.ProgressInline = func(s string, start bool) { inline, started = s, start }
	rc.Warn = func(w, a string) { what, action = w, a }
	rc.Env = func(n string) string { return "v-" + n }
	rc.Say("hello")
	rc.Inline("dots", true)
	rc.Warning("w", "a")
	if said != "hello" || inline != "dots" || !started || what != "w" || action != "a" || rc.Getenv("X") != "v-X" {
		t.Fatal("callbacks not invoked")
	}
}

func TestErrorTypes(t *testing.T) {
	if (&PlanError{Msg: "cluster x not found"}).Error() != "cluster x not found" {
		t.Fatal("PlanError")
	}
	if (&TargetError{Msg: "bad"}).Error() != "bad" {
		t.Fatal("TargetError")
	}
	if (&ConcurrentChange{Msg: "c"}).Error() != "c" {
		t.Fatal("ConcurrentChange")
	}
}

func TestTruthy(t *testing.T) {
	for v, want := range map[string]bool{"": false, "x": true} {
		if truthy(v) != want {
			t.Errorf("%q", v)
		}
	}
	for _, tc := range []struct {
		v    any
		want bool
	}{{nil, false}, {true, true}, {false, false}, {int64(0), false}, {int64(2), true}, {0.5, true}, {0.0, false}, {[]any{}, true}} {
		if truthy(tc.v) != tc.want {
			t.Errorf("%v", tc.v)
		}
	}
	if _, ok := toFloat(time.Second); ok {
		t.Fatal("not a number")
	}
}

// The engine core must not depend on the reconcilers or the CLI.
func TestImportGraph(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"/internal/resources", "/internal/cli", "/internal/importer"} {
			if strings.HasSuffix(dep, banned) {
				t.Errorf("plan must not import %s", dep)
			}
		}
	}
}
