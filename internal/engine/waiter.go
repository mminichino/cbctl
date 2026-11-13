package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Op is the cluster operation a waiter is waiting on.
type Op string

// Cluster operations understood by WaitForCluster.
const (
	OpCreate   Op = "create"
	OpUpdate   Op = "update"
	OpReady    Op = "ready"
	OpPowerOff Op = "power_off"
	OpPowerOn  Op = "power_on"
	OpDelete   Op = "delete"
)

// Transitional lists the cluster states that mean an operation is still in flight.
var Transitional = map[string]bool{
	"draft": true, "deploying": true, "scaling": true, "upgrading": true, "rebalancing": true,
	"peering": true, "destroying": true, "turningOff": true, "turningOn": true,
}

// Success maps each Op to the states that end its wait successfully. Delete
// has none: success is the cluster disappearing (not found).
var Success = map[Op]map[string]bool{
	OpCreate:   {"healthy": true},
	OpUpdate:   {"healthy": true},
	OpReady:    {"healthy": true},
	OpPowerOff: {"turnedOff": true},
	OpPowerOn:  {"healthy": true},
	OpDelete:   {},
}

// Failure maps each Op to the states that end its wait with a failure.
var Failure = map[Op]map[string]bool{
	OpCreate: {"deploymentFailed": true},
	OpUpdate: {"scaleFailed": true, "rebalanceFailed": true, "upgradeFailed": true},
	OpReady: {
		"deploymentFailed": true, "scaleFailed": true, "rebalanceFailed": true, "upgradeFailed": true,
		"destroyFailed": true, "turningOnFailed": true, "turningOffFailed": true, "peeringFailed": true,
	},
	OpPowerOff: {"turningOffFailed": true},
	OpPowerOn:  {"turningOnFailed": true},
	OpDelete:   {"destroyFailed": true},
}

const (
	firstPoll  = 10 * time.Second
	pollFactor = 1.5
	maxPoll    = 60 * time.Second
	// tickEvery is how much waiting time passes between WaitHooks.OnTick calls.
	tickEvery = 30 * time.Second
)

// ErrNotFound is what a ClusterGetter returns when the cluster does not exist.
// capellaapi imports this package (for Clock), so the dependency cannot point the
// other way; instead capellaapi.ErrNotFound is defined as this very value. An
// *capellaapi.APIError for a 404 therefore satisfies errors.Is against either name.
var ErrNotFound = errors.New("not found")

// ClusterWithState is the slice of a Capella cluster the waiter needs.
type ClusterWithState struct {
	Name         string
	CurrentState string
}

// ClusterGetter reads a cluster. capellaapi.Client.AsClusterGetter adapts a
// *capellaapi.Client to it; tests supply fakes.
type ClusterGetter interface {
	GetCluster(ctx context.Context, projectID, clusterID string) (*ClusterWithState, error)
}

// WaitHooks are optional progress callbacks.
type WaitHooks struct {
	// OnState is called on every state change, including the first poll.
	OnState func(state string, elapsed time.Duration)
	// OnTick is called every 30s of waiting (the CLI prints a dot to stderr).
	OnTick func()
}

// WaitFailed reports a cluster that entered a failure state for the awaited op.
type WaitFailed struct {
	Name, State string
}

func (e *WaitFailed) Error() string {
	return fmt.Sprintf("cluster %s entered %s; check the Capella UI activity log", e.Name, e.State)
}

// WaitTimeout reports a cluster still not in its target state at the deadline.
type WaitTimeout struct {
	Name, State string
	Timeout     time.Duration
}

func (e *WaitTimeout) Error() string {
	return fmt.Sprintf("cluster %s still %s after %s; the operation continues in Capella; re-run to resume waiting",
		e.Name, e.State, formatDuration(e.Timeout))
}

// StatusFailed reports a backup, export or restore that ended in a failure status.
type StatusFailed struct {
	Label, State string
}

func (e *StatusFailed) Error() string {
	return fmt.Sprintf("%s ended %s; check the Capella UI activity log", e.Label, e.State)
}

// StatusTimeout reports a backup, export or restore not finished at the deadline.
type StatusTimeout struct {
	Label, State string
	Timeout      time.Duration
}

func (e *StatusTimeout) Error() string {
	return fmt.Sprintf("%s still %s after %s; it continues in Capella", e.Label, e.State, formatDuration(e.Timeout))
}

// PollIntervals returns the first n sleep intervals between polls:
// 10s, 15s, 22.5s, 33.75s, 50.625s, 60s, 60s, ...
func PollIntervals(n int) []time.Duration {
	out := make([]time.Duration, 0, max(n, 0))
	v := firstPoll
	for range n {
		out = append(out, min(v, maxPoll))
		v = nextInterval(v)
	}
	return out
}

func nextInterval(v time.Duration) time.Duration {
	return min(time.Duration(float64(v)*pollFactor), maxPoll)
}

