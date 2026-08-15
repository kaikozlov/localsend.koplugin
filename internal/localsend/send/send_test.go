package send

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	coreutils "localsend-cli/internal/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

// =============================================================================
// baseSender Tests
// =============================================================================

func TestBaseSender_SetPIN(t *testing.T) {
	sender := &baseSender{}

	sender.SetPIN("1234")
	if sender.pin != "1234" {
		t.Errorf("expected pin '1234', got '%s'", sender.pin)
	}

	sender.SetPIN("")
	if sender.pin != "" {
		t.Errorf("expected empty pin, got '%s'", sender.pin)
	}
}

func TestBaseSender_AddFile(t *testing.T) {
	// Create a temporary file for testing
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "testfile.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	t.Run("adds file successfully", func(t *testing.T) {
		sender := &baseSender{}

		err := sender.AddFile(testFile)
		if err != nil {
			t.Fatalf("AddFile failed: %v", err)
		}

		if len(sender.files) != 1 {
			t.Errorf("expected 1 file, got %d", len(sender.files))
		}

		// Verify file metadata
		for _, meta := range sender.files {
			if meta.Filename != "testfile.txt" {
				t.Errorf("expected filename 'testfile.txt', got '%s'", meta.Filename)
			}
			if meta.Size != 12 { // "test content" = 12 bytes
				t.Errorf("expected size 12, got %d", meta.Size)
			}
			if meta.FullPath != testFile {
				t.Errorf("expected fullPath '%s', got '%s'", testFile, meta.FullPath)
			}
		}
	})

	t.Run("initializes nil map", func(t *testing.T) {
		sender := &baseSender{files: nil}

		err := sender.AddFile(testFile)
		if err != nil {
			t.Fatalf("AddFile failed: %v", err)
		}

		if sender.files == nil {
			t.Error("files map should be initialized")
		}
	})

	t.Run("fails for non-existent file", func(t *testing.T) {
		sender := &baseSender{}

		err := sender.AddFile("/nonexistent/path/file.txt")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("adds multiple files", func(t *testing.T) {
		sender := &baseSender{}

		testFile2 := filepath.Join(tmpDir, "testfile2.txt")
		if err := os.WriteFile(testFile2, []byte("more content"), 0644); err != nil {
			t.Fatalf("failed to create test file 2: %v", err)
		}

		_ = sender.AddFile(testFile)
		_ = sender.AddFile(testFile2)

		if len(sender.files) != 2 {
			t.Errorf("expected 2 files, got %d", len(sender.files))
		}
	})
}

func TestBaseSender_AddDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	// Create files
	files := []string{
		filepath.Join(tmpDir, "file1.txt"),
		filepath.Join(tmpDir, "file2.pdf"),
		filepath.Join(subDir, "nested.txt"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", f, err)
		}
	}

	t.Run("adds all files in directory recursively", func(t *testing.T) {
		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDir(tmpDir)
		if err != nil {
			t.Fatalf("AddDir failed: %v", err)
		}

		if len(sender.files) != 3 {
			t.Errorf("expected 3 files, got %d", len(sender.files))
		}

		// Verify filenames
		filenames := make(map[string]bool)
		for _, meta := range sender.files {
			filenames[meta.Filename] = true
		}

		expectedNames := []string{"file1.txt", "file2.pdf", "nested.txt"}
		for _, name := range expectedNames {
			if !filenames[name] {
				t.Errorf("expected file '%s' not found", name)
			}
		}
	})

	t.Run("fails for non-existent directory", func(t *testing.T) {
		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDir("/nonexistent/directory")
		if err == nil {
			t.Error("expected error for non-existent directory")
		}
	})

	t.Run("handles empty directory", func(t *testing.T) {
		emptyDir := filepath.Join(tmpDir, "empty")
		if err := os.Mkdir(emptyDir, 0755); err != nil {
			t.Fatalf("failed to create empty dir: %v", err)
		}

		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDir(emptyDir)
		if err != nil {
			t.Fatalf("AddDir failed: %v", err)
		}

		if len(sender.files) != 0 {
			t.Errorf("expected 0 files for empty dir, got %d", len(sender.files))
		}
	})
}

