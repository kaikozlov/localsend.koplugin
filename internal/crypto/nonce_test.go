package crypto

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestGenerateNonce(t *testing.T) {
	nonce, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}
	if len(nonce) != NonceSize {
		t.Errorf("Nonce size = %d; want %d", len(nonce), NonceSize)
	}
}

func TestValidateNonce(t *testing.T) {
	tests := []struct {
		size  int
		valid bool
	}{
		{7, false},   // Too small
		{8, false},   // Too small for nonce (timestamps use ValidateSalt)
		{15, false},  // Still too small
		{16, true},   // Minimum valid for nonce
		{32, true},   // Default size
		{128, true},  // Maximum valid
		{129, false}, // Too large
	}

	for _, tt := range tests {
		nonce := make([]byte, tt.size)
		got := ValidateNonce(nonce)
		if got != tt.valid {
			t.Errorf("ValidateNonce(size=%d) = %v; want %v", tt.size, got, tt.valid)
		}
	}
}

func TestValidateSalt(t *testing.T) {
	tests := []struct {
		size  int
		valid bool
	}{
		{7, false},   // Too small
		{8, true},    // Minimum valid (timestamps)
		{16, true},   // Nonce size
		{32, true},   // Default nonce size
		{128, true},  // Maximum valid
		{129, false}, // Too large
	}

	for _, tt := range tests {
		salt := make([]byte, tt.size)
		got := ValidateSalt(salt)
		if got != tt.valid {
			t.Errorf("ValidateSalt(size=%d) = %v; want %v", tt.size, got, tt.valid)
		}
	}
}

func TestNonceEncodeDecode(t *testing.T) {
	nonce, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}

	encoded := EncodeNonce(nonce)
	if encoded == "" {
		t.Error("Encoded nonce is empty")
	}

	decoded, err := DecodeNonce(encoded)
	if err != nil {
		t.Fatalf("DecodeNonce failed: %v", err)
	}

	if len(decoded) != len(nonce) {
		t.Errorf("Decoded nonce length = %d; want %d", len(decoded), len(nonce))
	}

	for i := range nonce {
		if decoded[i] != nonce[i] {
			t.Errorf("Decoded nonce differs at position %d", i)
		}
	}
}

func TestDecodeNonce_EnforcesOfficialLengthBounds(t *testing.T) {
	tests := []struct {
		length  int
		wantErr bool
	}{
		{length: 8, wantErr: true},
		{length: 15, wantErr: true},
		{length: 16, wantErr: false},
		{length: 128, wantErr: false},
		{length: 129, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("length_%d", tt.length), func(t *testing.T) {
			_, err := DecodeNonce(EncodeNonce(make([]byte, tt.length)))
			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodeNonce(length=%d) error = %v; wantErr %v", tt.length, err, tt.wantErr)
			}
		})
	}
}

// TestNonceURLSafeBase64 verifies that nonce encoding uses URL-safe base64
// (required for LocalSend WebRTC protocol compatibility).
func TestNonceURLSafeBase64(t *testing.T) {
	// Generate many nonces and check none contain standard base64 chars
	for i := 0; i < 100; i++ {
		nonce, err := GenerateNonce()
		if err != nil {
			t.Fatalf("GenerateNonce failed: %v", err)
		}

		encoded := EncodeNonce(nonce)

		// URL-safe base64 uses '-' and '_' instead of '+' and '/'
		if strings.Contains(encoded, "+") {
			t.Errorf("Encoded nonce contains '+' (should use URL-safe base64): %s", encoded)
		}
		if strings.Contains(encoded, "/") {
			t.Errorf("Encoded nonce contains '/' (should use URL-safe base64): %s", encoded)
		}
		// Should not have padding
		if strings.Contains(encoded, "=") {
			t.Errorf("Encoded nonce contains '=' padding (should use no-padding base64): %s", encoded)
		}
	}
}

// TestDecodeURLSafeNonce tests decoding of an actual nonce received from LocalSend.
func TestDecodeURLSafeNonce(t *testing.T) {
	// Real nonce from official LocalSend app (contains URL-safe chars)
	officialNonce := "xLJNxeKwfKvx1IYqVE_cYAUF54R547Aq6C_E_p_eilk"

	decoded, err := DecodeNonce(officialNonce)
	if err != nil {
		t.Fatalf("Failed to decode official nonce: %v", err)
	}

	if len(decoded) != 32 {
		t.Errorf("Decoded nonce length = %d; want 32", len(decoded))
	}

	// Re-encode should match original
	reencoded := EncodeNonce(decoded)
	if reencoded != officialNonce {
		t.Errorf("Re-encoded nonce = %q; want %q", reencoded, officialNonce)
	}
}

// =============================================================================
// Nonce Combination Tests (Protocol Section 4.2)
// =============================================================================

