package utils

import (
	"testing"
	"time"
)

func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	if rl.maxAttempts != 3 {
		t.Errorf("maxAttempts = %d; want 3", rl.maxAttempts)
	}
	if rl.blockDuration != 30*time.Second {
		t.Errorf("blockDuration = %v; want 30s", rl.blockDuration)
	}
	if rl.attempts == nil {
		t.Error("attempts map should be initialized")
	}
}

func TestRateLimiter_IsBlocked_InitiallyFalse(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	if rl.IsBlocked("192.168.1.1") {
		t.Error("New client should not be blocked")
	}
}

func TestRateLimiter_IsBlocked_AfterMaxAttempts(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	// Record max attempts
	for i := 0; i < 3; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	if !rl.IsBlocked("192.168.1.1") {
		t.Error("Client should be blocked after max attempts")
	}
}

func TestRateLimiter_IsBlocked_ExpiredBlock(t *testing.T) {
	clock := NewFakeClock()
	blockDur := time.Second
	rl := NewRateLimiter(3, blockDur, WithClock(clock.Now))

	// Record max attempts
	for i := 0; i < 3; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	if !rl.IsBlocked("192.168.1.1") {
		t.Fatal("Client should be blocked after max attempts")
	}

	// Advance past the block window to expire it
	clock.Advance(blockDur + time.Millisecond)

	if rl.IsBlocked("192.168.1.1") {
		t.Error("Client should not be blocked after expiry")
	}
}

func TestRateLimiter_RecordAttempt_ReturnsBlockedStatus(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	// First two attempts should not block
	if rl.RecordAttempt("192.168.1.1") {
		t.Error("First attempt should not block")
	}
	if rl.RecordAttempt("192.168.1.1") {
		t.Error("Second attempt should not block")
	}

	// Third attempt should block
	if !rl.RecordAttempt("192.168.1.1") {
		t.Error("Third attempt should block")
	}
}

func TestRateLimiter_Clear(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	// Block the client
	for i := 0; i < 3; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	if !rl.IsBlocked("192.168.1.1") {
		t.Fatal("Client should be blocked")
	}

	// Clear attempts
	rl.Clear("192.168.1.1")

	if rl.IsBlocked("192.168.1.1") {
		t.Error("Client should not be blocked after clear")
	}
	if rl.GetAttempts("192.168.1.1") != 0 {
		t.Error("Attempts should be 0 after clear")
	}
}

func TestRateLimiter_CleanupExpired_BlockedEntries(t *testing.T) {
	clock := NewFakeClock()
	blockDur := time.Second
	rl := NewRateLimiter(3, blockDur, WithClock(clock.Now))

	// Block a client
	for i := 0; i < 3; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	// Advance past the block window to expire it
	clock.Advance(blockDur + time.Millisecond)

	cleaned := rl.CleanupExpired()

	if cleaned != 1 {
		t.Errorf("CleanupExpired = %d; want 1", cleaned)
	}
	if rl.Count() != 0 {
		t.Errorf("Count = %d; want 0", rl.Count())
	}
}

func TestRateLimiter_CleanupExpired_PartialAttempts(t *testing.T) {
	clock := NewFakeClock()
	blockDur := time.Second
	rl := NewRateLimiter(3, blockDur, WithClock(clock.Now))

	// Record partial attempt
	rl.RecordAttempt("192.168.1.1")

	// Advance past the staleness window
	clock.Advance(blockDur + time.Millisecond)

	cleaned := rl.CleanupExpired()

	if cleaned != 1 {
		t.Errorf("CleanupExpired = %d; want 1", cleaned)
	}
	if rl.Count() != 0 {
		t.Errorf("Count = %d; want 0", rl.Count())
	}
}

func TestRateLimiter_CleanupExpired_ActiveEntries(t *testing.T) {
	rl := NewRateLimiter(3, 1*time.Hour)

	// Record recent attempt
	rl.RecordAttempt("192.168.1.1")

	cleaned := rl.CleanupExpired()

	if cleaned != 0 {
		t.Errorf("CleanupExpired = %d; want 0", cleaned)
	}
	if rl.Count() != 1 {
		t.Errorf("Count = %d; want 1", rl.Count())
	}
}

func TestRateLimiter_Count(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	if rl.Count() != 0 {
		t.Errorf("Count = %d; want 0", rl.Count())
	}

	rl.RecordAttempt("192.168.1.1")
	rl.RecordAttempt("192.168.1.2")

	if rl.Count() != 2 {
		t.Errorf("Count = %d; want 2", rl.Count())
	}
}

func TestRateLimiter_GetAttempts(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	if rl.GetAttempts("192.168.1.1") != 0 {
		t.Errorf("GetAttempts = %d; want 0", rl.GetAttempts("192.168.1.1"))
	}

	rl.RecordAttempt("192.168.1.1")
	if rl.GetAttempts("192.168.1.1") != 1 {
		t.Errorf("GetAttempts = %d; want 1", rl.GetAttempts("192.168.1.1"))
	}

	rl.RecordAttempt("192.168.1.1")
	if rl.GetAttempts("192.168.1.1") != 2 {
		t.Errorf("GetAttempts = %d; want 2", rl.GetAttempts("192.168.1.1"))
	}
}

func TestRateLimiter_IndependentClients(t *testing.T) {
	rl := NewRateLimiter(3, 30*time.Second)

	// Block client 1
	for i := 0; i < 3; i++ {
		rl.RecordAttempt("192.168.1.1")
	}

	// Client 2 should not be affected
	if rl.IsBlocked("192.168.1.2") {
		t.Error("Client 2 should not be blocked")
	}

	rl.RecordAttempt("192.168.1.2")
	if rl.IsBlocked("192.168.1.2") {
		t.Error("Client 2 should still not be blocked after 1 attempt")
	}
}
