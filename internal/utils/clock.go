package utils

import (
	"sync"
	"time"
)

// FakeClock is a controllable clock for deterministic tests that construct a
// RateLimiter via WithClock. It holds a single mutable instant that tests
// advance explicitly with Advance, removing any dependence on real time (and
// therefore any sub-millisecond timing race).
//
// It is safe for concurrent use, though tests are typically single-goroutine.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a FakeClock anchored at the Unix epoch.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Unix(0, 0)}
}

// Now returns the clock's current instant. Its signature matches the
// func() time.Time expected by WithClock, so it can be passed directly:
//
//	utils.NewRateLimiter(3, time.Second, utils.WithClock(clock.Now))
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d (or backward if d is negative).
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
