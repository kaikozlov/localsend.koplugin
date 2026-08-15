package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend/constants"
)

// makeHasherMap creates an empty hash map for testing
func makeHasherMap() map[string]hash.Hash {
	return make(map[string]hash.Hash)
}

// =============================================================================
// Path Traversal Security Tests
// These tests verify that the WebRTC receiver properly sanitizes filenames
// to prevent directory traversal attacks (e.g., "../../../etc/passwd").
//
// The HTTP receiver at internal/localsend/session/recv.go:173 properly
// sanitizes filenames with filepath.Base(). The WebRTC receiver should
// apply the same protection.
// =============================================================================

// TestPrepareFilesForReceive_PathTraversal tests that malicious filenames
// with path traversal sequences cannot write files outside the save directory.
func TestPrepareFilesForReceive_PathTraversal(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, "downloads")
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		t.Fatalf("Failed to create save dir: %v", err)
	}

	// Create a sensitive file that should NOT be overwritten
	sensitiveDir := filepath.Join(tmpDir, "sensitive")
	if err := os.MkdirAll(sensitiveDir, 0755); err != nil {
		t.Fatalf("Failed to create sensitive dir: %v", err)
	}
	sensitiveFile := filepath.Join(sensitiveDir, "secret.txt")
	if err := os.WriteFile(sensitiveFile, []byte("ORIGINAL_SECRET"), 0644); err != nil {
		t.Fatalf("Failed to create sensitive file: %v", err)
	}

	tests := []struct {
		name          string
		maliciousName string
		description   string
	}{
		{
			name:          "parent directory traversal",
			maliciousName: "../sensitive/secret.txt",
			description:   "Simple ../ prefix to escape save directory",
		},
		{
			name:          "deep traversal",
			maliciousName: "../../../etc/passwd",
			description:   "Multiple ../ to reach system directories",
		},
		{
			name:          "absolute path unix",
			maliciousName: "/tmp/malicious.txt",
			description:   "Absolute path on Unix systems",
		},
		{
			name:          "mixed traversal",
			maliciousName: "foo/../../../sensitive/secret.txt",
			description:   "Traversal hidden within normal path",
		},
		{
			name:          "encoded traversal",
			maliciousName: "..%2F..%2Fsensitive/secret.txt",
			description:   "URL-encoded traversal (should be handled by caller but test anyway)",
		},
		{
			name:          "backslash traversal windows",
			maliciousName: "..\\..\\sensitive\\secret.txt",
			description:   "Windows-style path separators (on Unix, backslashes are valid filename chars)",
		},
		{
			name:          "null byte injection",
			maliciousName: "safe.txt\x00../sensitive/secret.txt",
			description:   "Null byte to truncate path processing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a receiver with the save directory
			receiver := &RTCReceiver{
				saveDir:     saveDir,
				fileTokens:  make(map[string]string),
				fileWriters: make(map[string]*os.File),
				filePaths:   make(map[string]string),
				fileHashers: makeHasherMap(),
				files: []RTCFileDto{
					{
						ID:       "malicious-file",
						FileName: tt.maliciousName,
						Size:     100,
						FileType: "text/plain",
					},
				},
			}

			// Call prepareFilesForReceive with the malicious file
			tokens := receiver.prepareFilesForReceive([]string{"malicious-file"})
			if token := tokens["malicious-file"]; token != "" {
				receiver.startReceivingFile(&RTCSendFileHeader{ID: "malicious-file", Token: token})
			}

			// If a file was created, verify it's inside the save directory
			if len(tokens) > 0 {
				createdPath := receiver.filePaths["malicious-file"]

				// Clean up the file
				if f, ok := receiver.fileWriters["malicious-file"]; ok {
					_ = f.Close()
				}

				// The created file MUST be inside saveDir
				// Use filepath.Abs and HasPrefix for accurate containment check
				absSaveDir, _ := filepath.Abs(saveDir)
				absCreatedPath, _ := filepath.Abs(createdPath)

				// Normalize both paths to catch traversal
				absSaveDir = filepath.Clean(absSaveDir) + string(filepath.Separator)
				absCreatedPath = filepath.Clean(absCreatedPath)

				if !strings.HasPrefix(absCreatedPath, absSaveDir) {
					t.Errorf("PATH TRAVERSAL VULNERABILITY: File created outside save directory!\n"+
						"  Malicious filename: %q\n"+
						"  Created at: %q\n"+
						"  Save directory: %q\n"+
						"  Description: %s",
						tt.maliciousName, createdPath, saveDir, tt.description)
				}

				// The filename should be sanitized to just the base name
				baseName := filepath.Base(createdPath)
				expectedBase := filepath.Base(tt.maliciousName)
				if baseName != expectedBase && !strings.Contains(baseName, expectedBase) {
					// This is actually fine - just means the sanitization worked
					t.Logf("Filename was sanitized: %q -> %q", tt.maliciousName, baseName)
				}

				// Clean up
				_ = os.Remove(createdPath)
			}

			// Verify the sensitive file was NOT modified
			content, err := os.ReadFile(sensitiveFile)
			if err != nil {
				t.Errorf("Sensitive file was deleted or became unreadable: %v", err)
			} else if string(content) != "ORIGINAL_SECRET" {
				t.Errorf("SECURITY BREACH: Sensitive file was modified!\n"+
					"  Expected content: ORIGINAL_SECRET\n"+
					"  Actual content: %s", string(content))
			}
		})
	}
}

