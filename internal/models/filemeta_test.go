package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// FileMeta JSON Marshaling Tests
// =============================================================================

// TestFileMeta_JSONMarshaling_CorrectFieldNames verifies that FileMeta
// uses the correct JSON field names per protocol spec.
func TestFileMeta_JSONMarshaling_CorrectFieldNames(t *testing.T) {
	meta := FileMeta{
		Id:       "file-123",
		Filename: "test.txt",
		Size:     1024,
		FileMIME: "text/plain",
		Checksum: "abc123hash",
		Preview:  "base64data",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Failed to marshal FileMeta: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Verify expected field names match protocol spec
	expectedFields := map[string]string{
		"id":       "file-123",
		"fileName": "test.txt",
		"fileType": "text/plain",
		"sha256":   "abc123hash",
		"preview":  "base64data",
	}

	for field, expected := range expectedFields {
		if result[field] != expected {
			t.Errorf("Field %q = %v; want %q", field, result[field], expected)
		}
	}

	// Verify size is present
	if result["size"] != float64(1024) {
		t.Errorf("size = %v; want 1024", result["size"])
	}

	// Verify FullPath is not serialized (json:"-")
	if _, ok := result["FullPath"]; ok {
		t.Error("FullPath should not be serialized")
	}
	if _, ok := result["fullPath"]; ok {
		t.Error("fullPath should not be serialized")
	}
}

// TestFileMeta_OmitsEmptyOptionalFields verifies optional fields are omitted when empty.
func TestFileMeta_OmitsEmptyOptionalFields(t *testing.T) {
	meta := FileMeta{
		Id:       "file-123",
		Filename: "test.txt",
		Size:     1024,
		FileMIME: "text/plain",
		// Optional fields left empty
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Failed to marshal FileMeta: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// These fields should be omitted when empty
	omittedFields := []string{"sha256", "preview", "metadata"}
	for _, field := range omittedFields {
		if _, ok := result[field]; ok {
			t.Errorf("Field %q should be omitted when empty", field)
		}
	}
}

func TestFileMeta_UnmarshalJSON_RejectsFloatSize(t *testing.T) {
	var meta FileMeta
	err := json.Unmarshal([]byte(`{"id":"file-123","fileName":"test.txt","size":1024.0,"fileType":"text/plain"}`), &meta)
	if err == nil {
		t.Fatal("expected float-encoded size to be rejected")
	}
}

func TestFileMeta_UnmarshalJSON_RejectsStringSize(t *testing.T) {
	var meta FileMeta
	err := json.Unmarshal([]byte(`{"id":"file-123","fileName":"test.txt","size":"1024","fileType":"text/plain"}`), &meta)
	if err == nil {
		t.Fatal("expected string-encoded size to be rejected")
	}
}

// =============================================================================
// GenFileMeta Tests
// =============================================================================

// TestGenFileMeta_SetsCorrectMIMEType verifies that GenFileMeta detects MIME types correctly.
func TestGenFileMeta_SetsCorrectMIMEType(t *testing.T) {
	tmpDir := t.TempDir()

	testCases := []struct {
		filename     string
		content      string
		expectedMIME string
	}{
		{"test.txt", "hello world", "text/plain"},
		{"test.json", "{}", "application/json"},
		{"test.html", "<html></html>", "text/html"},
		{"test.pdf", "%PDF-1.4", "application/pdf"},
		{"test.unknown", "data", "text/plain"}, // Unknown extensions default to text/plain
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			path := filepath.Join(tmpDir, tc.filename)
			if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			meta, err := GenFileMeta(path)
			if err != nil {
				t.Fatalf("GenFileMeta failed: %v", err)
			}

			// MIME type detection may include parameters such as a charset.
			if !strings.HasPrefix(meta.FileMIME, tc.expectedMIME) {
				t.Errorf("FileMIME = %q; want prefix %q", meta.FileMIME, tc.expectedMIME)
			}
		})
	}
}

// TestGenFileMeta_SetsCorrectSize verifies that GenFileMeta sets the correct file size.
func TestGenFileMeta_SetsCorrectSize(t *testing.T) {
	tmpDir := t.TempDir()
	content := "test content with known size"
	path := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := GenFileMeta(path)
	if err != nil {
		t.Fatalf("GenFileMeta failed: %v", err)
	}

	expectedSize := int64(len(content))
	if meta.Size != expectedSize {
		t.Errorf("Size = %d; want %d", meta.Size, expectedSize)
	}
}

// TestGenFileMeta_GeneratesUniqueId verifies that GenFileMeta generates a unique ID.
func TestGenFileMeta_GeneratesUniqueId(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta1, _ := GenFileMeta(path)
	meta2, _ := GenFileMeta(path)

	if meta1.Id == "" {
		t.Error("Id should not be empty")
	}
	if meta1.Id == meta2.Id {
		t.Error("Each call should generate a unique ID")
	}
}

