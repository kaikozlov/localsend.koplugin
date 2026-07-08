package transfer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"localsend-cli/internal/crypto"
	"localsend-cli/internal/storage"
)

func cleanupTempDir(t *testing.T, tempDir string) {
	t.Helper()
	if err := os.RemoveAll(tempDir); err != nil {
		t.Errorf("Failed to remove temp dir: %v", err)
	}
}

func TestRTCReceiver_SetTrustedStore(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", "/tmp")

	// Initially nil
	if receiver.trustedStore != nil {
		t.Error("trustedStore should initially be nil")
	}

	// Create temp dir for test store
	tempDir, err := os.MkdirTemp("", "receiver_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create TrustedDeviceStore: %v", err)
	}

	// Set store
	receiver.SetTrustedStore(store)

	if receiver.trustedStore == nil {
		t.Error("trustedStore should be set after SetTrustedStore")
	}
}

func TestRTCReceiver_SetSenderInfo(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", "/tmp")

	// Initially empty
	if receiver.senderAlias != "" {
		t.Errorf("senderAlias should initially be empty, got %q", receiver.senderAlias)
	}

	// Set sender info
	receiver.SetSenderInfo("Remote Sender")

	if receiver.senderAlias != "Remote Sender" {
		t.Errorf("senderAlias = %q; want 'Remote Sender'", receiver.senderAlias)
	}
}

func TestRTCReceiver_SetRequirePairing(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", "/tmp")

	// Initially false
	if receiver.requirePairing {
		t.Error("requirePairing should initially be false")
	}

	// Enable pairing requirement
	receiver.SetRequirePairing(true)

	if !receiver.requirePairing {
		t.Error("requirePairing should be true after SetRequirePairing(true)")
	}

	// Disable again
	receiver.SetRequirePairing(false)

	if receiver.requirePairing {
		t.Error("requirePairing should be false after SetRequirePairing(false)")
	}
}

func TestRTCReceiver_TrustedStorePersistence(t *testing.T) {
	// Create temp dir
	tempDir, err := os.MkdirTemp("", "receiver_trust_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	// Create store and receiver
	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	receiver := NewRTCReceiver(nil, nil, "", "/tmp")
	receiver.SetTrustedStore(store)
	receiver.SetSenderInfo("Test Sender Device")

	// Simulate what handlePairResponse does - add a device
	testPublicKey := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAsenderkey123==\n-----END PUBLIC KEY-----"
	device := storage.TrustedDevice{
		Alias:     receiver.senderAlias,
		PublicKey: testPublicKey,
		AddedAt:   time.Now().Unix(),
	}

	if err := store.Add(device); err != nil {
		t.Fatalf("Failed to add device: %v", err)
	}

	// Verify persistence
	filePath := filepath.Join(tempDir, "trusted_devices.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("trusted_devices.json should exist")
	}

	// Create new store and verify device is still there
	store2, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}

	if !store2.IsTrusted(testPublicKey) {
		t.Error("Device should be trusted after reload")
	}

	devices := store2.List()
	if len(devices) != 1 {
		t.Errorf("List() len = %d; want 1", len(devices))
	}
	if devices[0].Alias != "Test Sender Device" {
		t.Errorf("Alias = %q; want 'Test Sender Device'", devices[0].Alias)
	}
}