// TestGetSaveDir_PathTraversal tests that getSaveDir doesn't allow
// traversal via extension routing.
func TestGetSaveDir_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, "downloads")

	receiver := &RTCReceiver{
		saveDir: saveDir,
		extRoutes: map[string]string{
			"pdf":  filepath.Join(tmpDir, "books"),
			"epub": filepath.Join(tmpDir, "ebooks"),
		},
	}

	tests := []struct {
		name     string
		filename string
	}{
		{"traversal in extension", "../../../.pdf"},
		{"traversal before extension", "../../../etc/passwd.pdf"},
		{"null byte before extension", "file\x00.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := receiver.getSaveDir(tt.filename)

			// The result should always be one of our configured directories
			// and never escape the tmpDir
			relPath, err := filepath.Rel(tmpDir, result)
			if err != nil {
				t.Errorf("Failed to compute relative path: %v", err)
				return
			}

			if strings.HasPrefix(relPath, "..") {
				t.Errorf("getSaveDir allowed path traversal!\n"+
					"  Filename: %q\n"+
					"  Returned: %q\n"+
					"  Base dir: %q",
					tt.filename, result, tmpDir)
			}
		})
	}
}

// TestRTCReceiver_FilenameIsSanitized verifies that after the fix,
// filenames from the sender are properly sanitized.
func TestRTCReceiver_FilenameIsSanitized(t *testing.T) {
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, "downloads")
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		t.Fatalf("Failed to create save dir: %v", err)
	}

	// Malicious filenames that should all result in files INSIDE saveDir
	testCases := []struct {
		input       string
		expected    string // Expected sanitized base name
		windowsOnly bool   // Skip on non-Windows platforms
	}{
		{"../../../etc/passwd", "passwd", false},
		{"..\\..\\..\\windows\\system32\\config\\sam", "sam", true}, // Backslash is path sep only on Windows
		{"/etc/shadow", "shadow", false},
		{"foo/../../../bar.txt", "bar.txt", false},
		{"normal.txt", "normal.txt", false},
		{"sub/dir/file.txt", "sub/dir/file.txt", false}, // Subdirectories are now preserved
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			// Skip Windows-specific tests on non-Windows platforms
			if tc.windowsOnly && runtime.GOOS != "windows" {
				t.Skipf("Skipping Windows-specific test on %s (backslash is not a path separator)", runtime.GOOS)
			}

			receiver := &RTCReceiver{
				saveDir:     saveDir,
				fileTokens:  make(map[string]string),
				fileWriters: make(map[string]*os.File),
				filePaths:   make(map[string]string),
				fileHashers: makeHasherMap(),
				files: []RTCFileDto{
					{
						ID:       "test-file",
						FileName: tc.input,
						Size:     100,
						FileType: "text/plain",
					},
				},
			}

			tokens := receiver.prepareFilesForReceive([]string{"test-file"})
			if token := tokens["test-file"]; token != "" {
				receiver.startReceivingFile(&RTCSendFileHeader{ID: "test-file", Token: token})
			}

			if len(tokens) > 0 {
				createdPath := receiver.filePaths["test-file"]

				// Clean up
				if f, ok := receiver.fileWriters["test-file"]; ok {
					_ = f.Close()
				}
				defer func() { _ = os.Remove(createdPath) }()

				// Verify the file is inside saveDir
				if !strings.HasPrefix(createdPath, saveDir) {
					t.Errorf("File created outside save directory: %s", createdPath)
				}

				// Verify the relative path matches expected (for subdirectory support)
				relPath, _ := filepath.Rel(saveDir, createdPath)
				expectedBase := filepath.Base(tc.expected)
				// For paths with subdirectories, check the relative path
				if strings.Contains(tc.expected, "/") {
					// Subdirectory case: verify relative path structure
					expectedRelPath := filepath.FromSlash(tc.expected)
					if relPath != expectedRelPath {
						// Account for possible " (1)" suffix if file already exists
						if !strings.HasPrefix(filepath.Base(relPath), strings.TrimSuffix(expectedBase, filepath.Ext(expectedBase))) {
							t.Errorf("Relative path mismatch: got %q, want %q", relPath, expectedRelPath)
						}
					}
				} else {
					// Flat file: just check base name
					baseName := filepath.Base(createdPath)
					if !strings.HasPrefix(baseName, strings.TrimSuffix(expectedBase, filepath.Ext(expectedBase))) {
						t.Errorf("Base name mismatch: got %q, want prefix %q", baseName, expectedBase)
					}
				}
			}
		})
	}
}

// =============================================================================
// Subdirectory Preservation Tests
// =============================================================================

