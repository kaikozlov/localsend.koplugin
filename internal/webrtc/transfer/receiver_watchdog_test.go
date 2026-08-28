package transfer

import (
	"testing"
	"time"
)

func TestRTCReceiver_StalledActiveFileReleasesTransferActivity(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", t.TempDir())
	doneCalls := 0
	receiver.OnTransferActivity(nil, func() { doneCalls++ })

	receiver.mu.Lock()
	receiver.currentFileID = "file-1"
	receiver.currentBytes = 42
	receiver.markTransferStartedLocked()
	generation := receiver.transferWatchdogGeneration
	receiver.transferWatchdogLastFileID = receiver.currentFileID
	receiver.transferWatchdogLastBytes = receiver.currentBytes
	receiver.transferWatchdogLastProgress = time.Now().Add(-receiveFileIdleTimeout - time.Second)
	receiver.mu.Unlock()

	receiver.checkTransferWatchdog(generation)

	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if receiver.transferActive {
		t.Fatal("stalled transfer remained active")
	}
	if receiver.state != stateDone {
		t.Fatalf("state = %d; want stateDone", receiver.state)
	}
	if doneCalls != 1 {
		t.Fatalf("transfer done callbacks = %d; want 1", doneCalls)
	}
}

func TestRTCReceiver_StaleWatchdogCannotTerminateNewTransfer(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", t.TempDir())

	receiver.mu.Lock()
	receiver.currentFileID = "old-file"
	receiver.markTransferStartedLocked()
	staleGeneration := receiver.transferWatchdogGeneration
	receiver.markTransferDoneLocked()
	receiver.currentFileID = "new-file"
	receiver.currentBytes = 7
	receiver.markTransferStartedLocked()
	newGeneration := receiver.transferWatchdogGeneration
	receiver.transferWatchdogLastProgress = time.Now().Add(-receiveFileIdleTimeout - time.Second)
	receiver.mu.Unlock()

	receiver.checkTransferWatchdog(staleGeneration)

	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if !receiver.transferActive {
		t.Fatal("stale watchdog terminated the newer transfer")
	}
	if receiver.transferWatchdogGeneration != newGeneration {
		t.Fatalf("watchdog generation = %d; want %d", receiver.transferWatchdogGeneration, newGeneration)
	}
	if receiver.transferWatchdog != nil {
		receiver.transferWatchdog.Stop()
	}
}