func TestRTCReceiver_MultipleDevicesTrust(t *testing.T) {
	// Create temp dir
	tempDir, err := os.MkdirTemp("", "multi_trust_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Add multiple devices
	devices := []storage.TrustedDevice{
		{Alias: "Device 1", PublicKey: "key1", AddedAt: time.Now().Unix()},
		{Alias: "Device 2", PublicKey: "key2", AddedAt: time.Now().Unix()},
		{Alias: "Device 3", PublicKey: "key3", AddedAt: time.Now().Unix()},
	}

	for _, d := range devices {
		if err := store.Add(d); err != nil {
			t.Fatalf("Failed to add device %s: %v", d.Alias, err)
		}
	}

	// Verify all are trusted
	for _, d := range devices {
		if !store.IsTrusted(d.PublicKey) {
			t.Errorf("Device %s should be trusted", d.Alias)
		}
	}

	// Verify untrusted key is not trusted
	if store.IsTrusted("unknown_key") {
		t.Error("Unknown key should not be trusted")
	}

	// Verify list count
	list := store.List()
	if len(list) != 3 {
		t.Errorf("List() len = %d; want 3", len(list))
	}
}

// TestRTCReceiver_SenderTokenStorage verifies that the sender's token is stored
// for later PAIR verification.
func TestRTCReceiver_SenderTokenStorage(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", "/tmp")

	// Initially empty
	if receiver.senderToken != "" {
		t.Errorf("senderToken should initially be empty, got %q", receiver.senderToken)
	}

	// Simulate storing a token (what handleToken does)
	testToken := "sha256.abc123.def456.ed25519.sig789"
	receiver.senderToken = testToken

	if receiver.senderToken != testToken {
		t.Errorf("senderToken = %q; want %q", receiver.senderToken, testToken)
	}
}

// TestRTCReceiver_PairVerificationSuccess verifies that token verification
// succeeds when the PAIR public key matches the token's signer.
func TestRTCReceiver_PairVerificationSuccess(t *testing.T) {
	// Create sender and receiver signing keys
	senderKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate sender key: %v", err)
	}

	// Create combined nonce (simulating the nonce exchange)
	senderNonce := make([]byte, 32)
	receiverNonce := make([]byte, 32)
	for i := range senderNonce {
		senderNonce[i] = byte(i)
		receiverNonce[i] = byte(i + 32)
	}
	combinedNonce := append(senderNonce, receiverNonce...)

	// Generate token with sender's key
	senderToken, err := senderKey.GenerateTokenWithNonce(combinedNonce)
	if err != nil {
		t.Fatalf("Failed to generate sender token: %v", err)
	}

	// Get sender's public key PEM (simulating PAIR response)
	senderPublicPEM := senderKey.PublicKeyPEM()

	// Parse the public key
	parsedKey, err := crypto.ParsePublicKeyPEM(senderPublicPEM)
	if err != nil {
		t.Fatalf("Failed to parse sender public key: %v", err)
	}

	// Verify the token against the public key - THIS IS WHAT handlePairResponse DOES
	err = crypto.VerifyTokenNonce(parsedKey, senderToken, combinedNonce)
	if err != nil {
		t.Errorf("Token verification should succeed with matching key: %v", err)
	}
}

// TestRTCReceiver_PairVerificationFailure verifies that token verification
// fails when the PAIR public key does NOT match the token's signer.
func TestRTCReceiver_PairVerificationFailure(t *testing.T) {
	// Create two different keys
	realSenderKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate real sender key: %v", err)
	}
	fakeSenderKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate fake sender key: %v", err)
	}

	// Create combined nonce
	senderNonce := make([]byte, 32)
	receiverNonce := make([]byte, 32)
	for i := range senderNonce {
		senderNonce[i] = byte(i)
		receiverNonce[i] = byte(i + 32)
	}
	combinedNonce := append(senderNonce, receiverNonce...)

	// Generate token with REAL sender's key
	senderToken, err := realSenderKey.GenerateTokenWithNonce(combinedNonce)
	if err != nil {
		t.Fatalf("Failed to generate sender token: %v", err)
	}

	// Get FAKE sender's public key PEM (simulating an attack)
	fakeSenderPublicPEM := fakeSenderKey.PublicKeyPEM()

	// Parse the fake public key
	parsedKey, err := crypto.ParsePublicKeyPEM(fakeSenderPublicPEM)
	if err != nil {
		t.Fatalf("Failed to parse fake sender public key: %v", err)
	}

	// Verify the token against the FAKE public key - SHOULD FAIL
	err = crypto.VerifyTokenNonce(parsedKey, senderToken, combinedNonce)
	if err == nil {
		t.Error("Token verification should FAIL with mismatched key")
	}
}