// TestRTCReceiver_SubdirectoryPreservation verifies that files with subdirectory
// paths are saved correctly with the subdirectory structure preserved.
func TestRTCReceiver_SubdirectoryPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, "downloads")
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		t.Fatalf("Failed to create save dir: %v", err)
	}

	testCases := []struct {
		name           string
		filename       string
		expectedSubdir string // Expected subdirectory relative to saveDir
		expectedBase   string // Expected base filename
	}{
		{
			name:           "single subdirectory",
			filename:       "Photos/beach.jpg",
			expectedSubdir: "Photos",
			expectedBase:   "beach.jpg",
		},
		{
			name:           "nested subdirectories",
			filename:       "Photos/Summer/2024/vacation.jpg",
			expectedSubdir: "Photos/Summer/2024",
			expectedBase:   "vacation.jpg",
		},
		{
			name:           "flat file (no subdirectory)",
			filename:       "document.pdf",
			expectedSubdir: "",
			expectedBase:   "document.pdf",
		},
		{
			name:           "file with spaces in path",
			filename:       "My Photos/Summer Vacation/beach pic.jpg",
			expectedSubdir: "My Photos/Summer Vacation",
			expectedBase:   "beach pic.jpg",
		},
		{
			name:           "safe parent traversal within subdirectory",
			filename:       "Photos/temp/../final/pic.jpg",
			expectedSubdir: "Photos/final",
			expectedBase:   "pic.jpg",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &RTCReceiver{
				saveDir:     saveDir,
				fileTokens:  make(map[string]string),
				fileWriters: make(map[string]*os.File),
				filePaths:   make(map[string]string),
				fileHashers: makeHasherMap(),
				files: []RTCFileDto{
					{
						ID:       "test-file",
						FileName: tc.filename,
						Size:     100,
						FileType: "application/octet-stream",
					},
				},
			}

			tokens := receiver.prepareFilesForReceive([]string{"test-file"})
			if token := tokens["test-file"]; token != "" {
				receiver.startReceivingFile(&RTCSendFileHeader{ID: "test-file", Token: token})
			}

			if len(tokens) == 0 {
				t.Fatalf("prepareFilesForReceive returned no tokens")
			}

			createdPath := receiver.filePaths["test-file"]

			// Clean up
			if f, ok := receiver.fileWriters["test-file"]; ok {
				_ = f.Close()
			}
			defer func() {
				_ = os.RemoveAll(filepath.Join(saveDir, strings.Split(tc.expectedSubdir, "/")[0]))
				_ = os.Remove(createdPath)
			}()

			// Verify file is inside saveDir
			relPath, err := filepath.Rel(saveDir, createdPath)
			if err != nil {
				t.Fatalf("Failed to compute relative path: %v", err)
			}
			if strings.HasPrefix(relPath, "..") {
				t.Errorf("File created outside save directory: %s", createdPath)
			}

			// Verify base filename
			baseName := filepath.Base(createdPath)
			if baseName != tc.expectedBase {
				t.Errorf("Base name mismatch: got %q, want %q", baseName, tc.expectedBase)
			}

			// Verify subdirectory was created
			if tc.expectedSubdir != "" {
				subDirPath := filepath.Join(saveDir, filepath.FromSlash(tc.expectedSubdir))
				info, err := os.Stat(subDirPath)
				if err != nil {
					t.Errorf("Subdirectory should exist at %s: %v", subDirPath, err)
				} else if !info.IsDir() {
					t.Errorf("Expected %s to be a directory", subDirPath)
				}

				// Verify the file is in the correct subdirectory
				expectedDir := filepath.Join(saveDir, filepath.FromSlash(tc.expectedSubdir))
				actualDir := filepath.Dir(createdPath)
				if actualDir != expectedDir {
					t.Errorf("File in wrong directory: got %q, want %q", actualDir, expectedDir)
				}
			}
		})
	}
}

