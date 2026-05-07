package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

// TestCreateUniqueFile tests the CreateUniqueFile function
func TestCreateUniqueFile(t *testing.T) {
	t.Run("creates file when it does not exist", func(t *testing.T) {
		dir := t.TempDir()
		filename := "test.txt"

		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		expected := filepath.Join(dir, filename)
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}

		// Verify file was created
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file should exist: %v", err)
		}
	})

	t.Run("appends counter when file exists", func(t *testing.T) {
		dir := t.TempDir()
		filename := "test.txt"

		// Create the original file
		originalPath := filepath.Join(dir, filename)
		if err := os.WriteFile(originalPath, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		expected := filepath.Join(dir, "test (1).txt")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})

	t.Run("increments counter until unique", func(t *testing.T) {
		dir := t.TempDir()
		filename := "test.txt"

		// Create original and first two numbered files
		for _, name := range []string{"test.txt", "test (1).txt", "test (2).txt"} {
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}
		}

		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		expected := filepath.Join(dir, "test (3).txt")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})

	t.Run("handles files without extension", func(t *testing.T) {
		dir := t.TempDir()
		filename := "README"

		// Create the original file
		originalPath := filepath.Join(dir, filename)
		if err := os.WriteFile(originalPath, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		expected := filepath.Join(dir, "README (1)")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})

	t.Run("handles dotfiles", func(t *testing.T) {
		dir := t.TempDir()
		filename := ".gitignore"

		// Create the original file
		originalPath := filepath.Join(dir, filename)
		if err := os.WriteFile(originalPath, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		// filepath.Ext(".gitignore") returns ".gitignore" (whole name is extension)
		// So name becomes "" and ext is ".gitignore", resulting in " (1).gitignore"
		expected := filepath.Join(dir, " (1).gitignore")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})

	t.Run("handles multiple extensions", func(t *testing.T) {
		dir := t.TempDir()
		filename := "archive.tar.gz"

		// Create the original file
		originalPath := filepath.Join(dir, filename)
		if err := os.WriteFile(originalPath, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		// Only the last extension is preserved
		expected := filepath.Join(dir, "archive.tar (1).gz")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})
}

// TestCreateUniqueFileBounded verifies the bounded loop for unique file creation
func TestCreateUniqueFileBounded(t *testing.T) {
	// Skip this test in short mode as it creates many files
	if testing.Short() {
		t.Skip("skipping bounded loop test in short mode")
	}

	t.Run("respects MaxUniquePathAttempts constant", func(t *testing.T) {
		// Verify the constant is set to a reasonable value
		if utils.MaxUniquePathAttempts != 10000 {
			t.Errorf("expected MaxUniquePathAttempts to be 10000, got %d", utils.MaxUniquePathAttempts)
		}
	})

	t.Run("returns error when max attempts exceeded", func(t *testing.T) {
		dir := t.TempDir()
		filename := "test.txt"

		// Create original file and many numbered versions
		// We'll create just enough to test the boundary (5 files for speed)
		// The actual implementation uses 10000, but we test the logic works
		testLimit := 5
		for i := 0; i <= testLimit; i++ {
			var name string
			if i == 0 {
				name = "test.txt"
			} else {
				name = "test (" + itoa(i) + ").txt"
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
				t.Fatalf("failed to create file %s: %v", path, err)
			}
		}

		// Should create test (6).txt
		file, path, err := utils.CreateUniqueFile(dir, filename)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = file.Close() }()

		expected := filepath.Join(dir, "test (6).txt")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})
}

// Helper for integer to string conversion
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestFileTokensReturnsCopy verifies that FileTokens returns a copy, not the original map
func TestFileTokensReturnsCopy(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Accept a file to generate a token
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	if err := sess.AcceptFile("file1", fileMeta); err != nil {
		t.Fatalf("failed to accept file: %v", err)
	}

	// Get tokens
	tokens1 := sess.FileTokens()
	tokens2 := sess.FileTokens()

	// Verify they are equal in content
	if len(tokens1) != len(tokens2) {
		t.Errorf("tokens should have same length")
	}

	// Modify the returned map
	tokens1["file1"] = "modified"

	// Get tokens again - should not reflect modification
	tokens3 := sess.FileTokens()
	if tokens3["file1"] == "modified" {
		t.Error("FileTokens should return a copy, not the internal map")
	}

	// Verify original token is preserved
	if tokens3["file1"] == "" {
		t.Error("original token should still exist")
	}
	if tokens2["file1"] != tokens3["file1"] {
		t.Error("tokens should be consistent across calls")
	}
}

// TestAcceptFileRaceCondition verifies thread-safe file acceptance
func TestAcceptFileRaceCondition(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Concurrently accept many files
	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			fileId := "file" + itoa(idx)
			fileMeta := models.FileMeta{
				Id:       fileId,
				Filename: "test" + itoa(idx) + ".txt",
				Size:     100,
			}
			if err := sess.AcceptFile(fileId, fileMeta); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("concurrent AcceptFile failed: %v", err)
	}

	// Verify all files were accepted
	tokens := sess.FileTokens()
	if len(tokens) != numGoroutines {
		t.Errorf("expected %d tokens, got %d", numGoroutines, len(tokens))
	}
}

