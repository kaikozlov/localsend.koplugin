package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// TestGenerateSelfSignedCert tests certificate generation functionality.
func TestGenerateSelfSignedCert(t *testing.T) {
	// Generate key pair
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Generate self-signed certificate
	certPEM, keyPEM, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Verify PEM format markers
	if !strings.HasPrefix(certPEM, "-----BEGIN CERTIFICATE-----") {
		t.Error("Certificate PEM missing BEGIN marker")
	}
	if !strings.HasSuffix(strings.TrimSpace(certPEM), "-----END CERTIFICATE-----") {
		t.Error("Certificate PEM missing END marker")
	}
	if !strings.HasPrefix(keyPEM, "-----BEGIN PRIVATE KEY-----") {
		t.Error("Private key PEM missing BEGIN marker")
	}
	if !strings.HasSuffix(strings.TrimSpace(keyPEM), "-----END PRIVATE KEY-----") {
		t.Error("Private key PEM missing END marker")
	}

	// Parse and verify certificate properties
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}

	// Verify CommonName
	if cert.Subject.CommonName != "LocalSend User" {
		t.Errorf("CommonName = %q; want %q", cert.Subject.CommonName, "LocalSend User")
	}

	// Verify 10 year validity (allowing for small time differences)
	validity := cert.NotAfter.Sub(cert.NotBefore)
	expectedValidity := 10 * 365 * 24 * time.Hour
	// Allow up to 24 hours difference due to leap years and timestamp precision
	diff := validity - expectedValidity
	if diff < 0 {
		diff = -diff
	}
	if diff > 24*time.Hour {
		t.Errorf("Certificate validity period = %v; want ~%v", validity, expectedValidity)
	}

	// Verify certificate is currently valid
	now := time.Now()
	if now.Before(cert.NotBefore) {
		t.Error("Certificate NotBefore is in the future")
	}
	if now.After(cert.NotAfter) {
		t.Error("Certificate NotAfter is in the past")
	}

	// Verify key usage
	expectedKeyUsage := x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
	if cert.KeyUsage != expectedKeyUsage {
		t.Errorf("KeyUsage = %v; want %v", cert.KeyUsage, expectedKeyUsage)
	}

	// Verify extended key usage
	if len(cert.ExtKeyUsage) != 2 {
		t.Errorf("ExtKeyUsage length = %d; want 2", len(cert.ExtKeyUsage))
	}
	hasServerAuth := false
	hasClientAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
		if usage == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	if !hasServerAuth {
		t.Error("Missing ExtKeyUsageServerAuth")
	}
	if !hasClientAuth {
		t.Error("Missing ExtKeyUsageClientAuth")
	}

	// Verify public key type
	pubKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		t.Errorf("Public key type = %T; want ed25519.PublicKey", cert.PublicKey)
	}

	// Verify public key matches the signing key
	if !bytes.Equal(pubKey, key.PublicKey()) {
		t.Error("Certificate public key does not match signing key")
	}
}

// TestPublicKeyFromCertDER tests public key extraction from certificate DER.
func TestPublicKeyFromCertDER(t *testing.T) {
	// Generate key pair and certificate
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	certPEM, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Parse certificate to get DER
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	// Extract public key from DER
	pubKeyBytes, err := PublicKeyFromCertDER(block.Bytes)
	if err != nil {
		t.Fatalf("PublicKeyFromCertDER failed: %v", err)
	}

	// Verify non-empty
	if len(pubKeyBytes) == 0 {
		t.Fatal("PublicKeyFromCertDER returned empty bytes")
	}

	// Verify it's valid PKIX format by parsing it
	pubKey, err := x509.ParsePKIXPublicKey(pubKeyBytes)
	if err != nil {
		t.Fatalf("Failed to parse extracted public key as PKIX: %v", err)
	}

	// Verify it's an Ed25519 key
	ed25519PubKey, ok := pubKey.(ed25519.PublicKey)
	if !ok {
		t.Errorf("Parsed public key type = %T; want ed25519.PublicKey", pubKey)
	}

	// Verify it matches the original key
	if !bytes.Equal(ed25519PubKey, key.PublicKey()) {
		t.Error("Extracted public key does not match original key")
	}
}