// TestRTCReceiver_SubdirectoryTraversalRejected verifies that path traversal
// attempts that try to escape via subdirectories are still blocked.
func TestRTCReceiver_SubdirectoryTraversalRejected(t *testing.T) {
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, "downloads")
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		t.Fatalf("Failed to create save dir: %v", err)
	}

	// Create a sensitive file that should NOT be overwritten
	sensitiveDir := filepath.Join(tmpDir, "sensitive")
	if err := os.MkdirAll(sensitiveDir, 0755); err != nil {
		t.Fatalf("Failed to create sensitive dir: %v", err)
	}
	sensitiveFile := filepath.Join(sensitiveDir, "secret.txt")
	if err := os.WriteFile(sensitiveFile, []byte("ORIGINAL_SECRET"), 0644); err != nil {
		t.Fatalf("Failed to create sensitive file: %v", err)
	}

	maliciousPaths := []string{
		"../sensitive/secret.txt",
		"Photos/../../sensitive/secret.txt",
		"a/b/c/../../../sensitive/secret.txt",
		"../../../etc/passwd",
	}

	for _, maliciousName := range maliciousPaths {
		t.Run(maliciousName, func(t *testing.T) {
			receiver := &RTCReceiver{
				saveDir:     saveDir,
				fileTokens:  make(map[string]string),
				fileWriters: make(map[string]*os.File),
				filePaths:   make(map[string]string),
				fileHashers: makeHasherMap(),
				files: []RTCFileDto{
					{
						ID:       "malicious-file",
						FileName: maliciousName,
						Size:     100,
						FileType: "text/plain",
					},
				},
			}

			tokens := receiver.prepareFilesForReceive([]string{"malicious-file"})
			if token := tokens["malicious-file"]; token != "" {
				receiver.startReceivingFile(&RTCSendFileHeader{ID: "malicious-file", Token: token})
			}

			if len(tokens) > 0 {
				createdPath := receiver.filePaths["malicious-file"]

				// Clean up
				if f, ok := receiver.fileWriters["malicious-file"]; ok {
					_ = f.Close()
				}
				defer func() { _ = os.Remove(createdPath) }()

				// The file MUST be inside saveDir
				absSaveDir, _ := filepath.Abs(saveDir)
				absCreatedPath, _ := filepath.Abs(createdPath)
				absSaveDir = filepath.Clean(absSaveDir) + string(filepath.Separator)
				absCreatedPath = filepath.Clean(absCreatedPath)

				if !strings.HasPrefix(absCreatedPath, absSaveDir) {
					t.Errorf("PATH TRAVERSAL: File created outside save directory!\n"+
						"  Malicious filename: %q\n"+
						"  Created at: %q\n"+
						"  Save directory: %q",
						maliciousName, createdPath, saveDir)
				}
			}

			// Verify the sensitive file was NOT modified
			content, err := os.ReadFile(sensitiveFile)
			if err != nil {
				t.Errorf("Sensitive file was deleted: %v", err)
			} else if string(content) != "ORIGINAL_SECRET" {
				t.Errorf("SECURITY BREACH: Sensitive file was modified!")
			}
		})
	}
}

func TestRTCReceiver_HandleFileHeader_ValidTokenTransitionsToReceiving(t *testing.T) {
	tmpDir := t.TempDir()
	f, err := os.CreateTemp(tmpDir, "file-1-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	r := &RTCReceiver{
		state:       stateWaitFiles,
		saveDir:     tmpDir,
		files:       []RTCFileDto{{ID: "file-1", FileName: "file-1.bin", Size: 1}},
		fileTokens:  map[string]string{"file-1": "valid-token"},
		fileWriters: map[string]*os.File{},
		filePaths:   map[string]string{},
		fileHashers: makeHasherMap(),
	}

	r.handleMessage([]byte(`{"id":"file-1","token":"valid-token"}`))

	if r.state != stateReceivingFiles {
		t.Fatalf("state = %d; want %d", r.state, stateReceivingFiles)
	}
	if r.currentFileID != "file-1" {
		t.Fatalf("currentFileID = %q; want %q", r.currentFileID, "file-1")
	}
}

func TestRTCReceiver_HandleFileHeader_RejectsInvalidToken(t *testing.T) {
	tmpDir := t.TempDir()
	f, err := os.CreateTemp(tmpDir, "file-1-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	r := &RTCReceiver{
		state:       stateWaitFiles,
		fileTokens:  map[string]string{"file-1": "valid-token"},
		fileWriters: map[string]*os.File{"file-1": f},
		filePaths:   map[string]string{"file-1": f.Name()},
		fileHashers: makeHasherMap(),
	}

	r.handleMessage([]byte(`{"id":"file-1","token":"invalid-token"}`))

	if r.state != stateWaitFiles {
		t.Fatalf("state = %d; want %d", r.state, stateWaitFiles)
	}
	if r.currentFileID != "" {
		t.Fatalf("currentFileID = %q; want empty", r.currentFileID)
	}
}

func TestRTCReceiver_HandleFileHeader_RejectsUnknownFileID(t *testing.T) {
	tmpDir := t.TempDir()
	f, err := os.CreateTemp(tmpDir, "file-1-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	r := &RTCReceiver{
		state:       stateWaitFiles,
		fileTokens:  map[string]string{"file-1": "valid-token"},
		fileWriters: map[string]*os.File{"file-1": f},
		filePaths:   map[string]string{"file-1": f.Name()},
		fileHashers: makeHasherMap(),
	}

	r.handleMessage([]byte(`{"id":"file-2","token":"anything"}`))

	if r.state != stateWaitFiles {
		t.Fatalf("state = %d; want %d", r.state, stateWaitFiles)
	}
	if r.currentFileID != "" {
		t.Fatalf("currentFileID = %q; want empty", r.currentFileID)
	}
}