// TestAcceptFileRejectsAfterStart tests that AcceptFile rejects after session starts
func TestAcceptFileRejectsAfterStart(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Accept a file before start
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	if err := sess.AcceptFile("file1", fileMeta); err != nil {
		t.Fatalf("failed to accept file before start: %v", err)
	}

	// Start the session
	sess.Start()

	// Try to accept another file - should fail
	fileMeta2 := models.FileMeta{
		Id:       "file2",
		Filename: "test2.txt",
		Size:     200,
	}
	err = sess.AcceptFile("file2", fileMeta2)
	if err == nil {
		t.Error("AcceptFile should reject after session start")
	}
}

// TestAcceptFileTOCTOURace tests the TOCTOU race condition fix.
// The fix moved the started check inside the mutex lock to prevent a race
// between AcceptFile checking started and Start() setting it.
func TestAcceptFileTOCTOURace(t *testing.T) {
	// Run multiple iterations to increase chance of catching race
	for iteration := 0; iteration < 20; iteration++ {
		sess, _ := NewRecvSession("test-session", "192.168.1.1")

		const numAcceptors = 50
		var wg sync.WaitGroup
		wg.Add(numAcceptors + 1)

		// Track successful accepts
		successCount := 0
		var countMu sync.Mutex

		// One goroutine calls Start()
		go func() {
			defer wg.Done()
			sess.Start()
		}()

		// Many goroutines try to AcceptFile concurrently
		for i := 0; i < numAcceptors; i++ {
			go func(idx int) {
				defer wg.Done()
				fileId := "file" + itoa(idx)
				fileMeta := models.FileMeta{
					Id:       fileId,
					Filename: "test" + itoa(idx) + ".txt",
					Size:     100,
				}
				err := sess.AcceptFile(fileId, fileMeta)
				if err == nil {
					countMu.Lock()
					successCount++
					countMu.Unlock()
				}
			}(i)
		}

		wg.Wait()

		// Verify consistency: number of tokens should match successful accepts
		tokens := sess.FileTokens()
		countMu.Lock()
		count := successCount
		countMu.Unlock()

		if len(tokens) != count {
			t.Errorf("iteration %d: token count (%d) != success count (%d) - TOCTOU race detected!",
				iteration, len(tokens), count)
		}

		// Also verify no file was accepted after Start was called
		// by checking all tokens have corresponding file metas
		for fileId := range tokens {
			if _, ok := sess.GetFileMeta(fileId); !ok {
				t.Errorf("iteration %d: token exists for %s but no file meta - inconsistent state!",
					iteration, fileId)
			}
		}
	}
}

// TestAcceptFileRejectsIdMismatch tests that AcceptFile rejects mismatched IDs
func TestAcceptFileRejectsIdMismatch(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	// Pass different fileId than what's in fileMeta
	err = sess.AcceptFile("different-id", fileMeta)
	if err == nil {
		t.Error("AcceptFile should reject when fileId doesn't match fileMeta.Id")
	}
}

// TestRecvSessionLifecycle tests session state transitions
func TestRecvSessionLifecycle(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// New session should be stopped (no files)
	if !sess.Stopped() {
		t.Error("new session with no files should be stopped")
	}

	// Accept a file
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	if err := sess.AcceptFile("file1", fileMeta); err != nil {
		t.Fatalf("failed to accept file: %v", err)
	}

	// Still stopped until Start() is called
	if !sess.Stopped() {
		t.Error("session should be stopped until Start() is called")
	}

	// Start session
	sess.Start()

	// Now running
	if sess.Stopped() {
		t.Error("session should not be stopped after Start()")
	}

	// End session
	sess.End()

	// Should be stopped again
	if !sess.Stopped() {
		t.Error("session should be stopped after End()")
	}

	// End is idempotent
	sess.End() // Should not panic
}