// formatDuration renders d like Go's Duration.String, rounded to whole
// seconds once it is 10s or more (4m0s, 1h2m3s, 12s, 1.2s).
func formatDuration(d time.Duration) string {
	if d >= 10*time.Second {
		d = d.Round(time.Second)
	}
	return d.String()
}

// WaitForCluster polls the cluster until it reaches op's success state, a
// failure state, or timeout. The first poll is immediate; later polls follow
// PollIntervals and never sleep past the deadline (the final sleep is the
// remaining time, followed by one last poll). For OpDelete, ErrNotFound is
// success and returns "deleted"; for other ops it is returned as an error. name
// labels errors, defaulting to the cluster's reported name, then clusterID.
func WaitForCluster(ctx context.Context, c ClusterGetter, projectID, clusterID string, op Op, timeout time.Duration,
	clk Clock, name string, h WaitHooks) (string, error) {
	success, ok := Success[op]
	if !ok {
		return "", fmt.Errorf("unknown wait operation %q", op)
	}
	failure := Failure[op]

	label := name
	if label == "" {
		label = clusterID
	}
	start := clk.Monotonic()
	nextTick := tickEvery
	interval := firstPoll
	last, polled := "", false
	for {
		cl, err := c.GetCluster(ctx, projectID, clusterID)
		elapsed := clk.Monotonic() - start
		if err != nil {
			if op == OpDelete && errors.Is(err, ErrNotFound) {
				if h.OnState != nil {
					h.OnState("deleted", elapsed)
				}
				return "deleted", nil
			}
			return "", fmt.Errorf("reading cluster %s: %w", label, err)
		}
		state := "unknown"
		if cl != nil {
			if cl.CurrentState != "" {
				state = cl.CurrentState
			}
			if name == "" && cl.Name != "" {
				label = cl.Name
			}
		}
		if !polled || state != last {
			if h.OnState != nil {
				h.OnState(state, elapsed)
			}
			last, polled = state, true
		}
		if success[state] {
			return state, nil
		}
		if failure[state] {
			return state, &WaitFailed{Name: label, State: state}
		}
		if elapsed >= timeout {
			return state, &WaitTimeout{Name: label, State: state, Timeout: timeout}
		}
		wake := elapsed + min(interval, maxPoll, timeout-elapsed)
		if nextTick, err = sleepUntil(ctx, clk, start, wake, nextTick, h.OnTick); err != nil {
			return "", err
		}
		interval = nextInterval(interval)
	}
}

// WaitForStatus polls read (a backup's, export's or restore's status) with the
// cluster waiter's intervals until a done or failed status, or timeout. read
// returning "" means Capella does not list the item yet and counts as
// "queued". Status comparison is case-insensitive; done and failed hold
// lowercase or mixed-case names.
func WaitForStatus(ctx context.Context, read func(context.Context) (string, error), done, failed map[string]bool,
	timeout time.Duration, clk Clock, label string, h WaitHooks) (string, error) {
	doneSet, failedSet := lowerSet(done), lowerSet(failed)
	start := clk.Monotonic()
	nextTick := tickEvery
	interval := firstPoll
	last, polled := "", false
	for {
		state, err := read(ctx)
		if err != nil {
			return "", fmt.Errorf("reading status of %s: %w", label, err)
		}
		if state == "" {
			state = "queued"
		}
		elapsed := clk.Monotonic() - start
		if !polled || state != last {
			if h.OnState != nil {
				h.OnState(state, elapsed)
			}
			last, polled = state, true
		}
		lower := strings.ToLower(state)
		if doneSet[lower] {
			return state, nil
		}
		if failedSet[lower] {
			return state, &StatusFailed{Label: label, State: state}
		}
		if elapsed >= timeout {
			return state, &StatusTimeout{Label: label, State: state, Timeout: timeout}
		}
		wake := elapsed + min(interval, maxPoll, timeout-elapsed)
		if nextTick, err = sleepUntil(ctx, clk, start, wake, nextTick, h.OnTick); err != nil {
			return "", err
		}
		interval = nextInterval(interval)
	}
}

func lowerSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		if v {
			out[strings.ToLower(k)] = true
		}
	}
	return out
}

// sleepUntil sleeps until wake (measured from start), in slices so onTick
// lands every tickEvery of waiting. It returns the next tick time.
func sleepUntil(ctx context.Context, clk Clock, start, wake, nextTick time.Duration, onTick func()) (time.Duration, error) {
	for onTick != nil && nextTick <= wake {
		if d := nextTick - (clk.Monotonic() - start); d > 0 {
			if err := clk.Sleep(ctx, d); err != nil {
				return nextTick, err
			}
		}
		onTick()
		nextTick += tickEvery
	}
	if rest := wake - (clk.Monotonic() - start); rest > 0 {
		if err := clk.Sleep(ctx, rest); err != nil {
			return nextTick, err
		}
	}
	return nextTick, nil
}