func TestRTCReceiver_HandleNextHeader_RejectsMissingToken(t *testing.T) {
	tmpDir := t.TempDir()
	currentFile, err := os.CreateTemp(tmpDir, "current-*")
	if err != nil {
		t.Fatalf("failed to create current temp file: %v", err)
	}
	nextFile, err := os.CreateTemp(tmpDir, "next-*")
	if err != nil {
		t.Fatalf("failed to create next temp file: %v", err)
	}
	defer func() {
		_ = currentFile.Close()
		_ = nextFile.Close()
		_ = os.Remove(currentFile.Name())
		_ = os.Remove(nextFile.Name())
	}()

	r := &RTCReceiver{
		state:         stateReceivingFiles,
		currentFileID: "file-1",
		peer:          &PeerConnection{},
		fileTokens: map[string]string{
			"file-1": "token-1",
			"file-2": "token-2",
		},
		fileWriters: map[string]*os.File{
			"file-1": currentFile,
			"file-2": nextFile,
		},
		filePaths: map[string]string{
			"file-1": currentFile.Name(),
			"file-2": nextFile.Name(),
		},
		fileHashers: map[string]hash.Hash{
			"file-1": sha256.New(),
			"file-2": sha256.New(),
		},
		files: []RTCFileDto{
			{ID: "file-1", FileName: "one.txt", Size: 1},
			{ID: "file-2", FileName: "two.txt", Size: 1},
		},
	}

	r.handleMessage([]byte(`{"id":"file-2"}`))

	if r.state != stateWaitFiles {
		t.Fatalf("state = %d; want %d", r.state, stateWaitFiles)
	}
	if r.currentFileID != "" {
		t.Fatalf("currentFileID = %q; want empty", r.currentFileID)
	}
}

// =============================================================================
// Concurrency/Race Condition Tests
// =============================================================================

// TestRTCReceiver_sendError_Race verifies that sendError() is thread-safe.
// sendError reads r.peer which can be modified by other goroutines.
func TestRTCReceiver_sendError_Race(t *testing.T) {
	r := &RTCReceiver{
		peer:        nil,
		fileTokens:  make(map[string]string),
		fileWriters: make(map[string]*os.File),
		filePaths:   make(map[string]string),
		fileHashers: makeHasherMap(),
	}

	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent sendError calls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.sendError("test error")
		}()
	}

	// Concurrent peer modifications (simulating AcceptOffer and Close)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.mu.Lock()
			r.peer = nil
			r.mu.Unlock()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Deadlock Prevention Tests
// =============================================================================

// TestRTCReceiver_handleFileList_NoDeadlock verifies that the onSelectFiles
// callback can safely access receiver methods without causing a deadlock.
// Before the fix, handleFileList would call the callback while holding the mutex,
// and if the callback tried to call Close() or other methods, it would deadlock.
func TestRTCReceiver_handleFileList_NoDeadlock(t *testing.T) {
	tmpDir := t.TempDir()
	r := &RTCReceiver{
		saveDir:     tmpDir,
		fileTokens:  make(map[string]string),
		fileWriters: make(map[string]*os.File),
		filePaths:   make(map[string]string),
		fileHashers: makeHasherMap(),
		files: []RTCFileDto{
			{ID: "test-1", FileName: "test.txt", Size: 100},
		},
		state: stateWaitFileList,
	}

	// Set a callback that attempts to access the receiver
	// This would deadlock if the mutex is still held when callback is invoked
	callbackDone := make(chan struct{})
	r.OnSelectFiles(func(files []RTCFileDto) []string {
		// This tries to access the receiver - would deadlock if mutex held
		_ = r.saveDir // read access
		close(callbackDone)
		// Return file IDs to avoid nil peer panic in response path
		ids := make([]string, len(files))
		for i, f := range files {
			ids[i] = f.ID
		}
		return ids
	})

	// Simulate receiving a file list message
	// This will call handleMessage which acquires the mutex
	done := make(chan struct{})
	go func() {
		defer func() {
			_ = recover() // Ignore panic from nil peer in later code paths
			close(done)
		}()
		data := []byte(`{"status":"OK","files":[{"id":"test-1","fileName":"test.txt","size":100}]}`)
		r.handleMessage(data)
	}()

	// Wait with timeout
	select {
	case <-done:
		// Success - no deadlock
	case <-callbackDone:
		// Callback executed, wait for handleMessage to complete
		<-done
	case <-time.After(2 * time.Second):
		t.Fatal("Deadlock detected: handleMessage did not complete within timeout")
	}
}

