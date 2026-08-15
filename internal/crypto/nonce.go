package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
)

// NonceSize is the default size for generated nonces.
const NonceSize = 32

// GenerateNonce generates a cryptographically secure random nonce.
func GenerateNonce() ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

// ValidateNonce checks if a nonce has a valid length (16-128 bytes).
// This matches the official Rust LocalSend implementation.
// Note: 8-byte timestamp salts are handled separately in token generation.
func ValidateNonce(nonce []byte) bool {
	return len(nonce) >= 16 && len(nonce) <= 128
}

// ValidateSalt checks if a salt has a valid length for token operations.
// Salts can be either timestamps (8 bytes) or nonces (16-128 bytes).
func ValidateSalt(salt []byte) bool {
	return len(salt) >= 8 && len(salt) <= 128
}

// EncodeNonce encodes a nonce to URL-safe base64 (no padding).
// Uses URL-safe encoding to match official LocalSend protocol.
func EncodeNonce(nonce []byte) string {
	return base64.RawURLEncoding.EncodeToString(nonce)
}

// DecodeNonce decodes a URL-safe base64-encoded protocol nonce and enforces
// the official 16-128 byte bounds. Eight-byte timestamp salts are not nonces.
func DecodeNonce(encoded string) ([]byte, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if !ValidateNonce(nonce) {
		return nil, errors.New("invalid nonce length")
	}
	return nonce, nil
}

// CombineNonces concatenates sender and receiver nonces for token verification.
// Order is sender_nonce || receiver_nonce per LocalSend protocol spec.
func CombineNonces(senderNonce, receiverNonce []byte) []byte {
	combined := make([]byte, len(senderNonce)+len(receiverNonce))
	copy(combined, senderNonce)
	copy(combined[len(senderNonce):], receiverNonce)
	return combined
}
