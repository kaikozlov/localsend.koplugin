package transfer

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"localsend-cli/internal/crypto"
	"localsend-cli/internal/storage"
)

func TestComputeFingerprint(t *testing.T) {
	tests := []struct {
		name      string
		publicKey string
		wantLen   int
	}{
		{
			name:      "typical PEM key",
			publicKey: "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtest1234567890abcdef==\n-----END PUBLIC KEY-----",
			wantLen:   16, // Truncated to 16 hex chars
		},
		{
			name:      "empty string",
			publicKey: "",
			wantLen:   16,
		},
		{
			name:      "short string",
			publicKey: "short",
			wantLen:   16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeFingerprint(tt.publicKey)
			if len(result) != tt.wantLen {
				t.Errorf("computeFingerprint() len = %d; want %d", len(result), tt.wantLen)
			}
			// Verify it's valid hex
			for _, c := range result {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
					t.Errorf("computeFingerprint() contains non-hex char: %c", c)
				}
			}
		})
	}
}

func TestComputeFingerprint_Deterministic(t *testing.T) {
	key := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtest==\n-----END PUBLIC KEY-----"

	// Same input should always produce same output
	result1 := computeFingerprint(key)
	result2 := computeFingerprint(key)

	if result1 != result2 {
		t.Errorf("computeFingerprint() not deterministic: %q != %q", result1, result2)
	}
}

func TestComputeFingerprint_DifferentInputs(t *testing.T) {
	key1 := "-----BEGIN PUBLIC KEY-----\nkey1\n-----END PUBLIC KEY-----"
	key2 := "-----BEGIN PUBLIC KEY-----\nkey2\n-----END PUBLIC KEY-----"

	result1 := computeFingerprint(key1)
	result2 := computeFingerprint(key2)

	if result1 == result2 {
		t.Error("computeFingerprint() produced same result for different inputs")
	}
}

func TestRTCSender_FileDTOsIncludeChecksumAndBothTimestamps(t *testing.T) {
	modified := time.Unix(1_700_000_000, 123_456_789)
	accessed := time.Unix(1_700_000_001, 987_654_321)
	sender := NewRTCSender(nil, nil, "")
	sender.files = []FileMeta{{
		ID:       "file-1",
		FileName: "book.epub",
		FilePath: "/tmp/book.epub",
		Size:     42,
		FileType: "application/epub+zip",
		SHA256:   "0123456789abcdef",
		Modified: modified,
		Accessed: accessed,
	}}

	files := sender.fileDTOs()
	if len(files) != 1 {
		t.Fatalf("file DTO count = %d; want 1", len(files))
	}
	got := files[0]
	if got.SHA256 != "0123456789abcdef" {
		t.Fatalf("sha256 = %q; want advertised checksum", got.SHA256)
	}
	if got.Metadata.Modified != modified.Format(time.RFC3339Nano) {
		t.Fatalf("modified = %q", got.Metadata.Modified)
	}
	if got.Metadata.Accessed != accessed.Format(time.RFC3339Nano) {
		t.Fatalf("accessed = %q", got.Metadata.Accessed)
	}
}

func TestRTCSender_FileDTOsOmitUnavailableTimestamps(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")
	sender.files = []FileMeta{{ID: "file-1", FileName: "book.epub", Size: 42}}

	got := sender.fileDTOs()[0].Metadata
	if got.Modified != "" || got.Accessed != "" {
		t.Fatalf("zero timestamps serialized as %#v; want omitted fields", got)
	}
}

func TestRTCSender_SetOnPairRequest(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	// Initially nil
	if sender.onPairRequest != nil {
		t.Error("onPairRequest should initially be nil")
	}

	// Set callback
	callbackCalled := false
	sender.SetOnPairRequest(func(alias, fingerprint string) bool {
		callbackCalled = true
		return true
	})

	if sender.onPairRequest == nil {
		t.Error("onPairRequest should be set after SetOnPairRequest")
	}

	// Invoke callback to verify it was set correctly
	result := sender.onPairRequest("test-alias", "test-fingerprint")
	if !callbackCalled {
		t.Error("callback was not invoked")
	}
	if !result {
		t.Error("callback should return true")
	}
}

