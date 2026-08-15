package recv

import (
	"context"
	"testing"
	"time"
)

func TestNextReconnectBackoff_CapsAtMaximum(t *testing.T) {
	if got := nextReconnectBackoff(webRTCReconnectMax); got != webRTCReconnectMax {
		t.Fatalf("backoff = %v; want cap %v", got, webRTCReconnectMax)
	}
}

func TestJitterReconnectDelay_StaysWithinTwentyPercent(t *testing.T) {
	base := 10 * time.Second
	for i := 0; i < 100; i++ {
		got := jitterReconnectDelay(base)
		if got < 8*time.Second || got > 12*time.Second {
			t.Fatalf("jittered delay %v outside ±20%%", got)
		}
	}
}

func TestWaitReconnect_CancellationReturnsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitReconnect(ctx, time.Minute) {
		t.Fatal("canceled reconnect wait reported success")
	}
}

func TestReconnectBackoffAfterSession_OnlyResetsAfterStableConnection(t *testing.T) {
	current := 16 * time.Second
	if got := reconnectBackoffAfterSession(current, time.Second); got != current {
		t.Fatalf("flapping session reset backoff to %v; want %v", got, current)
	}
	if got := reconnectBackoffAfterSession(current, webRTCStableSession); got != webRTCReconnectInitial {
		t.Fatalf("stable session backoff = %v; want %v", got, webRTCReconnectInitial)
	}
}
