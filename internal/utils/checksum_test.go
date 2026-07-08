package utils

import (
	"crypto/sha256"
	"testing"
)

func TestVerifyChecksum_EmptyExpected(t *testing.T) {
	hasher := sha256.New()
	hasher.Write([]byte("test data"))

	err := VerifyChecksum("", hasher)
	if err != nil {
		t.Errorf("VerifyChecksum with empty expected should return nil, got %v", err)
	}
}

func TestVerifyChecksum_Matching(t *testing.T) {
	data := []byte("test data")

	// Compute expected checksum
	expectedHasher := sha256.New()
	expectedHasher.Write(data)
	expected := ComputeChecksum(expectedHasher)

	// Verify against a fresh hasher with same data
	verifyHasher := sha256.New()
	verifyHasher.Write(data)

	err := VerifyChecksum(expected, verifyHasher)
	if err != nil {
		t.Errorf("VerifyChecksum with matching checksum should return nil, got %v", err)
	}
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	hasher := sha256.New()
	hasher.Write([]byte("test data"))

	// Use a wrong checksum
	wrongChecksum := "0000000000000000000000000000000000000000000000000000000000000000"

	err := VerifyChecksum(wrongChecksum, hasher)
	if err != ErrChecksumMismatch {
		t.Errorf("VerifyChecksum with mismatch should return ErrChecksumMismatch, got %v", err)
	}
}

func TestComputeChecksum(t *testing.T) {
	hasher := sha256.New()
	hasher.Write([]byte("test"))

	checksum := ComputeChecksum(hasher)

	// SHA256 of "test" is known
	expected := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	if checksum != expected {
		t.Errorf("ComputeChecksum = %s; want %s", checksum, expected)
	}
}

func TestComputeChecksum_EmptyInput(t *testing.T) {
	hasher := sha256.New()

	checksum := ComputeChecksum(hasher)

	// SHA256 of empty string
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if checksum != expected {
		t.Errorf("ComputeChecksum = %s; want %s", checksum, expected)
	}
}