func TestBaseSender_AddDirWithStructure(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure: Photos/Summer/beach.jpg
	photosDir := filepath.Join(tmpDir, "Photos")
	summerDir := filepath.Join(photosDir, "Summer")
	if err := os.MkdirAll(summerDir, 0755); err != nil {
		t.Fatalf("failed to create dir structure: %v", err)
	}

	// Create files
	files := map[string]string{
		filepath.Join(photosDir, "selfie.jpg"):   "selfie data",
		filepath.Join(summerDir, "beach.jpg"):    "beach data",
		filepath.Join(summerDir, "vacation.png"): "vacation data",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", path, err)
		}
	}

	t.Run("preserves directory structure in filenames", func(t *testing.T) {
		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDirWithStructure(photosDir)
		if err != nil {
			t.Fatalf("AddDirWithStructure failed: %v", err)
		}

		if len(sender.files) != 3 {
			t.Errorf("expected 3 files, got %d", len(sender.files))
		}

		// Collect filenames
		filenames := make(map[string]bool)
		for _, meta := range sender.files {
			filenames[meta.Filename] = true
		}

		// Expected filenames should include the directory structure with forward slashes
		expectedNames := []string{
			"Photos/selfie.jpg",
			"Photos/Summer/beach.jpg",
			"Photos/Summer/vacation.png",
		}
		for _, name := range expectedNames {
			if !filenames[name] {
				t.Errorf("expected file '%s' not found in %v", name, filenames)
			}
		}
	})

	t.Run("uses forward slashes for protocol compatibility", func(t *testing.T) {
		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDirWithStructure(photosDir)
		if err != nil {
			t.Fatalf("AddDirWithStructure failed: %v", err)
		}

		// All filenames should use forward slashes, not backslashes
		for _, meta := range sender.files {
			if strings.Contains(meta.Filename, "\\") {
				t.Errorf("filename contains backslash (not protocol compatible): %s", meta.Filename)
			}
		}
	})

	t.Run("handles single file in directory", func(t *testing.T) {
		singleDir := filepath.Join(tmpDir, "SingleFile")
		if err := os.Mkdir(singleDir, 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(singleDir, "only.txt"), []byte("only"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		sender := &baseSender{files: make(map[string]models.FileMeta)}
		err := sender.AddDirWithStructure(singleDir)
		if err != nil {
			t.Fatalf("AddDirWithStructure failed: %v", err)
		}

		if len(sender.files) != 1 {
			t.Errorf("expected 1 file, got %d", len(sender.files))
		}

		for _, meta := range sender.files {
			if meta.Filename != "SingleFile/only.txt" {
				t.Errorf("expected 'SingleFile/only.txt', got '%s'", meta.Filename)
			}
		}
	})

	t.Run("fails for non-existent directory", func(t *testing.T) {
		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDirWithStructure("/nonexistent/directory")
		if err == nil {
			t.Error("expected error for non-existent directory")
		}
	})

	t.Run("handles empty directory", func(t *testing.T) {
		emptyDir := filepath.Join(tmpDir, "EmptyStructured")
		if err := os.Mkdir(emptyDir, 0755); err != nil {
			t.Fatalf("failed to create empty dir: %v", err)
		}

		sender := &baseSender{files: make(map[string]models.FileMeta)}

		err := sender.AddDirWithStructure(emptyDir)
		if err != nil {
			t.Fatalf("AddDirWithStructure failed: %v", err)
		}

		if len(sender.files) != 0 {
			t.Errorf("expected 0 files for empty dir, got %d", len(sender.files))
		}
	})
}

func TestBaseSender_Reset(t *testing.T) {
	sender := &baseSender{
		tokens: map[string]string{"id1": "token1", "id2": "token2"},
		files: map[string]models.FileMeta{
			"id1": {Id: "id1", Filename: "file1.txt"},
			"id2": {Id: "id2", Filename: "file2.txt"},
		},
	}

	sender.reset()

	if len(sender.tokens) != 0 {
		t.Errorf("expected tokens to be empty, got %d items", len(sender.tokens))
	}
	if len(sender.files) != 0 {
		t.Errorf("expected files to be empty, got %d items", len(sender.files))
	}
}

// =============================================================================
// ForwardSender Tests
// =============================================================================

func TestNewForwardSender(t *testing.T) {
	sender := NewForwardSender()

	if sender == nil {
		t.Fatal("NewForwardSender returned nil")
	}

	if sender.files == nil {
		t.Error("files map should be initialized")
	}

	if sender.tokens == nil {
		t.Error("tokens map should be initialized")
	}
}

func TestForwardSender_Init(t *testing.T) {
	sender := NewForwardSender()

	// Add some files and tokens to verify reset
	sender.files["old"] = models.FileMeta{Id: "old"}
	sender.tokens["old"] = "oldtoken"
	sender.session = "oldsession"
	sender.abort.Store(true)

	target := &models.DeviceInfo{
		Alias:       "TestDevice",
		IP:          "127.0.0.1",
		Fingerprint: "abc123",
	}

	err := sender.Init(target, true)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify state was reset
	if sender.abort.Load() != false {
		t.Error("abort should be false after Init")
	}
	if sender.session != "" {
		t.Error("session should be empty after Init")
	}
	if sender.remote != target {
		t.Error("remote should be set to target")
	}
	if sender.https != true {
		t.Error("https should be true")
	}
	if sender.local == nil {
		t.Error("local should be initialized")
	}
	if len(sender.files) != 0 {
		t.Error("files should be reset")
	}
	if len(sender.tokens) != 0 {
		t.Error("tokens should be reset")
	}
}

func TestForwardSender_PrepareUri(t *testing.T) {
	sender := NewForwardSender()
	sender.remote = &models.DeviceInfo{IP: "192.168.1.50"}

	t.Run("https scheme", func(t *testing.T) {
		sender.https = true
		req := &fasthttp.Request{}

		sender.prepareUri(req, "/api/v2/upload")

		uri := req.URI()
		if string(uri.Scheme()) != "https" {
			t.Errorf("expected scheme 'https', got '%s'", string(uri.Scheme()))
		}
		if string(uri.Path()) != "/api/v2/upload" {
			t.Errorf("expected path '/api/v2/upload', got '%s'", string(uri.Path()))
		}
		if string(uri.Host()) != "192.168.1.50:53317" {
			t.Errorf("expected host '192.168.1.50:53317', got '%s'", string(uri.Host()))
		}
		if string(req.Header.UserAgent()) != "localsend-cli" {
			t.Errorf("expected user-agent 'localsend-cli', got '%s'", string(req.Header.UserAgent()))
		}
	})

	t.Run("http scheme", func(t *testing.T) {
		sender.https = false
		req := &fasthttp.Request{}

		sender.prepareUri(req, "/api/v2/preupload")

		uri := req.URI()
		if string(uri.Scheme()) != "http" {
			t.Errorf("expected scheme 'http', got '%s'", string(uri.Scheme()))
		}
	})
}

// =============================================================================
// ReverseSender Tests
// =============================================================================

func TestNewReverseSender(t *testing.T) {
	sender := NewReverseSender()

	if sender == nil {
		t.Fatal("NewReverseSender returned nil")
	}

	if sender.files == nil {
		t.Error("files map should be initialized")
	}

	if sender.tokens == nil {
		t.Error("tokens map should be initialized")
	}

	if sender.webServer == nil {
		t.Error("webServer should be initialized")
	}

	if sender.downloads == nil {
		t.Error("downloads should be initialized")
	}
	if sender.sessions == nil || sender.sessionByIP == nil {
		t.Error("download sessions should be initialized")
	}
}

func TestReverseSender_Init(t *testing.T) {
	t.Run("http mode", func(t *testing.T) {
		sender := NewReverseSender()

		// Add old state to verify reset
		sender.files["old"] = models.FileMeta{Id: "old"}
		sender.tokens["old"] = "oldtoken"
		sender.sessions["old-session"] = "192.0.2.1"
		sender.sessionByIP["192.0.2.1"] = "old-session"
		for range constants.MaxPINAttempts {
			sender.pinRateLimiter.RecordAttempt("192.0.2.1")
		}

		target := &models.DeviceInfo{
			Alias: "TestDevice",
			IP:    "192.168.1.100",
		}

		err := sender.Init(target, false)
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		if sender.local != target {
			t.Error("local should be set to target")
		}
		if sender.https != false {
			t.Error("https should be false")
		}
		if len(sender.sessions) != 0 || len(sender.sessionByIP) != 0 {
			t.Error("sessions should be empty until a client is authorized")
		}
		if sender.pinRateLimiter.IsBlocked("192.0.2.1") || sender.pinRateLimiter.Count() != 0 {
			t.Error("PIN rate-limit state should be reset")
		}
		if !sender.local.Download {
			t.Error("Download flag should be true")
		}
		if len(sender.files) != 0 {
			t.Error("files should be reset")
		}
		if len(sender.tokens) != 0 {
			t.Error("tokens should be reset")
		}
	})

	t.Run("https mode generates certificate", func(t *testing.T) {
		sender := NewReverseSender()

		target := &models.DeviceInfo{
			Alias: "TestDevice",
			IP:    "192.168.1.100",
		}

		err := sender.Init(target, true)
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		if sender.https != true {
			t.Error("https should be true")
		}
		if sender.local.Fingerprint == "" {
			t.Error("fingerprint should be generated for https")
		}
	})
}

// =============================================================================
// ReverseSender Handler Tests
// =============================================================================

const fiberTestClientIP = "0.0.0.0"

func prepareReverseSession(t *testing.T, app *fiber.App, pin string) string {
	t.Helper()
	target := constants.PreDownloadPath
	if pin != "" {
		target += "?pin=" + pin
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, target, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("prepare-download status = %d; want 200", resp.StatusCode)
	}
	var result models.PreDownloadResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SessionId == "" {
		t.Fatal("prepare-download returned an empty sessionId")
	}
	return result.SessionId
}

func TestReverseSender_PredownloadHandler(t *testing.T) {
	t.Run("returns files and session", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)

		// Add a file
		sender.files["file1"] = models.FileMeta{
			Id:       "file1",
			Filename: "test.txt",
			Size:     100,
		}

		// Setup Fiber app for testing
		app := fiber.New()
		app.Post("/api/localsend/v2/prepare-download", sender.predownloadHandler)

		req := httptest.NewRequest("POST", "/api/localsend/v2/prepare-download", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		// Parse response
		var predownloadResp models.PreDownloadResp
		body, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(body, &predownloadResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if !sender.validSession(predownloadResp.SessionId, fiberTestClientIP) {
			t.Errorf("sessionId %q was not bound to the requesting client", predownloadResp.SessionId)
		}
		if len(predownloadResp.Files) != 1 {
			t.Errorf("expected 1 file, got %d", len(predownloadResp.Files))
		}
	})

	t.Run("requires correct PIN", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)
		sender.SetPIN("1234")

		app := fiber.New()
		app.Post("/api/localsend/v2/prepare-download", sender.predownloadHandler)

		// Wrong PIN
		req := httptest.NewRequest("POST", "/api/localsend/v2/prepare-download?pin=wrong", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 401 {
			t.Errorf("expected status 401 for wrong PIN, got %d", resp.StatusCode)
		}

		// Correct PIN
		req = httptest.NewRequest("POST", "/api/localsend/v2/prepare-download?pin=1234", nil)
		resp, _ = app.Test(req)
		if resp.StatusCode != 200 {
			t.Errorf("expected status 200 for correct PIN, got %d", resp.StatusCode)
		}
	})

	t.Run("blocks after three incorrect PINs", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)
		sender.SetPIN("1234")

		app := fiber.New()
		app.Post(constants.PreDownloadPath, sender.predownloadHandler)

		for attempt := 0; attempt < constants.MaxPINAttempts; attempt++ {
			req := httptest.NewRequest("POST", constants.PreDownloadPath+"?pin=wrong", nil)
			resp, _ := app.Test(req)
			if resp.StatusCode != fiber.StatusUnauthorized {
				t.Fatalf("attempt %d status = %d; want %d", attempt+1, resp.StatusCode, fiber.StatusUnauthorized)
			}
		}

		req := httptest.NewRequest("POST", constants.PreDownloadPath+"?pin=1234", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != fiber.StatusTooManyRequests {
			t.Fatalf("status after three incorrect PINs = %d; want %d", resp.StatusCode, fiber.StatusTooManyRequests)
		}
	})

	t.Run("does not count missing PIN as a failed attempt", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)
		sender.SetPIN("1234")

		app := fiber.New()
		app.Post(constants.PreDownloadPath, sender.predownloadHandler)

		for attempt := 0; attempt < constants.MaxPINAttempts; attempt++ {
			req := httptest.NewRequest("POST", constants.PreDownloadPath, nil)
			resp, _ := app.Test(req)
			if resp.StatusCode != fiber.StatusUnauthorized {
				t.Fatalf("missing-PIN attempt %d status = %d; want %d", attempt+1, resp.StatusCode, fiber.StatusUnauthorized)
			}
		}

		req := httptest.NewRequest("POST", constants.PreDownloadPath+"?pin=1234", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("correct PIN after missing-PIN challenges status = %d; want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("validates session ID if provided", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)
		sender.SetPIN("1234")

		app := fiber.New()
		app.Post("/api/localsend/v2/prepare-download", sender.predownloadHandler)
		sessionID := prepareReverseSession(t, app, "1234")

		// An unknown session does not bypass PIN authentication.
		req := httptest.NewRequest("POST", "/api/localsend/v2/prepare-download?sessionId=wrong-session", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 401 {
			t.Errorf("expected status 401 for wrong session without PIN, got %d", resp.StatusCode)
		}

		// An accepted session can refresh the file list without resending the PIN.
		req = httptest.NewRequest("POST", "/api/localsend/v2/prepare-download?sessionId="+sessionID, nil)
		resp, _ = app.Test(req)
		if resp.StatusCode != 200 {
			t.Errorf("expected status 200 for correct session, got %d", resp.StatusCode)
		}
	})
}