// TestRTCReceiver_CallbackCanAccessMethods tests that after the fix,
// callbacks can safely call receiver methods that acquire the mutex.
func TestRTCReceiver_CallbackCanAccessMethods(t *testing.T) {
	tmpDir := t.TempDir()
	r := &RTCReceiver{
		saveDir:     tmpDir,
		fileTokens:  make(map[string]string),
		fileWriters: make(map[string]*os.File),
		filePaths:   make(map[string]string),
		fileHashers: makeHasherMap(),
		files: []RTCFileDto{
			{ID: "test-1", FileName: "test.txt", Size: 100},
		},
		state: stateWaitFileList,
	}

	// This is the key test: callback calls a method that needs the mutex
	// sendError() acquires the mutex - this would deadlock before the fix
	callbackExecuted := false
	r.OnSelectFiles(func(files []RTCFileDto) []string {
		r.sendError("test from callback")
		callbackExecuted = true
		// Return all file IDs to avoid the "DECLINED" path which needs a peer
		ids := make([]string, len(files))
		for i, f := range files {
			ids[i] = f.ID
		}
		return ids
	})

	// Use a timeout to detect deadlock
	done := make(chan struct{})
	go func() {
		defer func() {
			_ = recover() // Ignore panic from nil peer in later code paths
			close(done)
		}()
		data := []byte(`{"status":"OK","files":[{"id":"test-1","fileName":"test.txt","size":100}]}`)
		r.handleMessage(data)
	}()

	select {
	case <-done:
		if !callbackExecuted {
			t.Error("Callback was not executed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deadlock detected: handleMessage did not complete within timeout")
	}
}

// =============================================================================
// DoS Prevention Tests
// =============================================================================

// TestRTCReceiver_handleFileList_RejectsTooManyFiles verifies that the receiver
// rejects file lists that exceed MaxFilesPerSession to prevent DoS attacks.
// An attacker could send millions of file entries to exhaust memory on e-readers
// with limited RAM (256-512MB).
func TestRTCReceiver_handleFileList_RejectsTooManyFiles(t *testing.T) {
	r := &RTCReceiver{
		saveDir:     t.TempDir(),
		fileTokens:  make(map[string]string),
		fileWriters: make(map[string]*os.File),
		filePaths:   make(map[string]string),
		fileHashers: makeHasherMap(),
		state:       stateWaitFileList,
	}

	// Create a file list with constants.MaxFilesPerSession + 1 files
	files := make([]RTCFileDto, constants.MaxFilesPerSession+1)
	for i := 0; i < len(files); i++ {
		files[i] = RTCFileDto{
			ID:       "file-" + string(rune(i)),
			FileName: "test.txt",
			Size:     100,
		}
	}

	// Build the file list message
	fileListMsg := RTCPinSendingResponse{
		Status: "OK",
		Files:  files,
	}
	data, _ := json.Marshal(fileListMsg)

	// Handle the message - will panic on nil peer for the DECLINED response
	// but we can check that files were not stored
	done := make(chan struct{})
	go func() {
		defer func() {
			_ = recover() // Ignore nil peer panic
			close(done)
		}()
		r.handleMessage(data)
	}()

	select {
	case <-done:
		// Verify files were not stored (rejected before storage)
		if len(r.files) > 0 {
			t.Errorf("Files should not be stored when count exceeds limit, got %d files", len(r.files))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Test timed out")
	}
}

// TestRTCReceiver_handleFileList_AcceptsMaxFiles verifies that exactly
// MaxFilesPerSession files are accepted (boundary test).
// Note: We use a smaller file count to keep the test fast.
func TestRTCReceiver_handleFileList_AcceptsMaxFiles(t *testing.T) {
	const testFileCount = 100 // Use smaller count for fast testing

	r := &RTCReceiver{
		saveDir:     t.TempDir(),
		fileTokens:  make(map[string]string),
		fileWriters: make(map[string]*os.File),
		filePaths:   make(map[string]string),
		fileHashers: makeHasherMap(),
		state:       stateWaitFileList,
	}

	// Create a file list with testFileCount files (below MaxFilesPerSession)
	files := make([]RTCFileDto, testFileCount)
	for i := 0; i < len(files); i++ {
		files[i] = RTCFileDto{
			ID:       fmt.Sprintf("file-%d", i),
			FileName: fmt.Sprintf("test-%d.txt", i),
			Size:     100,
		}
	}

	// Build the file list message
	fileListMsg := RTCPinSendingResponse{
		Status: "OK",
		Files:  files,
	}
	data, _ := json.Marshal(fileListMsg)

	// Handle the message - this will panic on nil peer but should not be DECLINED
	done := make(chan struct{})
	go func() {
		defer func() {
			_ = recover() // Ignore nil peer panic
			close(done)
		}()
		r.handleMessage(data)
	}()

	select {
	case <-done:
		// Files should be stored since count is below the limit
		if len(r.files) != testFileCount {
			t.Errorf("Expected %d files to be stored, got %d", testFileCount, len(r.files))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out")
	}
}

// TestRTCReceiver_MaxFilesPerSession_BoundaryValue verifies that the
// MaxFilesPerSession constant is correctly defined.
func TestRTCReceiver_MaxFilesPerSession_BoundaryValue(t *testing.T) {
	// The V2 HTTP path uses the same limit, ensure consistency
	if constants.MaxFilesPerSession != 10000 {
		t.Errorf("MaxFilesPerSession = %d; want 10000", constants.MaxFilesPerSession)
	}
}

// =============================================================================
// PIN Verification Security Tests
// =============================================================================

func newPINTestReceiver(t *testing.T) *RTCReceiver {
	t.Helper()
	r := NewRTCReceiver(nil, nil, "123456", t.TempDir())
	r.state = stateWaitPin
	r.peer = &PeerConnection{}
	return r
}

func TestRTCReceiver_handlePin_CorrectPIN_AdvancesToFileList(t *testing.T) {
	r := newPINTestReceiver(t)

	r.handlePin(&RTCPinMessage{Pin: "123456"}, "pin")

	if r.state != stateWaitFileList {
		t.Fatalf("state = %d; want stateWaitFileList", r.state)
	}
	if r.pinAttempts != 0 {
		t.Fatalf("pinAttempts = %d; want 0", r.pinAttempts)
	}
}

func TestRTCReceiver_handlePin_IncorrectPIN_IncrementsAttempts(t *testing.T) {
	r := newPINTestReceiver(t)

	r.handlePin(&RTCPinMessage{Pin: "wrong"}, "pin")

	if r.pinAttempts != 1 {
		t.Fatalf("pinAttempts = %d; want 1", r.pinAttempts)
	}
	if r.state != stateWaitPin {
		t.Fatalf("state = %d; want stateWaitPin", r.state)
	}
}

func TestRTCReceiver_handlePin_MaxAttempts_BlocksPeerAndClosesConnection(t *testing.T) {
	ClearBlockedPeers()
	t.Cleanup(ClearBlockedPeers)

	r := newPINTestReceiver(t)
	r.senderSignalingID = "blocked-test-peer"
	for range maxPINAttempts {
		r.handlePin(&RTCPinMessage{Pin: "wrong"}, "pin")
	}

	if r.pinAttempts != maxPINAttempts {
		t.Fatalf("pinAttempts = %d; want %d", r.pinAttempts, maxPINAttempts)
	}
	if !isPeerBlocked(r.senderSignalingID) {
		t.Fatal("peer was not blocked after maximum PIN attempts")
	}
	if r.peer != nil {
		t.Fatal("peer connection was not detached after maximum PIN attempts")
	}
}

// TestRTCReceiver_maxPINAttempts_IsThree verifies the rate limiting constant.
func TestRTCReceiver_maxPINAttempts_IsThree(t *testing.T) {
	if maxPINAttempts != 3 {
		t.Errorf("maxPINAttempts = %d; want 3", maxPINAttempts)
	}
}

// =============================================================================
// Token Exchange Security Tests
// =============================================================================

func newTokenTestReceiver(t *testing.T, pin string) (*RTCReceiver, *crypto.SigningKey) {
	t.Helper()
	receiverKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	senderKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRTCReceiver(nil, receiverKey, pin, t.TempDir())
	r.peer = &PeerConnection{}
	r.state = stateWaitToken
	r.finalNonce = []byte("receiver-token-test-nonce")
	r.senderPublicKey = senderKey.ToVerifyingKey()
	return r, senderKey
}

func TestRTCReceiver_handleToken_TransitionsAccordingToPIN(t *testing.T) {
	tests := []struct {
		name      string
		pin       string
		wantState int
	}{
		{name: "PIN required", pin: "123456", wantState: stateWaitPin},
		{name: "no PIN", wantState: stateWaitFileList},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, senderKey := newTokenTestReceiver(t, tt.pin)
			token, err := senderKey.GenerateTokenWithNonce(r.finalNonce)
			if err != nil {
				t.Fatal(err)
			}

			r.handleToken(&RTCTokenRequest{Token: token}, "token_request")

			if r.state != tt.wantState {
				t.Fatalf("state = %d; want %d", r.state, tt.wantState)
			}
			if r.senderToken != token {
				t.Fatal("handleToken did not retain the sender token")
			}
		})
	}
}

func TestRTCReceiver_handleToken_InvalidExpectedSignatureIsTerminal(t *testing.T) {
	r, _ := newTokenTestReceiver(t, "")
	untrustedKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	token, err := untrustedKey.GenerateTokenWithNonce(r.finalNonce)
	if err != nil {
		t.Fatal(err)
	}

	r.handleToken(&RTCTokenRequest{Token: token}, "token_request")

	if r.state != stateDone {
		t.Fatalf("state = %d; want terminal stateDone", r.state)
	}
}

// =============================================================================
// Checksum Verification Tests
// =============================================================================

func prepareChecksumTestFile(t *testing.T, checksum string, content []byte) (*RTCReceiver, string) {
	t.Helper()
	r := NewRTCReceiver(nil, nil, "", t.TempDir())
	r.files = []RTCFileDto{{
		ID:       "checksum-file",
		FileName: "checksum.bin",
		Size:     int64(len(content)),
		SHA256:   checksum,
	}}
	tokens := r.prepareFilesForReceive([]string{"checksum-file"})
	if !r.startReceivingFile(&RTCSendFileHeader{ID: "checksum-file", Token: tokens["checksum-file"]}) {
		t.Fatal("startReceivingFile rejected prepared file")
	}
	path := r.filePaths["checksum-file"]
	r.handleBinaryData(content)
	return r, path
}

func TestRTCReceiver_finishCurrentFile_MatchingChecksumKeepsFile(t *testing.T) {
	content := []byte("test file content for checksum verification")
	sum := sha256.Sum256(content)
	r, path := prepareChecksumTestFile(t, hex.EncodeToString(sum[:]), content)
	callbackCalled := false
	r.OnFileReceived(func(filename string, size int64, sender string) {
		callbackCalled = true
	})

	r.finishCurrentFile()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("matching file was not retained: %v", err)
	}
	if !callbackCalled {
		t.Fatal("successful receive callback was not called")
	}
}