// TestRTCReceiver_findTrustedSender_MatchesTrustedDevice verifies that findTrustedSender
// returns the correct key and PEM for a trusted device that can verify the sender's token.
func TestRTCReceiver_findTrustedSender_MatchesTrustedDevice(t *testing.T) {
	// Create temp dir for trusted store
	tempDir, err := os.MkdirTemp("", "find_trusted_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	// Create sender key
	senderKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate sender key: %v", err)
	}

	// Create nonces
	senderNonce := make([]byte, 32)
	receiverNonce := make([]byte, 32)
	for i := range senderNonce {
		senderNonce[i] = byte(i)
		receiverNonce[i] = byte(i + 32)
	}
	combinedNonce := crypto.CombineNonces(senderNonce, receiverNonce)

	// Add sender to trusted store
	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	senderPEM := senderKey.PublicKeyPEM()
	device := storage.TrustedDevice{
		Alias:     "Trusted Sender",
		PublicKey: senderPEM,
		AddedAt:   time.Now().Unix(),
	}
	if err := store.Add(device); err != nil {
		t.Fatalf("Failed to add device: %v", err)
	}

	// Create receiver with trusted store and finalNonce
	receiver := NewRTCReceiver(nil, nil, "", "/tmp")
	receiver.SetTrustedStore(store)
	receiver.finalNonce = combinedNonce

	// Generate sender's token with the combined nonce
	senderToken, err := senderKey.GenerateTokenWithNonce(combinedNonce)
	if err != nil {
		t.Fatalf("Failed to generate sender token: %v", err)
	}

	// findTrustedSender should match and return the key
	foundKey, foundPEM := receiver.findTrustedSender(senderToken)
	if foundKey == nil {
		t.Error("Expected to find trusted sender, got nil key")
	}
	if foundPEM != senderPEM {
		t.Errorf("Expected PEM %q, got %q", senderPEM, foundPEM)
	}
}

// TestRTCReceiver_findTrustedSender_NoMatchReturnsNil verifies that findTrustedSender
// returns nil for a token from an unknown device.
func TestRTCReceiver_findTrustedSender_NoMatchReturnsNil(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "find_trusted_nomatch_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	// Create different keys
	senderKey, _ := crypto.GenerateKeyPair()
	otherKey, _ := crypto.GenerateKeyPair()

	// Add OTHER device (not sender) to trusted store
	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	device := storage.TrustedDevice{
		Alias:     "Other Device",
		PublicKey: otherKey.PublicKeyPEM(),
		AddedAt:   time.Now().Unix(),
	}
	if err := store.Add(device); err != nil {
		t.Fatalf("Failed to add device: %v", err)
	}

	// Create nonces and token with sender's key (not in store)
	senderNonce := make([]byte, 32)
	receiverNonce := make([]byte, 32)
	combinedNonce := crypto.CombineNonces(senderNonce, receiverNonce)

	receiver := NewRTCReceiver(nil, nil, "", "/tmp")
	receiver.SetTrustedStore(store)
	receiver.finalNonce = combinedNonce

	senderToken, _ := senderKey.GenerateTokenWithNonce(combinedNonce)

	// Should NOT find the sender (different key)
	foundKey, foundPEM := receiver.findTrustedSender(senderToken)
	if foundKey != nil {
		t.Error("Expected nil key for unknown sender")
	}
	if foundPEM != "" {
		t.Errorf("Expected empty PEM, got %q", foundPEM)
	}
}

// TestRTCReceiver_findTrustedSender_NilStoreReturnsNil verifies that findTrustedSender
// handles nil trustedStore gracefully.
func TestRTCReceiver_findTrustedSender_NilStoreReturnsNil(t *testing.T) {
	receiver := NewRTCReceiver(nil, nil, "", "/tmp")
	// trustedStore is nil by default

	foundKey, foundPEM := receiver.findTrustedSender("some-token")
	if foundKey != nil {
		t.Error("Expected nil key when trustedStore is nil")
	}
	if foundPEM != "" {
		t.Error("Expected empty PEM when trustedStore is nil")
	}
}

// TestRTCReceiver_findTrustedSender_EmptyStoreReturnsNil verifies that findTrustedSender
// returns nil when the store is empty.
func TestRTCReceiver_findTrustedSender_EmptyStoreReturnsNil(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "find_trusted_empty_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	// Don't add any devices

	receiver := NewRTCReceiver(nil, nil, "", "/tmp")
	receiver.SetTrustedStore(store)
	receiver.finalNonce = make([]byte, 64)

	foundKey, foundPEM := receiver.findTrustedSender("some-token")
	if foundKey != nil {
		t.Error("Expected nil key for empty store")
	}
	if foundPEM != "" {
		t.Error("Expected empty PEM for empty store")
	}
}

