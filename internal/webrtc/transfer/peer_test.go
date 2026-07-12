package transfer

import (
	"sync"
	"testing"
)

func TestNewPeerConnection_CreatesPeerWithDefaultConfiguration(t *testing.T) {
	peer, err := NewPeerConnection(PeerConfig{})
	if err != nil {
		t.Fatalf("NewPeerConnection() error = %v", err)
	}
	t.Cleanup(func() {
		if err := peer.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

// TestPeerConnection_CallbackSettersRace verifies that callback setters are thread-safe.
// The race detector should not find any races when setting and reading callbacks concurrently.
func TestPeerConnection_CallbackSettersRace(t *testing.T) {
	// Create a minimal PeerConnection without actual WebRTC (just test callback fields)
	p := &PeerConnection{}

	var wg sync.WaitGroup
	const goroutines = 100

	// Concurrent writes to callback setters
	for i := 0; i < goroutines; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			p.OnMessage(func([]byte) {})
		}()
		go func() {
			defer wg.Done()
			p.OnOpen(func() {})
		}()
		go func() {
			defer wg.Done()
			p.OnClose(func() {})
		}()
	}

	// Concurrent reads (simulating what setupDataChannel and OnConnectionStateChange do)
	for i := 0; i < goroutines; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			// Read onMessage like setupDataChannel does
			p.mu.Lock()
			handler := p.onMessage
			p.mu.Unlock()
			if handler != nil {
				handler([]byte{})
			}
		}()
		go func() {
			defer wg.Done()
			// Read onOpen like setupDataChannel does
			p.mu.Lock()
			handler := p.onOpen
			p.mu.Unlock()
			if handler != nil {
				handler()
			}
		}()
		go func() {
			defer wg.Done()
			// Read onClose like OnConnectionStateChange does
			p.mu.Lock()
			handler := p.onClose
			p.mu.Unlock()
			if handler != nil {
				handler()
			}
		}()
	}

	wg.Wait()
}

// TestPeerConnection_CallbackInvocationSafe verifies callbacks can be safely invoked
// while other goroutines are modifying them.
func TestPeerConnection_CallbackInvocationSafe(t *testing.T) {
	p := &PeerConnection{}
	called := make(chan struct{}, 100)

	// Set initial callback
	p.OnMessage(func([]byte) {
		called <- struct{}{}
	})

	var wg sync.WaitGroup

	// Goroutine that keeps changing the callback
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			p.OnMessage(func([]byte) {
				called <- struct{}{}
			})
		}
	}()

	// Goroutine that keeps invoking the callback (simulating data channel messages)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			// This is how the callback should be read safely
			p.mu.Lock()
			handler := p.onMessage
			p.mu.Unlock()
			if handler != nil {
				handler([]byte("test"))
			}
		}
	}()

	wg.Wait()
	close(called)
}