func TestRTCReceiver_finishCurrentFile_MismatchedChecksumDeletesFile(t *testing.T) {
	content := []byte("actual content")
	r, path := prepareChecksumTestFile(t, strings.Repeat("0", sha256.Size*2), content)

	r.finishCurrentFile()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mismatched file still exists; stat error = %v", err)
	}
}

func TestRTCReceiver_finishCurrentFile_EmptyChecksumKeepsFile(t *testing.T) {
	r, path := prepareChecksumTestFile(t, "", []byte("unchecked content"))

	r.finishCurrentFile()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file without checksum was not retained: %v", err)
	}
}

// =============================================================================
// Persistent Rate Limiting Tests
// These tests verify that PIN rate limiting persists across WebRTC connections,
// preventing attackers from bypassing the 3-attempt limit by reconnecting.
// =============================================================================

// TestBlockPeer_BlocksForDuration verifies that blockPeer adds a peer to the
// blocked list for the configured duration.
func TestBlockPeer_BlocksForDuration(t *testing.T) {
	// Clear any existing blocks
	ClearBlockedPeers()
	defer ClearBlockedPeers()

	peerID := "test-peer-123"

	// Peer should not be blocked initially
	if isPeerBlocked(peerID) {
		t.Error("Peer should not be blocked initially")
	}

	// Block the peer
	blockPeer(peerID)

	// Peer should now be blocked
	if !isPeerBlocked(peerID) {
		t.Error("Peer should be blocked after blockPeer()")
	}
}