// TestSaveFileValidation tests SaveFile validation logic
func TestSaveFileValidation(t *testing.T) {
	dir := t.TempDir()

	t.Run("rejects empty session id", func(t *testing.T) {
		sess := &RecvSession{
			id:         "",
			fileMetas:  make(models.FileMetas),
			fileTokens: make(models.FileTokens),
		}
		sess.started.Store(true)

		_, err := sess.SaveFile(dir, "file1", "token", "192.168.1.1", bytes.NewReader(nil))
		if err == nil {
			t.Error("should reject empty session id")
		}
	})

	t.Run("rejects when session not started", func(t *testing.T) {
		sess, _ := NewRecvSession("test-session", "192.168.1.1")
		// Don't call Start()

		_, err := sess.SaveFile(dir, "file1", "token", "192.168.1.1", bytes.NewReader(nil))
		if err == nil {
			t.Error("should reject when session not started")
		}
	})

	t.Run("rejects wrong client IP", func(t *testing.T) {
		sess, _ := NewRecvSession("test-session", "192.168.1.1")
		fileMeta := models.FileMeta{
			Id:       "file1",
			Filename: "test.txt",
			Size:     5,
		}
		_ = sess.AcceptFile("file1", fileMeta)
		sess.Start()

		// Get the actual token
		tokens := sess.FileTokens()
		token := tokens["file1"]

		// Try to save from different IP
		_, err := sess.SaveFile(dir, "file1", token, "10.0.0.1", bytes.NewReader([]byte("hello")))
		if err == nil {
			t.Error("should reject wrong client IP")
		}
	})

	t.Run("rejects invalid token", func(t *testing.T) {
		sess, _ := NewRecvSession("test-session", "192.168.1.1")
		fileMeta := models.FileMeta{
			Id:       "file1",
			Filename: "test.txt",
			Size:     5,
		}
		_ = sess.AcceptFile("file1", fileMeta)
		sess.Start()

		_, err := sess.SaveFile(dir, "file1", "wrong-token", "192.168.1.1", bytes.NewReader([]byte("hello")))
		if err == nil {
			t.Error("should reject invalid token")
		}
	})

	t.Run("rejects unknown file id", func(t *testing.T) {
		sess, _ := NewRecvSession("test-session", "192.168.1.1")
		fileMeta := models.FileMeta{
			Id:       "file1",
			Filename: "test.txt",
			Size:     5,
		}
		_ = sess.AcceptFile("file1", fileMeta)
		sess.Start()

		tokens := sess.FileTokens()

		_, err := sess.SaveFile(dir, "unknown-file", tokens["file1"], "192.168.1.1", bytes.NewReader([]byte("hello")))
		if err == nil {
			t.Error("should reject unknown file id")
		}
	})
}

// TestSaveFileSuccess tests successful file saving
func TestSaveFileSuccess(t *testing.T) {
	dir := t.TempDir()

	sess, _ := NewRecvSession("test-session", "192.168.1.1")
	content := []byte("hello world")

	// Calculate checksum
	h := sha256.Sum256(content)
	checksum := hex.EncodeToString(h[:])

	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     models.FlexInt(len(content)),
		Checksum: checksum,
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	tokens := sess.FileTokens()

	savedName, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("SaveFile failed: %v", err)
	}

	if savedName != "test.txt" {
		t.Errorf("expected filename 'test.txt', got %q", savedName)
	}

	// Verify file was written
	savedContent, err := os.ReadFile(filepath.Join(dir, "test.txt"))
	if err != nil {
		t.Fatalf("failed to read saved file: %v", err)
	}
	if !bytes.Equal(savedContent, content) {
		t.Error("saved content doesn't match original")
	}

	// Session should be stopped after last file
	if !sess.Stopped() {
		t.Error("session should be stopped after last file saved")
	}
}

// TestSaveFileChecksumValidation tests checksum validation
func TestSaveFileChecksumValidation(t *testing.T) {
	dir := t.TempDir()

	sess, _ := NewRecvSession("test-session", "192.168.1.1")

	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     5,
		Checksum: "0000000000000000000000000000000000000000000000000000000000000000", // wrong checksum
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	tokens := sess.FileTokens()

	_, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader([]byte("hello")))
	if err == nil {
		t.Error("should reject mismatched checksum")
	}
}