// TestRTCReceiver_findTrustedSender_MalformedPEMSkipped verifies that findTrustedSender
// skips malformed PEM entries without crashing and continues checking other entries.
func TestRTCReceiver_findTrustedSender_MalformedPEMSkipped(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "find_trusted_malformed_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	// Create valid sender key
	senderKey, _ := crypto.GenerateKeyPair()
	senderPEM := senderKey.PublicKeyPEM()

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Add malformed PEM first
	badDevice := storage.TrustedDevice{
		Alias:     "Bad Device",
		PublicKey: "-----BEGIN PUBLIC KEY-----\nINVALID_BASE64!\n-----END PUBLIC KEY-----",
		AddedAt:   time.Now().Unix(),
	}
	if err := store.Add(badDevice); err != nil {
		t.Fatalf("Failed to add bad device: %v", err)
	}

	// Add valid sender
	goodDevice := storage.TrustedDevice{
		Alias:     "Good Device",
		PublicKey: senderPEM,
		AddedAt:   time.Now().Unix(),
	}
	if err := store.Add(goodDevice); err != nil {
		t.Fatalf("Failed to add good device: %v", err)
	}

	// Create nonces and token
	senderNonce := make([]byte, 32)
	receiverNonce := make([]byte, 32)
	combinedNonce := crypto.CombineNonces(senderNonce, receiverNonce)

	receiver := NewRTCReceiver(nil, nil, "", "/tmp")
	receiver.SetTrustedStore(store)
	receiver.finalNonce = combinedNonce

	senderToken, _ := senderKey.GenerateTokenWithNonce(combinedNonce)

	// Should skip malformed PEM and still find the valid sender
	foundKey, foundPEM := receiver.findTrustedSender(senderToken)
	if foundKey == nil {
		t.Error("Expected to find trusted sender despite malformed entry")
	}
	if foundPEM != senderPEM {
		t.Errorf("Expected sender PEM, got %q", foundPEM)
	}
}

// TestRTCReceiver_PairVerificationIntegration tests the full PAIR flow
// with cryptographic binding verification.
func TestRTCReceiver_PairVerificationIntegration(t *testing.T) {
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
	tempDir, err := os.MkdirTemp("", "pair_verify_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTempDir(t, tempDir)

	store, err := storage.NewTrustedDeviceStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Set up receiver
	receiver := NewRTCReceiver(nil, receiverKey, "", tempDir)
	receiver.SetTrustedStore(store)
	receiver.SetSenderInfo("Test Sender")
	receiver.finalNonce = combinedNonce

	// Simulate token exchange: sender sends token
	senderToken, err := senderKey.GenerateTokenWithNonce(combinedNonce)
	if err != nil {
		t.Fatalf("Failed to generate sender token: %v", err)
	}

	// Store sender's token (what handleToken does)
	receiver.senderToken = senderToken

	// Simulate PAIR response: sender sends their public key
	senderPublicPEM := senderKey.PublicKeyPEM()

	// Parse the public key
	parsedKey, err := crypto.ParsePublicKeyPEM(senderPublicPEM)
	if err != nil {
		t.Fatalf("Failed to parse sender public key: %v", err)
	}

	// Verify token against PAIR public key (what handlePairResponse does)
	err = crypto.VerifyTokenNonce(parsedKey, receiver.senderToken, receiver.finalNonce)
	if err != nil {
		t.Fatalf("Token verification failed: %v", err)
	}

	// Now safe to store the key and persist
	receiver.senderPublicKey = parsedKey
	receiver.senderPublicPEM = senderPublicPEM

	device := storage.TrustedDevice{
		Alias:     receiver.senderAlias,
		PublicKey: senderPublicPEM,
		AddedAt:   time.Now().Unix(),
	}
	if err := store.Add(device); err != nil {
		t.Fatalf("Failed to add device: %v", err)
	}

	// Verify the device is trusted
	if !store.IsTrusted(senderPublicPEM) {
		t.Error("Sender should be trusted after successful PAIR")
	}
}