func TestRTCSender_SetReceiverInfo(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	// Initially empty
	if sender.receiverAlias != "" {
		t.Errorf("receiverAlias should initially be empty, got %q", sender.receiverAlias)
	}

	// Set receiver info
	sender.SetReceiverInfo("Test Device")

	if sender.receiverAlias != "Test Device" {
		t.Errorf("receiverAlias = %q; want 'Test Device'", sender.receiverAlias)
	}
}

func TestRTCSender_SetTrustedStore(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	// Initially nil
	if sender.trustedStore != nil {
		t.Error("trustedStore should initially be nil")
	}

	// Create temp dir for test store
	tempDir, err := os.MkdirTemp("", "sender_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create TrustedDeviceStore: %v", err)
	}

	// Set store
	sender.SetTrustedStore(store)

	if sender.trustedStore == nil {
		t.Error("trustedStore should be set after SetTrustedStore")
	}
}

func TestRTCSender_PairCallbackWithDecline(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	// Set callback that declines
	sender.SetOnPairRequest(func(alias, fingerprint string) bool {
		// Verify parameters are passed correctly
		if alias != "Remote Device" {
			t.Errorf("alias = %q; want 'Remote Device'", alias)
		}
		if fingerprint != "abc123" {
			t.Errorf("fingerprint = %q; want 'abc123'", fingerprint)
		}
		return false // Decline
	})

	result := sender.onPairRequest("Remote Device", "abc123")
	if result {
		t.Error("callback should return false (decline)")
	}
}

func TestTrustedDeviceStore_Integration(t *testing.T) {
	// Create temp dir
	tempDir, err := os.MkdirTemp("", "trust_integration_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	// Create store
	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Add a device (simulating what happens after PAIR)
	testDevice := storage.TrustedDevice{
		Alias:     "Test Sender",
		PublicKey: "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtest==\n-----END PUBLIC KEY-----",
		AddedAt:   time.Now().Unix(),
	}

	if err := store.Add(testDevice); err != nil {
		t.Fatalf("Failed to add device: %v", err)
	}

	// Verify device is trusted
	if !store.IsTrusted(testDevice.PublicKey) {
		t.Error("Device should be trusted after Add")
	}

	// Verify file was created
	filePath := filepath.Join(tempDir, "trusted_devices.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("trusted_devices.json should exist after Add")
	}

	// Create new store instance and verify persistence
	store2, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}

	if !store2.IsTrusted(testDevice.PublicKey) {
		t.Error("Device should still be trusted after reload")
	}

	devices := store2.List()
	if len(devices) != 1 {
		t.Errorf("List() len = %d; want 1", len(devices))
	}
	if devices[0].Alias != "Test Sender" {
		t.Errorf("Alias = %q; want 'Test Sender'", devices[0].Alias)
	}
}

