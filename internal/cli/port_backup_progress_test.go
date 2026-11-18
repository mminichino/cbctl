package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/plan"
)

// runBackupWait waits on a status that changes at the given offsets, rendering progress like
// waitBackupStatus on a terminal. It returns stderr, when the ticks landed, and the wait error.
func runBackupWait(t *testing.T, statuses []struct {
	at    time.Duration
	state string
}) (string, []time.Duration, error) {
	t.Helper()
	clk := engine.NewFakeClock(time.Time{})
	start := clk.Monotonic()
	var buf bytes.Buffer
	pw := output.NewProgress(&buf, false, true)
	done := map[string]bool{"complete": true, "ready": true}
	failed := map[string]bool{"failed": true}
	hooks := statusProgress(pw, "backup of orders", func(s string) bool { return done[s] || failed[s] })
	var ticks []time.Duration
	tick := hooks.OnTick
	hooks.OnTick = func() {
		ticks = append(ticks, clk.Monotonic()-start)
		tick()
	}
	read := func(context.Context) (string, error) {
		elapsed := clk.Monotonic() - start
		state := statuses[0].state
		for _, s := range statuses {
			if s.at <= elapsed {
				state = s.state
			}
		}
		return state, nil
	}
	_, err := engine.WaitForStatus(context.Background(), read, done, failed, time.Hour, clk, "backup of orders", hooks)
	pw.Close()
	return buf.String(), ticks, err
}

// TestBackupProgress_CAP057_AC8_DotsEvery30sOnOneLine: each state starts a line with its elapsed
// time, a dot follows every 30 s on that line, and the final state ends on a line of its own.
func TestBackupProgress_CAP057_AC8_DotsEvery30sOnOneLine(t *testing.T) {
	out, ticks, err := runBackupWait(t, []struct {
		at    time.Duration
		state string
	}{{0, "queued"}, {20 * time.Second, "running"}, {100 * time.Second, "complete"}})
	if err != nil {
		t.Fatal(err)
	}
	for i, tk := range ticks {
		if want := time.Duration(i+1) * 30 * time.Second; tk != want {
			t.Errorf("tick %d at %s, want %s", i, tk, want)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.HasPrefix(lines[0], "  backup of orders: queued (0s) ") {
		t.Errorf("line 1 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  backup of orders: running (") {
		t.Errorf("line 2 = %q", lines[1])
	}
	dots := strings.Count(lines[0], ".") + strings.Count(lines[1], ".")
	if dots != len(ticks) || len(ticks) < 3 {
		t.Errorf("dots = %d, ticks = %d:\n%s", dots, len(ticks), out)
	}
	if !strings.HasPrefix(lines[2], "  backup of orders: complete (") || strings.Contains(lines[2], ".") {
		t.Errorf("line 3 = %q", lines[2])
	}
}

// TestBackupProgress_CAP059_AC8_FailedEndsTheLine: a failed wait closes the dotted line and prints
// the failed state on its own line.
func TestBackupProgress_CAP059_AC8_FailedEndsTheLine(t *testing.T) {
	out, _, err := runBackupWait(t, []struct {
		at    time.Duration
		state string
	}{{0, "pending"}, {40 * time.Second, "failed"}})
	var sf *engine.StatusFailed
	if !errors.As(err, &sf) {
		t.Fatalf("err = %v, want StatusFailed", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "  backup of orders: pending (0s) .") ||
		!strings.HasPrefix(lines[1], "  backup of orders: failed (") {
		t.Errorf("lines = %q", lines)
	}
}

// TestWaiter_CAP007_AC9_EachStateOnItsOwnLine: two in-progress states in a row each start a line,
// dots continue the open one, and the final state has its own line (the apply cluster wait drives
// the run context's Inline/Say exactly this way). Without a terminal there are no dots.
func TestWaiter_CAP007_AC9_EachStateOnItsOwnLine(t *testing.T) {
	render := func(tty bool) []string {
		var buf bytes.Buffer
		pw := output.NewProgress(&buf, false, tty)
		rc := &plan.RunContext{Progress: pw.Line, ProgressInline: pw.Inline}
		state := func(s string, d time.Duration, final bool) {
			line := "  cluster nova-dev-01: " + s + " (" + plan.FormatDuration(d) + ")"
			if final {
				rc.Say(line)
				return
			}
			rc.Inline(line+" ", true)
		}
		state("deploying", 0, false)
		rc.Inline(".", false)
		state("rebalancing", 400*time.Second, false)
		rc.Inline(".", false)
		state("healthy", 700*time.Second, true)
		return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	}
	want := []string{
		"  cluster nova-dev-01: deploying (0s) .",
		"  cluster nova-dev-01: rebalancing (" + plan.FormatDuration(400*time.Second) + ") .",
		"  cluster nova-dev-01: healthy (" + plan.FormatDuration(700*time.Second) + ")",
	}
	if got := render(true); !reflect.DeepEqual(got, want) {
		t.Errorf("tty:\n got %q\nwant %q", got, want)
	}
	for i := range want {
		want[i] = strings.TrimSuffix(want[i], ".")
	}
	if got := render(false); !reflect.DeepEqual(got, want) {
		t.Errorf("no tty:\n got %q\nwant %q", got, want)
	}
}