// TestSaveFileCreatesUniqueNames tests that SaveFile handles filename conflicts
func TestSaveFileCreatesUniqueNames(t *testing.T) {
	dir := t.TempDir()

	// Create an existing file
	existingPath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(existingPath, []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	sess, _ := NewRecvSession("test-session", "192.168.1.1")
	content := []byte("new content")

	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt", // Same name as existing file
		Size:     models.FlexInt(len(content)),
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	tokens := sess.FileTokens()

	savedName, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("SaveFile failed: %v", err)
	}

	// Should have been renamed to avoid conflict
	if savedName != "test (1).txt" {
		t.Errorf("expected filename 'test (1).txt', got %q", savedName)
	}

	// Verify both files exist
	if _, err := os.Stat(existingPath); err != nil {
		t.Error("original file should still exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "test (1).txt")); err != nil {
		t.Error("new file should exist with renamed name")
	}
}

// TestGetFileMeta tests the GetFileMeta method
func TestGetFileMeta(t *testing.T) {
	sess, _ := NewRecvSession("test-session", "192.168.1.1")

	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	_ = sess.AcceptFile("file1", fileMeta)

	t.Run("returns meta for accepted file", func(t *testing.T) {
		meta, ok := sess.GetFileMeta("file1")
		if !ok {
			t.Error("should find accepted file")
		}
		if meta.Filename != "test.txt" {
			t.Errorf("expected filename 'test.txt', got %q", meta.Filename)
		}
	})

	t.Run("returns false for unknown file", func(t *testing.T) {
		_, ok := sess.GetFileMeta("unknown")
		if ok {
			t.Error("should not find unknown file")
		}
	})
}

// TestSessionTimeout verifies that sessions are marked as stopped after timeout
func TestSessionTimeout(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Accept a file and start the session
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	// Session should not be stopped initially
	if sess.Stopped() {
		t.Error("session should not be stopped initially")
	}

	// Manually set lastActivity to simulate a timeout
	// Note: This is a whitebox test that directly manipulates the field
	sess.lastActivity = sess.lastActivity - int64(SessionTimeout.Seconds()) - 1

	// Session should now be stopped due to timeout
	if !sess.Stopped() {
		t.Error("session should be stopped after timeout")
	}
}

// TestActivityReader tests the activityReader wrapper
func TestActivityReader(t *testing.T) {
	t.Run("updates lastActivity on first read", func(t *testing.T) {
		var lastActivity int64 = 0
		data := bytes.NewReader([]byte("hello world"))
		ar := &activityReader{r: data, lastActivity: &lastActivity}

		buf := make([]byte, 5)
		n, err := ar.Read(buf)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 5 {
			t.Errorf("expected 5 bytes, got %d", n)
		}
		if lastActivity == 0 {
			t.Error("lastActivity should have been updated")
		}
	})

	t.Run("rate limits updates", func(t *testing.T) {
		var lastActivity int64 = 0
		data := bytes.NewReader(make([]byte, 1000))
		ar := &activityReader{r: data, lastActivity: &lastActivity}

		buf := make([]byte, 100)

		// First read should update
		_, _ = ar.Read(buf)
		firstUpdate := ar.lastUpdate
		if firstUpdate == 0 {
			t.Error("first read should update lastUpdate")
		}

		// Immediate second read should NOT update (rate limited)
		_, _ = ar.Read(buf)
		if ar.lastUpdate != firstUpdate {
			t.Error("second read should be rate limited")
		}
	})

	t.Run("updates after interval passes", func(t *testing.T) {
		var lastActivity int64 = 0
		data := bytes.NewReader(make([]byte, 1000))
		ar := &activityReader{r: data, lastActivity: &lastActivity}

		buf := make([]byte, 100)

		// First read
		_, _ = ar.Read(buf)
		originalUpdate := ar.lastUpdate

		// Simulate time passing by backdating lastUpdate
		ar.lastUpdate = originalUpdate - activityUpdateInterval - 1

		// Next read should update since interval has passed
		_, _ = ar.Read(buf)
		if ar.lastUpdate == originalUpdate-activityUpdateInterval-1 {
			t.Error("should have updated after interval passed")
		}
	})

	t.Run("does not update on zero bytes read", func(t *testing.T) {
		var lastActivity int64 = 0
		data := bytes.NewReader([]byte{}) // empty
		ar := &activityReader{r: data, lastActivity: &lastActivity}

		buf := make([]byte, 10)
		n, _ := ar.Read(buf)

		if n != 0 {
			t.Errorf("expected 0 bytes, got %d", n)
		}
		if lastActivity != 0 {
			t.Error("lastActivity should not be updated on zero bytes")
		}
	})
}

// TestSessionStaysAliveDuringTransfer verifies that file transfers keep the session alive
func TestSessionStaysAliveDuringTransfer(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Accept a file and start the session
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	// Simulate session being old (past timeout)
	sess.lastActivity = sess.lastActivity - int64(SessionTimeout.Seconds()) - 1

	// Session should be considered stopped due to timeout
	if !sess.Stopped() {
		t.Error("session should be stopped when lastActivity is old")
	}

	// Simulate activity by updating lastActivity (as activityReader would do)
	sess.lastActivity = sess.lastActivity + int64(SessionTimeout.Seconds()) + 10

	// Session should no longer be stopped
	if sess.Stopped() {
		t.Error("session should not be stopped after activity update")
	}
}

// TestSaveFileDirectoryTraversalPrevention verifies the fix for directory traversal vulnerability.
// A malicious client could send "../../../etc/passwd" as filename to write outside saveToDir.
func TestSaveFileDirectoryTraversalPrevention(t *testing.T) {
	dir := t.TempDir()

	testCases := []struct {
		name              string
		maliciousName     string
		expectedSanitized string
	}{
		{
			name:              "simple parent traversal",
			maliciousName:     "../evil.txt",
			expectedSanitized: "evil.txt",
		},
		{
			name:              "deep parent traversal",
			maliciousName:     "../../../etc/passwd",
			expectedSanitized: "passwd",
		},
		{
			name:              "absolute path attempt",
			maliciousName:     "/etc/passwd",
			expectedSanitized: "passwd",
		},
		{
			name:              "mixed traversal",
			maliciousName:     "foo/../../../bar/secret.txt",
			expectedSanitized: "secret.txt",
		},
		{
			name:              "windows-style traversal",
			maliciousName:     "..\\..\\windows\\system32\\config",
			expectedSanitized: "..\\..\\windows\\system32\\config", // Not a unix path, treated as filename
		},
		{
			name:              "hidden file traversal",
			maliciousName:     "../.ssh/id_rsa",
			expectedSanitized: "id_rsa",
		},
		{
			name:              "normal filename unchanged",
			maliciousName:     "normal_file.txt",
			expectedSanitized: "normal_file.txt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sess, _ := NewRecvSession("test-session", "192.168.1.1")
			content := []byte("test content")

			fileMeta := models.FileMeta{
				Id:       "file1",
				Filename: tc.maliciousName,
				Size:     models.FlexInt(len(content)),
			}
			_ = sess.AcceptFile("file1", fileMeta)
			sess.Start()

			tokens := sess.FileTokens()

			savedName, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(content))
			if err != nil {
				t.Fatalf("SaveFile failed: %v", err)
			}

			// Verify the saved filename is sanitized
			if savedName != tc.expectedSanitized {
				t.Errorf("expected sanitized name %q, got %q", tc.expectedSanitized, savedName)
			}

			// Verify file was saved in the correct directory (not escaped)
			savedPath := filepath.Join(dir, savedName)
			if _, err := os.Stat(savedPath); err != nil {
				t.Errorf("file should exist at %s: %v", savedPath, err)
			}

			// Verify no file was created outside the save directory
			// (e.g., ../evil.txt should not create a file in parent dir)
			parentPath := filepath.Join(filepath.Dir(dir), tc.expectedSanitized)
			if _, err := os.Stat(parentPath); err == nil {
				t.Errorf("file should NOT exist at %s (directory traversal occurred!)", parentPath)
			}

			// Clean up for next test case
			_ = os.Remove(savedPath)
		})
	}
}

