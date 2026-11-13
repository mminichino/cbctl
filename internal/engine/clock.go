// Package engine waits for asynchronous Capella operations. Time is injected
// through Clock so waits and retries run instantly under test.
package engine

import (
	"context"
	"sync"
	"time"
)

// Clock abstracts time so waiters can be driven by a fake clock in tests.
type Clock interface {
	// Now returns the wall-clock time.
	Now() time.Time
	// Monotonic returns the time elapsed since an arbitrary fixed point.
	Monotonic() time.Duration
	// Sleep blocks for d or until ctx is done, in which case it returns ctx.Err().
	Sleep(ctx context.Context, d time.Duration) error
}

// processStart anchors RealClock.Monotonic; time.Since uses the monotonic reading.
var processStart = time.Now()

// RealClock is the Clock backed by the system clock.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// Monotonic returns the time elapsed since process start.
func (RealClock) Monotonic() time.Duration { return time.Since(processStart) }

// Sleep waits for d or until ctx is cancelled.
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// defaultFakeStart is the wall-clock origin of a FakeClock created with a zero start.
var defaultFakeStart = time.Date(2026, time.October, 5, 17, 0, 0, 0, time.UTC)

// FakeClock is a deterministic Clock: Sleep advances time instantly and
// records the requested duration. It is safe for concurrent use.
type FakeClock struct {
	mu     sync.Mutex
	start  time.Time
	t      time.Duration
	sleeps []time.Duration
}

// NewFakeClock returns a FakeClock whose Now starts at start (a fixed default when zero).
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = defaultFakeStart
	}
	return &FakeClock{start: start}
}

// Now returns the start time plus the fake time elapsed.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.start.Add(f.t)
}

// Monotonic returns the fake time elapsed.
func (f *FakeClock) Monotonic() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the clock forward by d without recording a sleep.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t += d
}

// Sleep records d and advances the clock by it immediately. If ctx is already
// cancelled it returns ctx.Err() and neither records nor advances.
func (f *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleeps = append(f.sleeps, d)
	if d > 0 {
		f.t += d
	}
	return nil
}

// Sleeps returns a copy of the durations passed to Sleep, in call order.
func (f *FakeClock) Sleeps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.sleeps...)
}
