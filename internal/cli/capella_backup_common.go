package cli

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/plan"
)

// Capella documents backup statuses as free text; these are the ones it uses for done and failed.
var (
	backupDone   = map[string]bool{"complete": true, "completed": true, "succeeded": true, "success": true, "done": true, "ready": true}
	backupFailed = map[string]bool{"failed": true, "error": true, "cancelled": true, "canceled": true, "expired": true}
)

const freeNoBackups = "free-tier clusters have no backups"

// refuseFree fails with ExitBlocked for free-tier clusters, which have no backups.
func refuseFree(t *clusterTarget) error {
	if t.freeTier() {
		return blockedError("create a provisioned cluster for data that needs backups ('cbctl capella cluster create')", "%s", freeNoBackups)
	}
	return nil
}

// locateBackupCluster resolves a cluster and refuses free-tier ones.
func (rt *runtime) locateBackupCluster(name, project string) (*clusterTarget, error) {
	t, err := rt.locateCluster(name, project)
	if err != nil {
		return nil, err
	}
	return t, refuseFree(t)
}

var hoursPattern = regexp.MustCompile(`^\s*(\d+)\s*([hd]?)\s*$`)

// parseHours reads "168", "168h" or "7d" as hours.
func parseHours(text, flag string) (int, error) {
	m := hoursPattern.FindStringSubmatch(strings.ToLower(text))
	if m != nil {
		if n, _ := strconv.Atoi(m[1]); n > 0 {
			if m[2] == "d" {
				n *= 24
			}
			return n, nil
		}
	}
	return 0, usageError("give a number of hours, or days with d, for example 168, 168h or 7d",
		"%s takes hours or days, for example 168, 168h or 7d (got %q)", flag, text)
}

// hoursText renders 720 as "30 days" and 36 as "36 h".
func hoursText(h int) string {
	switch {
	case h == 24:
		return "1 day"
	case h != 0 && h%24 == 0:
		return fmt.Sprintf("%d days", h/24)
	}
	return fmt.Sprintf("%d h", h)
}

// when renders an RFC 3339 time as "2026-10-28 02:00" in UTC; "" when absent.
func when(v any) string {
	s, _ := v.(string)
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// sizeText renders a byte count as "42 GB".
func sizeText(v any) string {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int:
		n = float64(x)
	default:
		return ""
	}
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if n < 1024 {
			if unit == "B" || n >= 10 {
				return fmt.Sprintf("%.0f %s", n, unit)
			}
			return fmt.Sprintf("%.1f %s", n, unit)
		}
		n /= 1024
	}
	return fmt.Sprintf("%.1f TB", n)
}

// strOf reads a string field of a free-form Capella object.
func strOf(o capellaapi.Object, k string) string {
	s, _ := o[k].(string)
	return s
}

// nestedStr reads o[outer][inner] as a string.
func nestedStr(o capellaapi.Object, outer, inner string) string {
	m, _ := o[outer].(map[string]any)
	s, _ := m[inner].(string)
	return s
}

// statusProgress renders a wait on pw like a cluster wait in apply: "  LABEL: STATE (time) " with a
// dot every 30 s of waiting on the same line, and a final state (final reports which) on a line of
// its own.
func statusProgress(pw *output.Progress, label string, final func(state string) bool) engine.WaitHooks {
	return engine.WaitHooks{
		OnState: func(state string, elapsed time.Duration) {
			line := fmt.Sprintf("  %s: %s (%s)", label, state, plan.FormatDuration(elapsed))
			if final(state) {
				pw.Line(line)
				return
			}
			pw.Inline(line+" ", true)
		},
		OnTick: func() { pw.Inline(".", false) },
	}
}

// waitBackupStatus waits for a backup, export or restore with the global --timeout, showing its
// status on stderr with a dot every 30 seconds.
func (rt *runtime) waitBackupStatus(label string, done, failed map[string]bool, read func() (string, error)) (string, error) {
	pw := output.NewProgress(rt.errOut(), rt.Quiet, ttyCheck(rt.errOut()))
	hooks := statusProgress(pw, label, func(s string) bool {
		s = strings.ToLower(s)
		return done[s] || failed[s]
	})
	state, err := engine.WaitForStatus(rt.Ctx, func(context.Context) (string, error) { return read() }, done, failed, rt.Timeout, rt.Clock, label, hooks)
	pw.Close()
	if err != nil && rt.Ctx.Err() != nil {
		writeLabeled(rt.errOut(), output.KindInfo, "the operation continues in Capella")
	}
	return state, err
}