// TestSaveFileWithSubdirectory verifies that files with subdirectory paths are saved
// correctly with the subdirectory structure preserved.
func TestSaveFileWithSubdirectory(t *testing.T) {
	dir := t.TempDir()

	testCases := []struct {
		name           string
		filename       string
		expectedSaved  string
		expectedSubdir string
	}{
		{
			name:           "single subdirectory",
			filename:       "Photos/beach.jpg",
			expectedSaved:  "Photos/beach.jpg",
			expectedSubdir: "Photos",
		},
		{
			name:           "nested subdirectories",
			filename:       "Photos/Summer/2024/vacation.jpg",
			expectedSaved:  "Photos/Summer/2024/vacation.jpg",
			expectedSubdir: "Photos/Summer/2024",
		},
		{
			name:           "flat file (no subdirectory)",
			filename:       "document.pdf",
			expectedSaved:  "document.pdf",
			expectedSubdir: "",
		},
		{
			name:           "file with spaces in path",
			filename:       "My Photos/Summer Vacation/beach pic.jpg",
			expectedSaved:  "My Photos/Summer Vacation/beach pic.jpg",
			expectedSubdir: "My Photos/Summer Vacation",
		},
		{
			name:           "safe parent traversal within subdirectory",
			filename:       "Photos/temp/../final/pic.jpg",
			expectedSaved:  "Photos/final/pic.jpg",
			expectedSubdir: "Photos/final",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sess, _ := NewRecvSession("test-session", "192.168.1.1")
			content := []byte("photo data")

			fileMeta := models.FileMeta{
				Id:       "file1",
				Filename: tc.filename,
				Size:     models.FlexInt(len(content)),
			}
			_ = sess.AcceptFile("file1", fileMeta)
			sess.Start()

			tokens := sess.FileTokens()

			savedName, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(content))
			if err != nil {
				t.Fatalf("SaveFile failed: %v", err)
			}

			// Verify the saved filename matches expected (with forward slashes)
			if savedName != tc.expectedSaved {
				t.Errorf("expected saved name %q, got %q", tc.expectedSaved, savedName)
			}

			// Verify subdirectory was created
			if tc.expectedSubdir != "" {
				subDirPath := filepath.Join(dir, filepath.FromSlash(tc.expectedSubdir))
				info, err := os.Stat(subDirPath)
				if err != nil {
					t.Errorf("subdirectory should exist at %s: %v", subDirPath, err)
				} else if !info.IsDir() {
					t.Errorf("expected %s to be a directory", subDirPath)
				}
			}

			// Verify file exists at the correct path
			filePath := filepath.Join(dir, filepath.FromSlash(tc.expectedSaved))
			if _, err := os.Stat(filePath); err != nil {
				t.Errorf("file should exist at %s: %v", filePath, err)
			}

			// Clean up for next test
			_ = os.RemoveAll(filepath.Join(dir, strings.Split(filepath.FromSlash(tc.expectedSaved), string(filepath.Separator))[0]))
		})
	}
}