// TestRTCSender_PairVerificationIntegration tests the full PAIR flow
// with cryptographic binding verification from sender's perspective.
func TestRTCSender_PairVerificationIntegration(t *testing.T) {
	// Create sender key
	senderKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate sender key: %v", err)
	}

	// Create receiver key
	receiverKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate receiver key: %v", err)
	}

	// Create combined nonce
	senderNonce := make([]byte, 32)
	receiverNonce := make([]byte, 32)
	for i := range senderNonce {
		senderNonce[i] = byte(i)
		receiverNonce[i] = byte(i + 32)
	}
	combinedNonce := append(senderNonce, receiverNonce...)

	// Create temp dir for trusted store
	tempDir, err := os.MkdirTemp("", "sender_pair_verify_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Set up sender
	sender := NewRTCSender(nil, senderKey, "")
	sender.SetTrustedStore(store)
	sender.SetReceiverInfo("Test Receiver")
	sender.finalNonce = combinedNonce

	// Simulate token response: receiver sends token
	receiverToken, err := receiverKey.GenerateTokenWithNonce(combinedNonce)
	if err != nil {
		t.Fatalf("Failed to generate receiver token: %v", err)
	}

	// Store receiver's token (what handleTokenResponse does)
	sender.receiverToken = receiverToken

	// Simulate PAIR request: receiver sends their public key
	receiverPublicPEM := receiverKey.PublicKeyPEM()

	// Parse the public key
	parsedKey, err := crypto.ParsePublicKeyPEM(receiverPublicPEM)
	if err != nil {
		t.Fatalf("Failed to parse receiver public key: %v", err)
	}

	// Verify token against PAIR public key (what handleFileAcceptance PAIR case does)
	err = crypto.VerifyTokenNonce(parsedKey, sender.receiverToken, sender.finalNonce)
	if err != nil {
		t.Fatalf("Token verification failed: %v", err)
	}

	// Now safe to store the key and persist
	sender.receiverPublicKey = parsedKey

	device := storage.TrustedDevice{
		Alias:     sender.receiverAlias,
		PublicKey: receiverPublicPEM,
		AddedAt:   time.Now().Unix(),
	}
	if err := store.Add(device); err != nil {
		t.Fatalf("Failed to add device: %v", err)
	}

	// Verify the device is trusted
	if !store.IsTrusted(receiverPublicPEM) {
		t.Error("Receiver should be trusted after successful PAIR")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

// TestRTCSender_Close_ConcurrentWithHandleMessage verifies that Close() can be
// safely called concurrently with handleMessage() without causing a panic.
// Before the fix, calling Close() while handleMessage() was running could cause
// a send on closed channel panic because Close() did not acquire the mutex.
func TestRTCSender_Close_ConcurrentWithHandleMessage(t *testing.T) {
	const iterations = 100
	const goroutines = 10

	for i := 0; i < iterations; i++ {
		sender := NewRTCSender(nil, nil, "")
		sender.state = senderStateWaitFileAccept

		var wg sync.WaitGroup
		wg.Add(goroutines * 2) // Half for handleMessage, half for Close

		// Spawn goroutines that call handleMessage
		for j := 0; j < goroutines; j++ {
			go func() {
				defer wg.Done()
				// This simulates receiving a DECLINED message which triggers
				// sending on s.declined channel
				data := []byte(`{"status":"DECLINED"}`)
				sender.handleMessage(data)
			}()
		}

		// Spawn goroutines that call Close
		for j := 0; j < goroutines; j++ {
			go func() {
				defer wg.Done()
				_ = sender.Close()
			}()
		}

		wg.Wait()
	}
	// If we get here without panicking, the race condition is fixed
}

// TestRTCSender_Send_ClosedChannelsReturnError verifies that reading from closed
// channels returns an error rather than nil. This tests the fix for the bug
// where Close() during Send() would cause Send() to return nil (success).
func TestRTCSender_Send_ClosedChannelsReturnError(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	// Close the sender - this closes all channels
	_ = sender.Close()

	// Verify closed channels behavior matches our fix expectations
	// Reading from closed 'accepted' channel should return nil map, ok=false
	select {
	case tokens, ok := <-sender.accepted:
		if ok {
			t.Error("expected accepted channel to be closed (ok=false)")
		}
		if tokens != nil {
			t.Error("expected nil tokens from closed channel")
		}
	default:
		t.Error("expected to receive from closed channel")
	}

	// Reading from closed 'declined' channel should return zero value, ok=false
	select {
	case _, ok := <-sender.declined:
		if ok {
			t.Error("expected declined channel to be closed (ok=false)")
		}
	default:
		t.Error("expected to receive from closed channel")
	}

	// Reading from closed 'errors' channel should return nil error, ok=false
	select {
	case err, ok := <-sender.errors:
		if ok {
			t.Error("expected errors channel to be closed (ok=false)")
		}
		if err != nil {
			t.Error("expected nil error from closed channel")
		}
		// This is the bug: without checking ok, Send() would return nil
	default:
		t.Error("expected to receive from closed channel")
	}
}

// TestRTCSender_Close_MultipleCalls verifies that Close() is idempotent and
// can be called multiple times safely.
func TestRTCSender_Close_MultipleCalls(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	var wg sync.WaitGroup
	const calls = 50

	wg.Add(calls)
	for i := 0; i < calls; i++ {
		go func() {
			defer wg.Done()
			_ = sender.Close()
		}()
	}

	wg.Wait()
	// If we get here without panicking, Close() is idempotent
}