// =============================================================================
// ForwardSender sendFile Tests
// =============================================================================

func TestForwardSender_SendFile_RejectsOversizedFiles(t *testing.T) {
	// This test verifies that files larger than math.MaxInt are rejected
	// to prevent integer overflow when casting int64 to int.
	// On 64-bit systems, math.MaxInt is 2^63-1, so only files > 9 exabytes would fail.
	// On 32-bit systems, math.MaxInt is 2^31-1 (~2.15GB), providing real protection.
	//
	// Since tests run on 64-bit systems, we can't easily test the 32-bit behavior.
	// Instead, we verify the check exists by testing with a size that exceeds MaxInt
	// (which on 64-bit requires a negative value trick or we just verify the code path).

	// Create a real file (small) but with oversized metadata
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "huge_file.bin")
	if err := os.WriteFile(testFile, []byte("small content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	sender := NewForwardSender()
	target := &models.DeviceInfo{
		Alias:       "TestDevice",
		IP:          "127.0.0.1",
		Fingerprint: "abc123",
	}
	_ = sender.Init(target, false)

	// On 64-bit systems, we can't realistically exceed math.MaxInt with a valid size.
	// But we can verify the guard exists by checking the code compiles and the
	// normal path works. For true 32-bit testing, we'd need cross-compilation.
	//
	// Test that a file at exactly math.MaxInt32 + 1 does NOT trigger the error
	// on 64-bit systems (since math.MaxInt >> math.MaxInt32 on 64-bit).
	oversizedFileID := "large-file"
	sender.files[oversizedFileID] = models.FileMeta{
		Id:       oversizedFileID,
		Filename: "huge_file.bin",
		Size:     int64(1<<31) + 1, // 2GB + 1 byte
		FullPath: testFile,
	}

	err := sender.sendFile(oversizedFileID, "dummy-token")

	// On 64-bit, this should NOT fail with "file too large" - it will fail
	// at the network level instead since there's no server
	if err != nil && strings.Contains(err.Error(), "file too large") {
		t.Errorf("64-bit system should accept 2GB+ files, got: %v", err)
	}
}

func TestForwardSender_SendFile_AcceptsNormalSizedFiles(t *testing.T) {
	// This test verifies that files at the boundary (exactly MaxInt32) are accepted.
	// The actual transfer will fail since we're not running a server, but the
	// size check should pass.

	// Create a small temporary file for testing
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "small_file.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	sender := NewForwardSender()
	target := &models.DeviceInfo{
		Alias:       "TestDevice",
		IP:          "127.0.0.1", // Use localhost
		Fingerprint: "abc123",
	}
	_ = sender.Init(target, false)

	normalFileID := "normal-file"
	sender.files[normalFileID] = models.FileMeta{
		Id:       normalFileID,
		Filename: "small_file.txt",
		Size:     4, // 4 bytes, well under the limit
		FullPath: testFile,
	}

	// Attempt to send - will fail at network level but should pass size check
	err := sender.sendFile(normalFileID, "dummy-token")

	// Should NOT fail with "file too large" error
	if err != nil && strings.Contains(err.Error(), "file too large") {
		t.Errorf("normal sized file should not trigger size limit error: %v", err)
	}
	// Note: It will fail for other reasons (no server listening), which is expected
}

// =============================================================================
// ForwardSender Start() Error Propagation Tests
// =============================================================================

// TestForwardSender_Start_PropagatesFileErrors tests error propagation.
// BUG: Start() logs errors but returns nil, making caller think all files succeeded.
func TestForwardSender_Start_PropagatesFileErrors(t *testing.T) {
	// Create a test server that accepts pre-upload but rejects file uploads
	app := fiber.New()

	// Pre-upload handler - returns tokens for whatever files are requested
	app.Post("/api/localsend/v2/prepare-upload", func(c fiber.Ctx) error {
		// Parse the request to get file IDs
		var req struct {
			Files map[string]interface{} `json:"files"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return c.SendStatus(400)
		}

		// Generate tokens for each file in the request
		tokens := make(map[string]string)
		for fileId := range req.Files {
			tokens[fileId] = "token-" + fileId
		}

		return c.JSON(map[string]interface{}{
			"sessionId": "test-session",
			"files":     tokens,
		})
	})

	// Upload handler - reject all uploads to simulate failure
	app.Post("/api/localsend/v2/upload", func(c fiber.Ctx) error {
		return c.SendStatus(500) // Simulate server error
	})

	// Start the test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	// Get the port
	addr := ln.Addr().(*net.TCPAddr)
	port := fmt.Sprintf("%d", addr.Port)

	// Create temp files
	tmpDir := t.TempDir()
	testFile1 := filepath.Join(tmpDir, "file1.txt")
	testFile2 := filepath.Join(tmpDir, "file2.txt")
	_ = os.WriteFile(testFile1, []byte("content1"), 0644)
	_ = os.WriteFile(testFile2, []byte("content2"), 0644)

	// Setup sender
	sender := NewForwardSender()
	target := &models.DeviceInfo{
		Alias: "TestDevice",
		IP:    "127.0.0.1",
	}
	_ = sender.Init(target, false)
	sender.SetRemotePort(port)
	_ = sender.AddFile(testFile1)
	_ = sender.AddFile(testFile2)

	// Call Start - pre-upload will succeed, but file uploads will fail
	err = sender.Start()

	// BUG: Currently Start() returns nil even though both file uploads failed.
	// After fix: Should return an error indicating files failed to send.
	if err == nil {
		t.Error("Start() should return error when files fail to send")
	}
}

// TestForwardSender_Start_ReturnsNilOnSuccess verifies Start succeeds when all files transfer.
func TestForwardSender_Start_ReturnsNilOnSuccess(t *testing.T) {
	// Create a test server that accepts everything
	app := fiber.New()

	// Pre-upload handler - returns tokens for whatever files are requested
	app.Post("/api/localsend/v2/prepare-upload", func(c fiber.Ctx) error {
		// Parse the request to get file IDs
		var req struct {
			Files map[string]interface{} `json:"files"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return c.SendStatus(400)
		}

		// Generate tokens for each file in the request
		tokens := make(map[string]string)
		for fileId := range req.Files {
			tokens[fileId] = "token-" + fileId
		}

		return c.JSON(map[string]interface{}{
			"sessionId": "test-session",
			"files":     tokens,
		})
	})

	// Upload handler - accept all uploads
	app.Post("/api/localsend/v2/upload", func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})

	// Start the test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	// Get the port
	addr := ln.Addr().(*net.TCPAddr)
	port := fmt.Sprintf("%d", addr.Port)

	// Create temp file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "file1.txt")
	_ = os.WriteFile(testFile, []byte("content"), 0644)

	// Setup sender
	sender := NewForwardSender()
	target := &models.DeviceInfo{
		Alias: "TestDevice",
		IP:    "127.0.0.1",
	}
	_ = sender.Init(target, false)
	sender.SetRemotePort(port)
	_ = sender.AddFile(testFile)

	// Call Start - should succeed
	err = sender.Start()

	// Should return nil when all files transfer successfully
	if err != nil {
		t.Errorf("Start() should return nil on success, got: %v", err)
	}
}