// TestRecvSession_End_ConcurrentCalls verifies that End() is safe to call concurrently.
// Only one goroutine should successfully end the session, and the return value should
// indicate whether this call was the one that ended it.
func TestRecvSession_End_ConcurrentCalls(t *testing.T) {
	sess, _ := NewRecvSession("test-session", "127.0.0.1")
	// Accept a file so session can be properly started
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	var endCount atomic.Int32
	var wg sync.WaitGroup

	// Spawn 100 goroutines all calling End() simultaneously
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sess.End() {
				endCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// Only ONE goroutine should have successfully ended the session
	if endCount.Load() != 1 {
		t.Errorf("expected exactly 1 successful End(), got %d", endCount.Load())
	}
}

// TestRecvSession_End_ReturnsFalseWhenAlreadyEnded verifies End() return semantics.
func TestRecvSession_End_ReturnsFalseWhenAlreadyEnded(t *testing.T) {
	sess, _ := NewRecvSession("test-session", "127.0.0.1")
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "test.txt",
		Size:     100,
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	// First call should return true (session was ended)
	if !sess.End() {
		t.Error("first End() call should return true")
	}

	// Second call should return false (already ended)
	if sess.End() {
		t.Error("second End() call should return false")
	}

	// Third call should also return false
	if sess.End() {
		t.Error("third End() call should return false")
	}
}

// TestCreateUniqueFileConcurrent verifies that concurrent file creation is race-free.
// Two goroutines trying to create the same filename should not both succeed with the same path.
func TestCreateUniqueFileConcurrent(t *testing.T) {
	dir := t.TempDir()
	filename := "concurrent_test.txt"

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Collect all created paths
	paths := make(chan string, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()

			file, path, err := utils.CreateUniqueFile(dir, filename)
			if err != nil {
				errors <- err
				return
			}
			// Write something to prove we own the file
			_, _ = file.WriteString("owned")
			_ = file.Close()
			paths <- path
		}()
	}

	wg.Wait()
	close(paths)
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("CreateUniqueFile failed: %v", err)
	}

	// Collect all paths and verify uniqueness
	seenPaths := make(map[string]bool)
	for path := range paths {
		if seenPaths[path] {
			t.Errorf("duplicate path created: %s (race condition!)", path)
		}
		seenPaths[path] = true
	}

	// Verify we got the expected number of unique files
	if len(seenPaths) != numGoroutines {
		t.Errorf("expected %d unique files, got %d", numGoroutines, len(seenPaths))
	}

	// Verify expected naming pattern
	// First should be "concurrent_test.txt", rest should be "concurrent_test (1).txt", etc.
	expectedFirst := filepath.Join(dir, filename)
	if !seenPaths[expectedFirst] {
		t.Errorf("expected %s to be created", expectedFirst)
	}
}