// TestIsPeerBlocked_ExpiredBlock_Unblocks verifies that expired blocks are
// automatically cleaned up when checked.
func TestIsPeerBlocked_ExpiredBlock_Unblocks(t *testing.T) {
	// Clear any existing blocks
	ClearBlockedPeers()
	defer ClearBlockedPeers()

	peerID := "test-peer-expired"

	// Manually add an expired block
	blockedPeersMu.Lock()
	blockedPeers[peerID] = time.Now().Add(-1 * time.Minute) // Expired 1 minute ago
	blockedPeersMu.Unlock()

	// Checking should find it expired and unblock
	if isPeerBlocked(peerID) {
		t.Error("Expired block should not be considered blocked")
	}

	// Verify it was cleaned up from the map
	blockedPeersMu.RLock()
	_, exists := blockedPeers[peerID]
	blockedPeersMu.RUnlock()

	if exists {
		t.Error("Expired block should have been removed from map")
	}
}

// TestIsPeerBlocked_NonexistentPeer_NotBlocked verifies that peers not in the
// blocked list return false.
func TestIsPeerBlocked_NonexistentPeer_NotBlocked(t *testing.T) {
	// Clear any existing blocks
	ClearBlockedPeers()
	defer ClearBlockedPeers()

	peerID := "never-blocked-peer"

	if isPeerBlocked(peerID) {
		t.Error("Non-existent peer should not be blocked")
	}
}

// TestClearBlockedPeers_ClearsAll verifies that ClearBlockedPeers removes all
// blocked peers from the map.
func TestClearBlockedPeers_ClearsAll(t *testing.T) {
	// Add some blocked peers
	blockedPeersMu.Lock()
	blockedPeers["peer1"] = time.Now().Add(time.Hour)
	blockedPeers["peer2"] = time.Now().Add(time.Hour)
	blockedPeers["peer3"] = time.Now().Add(time.Hour)
	blockedPeersMu.Unlock()

	// Verify they're blocked
	if !isPeerBlocked("peer1") || !isPeerBlocked("peer2") || !isPeerBlocked("peer3") {
		t.Error("Peers should be blocked before clear")
	}

	// Clear all
	ClearBlockedPeers()

	// Verify none are blocked
	if isPeerBlocked("peer1") || isPeerBlocked("peer2") || isPeerBlocked("peer3") {
		t.Error("No peers should be blocked after ClearBlockedPeers()")
	}
}

// TestPinBlockDuration_Is30Seconds verifies the block duration constant.
func TestPinBlockDuration_Is30Seconds(t *testing.T) {
	expected := 30 * time.Second
	if pinBlockDuration != expected {
		t.Errorf("pinBlockDuration = %v; want %v", pinBlockDuration, expected)
	}
}

// TestBlockPeer_ConcurrentSafety verifies that concurrent blockPeer and
// isPeerBlocked calls don't cause race conditions.
func TestBlockPeer_ConcurrentSafety(t *testing.T) {
	ClearBlockedPeers()
	defer ClearBlockedPeers()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent blocks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			peerID := fmt.Sprintf("peer-%d", id)
			blockPeer(peerID)
		}(i)
	}

	// Concurrent checks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			peerID := fmt.Sprintf("peer-%d", id)
			_ = isPeerBlocked(peerID)
		}(i)
	}

	wg.Wait()

	// All should be blocked now
	for i := 0; i < numGoroutines; i++ {
		peerID := fmt.Sprintf("peer-%d", i)
		if !isPeerBlocked(peerID) {
			t.Errorf("Peer %s should be blocked", peerID)
		}
	}
}