func TestGenFileMeta_PreservesNanosecondTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timestamps.bin")
	if err := os.WriteFile(path, []byte("timestamp precision"), 0644); err != nil {
		t.Fatal(err)
	}
	want := time.Unix(1_700_000_000, 123_456_789)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}

	meta, err := GenFileMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339Nano, meta.Metadata.Modified)
	if err != nil {
		t.Fatalf("parse modified timestamp %q: %v", meta.Metadata.Modified, err)
	}
	if !got.Equal(want) {
		t.Fatalf("modified timestamp = %s; want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// TestGenFileMeta_ComputesChecksum verifies that GenFileMeta computes SHA256 checksum.
func TestGenFileMeta_ComputesChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := GenFileMeta(path)
	if err != nil {
		t.Fatalf("GenFileMeta failed: %v", err)
	}

	if meta.Checksum == "" {
		t.Error("Checksum should not be empty")
	}
	// SHA256 produces 64 hex characters
	if len(meta.Checksum) != 64 {
		t.Errorf("Checksum length = %d; want 64 (SHA256 hex)", len(meta.Checksum))
	}
}

// TestGenFileMeta_NonexistentFile_ReturnsError verifies error handling for missing files.
func TestGenFileMeta_NonexistentFile_ReturnsError(t *testing.T) {
	_, err := GenFileMeta("/nonexistent/path/file.txt")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

// TestGenFileMeta_SetsMetadata verifies that GenFileMeta sets file metadata.
func TestGenFileMeta_SetsMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := GenFileMeta(path)
	if err != nil {
		t.Fatalf("GenFileMeta failed: %v", err)
	}

	if meta.Metadata == nil {
		t.Fatal("Metadata should not be nil")
	}
	if meta.Metadata.Modified == "" {
		t.Error("Modified timestamp should not be empty")
	}
}

// =============================================================================
// GenFileMetaWithBase Tests
// =============================================================================

// TestGenFileMetaWithBase_CalculatesRelativePath verifies that GenFileMetaWithBase
// correctly calculates relative paths for folder transfers.
func TestGenFileMetaWithBase_CalculatesRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "Photos", "Summer")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	filePath := filepath.Join(subDir, "beach.jpg")
	if err := os.WriteFile(filePath, []byte("fake image data"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := GenFileMetaWithBase(filePath, tmpDir)
	if err != nil {
		t.Fatalf("GenFileMetaWithBase failed: %v", err)
	}

	// Relative path should use forward slashes for protocol compatibility
	expected := "Photos/Summer/beach.jpg"
	if meta.Filename != expected {
		t.Errorf("Filename = %q; want %q", meta.Filename, expected)
	}
}

// TestGenFileMetaWithBase_FlatFile verifies behavior for files without subdirectories.
func TestGenFileMetaWithBase_FlatFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "document.pdf")
	if err := os.WriteFile(filePath, []byte("fake pdf"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := GenFileMetaWithBase(filePath, tmpDir)
	if err != nil {
		t.Fatalf("GenFileMetaWithBase failed: %v", err)
	}

	if meta.Filename != "document.pdf" {
		t.Errorf("Filename = %q; want 'document.pdf'", meta.Filename)
	}
}

// TestGenFileMetaWithBase_StoresFullPath verifies that FullPath is set.
func TestGenFileMetaWithBase_StoresFullPath(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := GenFileMetaWithBase(filePath, tmpDir)
	if err != nil {
		t.Fatalf("GenFileMetaWithBase failed: %v", err)
	}

	if meta.FullPath != filePath {
		t.Errorf("FullPath = %q; want %q", meta.FullPath, filePath)
	}
}

// =============================================================================
// FileMetadata Tests
// =============================================================================

// TestFileMetadata_JSONMarshaling verifies FileMetadata JSON serialization.
func TestFileMetadata_JSONMarshaling(t *testing.T) {
	meta := FileMetadata{
		Modified: "2024-01-15T10:30:00Z",
		Accessed: "2024-01-15T11:00:00Z",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Failed to marshal FileMetadata: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	if result["modified"] != "2024-01-15T10:30:00Z" {
		t.Errorf("modified = %v; want '2024-01-15T10:30:00Z'", result["modified"])
	}
	if result["accessed"] != "2024-01-15T11:00:00Z" {
		t.Errorf("accessed = %v; want '2024-01-15T11:00:00Z'", result["accessed"])
	}
}

// TestFileMetadata_OmitsEmptyFields verifies empty timestamps are omitted.
func TestFileMetadata_OmitsEmptyFields(t *testing.T) {
	meta := FileMetadata{
		Modified: "2024-01-15T10:30:00Z",
		// Accessed left empty
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Failed to marshal FileMetadata: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	if _, ok := result["accessed"]; ok {
		t.Error("accessed should be omitted when empty")
	}
}
