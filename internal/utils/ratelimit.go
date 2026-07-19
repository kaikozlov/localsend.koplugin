// Package utils provides utility functions shared across the application.
package utils

import (
	"log/slog"
	"sync"
	"time"
)

// RateLimiter provides generic rate limiting based on failed attempts.
// It tracks attempts per client ID and blocks clients after exceeding
// the maximum number of attempts for a configurable duration.
//
// Thread-safe: all methods use mutex protection.
//
// Used by:
//   - HTTP receiver: rate limits PIN attempts by IP address
//   - WebRTC receiver: rate limits PIN attempts by signaling ID
type RateLimiter struct {
	attempts      map[string]*attemptInfo
	mu            sync.RWMutex
	maxAttempts   int
	blockDuration time.Duration
	now           func() time.Time // current time; defaults to time.Now, overridable via WithClock
}

// Option configures a RateLimiter.
type Option func(*RateLimiter)

// WithClock overrides the function used to read the current time. It defaults
// to time.Now; tests inject a controllable clock (e.g. FakeClock) so expiry
// behaviour is deterministic and free of real-time sleeps.
func WithClock(now func() time.Time) Option {
	return func(rl *RateLimiter) {
		if now != nil {
			rl.now = now
		}
	}
}

// attemptInfo tracks failed attempts for a client.
type attemptInfo struct {
	count       int
	blockedAt   time.Time
	lastAttempt time.Time // When the last attempt occurred (for stale entry cleanup)
}

// NewRateLimiter creates a new rate limiter with the given configuration.
//
// Parameters:
//   - maxAttempts: number of failed attempts before blocking
//   - blockDuration: how long to block after max attempts
//   - opts: optional configuration (e.g. WithClock)
func NewRateLimiter(maxAttempts int, blockDuration time.Duration, opts ...Option) *RateLimiter {
	rl := &RateLimiter{
		attempts:      make(map[string]*attemptInfo),
		maxAttempts:   maxAttempts,
		blockDuration: blockDuration,
		now:           time.Now,
	}
	for _, opt := range opts {
		opt(rl)
	}
	return rl
}

// IsBlocked returns true if the client is currently blocked due to
// too many failed attempts.
func (rl *RateLimiter) IsBlocked(clientID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	info, ok := rl.attempts[clientID]
	if !ok {
		return false
	}

	// Check if block has expired
	if info.count >= rl.maxAttempts {
		if rl.now().Sub(info.blockedAt) > rl.blockDuration {
			delete(rl.attempts, clientID) // Clear expired block
			return false
		}
		return true
	}
	return false
}

// RecordAttempt records a failed attempt for a client.
// Returns true if the client is now blocked (reached max attempts).
func (rl *RateLimiter) RecordAttempt(clientID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	info, ok := rl.attempts[clientID]
	if !ok {
		info = &attemptInfo{}
		rl.attempts[clientID] = info
	}

	now := rl.now()
	info.count++
	info.lastAttempt = now
	if info.count >= rl.maxAttempts {
		info.blockedAt = now
		slog.Warn("Rate limit reached, blocking client", "clientID", clientID, "duration", rl.blockDuration)
		return true
	}
	return false
}

// Clear removes all attempt records for a client.
// Typically called on successful authentication.
func (rl *RateLimiter) Clear(clientID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, clientID)
}

// CleanupExpired removes expired entries to prevent unbounded memory growth.
// This includes both blocked clients whose block has expired and stale
// partial-attempt entries from clients that haven't made attempts recently.
//
// Should be called periodically via a cleanup goroutine.
func (rl *RateLimiter) CleanupExpired() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	cleaned := 0
	for clientID, info := range rl.attempts {
		// Remove blocked entries whose block has expired
		if info.count >= rl.maxAttempts && now.Sub(info.blockedAt) > rl.blockDuration {
			delete(rl.attempts, clientID)
			cleaned++
			continue
		}
		// Remove stale partial-attempt entries (no activity for blockDuration)
		if info.count < rl.maxAttempts && now.Sub(info.lastAttempt) > rl.blockDuration {
			delete(rl.attempts, clientID)
			cleaned++
		}
	}
	if cleaned > 0 {
		slog.Debug("Cleaned up expired rate limit entries", "count", cleaned)
	}
	return cleaned
}

// Count returns the current number of tracked clients.
// Useful for testing and monitoring.
func (rl *RateLimiter) Count() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.attempts)
}

// GetAttempts returns the number of attempts for a client.
// Useful for testing.
func (rl *RateLimiter) GetAttempts(clientID string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	if info, ok := rl.attempts[clientID]; ok {
		return info.count
	}
	return 0
}