// TestFingerprintFromCertDER tests certificate fingerprint generation.
func TestFingerprintFromCertDER(t *testing.T) {
	// Generate key pair and certificate
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	certPEM, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Parse certificate to get DER
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	// Get fingerprint
	fingerprint := FingerprintFromCertDER(block.Bytes)

	// Verify non-empty
	if fingerprint == "" {
		t.Fatal("FingerprintFromCertDER returned empty string")
	}

	wantDigest := sha256.Sum256(block.Bytes)
	wantFingerprint := strings.ToUpper(hex.EncodeToString(wantDigest[:]))
	if fingerprint != wantFingerprint {
		t.Errorf("Fingerprint = %q; want certificate DER SHA-256 %q", fingerprint, wantFingerprint)
	}

	// Verify consistent - calling again should produce same result
	fingerprint2 := FingerprintFromCertDER(block.Bytes)
	if fingerprint != fingerprint2 {
		t.Error("FingerprintFromCertDER is not consistent")
	}

	// SHA-256 is rendered as 64 uppercase hexadecimal characters.
	if len(fingerprint) != 64 || fingerprint != strings.ToUpper(fingerprint) {
		t.Errorf("Fingerprint format = %q; want 64 uppercase hex characters", fingerprint)
	}
}

// TestPublicKeyFromCertDERGenerated tests round-trip key extraction.
func TestPublicKeyFromCertDERGenerated(t *testing.T) {
	// Generate key pair
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Generate certificate
	certPEM, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Parse certificate to get DER
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	// Extract public key from certificate
	extractedPubKeyBytes, err := PublicKeyFromCertDER(block.Bytes)
	if err != nil {
		t.Fatalf("PublicKeyFromCertDER failed: %v", err)
	}

	// Get original public key in PKIX format
	originalPubKeyBytes, err := x509.MarshalPKIXPublicKey(key.PublicKey())
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey failed: %v", err)
	}

	// Compare - they should be identical
	if !bytes.Equal(extractedPubKeyBytes, originalPubKeyBytes) {
		t.Error("Extracted public key does not match original public key")
	}

	// Parse both and verify they're functionally equivalent
	extractedPubKey, err := x509.ParsePKIXPublicKey(extractedPubKeyBytes)
	if err != nil {
		t.Fatalf("Failed to parse extracted public key: %v", err)
	}

	originalPubKey, err := x509.ParsePKIXPublicKey(originalPubKeyBytes)
	if err != nil {
		t.Fatalf("Failed to parse original public key: %v", err)
	}

	extractedEd25519, ok1 := extractedPubKey.(ed25519.PublicKey)
	originalEd25519, ok2 := originalPubKey.(ed25519.PublicKey)

	if !ok1 || !ok2 {
		t.Fatal("Public keys are not Ed25519")
	}

	if !bytes.Equal(extractedEd25519, originalEd25519) {
		t.Error("Parsed public keys are not equal")
	}
}

// TestFingerprintDifferent verifies different certificates have different fingerprints.
func TestFingerprintDifferent(t *testing.T) {
	// Generate first key and certificate
	key1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair (1) failed: %v", err)
	}

	certPEM1, _, err := GenerateSelfSignedCert(key1)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert (1) failed: %v", err)
	}

	block1, _ := pem.Decode([]byte(certPEM1))
	if block1 == nil {
		t.Fatal("Failed to decode certificate PEM (1)")
	}

	fingerprint1 := FingerprintFromCertDER(block1.Bytes)

	// Generate second key and certificate
	key2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair (2) failed: %v", err)
	}

	certPEM2, _, err := GenerateSelfSignedCert(key2)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert (2) failed: %v", err)
	}

	block2, _ := pem.Decode([]byte(certPEM2))
	if block2 == nil {
		t.Fatal("Failed to decode certificate PEM (2)")
	}

	fingerprint2 := FingerprintFromCertDER(block2.Bytes)

	// Verify fingerprints are different
	if fingerprint1 == fingerprint2 {
		t.Error("Different certificates produced identical fingerprints")
	}

	// Verify both fingerprints are non-empty
	if fingerprint1 == "" {
		t.Error("Fingerprint 1 is empty")
	}
	if fingerprint2 == "" {
		t.Error("Fingerprint 2 is empty")
	}
}

// TestPublicKeyFromCertDERInvalid tests error handling with invalid input.
func TestPublicKeyFromCertDERInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{
			name:  "empty input",
			input: []byte{},
		},
		{
			name:  "invalid DER",
			input: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name:  "random bytes",
			input: []byte("this is not a certificate"),
		},
		{
			name:  "nil input",
			input: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PublicKeyFromCertDER(tt.input)
			if err == nil {
				t.Error("PublicKeyFromCertDER should fail with invalid input")
			}
		})
	}
}

// TestFingerprintFromCertDERInvalid tests fingerprint error handling.
func TestFingerprintFromCertDERInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{
			name:  "empty input",
			input: []byte{},
		},
		{
			name:  "invalid DER",
			input: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name:  "nil input",
			input: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fingerprint := FingerprintFromCertDER(tt.input)
			// FingerprintFromCertDER returns empty string on error
			if fingerprint != "" {
				t.Error("FingerprintFromCertDER should return empty string for invalid input")
			}
		})
	}
}