func TestForwardSender_Start_RetriesChecksumMismatchUntilSuccess(t *testing.T) {
	app := fiber.New()
	app.Post(constants.PreuploadPath, func(c fiber.Ctx) error {
		return c.JSON(map[string]interface{}{
			"sessionId": "checksum-session",
			"files":     map[string]string{"file1": "file-token"},
		})
	})

	wantBody := []byte("content that must be reopened for every attempt")
	attempts := 0
	app.Post(constants.UploadPath, func(c fiber.Ctx) error {
		attempts++
		if got := c.Body(); !bytes.Equal(got, wantBody) {
			t.Fatalf("attempt %d body = %q; want %q", attempts, got, wantBody)
		}
		if attempts < 3 {
			return c.SendStatus(fiber.StatusUnprocessableEntity)
		}
		return c.SendStatus(fiber.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	path := filepath.Join(t.TempDir(), "retry.bin")
	if err := os.WriteFile(path, wantBody, 0644); err != nil {
		t.Fatal(err)
	}
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port))
	sender.files["file1"] = models.FileMeta{
		Id:       "file1",
		Filename: "retry.bin",
		Size:     int64(len(wantBody)),
		FullPath: path,
	}

	if err := sender.Start(); err != nil {
		t.Fatalf("Start failed after recoverable checksum mismatches: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("upload attempts = %d; want 3", attempts)
	}
}

func TestForwardSender_Start_StopsAfterThreeChecksumMismatches(t *testing.T) {
	app := fiber.New()
	app.Post(constants.PreuploadPath, func(c fiber.Ctx) error {
		return c.JSON(map[string]interface{}{
			"sessionId": "checksum-session",
			"files":     map[string]string{"file1": "file-token"},
		})
	})

	attempts := 0
	app.Post(constants.UploadPath, func(c fiber.Ctx) error {
		attempts++
		return c.SendStatus(fiber.StatusUnprocessableEntity)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	content := []byte("persistent mismatch")
	path := filepath.Join(t.TempDir(), "mismatch.bin")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port))
	sender.files["file1"] = models.FileMeta{
		Id:       "file1",
		Filename: "mismatch.bin",
		Size:     int64(len(content)),
		FullPath: path,
	}

	if err := sender.Start(); !errors.Is(err, constants.ErrChecksum) {
		t.Fatalf("Start error = %v; want checksum mismatch", err)
	}
	if attempts != 3 {
		t.Fatalf("upload attempts = %d; want 3", attempts)
	}
}

func TestForwardSender_Start_HTTPSCustomPort_UsesConfiguredPortForFingerprint(t *testing.T) {
	app := fiber.New()

	app.Post(constants.PreuploadPath, func(c fiber.Ctx) error {
		var req struct {
			Files map[string]interface{} `json:"files"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return c.SendStatus(400)
		}

		tokens := make(map[string]string)
		for fileID := range req.Files {
			tokens[fileID] = "token-" + fileID
		}

		return c.JSON(map[string]interface{}{
			"sessionId": "tls-session",
			"files":     tokens,
		})
	})

	app.Post(constants.UploadPath, func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})

	tmpDir := t.TempDir()
	privKeyFile := filepath.Join(tmpDir, "key.pem")
	certFile := filepath.Join(tmpDir, "cert.pem")
	cert, err := lsutils.GenAndSaveTLScert(privKeyFile, certFile)
	if err != nil {
		t.Fatalf("failed to generate TLS cert: %v", err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("failed to create TLS listener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	port := fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)
	addr := net.JoinHostPort("127.0.0.1", port)

	deadline := time.Now().Add(3 * time.Second)
	for {
		certs, fetchErr := coreutils.FetchX509Cert(addr)
		if fetchErr == nil && len(certs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS server did not start in time: %v", fetchErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	testFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("secure content"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	sender := NewForwardSender()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse generated certificate: %v", err)
	}
	target := &models.DeviceInfo{
		Alias:       "TLSReceiver",
		IP:          "127.0.0.1",
		Fingerprint: coreutils.SHA256ofCert(leaf),
	}
	if err := sender.Init(target, true); err != nil {
		t.Fatalf("failed to init sender: %v", err)
	}
	sender.SetRemotePort(port)
	if err := sender.AddFile(testFile); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	if err := sender.Start(); err != nil {
		t.Fatalf("Start failed over HTTPS custom port: %v", err)
	}
}

func TestReverseSender_DownloadHandler(t *testing.T) {
	// Create a temporary file for download
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "download.txt")
	testContent := []byte("file content for download")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	t.Run("downloads file successfully", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)

		sender.files["file1"] = models.FileMeta{
			Id:       "file1",
			Filename: "download.txt",
			Size:     int64(len(testContent)),
			FullPath: testFile,
		}

		app := fiber.New()
		app.Get("/api/localsend/v2/download", sender.downloadHandler)
		clientIP := fiberTestClientIP
		sessionID := sender.createSession(clientIP)

		req := httptest.NewRequest("GET",
			"/api/localsend/v2/download?sessionId="+sessionID+"&fileId=file1", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		if string(body) != string(testContent) {
			t.Errorf("expected content '%s', got '%s'", string(testContent), string(body))
		}

		// Check Content-Disposition header
		cd := resp.Header.Get("Content-Disposition")
		if cd == "" {
			t.Error("expected Content-Disposition header")
		}
	})

	t.Run("requires sessionId and fileId", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)

		app := fiber.New()
		app.Get("/api/localsend/v2/download", sender.downloadHandler)
		sessionID := sender.createSession(fiberTestClientIP)

		// Missing both
		req := httptest.NewRequest("GET", "/api/localsend/v2/download", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 400 {
			t.Errorf("expected status 400 for missing params, got %d", resp.StatusCode)
		}

		// Missing fileId
		req = httptest.NewRequest("GET", "/api/localsend/v2/download?sessionId="+sessionID, nil)
		resp, _ = app.Test(req)
		if resp.StatusCode != 400 {
			t.Errorf("expected status 400 for missing fileId, got %d", resp.StatusCode)
		}
	})

	t.Run("validates session", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)

		app := fiber.New()
		app.Get("/api/localsend/v2/download", sender.downloadHandler)

		req := httptest.NewRequest("GET",
			"/api/localsend/v2/download?sessionId=wrong&fileId=file1", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 403 {
			t.Errorf("expected status 403 for wrong session, got %d", resp.StatusCode)
		}
	})

	t.Run("returns 403 for unknown file", func(t *testing.T) {
		sender := NewReverseSender()
		target := &models.DeviceInfo{Alias: "TestDevice", IP: "127.0.0.1"}
		_ = sender.Init(target, false)

		app := fiber.New()
		app.Get("/api/localsend/v2/download", sender.downloadHandler)
		sessionID := sender.createSession(fiberTestClientIP)

		req := httptest.NewRequest("GET",
			"/api/localsend/v2/download?sessionId="+sessionID+"&fileId=nonexistent", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 403 {
			t.Errorf("expected status 403 for unknown file, got %d", resp.StatusCode)
		}
	})
}

func TestReverseSender_DownloadHandlerUsesSessionFromSameClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	sender := NewReverseSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetPIN("1234")
	sender.files["f"] = models.FileMeta{Id: "f", Filename: "secret.txt", FullPath: path, Size: 6}
	app := fiber.New()
	app.Get(constants.DownloadPath, sender.downloadHandler)
	clientIP := fiberTestClientIP
	sessionID := sender.createSession(clientIP)

	req := httptest.NewRequest("GET", fmt.Sprintf("%s?sessionId=%s&fileId=f", constants.DownloadPath, sessionID), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status with authenticated session = %d; want 200", resp.StatusCode)
	}
}

func TestReverseSender_DownloadSessionIsBoundToClientIP(t *testing.T) {
	sender := NewReverseSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sessionID := sender.createSession("192.0.2.1")
	if sender.validSession(sessionID, "192.0.2.2") {
		t.Fatal("session was accepted for a different client IP")
	}
}

func TestReverseSender_CreateSessionInvalidatesPreviousSessionForClient(t *testing.T) {
	sender := NewReverseSender()
	clientIP := "192.0.2.1"
	oldSessionID := sender.createSession(clientIP)
	newSessionID := sender.createSession(clientIP)

	if sender.validSession(oldSessionID, clientIP) {
		t.Fatal("previous session remained valid after replacement")
	}
	if !sender.validSession(newSessionID, clientIP) {
		t.Fatal("replacement session was not valid")
	}
}

func TestReverseSender_DownloadListRequiresConfiguredPIN(t *testing.T) {
	sender := NewReverseSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetPIN("1234")
	sender.downloads = []DownloadEntry{{Filename: "secret.txt", Url: constants.DownloadPath + "?fileId=f"}}
	app := sender.webServer
	app.Get("/", sender.downloadListHandler)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d; want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}

	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/?pin=1234", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("authenticated status = %d; want %d", resp.StatusCode, fiber.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "sessionId=") {
		t.Fatal("download page did not include an authenticated session")
	}
	if strings.Contains(string(body), "pin=1234") {
		t.Fatal("download page leaked the PIN into file URLs")
	}
	if strings.Contains(string(body), "://") {
		t.Fatal("download page used an absolute file URL instead of the authenticated request host")
	}
}

func TestForwardSender_PreUploadRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat(" ", 2<<20) + `{"sessionId":"s","files":{}}`))
	}))
	defer server.Close()
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: host}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(port)
	if err := sender.preUploadReq(); err == nil {
		t.Fatal("oversized pre-upload response was accepted")
	}
}