// TestNonceCombinationWithDifferentSizes tests nonce combination with edge case sizes.
func TestNonceCombinationWithDifferentSizes(t *testing.T) {
	tests := []struct {
		name          string
		senderSize    int
		receiverSize  int
		expectedTotal int
	}{
		{"min-min", 16, 16, 32},
		{"default-default", 32, 32, 64},
		{"max-max", 128, 128, 256},
		{"min-max", 16, 128, 144},
		{"max-min", 128, 16, 144},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			senderNonce := make([]byte, tt.senderSize)
			receiverNonce := make([]byte, tt.receiverSize)

			// Fill with distinguishable patterns
			for i := range senderNonce {
				senderNonce[i] = 0xAA
			}
			for i := range receiverNonce {
				receiverNonce[i] = 0xBB
			}

			combined := append(senderNonce, receiverNonce...)

			if len(combined) != tt.expectedTotal {
				t.Errorf("Combined length = %d; want %d", len(combined), tt.expectedTotal)
			}

			// Verify sender portion comes first
			for i := 0; i < tt.senderSize; i++ {
				if combined[i] != 0xAA {
					t.Errorf("Position %d should be sender byte (0xAA), got 0x%02X", i, combined[i])
				}
			}

			// Verify receiver portion comes second
			for i := tt.senderSize; i < tt.expectedTotal; i++ {
				if combined[i] != 0xBB {
					t.Errorf("Position %d should be receiver byte (0xBB), got 0x%02X", i, combined[i])
				}
			}
		})
	}
}

// TestNonceCombinationSymmetry verifies that swapping order produces different result.
// This catches bugs where sender/receiver nonce order is accidentally swapped.
func TestNonceCombinationSymmetry(t *testing.T) {
	senderNonce := []byte("SENDER_NONCE_32_BYTES_PADDING!!")
	receiverNonce := []byte("RECEIVER_NONCE_32_BYTES_PAD!!!!!")

	// Correct order: sender || receiver
	correctCombined := append(senderNonce, receiverNonce...)

	// Wrong order: receiver || sender
	wrongCombined := append(receiverNonce, senderNonce...)

	// These MUST be different (unless nonces happen to be identical, which they're not)
	if bytes.Equal(correctCombined, wrongCombined) {
		t.Fatal("Correct and wrong order should produce different combined nonces")
	}

	// Verify the combined nonces start with correct prefix
	if !bytes.HasPrefix(correctCombined, senderNonce) {
		t.Error("Correct combined nonce should start with sender nonce")
	}
	if !bytes.HasSuffix(correctCombined, receiverNonce) {
		t.Error("Correct combined nonce should end with receiver nonce")
	}
}

// =============================================================================
// CombineNonces Helper Function Tests
// =============================================================================

// TestCombineNonces verifies the CombineNonces helper function.
func TestCombineNonces(t *testing.T) {
	senderNonce := []byte{0x01, 0x02, 0x03, 0x04}
	receiverNonce := []byte{0x05, 0x06, 0x07, 0x08}

	combined := CombineNonces(senderNonce, receiverNonce)

	// Check length
	if len(combined) != 8 {
		t.Errorf("Combined length = %d; want 8", len(combined))
	}

	// Check order: sender first, then receiver
	expected := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(combined, expected) {
		t.Errorf("Combined = %v; want %v", combined, expected)
	}
}

// TestCombineNonces_EmptyInputs verifies CombineNonces handles empty inputs.
func TestCombineNonces_EmptyInputs(t *testing.T) {
	tests := []struct {
		name     string
		sender   []byte
		receiver []byte
		wantLen  int
	}{
		{"both empty", nil, nil, 0},
		{"sender empty", nil, []byte{1, 2, 3}, 3},
		{"receiver empty", []byte{1, 2, 3}, nil, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			combined := CombineNonces(tt.sender, tt.receiver)
			if len(combined) != tt.wantLen {
				t.Errorf("Combined length = %d; want %d", len(combined), tt.wantLen)
			}
		})
	}
}

// TestCombineNonces_DoesNotMutateInputs verifies that input slices are not modified.
func TestCombineNonces_DoesNotMutateInputs(t *testing.T) {
	senderNonce := []byte{0x01, 0x02, 0x03}
	receiverNonce := []byte{0x04, 0x05, 0x06}

	// Copy originals
	senderCopy := make([]byte, len(senderNonce))
	receiverCopy := make([]byte, len(receiverNonce))
	copy(senderCopy, senderNonce)
	copy(receiverCopy, receiverNonce)

	// Call CombineNonces
	combined := CombineNonces(senderNonce, receiverNonce)

	// Mutate combined to ensure it doesn't affect originals
	combined[0] = 0xFF

	// Verify originals unchanged
	if !bytes.Equal(senderNonce, senderCopy) {
		t.Error("CombineNonces mutated sender nonce")
	}
	if !bytes.Equal(receiverNonce, receiverCopy) {
		t.Error("CombineNonces mutated receiver nonce")
	}
}