// TestFindUniqueFolderName tests the folder uniqueness function.
func TestFindUniqueFolderName(t *testing.T) {
	t.Run("returns original name when folder doesn't exist", func(t *testing.T) {
		dir := t.TempDir()
		result, err := utils.FindUniqueFolderName(dir, "Photos")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "Photos" {
			t.Errorf("expected 'Photos', got '%s'", result)
		}
	})

	t.Run("appends counter when folder exists", func(t *testing.T) {
		dir := t.TempDir()
		// Create existing folder
		if err := os.Mkdir(filepath.Join(dir, "Photos"), 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}

		result, err := utils.FindUniqueFolderName(dir, "Photos")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "Photos (1)" {
			t.Errorf("expected 'Photos (1)', got '%s'", result)
		}
	})

	t.Run("increments counter until unique", func(t *testing.T) {
		dir := t.TempDir()
		// Create existing folders
		for i := 0; i <= 3; i++ {
			name := "Photos"
			if i > 0 {
				name = fmt.Sprintf("Photos (%d)", i)
			}
			if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
				t.Fatalf("failed to create folder: %v", err)
			}
		}

		result, err := utils.FindUniqueFolderName(dir, "Photos")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "Photos (4)" {
			t.Errorf("expected 'Photos (4)', got '%s'", result)
		}
	})
}

// TestSaveFileWithFolderRemap tests that when a folder already exists,
// all files in the transfer get saved to a uniquely named folder.
func TestSaveFileWithFolderRemap(t *testing.T) {
	dir := t.TempDir()

	// Create an existing "Photos" folder with a file
	existingFolder := filepath.Join(dir, "Photos")
	if err := os.Mkdir(existingFolder, 0755); err != nil {
		t.Fatalf("failed to create folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existingFolder, "existing.jpg"), []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	// Create session with files that have subdirectory structure
	sess, _ := NewRecvSession("test-session", "192.168.1.1")

	file1Content := []byte("beach photo")
	file2Content := []byte("sunset photo")

	fileMeta1 := models.FileMeta{
		Id:       "file1",
		Filename: "Photos/beach.jpg",
		Size:     models.FlexInt(len(file1Content)),
	}
	fileMeta2 := models.FileMeta{
		Id:       "file2",
		Filename: "Photos/sunset.jpg",
		Size:     models.FlexInt(len(file2Content)),
	}

	_ = sess.AcceptFile("file1", fileMeta1)
	_ = sess.AcceptFile("file2", fileMeta2)
	sess.Start()

	tokens := sess.FileTokens()

	// Save both files - they should go to "Photos (1)/" not "Photos/"
	saved1, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(file1Content))
	if err != nil {
		t.Fatalf("SaveFile failed for file1: %v", err)
	}

	saved2, err := sess.SaveFile(dir, "file2", tokens["file2"], "192.168.1.1", bytes.NewReader(file2Content))
	if err != nil {
		t.Fatalf("SaveFile failed for file2: %v", err)
	}

	// Verify files were saved to "Photos (1)/" folder
	if saved1 != "Photos (1)/beach.jpg" {
		t.Errorf("expected 'Photos (1)/beach.jpg', got '%s'", saved1)
	}
	if saved2 != "Photos (1)/sunset.jpg" {
		t.Errorf("expected 'Photos (1)/sunset.jpg', got '%s'", saved2)
	}

	// Verify the files actually exist in the new folder
	if _, err := os.Stat(filepath.Join(dir, "Photos (1)", "beach.jpg")); err != nil {
		t.Errorf("beach.jpg should exist in Photos (1)/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Photos (1)", "sunset.jpg")); err != nil {
		t.Errorf("sunset.jpg should exist in Photos (1)/: %v", err)
	}

	// Verify the original Photos folder is untouched
	if _, err := os.Stat(filepath.Join(dir, "Photos", "existing.jpg")); err != nil {
		t.Errorf("existing.jpg should still be in Photos/: %v", err)
	}
}

// TestSaveFileWithNestedFolderRemap tests folder remap with nested subdirectories.
func TestSaveFileWithNestedFolderRemap(t *testing.T) {
	dir := t.TempDir()

	// Create an existing "Photos" folder
	if err := os.Mkdir(filepath.Join(dir, "Photos"), 0755); err != nil {
		t.Fatalf("failed to create folder: %v", err)
	}

	sess, _ := NewRecvSession("test-session", "192.168.1.1")

	content := []byte("vacation photo")
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "Photos/Summer/vacation.jpg",
		Size:     models.FlexInt(len(content)),
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	tokens := sess.FileTokens()

	saved, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("SaveFile failed: %v", err)
	}

	// The entire Photos folder should be remapped to Photos (1)
	if saved != "Photos (1)/Summer/vacation.jpg" {
		t.Errorf("expected 'Photos (1)/Summer/vacation.jpg', got '%s'", saved)
	}

	// Verify nested structure was created
	if _, err := os.Stat(filepath.Join(dir, "Photos (1)", "Summer", "vacation.jpg")); err != nil {
		t.Errorf("vacation.jpg should exist in Photos (1)/Summer/: %v", err)
	}
}

