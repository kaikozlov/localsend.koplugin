package recv

import (
	"testing"
	"time"

	"localsend-cli/internal/utils"
)

// TestFileReceiver_cleanupExpiredPINAttempts_RemovesExpiredBlocks verifies that
// blocked IPs with expired blocks are removed during cleanup.
func TestFileReceiver_cleanupExpiredPINAttempts_RemovesExpiredBlocks(t *testing.T) {
	clock := utils.NewFakeClock()
	blockDur := time.Second
	rl := utils.NewRateLimiter(maxPINAttempts, blockDur, utils.WithClock(clock.Now))

	// Record max attempts to trigger block
	for i := 0; i < maxPINAttempts; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	// Deterministically blocked immediately: with a controllable clock there is
	// no real-time window between setting blockedAt and the IsBlocked check.
	if !rl.IsBlocked("192.168.1.1") {
		t.Fatal("IP should be blocked after max attempts")
	}

	// Advance past the block window to expire it
	clock.Advance(blockDur + time.Millisecond)

	// Run cleanup
	cleaned := rl.CleanupExpired()

	// Verify the entry was removed
	if rl.IsBlocked("192.168.1.1") {
		t.Error("Expired blocked IP should have been removed")
	}
	if cleaned != 1 {
		t.Errorf("Expected 1 entry cleaned, got %d", cleaned)
	}
}

// TestFileReceiver_cleanupExpiredPINAttempts_RemovesStalePartialAttempts verifies that
// partial attempts (not yet blocked) with old lastAttempt are removed.
func TestFileReceiver_cleanupExpiredPINAttempts_RemovesStalePartialAttempts(t *testing.T) {
	clock := utils.NewFakeClock()
	blockDur := time.Second
	rl := utils.NewRateLimiter(maxPINAttempts, blockDur, utils.WithClock(clock.Now))

	// Record a partial attempt (not blocked)
	rl.RecordAttempt("192.168.1.1")

	// Advance so the entry becomes stale
	clock.Advance(blockDur + time.Millisecond)

	// Run cleanup
	cleaned := rl.CleanupExpired()

	// Verify the entry was removed
	if rl.GetAttempts("192.168.1.1") != 0 {
		t.Error("Stale partial attempt should have been removed")
	}
	if cleaned != 1 {
		t.Errorf("Expected 1 entry cleaned, got %d", cleaned)
	}
}

// TestFileReceiver_cleanupExpiredPINAttempts_KeepsActiveEntries verifies that
// recent entries are NOT removed during cleanup.
func TestFileReceiver_cleanupExpiredPINAttempts_KeepsActiveEntries(t *testing.T) {
	clock := utils.NewFakeClock()
	rl := utils.NewRateLimiter(maxPINAttempts, 1*time.Hour, utils.WithClock(clock.Now))

	// Recent blocked IP (should be kept)
	for i := 0; i < maxPINAttempts; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	// Recent partial attempt (should be kept)
	rl.RecordAttempt("192.168.1.2")

	// Run cleanup (clock not advanced: both entries are recent)
	cleaned := rl.CleanupExpired()

	// Verify both entries are still present
	if cleaned != 0 {
		t.Errorf("Expected 0 entries cleaned, got %d", cleaned)
	}
	if !rl.IsBlocked("192.168.1.1") {
		t.Error("Recent blocked IP should NOT have been removed")
	}
	if rl.GetAttempts("192.168.1.2") != 1 {
		t.Error("Recent partial attempt should NOT have been removed")
	}
}

// TestFileReceiver_cleanupExpiredPINAttempts_EmptyNoOp verifies that
// cleanup handles empty rate limiter gracefully (no panic).
func TestFileReceiver_cleanupExpiredPINAttempts_EmptyNoOp(t *testing.T) {
	clock := utils.NewFakeClock()
	rl := utils.NewRateLimiter(maxPINAttempts, pinBlockDuration, utils.WithClock(clock.Now))

	// This should not panic
	cleaned := rl.CleanupExpired()

	// Verify nothing was cleaned
	if cleaned != 0 {
		t.Errorf("Expected 0 entries cleaned from empty limiter, got %d", cleaned)
	}
}

// TestFileReceiver_cleanupExpiredPINAttempts_MixedEntries verifies cleanup
// correctly handles a mix of expired and active entries.
func TestFileReceiver_cleanupExpiredPINAttempts_MixedEntries(t *testing.T) {
	clock := utils.NewFakeClock()
	blockDur := time.Second
	rl := utils.NewRateLimiter(maxPINAttempts, blockDur, utils.WithClock(clock.Now))

	// Add entry that will expire
	rl.RecordAttempt("expired-partial")

	// Advance so it becomes stale
	clock.Advance(blockDur + time.Millisecond)

	// Add recent entries (recorded after the clock advanced, so they're fresh)
	for i := 0; i < maxPINAttempts; i++ {
		rl.RecordAttempt("recent-blocked")
	}
	rl.RecordAttempt("recent-partial")

	// Run cleanup
	cleaned := rl.CleanupExpired()

	// Verify results
	if cleaned != 1 {
		t.Errorf("Expected 1 entry cleaned, got %d", cleaned)
	}
	if rl.GetAttempts("expired-partial") != 0 {
		t.Error("Expired partial entry should have been removed")
	}
	if rl.GetAttempts("recent-blocked") == 0 {
		t.Error("Recent blocked entry should have been kept")
	}
	if rl.GetAttempts("recent-partial") != 1 {
		t.Error("Recent partial entry should have been kept")
	}
	if rl.Count() != 2 {
		t.Errorf("Expected 2 remaining entries, got %d", rl.Count())
	}
}

// TestRateLimiter_Integration tests the full rate limiter through FileReceiver methods.
func TestRateLimiter_Integration(t *testing.T) {
	fr := NewFileReceiver("test", t.TempDir(), false)

	// Initially not blocked
	if fr.isPINBlocked("192.168.1.1") {
		t.Error("IP should not be blocked initially")
	}

	// Record attempts up to but not including max
	for i := 0; i < maxPINAttempts-1; i++ {
		fr.recordPINAttempt("192.168.1.1")
	}

	// Still not blocked
	if fr.isPINBlocked("192.168.1.1") {
		t.Error("IP should not be blocked before max attempts")
	}

	// One more to trigger block
	fr.recordPINAttempt("192.168.1.1")

	// Now blocked
	if !fr.isPINBlocked("192.168.1.1") {
		t.Error("IP should be blocked after max attempts")
	}

	// Clear attempts
	fr.clearPINAttempts("192.168.1.1")

	// No longer blocked
	if fr.isPINBlocked("192.168.1.1") {
		t.Error("IP should not be blocked after clear")
	}
}
