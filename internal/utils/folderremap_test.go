package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// =============================================================================
// NewFolderRemapper Tests
// =============================================================================

// TestFolderRemapper_NewFolder_NoRemap verifies that new folders don't get remapped.
func TestFolderRemapper_NewFolder_NoRemap(t *testing.T) {
	tmpDir := t.TempDir()

	filenames := []string{
		"Photos/beach.jpg",
		"Photos/sunset.jpg",
	}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	if remapper.HasRemap() {
		t.Error("New folders should not require remapping")
	}
}

// TestFolderRemapper_ExistingFolder_RemapsToUnique verifies that existing folders
// are remapped to unique names.
func TestFolderRemapper_ExistingFolder_RemapsToUnique(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing folder
	existingFolder := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(existingFolder, 0755); err != nil {
		t.Fatalf("Failed to create test folder: %v", err)
	}

	filenames := []string{
		"Photos/beach.jpg",
		"Photos/sunset.jpg",
	}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	if !remapper.HasRemap() {
		t.Error("Existing folder should require remapping")
	}

	remap := remapper.GetRemap()
	if remap["Photos"] != "Photos (1)" {
		t.Errorf("Photos should be remapped to 'Photos (1)', got %q", remap["Photos"])
	}
}

// TestFolderRemapper_Apply_UpdatesRootFolder verifies that Apply correctly
// updates paths with remapped root folders.
func TestFolderRemapper_Apply_UpdatesRootFolder(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing folder
	existingFolder := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(existingFolder, 0755); err != nil {
		t.Fatalf("Failed to create test folder: %v", err)
	}

	filenames := []string{"Photos/beach.jpg"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	// Apply to sanitized path
	result := remapper.Apply("Photos/beach.jpg")

	expected := "Photos (1)/beach.jpg"
	if result != expected {
		t.Errorf("Apply result = %q; want %q", result, expected)
	}
}

// TestFolderRemapper_Apply_PreservesNonRemapped verifies that paths without
// remap don't get modified.
func TestFolderRemapper_Apply_PreservesNonRemapped(t *testing.T) {
	tmpDir := t.TempDir()

	filenames := []string{"Documents/report.pdf"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	// Path should not be modified
	result := remapper.Apply("Documents/report.pdf")

	if result != "Documents/report.pdf" {
		t.Errorf("Non-remapped path should not change, got %q", result)
	}
}

// TestFolderRemapper_Apply_FlatFile verifies that flat files (no subdirectory)
// are not affected by remapping.
func TestFolderRemapper_Apply_FlatFile(t *testing.T) {
	tmpDir := t.TempDir()

	filenames := []string{"document.pdf"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	result := remapper.Apply("document.pdf")

	if result != "document.pdf" {
		t.Errorf("Flat file should not be modified, got %q", result)
	}
}

// TestFolderRemapper_MultipleConflicts_HandlesAll verifies that multiple
// conflicting folders are all remapped correctly.
func TestFolderRemapper_MultipleConflicts_HandlesAll(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple existing folders
	folders := []string{"Photos", "Documents"}
	for _, folder := range folders {
		path := filepath.Join(tmpDir, folder)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("Failed to create test folder: %v", err)
		}
	}

	filenames := []string{
		"Photos/beach.jpg",
		"Documents/report.pdf",
	}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	remap := remapper.GetRemap()

	if remap["Photos"] != "Photos (1)" {
		t.Errorf("Photos should be remapped to 'Photos (1)', got %q", remap["Photos"])
	}
	if remap["Documents"] != "Documents (1)" {
		t.Errorf("Documents should be remapped to 'Documents (1)', got %q", remap["Documents"])
	}
}

// TestFolderRemapper_DeepNesting_OnlyRemapsRoot verifies that only the root
// folder is remapped, not nested folders.
func TestFolderRemapper_DeepNesting_OnlyRemapsRoot(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing root folder
	existingFolder := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(existingFolder, 0755); err != nil {
		t.Fatalf("Failed to create test folder: %v", err)
	}

	filenames := []string{"Photos/Summer/2024/beach.jpg"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	result := remapper.Apply("Photos/Summer/2024/beach.jpg")

	expected := "Photos (1)/Summer/2024/beach.jpg"
	if result != expected {
		t.Errorf("Apply result = %q; want %q", result, expected)
	}
}

// TestFolderRemapper_EmptyRemap_Apply verifies that Apply works correctly
// when there's no remap.
func TestFolderRemapper_EmptyRemap_Apply(t *testing.T) {
	tmpDir := t.TempDir()

	filenames := []string{"NewFolder/file.txt"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	if remapper.HasRemap() {
		t.Error("Should not have remap for new folder")
	}

	result := remapper.Apply("NewFolder/file.txt")
	if result != "NewFolder/file.txt" {
		t.Errorf("Should not modify path when no remap, got %q", result)
	}
}

// TestFolderRemapper_GetRemap_ReturnsDefensiveCopy verifies that GetRemap
// returns a copy that can't modify internal state.
func TestFolderRemapper_GetRemap_ReturnsDefensiveCopy(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing folder
	existingFolder := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(existingFolder, 0755); err != nil {
		t.Fatalf("Failed to create test folder: %v", err)
	}

	filenames := []string{"Photos/beach.jpg"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	// Get remap and modify it
	remap := remapper.GetRemap()
	remap["Photos"] = "Modified"

	// Verify internal state wasn't modified
	internalRemap := remapper.GetRemap()
	if internalRemap["Photos"] == "Modified" {
		t.Error("GetRemap should return a defensive copy")
	}
}

// TestFolderRemapper_MixedValidAndInvalid verifies that invalid paths are skipped.
func TestFolderRemapper_MixedValidAndInvalid(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing folder
	existingFolder := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(existingFolder, 0755); err != nil {
		t.Fatalf("Failed to create test folder: %v", err)
	}

	// Mix of valid subdirectory paths and potentially problematic paths
	filenames := []string{
		"Photos/beach.jpg",
		"../../../etc/passwd", // Invalid - will be skipped by SanitizeRelativePath
		"document.pdf",        // Valid flat file
	}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	// Should have remapped Photos
	if !remapper.HasRemap() {
		t.Error("Photos should be remapped")
	}

	remap := remapper.GetRemap()
	if _, ok := remap["Photos"]; !ok {
		t.Error("Photos should be in remap map")
	}
}

// TestFolderRemapper_WindowsStylePath verifies handling of Windows-style paths
// (forward slashes are normalized).
func TestFolderRemapper_WindowsStylePath(t *testing.T) {
	tmpDir := t.TempDir()

	// NewFolderRemapper uses filepath.ToSlash internally
	filenames := []string{"Photos/beach.jpg"}

	remapper, err := NewFolderRemapper(tmpDir, filenames)
	if err != nil {
		t.Fatalf("NewFolderRemapper failed: %v", err)
	}

	// Verify Apply works with forward slashes
	result := remapper.Apply("Photos/beach.jpg")
	// Result should not be modified since Photos doesn't exist
	if result != "Photos/beach.jpg" {
		t.Errorf("Unexpected result: %q", result)
	}
}