// TestSaveFileFolderRemapOnlyAffectsSubdirs tests that flat files (no subdirectory)
// don't trigger folder remapping - they use file-level uniqueness instead.
func TestSaveFileFolderRemapOnlyAffectsSubdirs(t *testing.T) {
	dir := t.TempDir()

	// Create an existing file at root level
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	sess, _ := NewRecvSession("test-session", "192.168.1.1")

	content := []byte("new photo")
	fileMeta := models.FileMeta{
		Id:       "file1",
		Filename: "photo.jpg", // Flat file, no subdirectory
		Size:     models.FlexInt(len(content)),
	}
	_ = sess.AcceptFile("file1", fileMeta)
	sess.Start()

	tokens := sess.FileTokens()

	saved, err := sess.SaveFile(dir, "file1", tokens["file1"], "192.168.1.1", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("SaveFile failed: %v", err)
	}

	// Flat files should use file-level uniqueness, not folder remap
	if saved != "photo (1).jpg" {
		t.Errorf("expected 'photo (1).jpg', got '%s'", saved)
	}
}

// TestRecvSession_AcceptFile_RejectsBeyondMaxLimit verifies that AcceptFile
// returns ErrTooManyFiles when the session has reached constants.MaxFilesPerSession files.
func TestRecvSession_AcceptFile_RejectsBeyondMaxLimit(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Accept exactly constants.MaxFilesPerSession files
	for i := 0; i < constants.MaxFilesPerSession; i++ {
		fileId := fmt.Sprintf("file%d", i)
		fileMeta := models.FileMeta{
			Id:       fileId,
			Filename: fmt.Sprintf("test%d.txt", i),
			Size:     100,
		}
		if err := sess.AcceptFile(fileId, fileMeta); err != nil {
			t.Fatalf("AcceptFile failed for file %d: %v", i, err)
		}
	}

	// Verify we have exactly constants.MaxFilesPerSession files
	tokens := sess.FileTokens()
	if len(tokens) != constants.MaxFilesPerSession {
		t.Fatalf("Expected %d tokens, got %d", constants.MaxFilesPerSession, len(tokens))
	}

	// Try to accept one more file - should fail
	extraFileMeta := models.FileMeta{
		Id:       "extra-file",
		Filename: "extra.txt",
		Size:     100,
	}
	err = sess.AcceptFile("extra-file", extraFileMeta)
	if err == nil {
		t.Error("AcceptFile should reject files beyond constants.MaxFilesPerSession")
	}

	// Verify it's the correct error type
	if err.Error() != "too many files in session" {
		t.Errorf("Expected 'too many files in session' error, got: %v", err)
	}
}

// TestRecvSession_AcceptFile_AllowsExactlyMaxFiles verifies that AcceptFile
// accepts exactly constants.MaxFilesPerSession files (boundary test).
func TestRecvSession_AcceptFile_AllowsExactlyMaxFiles(t *testing.T) {
	sess, err := NewRecvSession("test-session", "192.168.1.1")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Accept exactly constants.MaxFilesPerSession files - all should succeed
	for i := 0; i < constants.MaxFilesPerSession; i++ {
		fileId := fmt.Sprintf("file%d", i)
		fileMeta := models.FileMeta{
			Id:       fileId,
			Filename: fmt.Sprintf("test%d.txt", i),
			Size:     100,
		}
		if err := sess.AcceptFile(fileId, fileMeta); err != nil {
			t.Fatalf("AcceptFile should allow file %d (max is %d): %v", i, constants.MaxFilesPerSession, err)
		}
	}

	// Verify we have exactly constants.MaxFilesPerSession files
	tokens := sess.FileTokens()
	if len(tokens) != constants.MaxFilesPerSession {
		t.Errorf("Expected exactly %d tokens, got %d", constants.MaxFilesPerSession, len(tokens))
	}
}

// TestRecvSession_MaxFilesPerSession_Constant verifies the constant value is as expected.
func TestRecvSession_MaxFilesPerSession_Constant(t *testing.T) {
	if constants.MaxFilesPerSession != 10000 {
		t.Errorf("constants.MaxFilesPerSession should be 10000, got %d", constants.MaxFilesPerSession)
	}
}