// TestGenerateSelfSignedCertMultiple tests that generating multiple certificates
// produces unique certificates with different serial numbers.
func TestGenerateSelfSignedCertMultiple(t *testing.T) {
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Generate multiple certificates with the same key
	certPEM1, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert (1) failed: %v", err)
	}

	certPEM2, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert (2) failed: %v", err)
	}

	// Parse certificates
	block1, _ := pem.Decode([]byte(certPEM1))
	if block1 == nil {
		t.Fatal("Failed to decode certificate PEM (1)")
	}

	block2, _ := pem.Decode([]byte(certPEM2))
	if block2 == nil {
		t.Fatal("Failed to decode certificate PEM (2)")
	}

	cert1, err := x509.ParseCertificate(block1.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate (1): %v", err)
	}

	cert2, err := x509.ParseCertificate(block2.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate (2): %v", err)
	}

	// Verify serial numbers are different
	if cert1.SerialNumber.Cmp(cert2.SerialNumber) == 0 {
		t.Error("Multiple certificates generated with same serial number")
	}

	// Verify both have the same public key (since they use the same signing key)
	pubKey1, ok1 := cert1.PublicKey.(ed25519.PublicKey)
	pubKey2, ok2 := cert2.PublicKey.(ed25519.PublicKey)

	if !ok1 || !ok2 {
		t.Fatal("Certificate public keys are not Ed25519")
	}

	if !bytes.Equal(pubKey1, pubKey2) {
		t.Error("Certificates generated from same key have different public keys")
	}
}

// TestCertificateKeyPairConsistency verifies that the private key PEM matches
// the public key in the certificate.
func TestCertificateKeyPairConsistency(t *testing.T) {
	// Generate key pair
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Generate certificate
	certPEM, keyPEM, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Parse certificate
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}

	// Parse private key
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		t.Fatal("Failed to decode private key PEM")
	}

	privKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse private key: %v", err)
	}

	// Verify it's an Ed25519 private key
	ed25519PrivKey, ok := privKey.(ed25519.PrivateKey)
	if !ok {
		t.Fatalf("Private key type = %T; want ed25519.PrivateKey", privKey)
	}

	// Extract public key from private key
	ed25519PubKey := ed25519PrivKey.Public().(ed25519.PublicKey)

	// Extract public key from certificate
	certPubKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("Certificate public key type = %T; want ed25519.PublicKey", cert.PublicKey)
	}

	// Verify they match
	if !bytes.Equal(ed25519PubKey, certPubKey) {
		t.Error("Private key does not match certificate public key")
	}
}

// TestFingerprintStability verifies that fingerprints are stable across
// multiple calls and certificate re-parsing.
func TestFingerprintStability(t *testing.T) {
	// Generate key and certificate
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	certPEM, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Parse certificate
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	// Get fingerprint multiple times
	fingerprints := make([]string, 5)
	for i := 0; i < 5; i++ {
		fingerprints[i] = FingerprintFromCertDER(block.Bytes)
	}

	// Verify all fingerprints are identical
	for i := 1; i < len(fingerprints); i++ {
		if fingerprints[i] != fingerprints[0] {
			t.Errorf("Fingerprint %d = %q; want %q", i, fingerprints[i], fingerprints[0])
		}
	}

	// Re-parse the DER and verify fingerprint is still the same
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}

	// Re-encode to DER and get fingerprint
	fingerprint2 := FingerprintFromCertDER(cert.Raw)
	if fingerprint2 != fingerprints[0] {
		t.Errorf("Fingerprint after re-parsing = %q; want %q", fingerprint2, fingerprints[0])
	}
}

// TestVerifyingKeyFromCert tests extracting VerifyingKey from certificate.
func TestVerifyingKeyFromCert(t *testing.T) {
	// Generate key pair and certificate
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	certPEM, _, err := GenerateSelfSignedCert(key)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Parse certificate
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}

	// Extract VerifyingKey from certificate
	verifyKey, err := VerifyingKeyFromCert(cert)
	if err != nil {
		t.Fatalf("VerifyingKeyFromCert failed: %v", err)
	}

	// Verify it's Ed25519
	if verifyKey.SignatureMethod() != "ed25519" {
		t.Errorf("SignatureMethod() = %q; want 'ed25519'", verifyKey.SignatureMethod())
	}

	// Test that we can verify a token with this key
	token, err := key.GenerateTokenTimestamp()
	if err != nil {
		t.Fatalf("GenerateTokenTimestamp failed: %v", err)
	}

	if err := VerifyTokenTimestamp(verifyKey, token); err != nil {
		t.Errorf("Token verification failed: %v", err)
	}
}

// TestVerifyingKeyFromCertNil tests error handling with nil certificate.
func TestVerifyingKeyFromCertNil(t *testing.T) {
	_, err := VerifyingKeyFromCert(nil)
	if err == nil {
		t.Error("VerifyingKeyFromCert(nil) should return error")
	}
}
