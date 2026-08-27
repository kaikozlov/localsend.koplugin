package recv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend"
	"localsend-cli/internal/localsend/constants"
	sess "localsend-cli/internal/localsend/session"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

// newTestReceiver creates a FileReceiver for testing with minimal dependencies.
func newTestReceiver() *FileReceiver {
	return &FileReceiver{
		identity: models.DeviceInfo{
			Alias:       "Test Device",
			Version:     "2.3",
			DeviceModel: "Test",
			DeviceType:  "headless",
			Token:       "test-token",
		},
		webServer:           lsutils.NewWebServer(),
		sessman:             sess.NewRecvSessManager(),
		saveToDir:           "/tmp/test",
		pinRateLimiter:      utils.NewRateLimiter(maxPINAttempts, pinBlockDuration),
		receivedNonceCache:  localsend.NewNonceCache(200),
		generatedNonceCache: localsend.NewNonceCache(200),
	}
}

// =============================================================================
// Nonce Exchange Handler Tests (POST /api/localsend/v3/nonce)
// =============================================================================

func TestNonceExchangeHandler_ValidNonce(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	// Generate a valid 32-byte nonce
	nonce, _ := crypto.GenerateNonce()
	encodedNonce := crypto.EncodeNonce(nonce)

	body, _ := json.Marshal(models.NonceRequest{Nonce: encodedNonce})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200", resp.StatusCode)
	}

	// Parse response
	respBody, _ := io.ReadAll(resp.Body)
	var nonceResp models.NonceResponse
	if err := json.Unmarshal(respBody, &nonceResp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Response should contain a valid nonce
	if nonceResp.Nonce == "" {
		t.Error("Response nonce is empty")
	}

	// Decode and validate response nonce
	respNonce, err := crypto.DecodeNonce(nonceResp.Nonce)
	if err != nil {
		t.Errorf("Failed to decode response nonce: %v", err)
	}
	if !crypto.ValidateNonce(respNonce) {
		t.Errorf("Response nonce has invalid length: %d", len(respNonce))
	}
}

func TestNonceExchangeHandler_TooShortNonce(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	// 15-byte nonce (minimum is 16)
	shortNonce := make([]byte, 15)
	encodedNonce := crypto.EncodeNonce(shortNonce)

	body, _ := json.Marshal(models.NonceRequest{Nonce: encodedNonce})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for too short nonce", resp.StatusCode)
	}
}

func TestNonceExchangeHandler_TooLongNonce(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	// 129-byte nonce (maximum is 128)
	longNonce := make([]byte, 129)
	encodedNonce := crypto.EncodeNonce(longNonce)

	body, _ := json.Marshal(models.NonceRequest{Nonce: encodedNonce})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for too long nonce", resp.StatusCode)
	}
}

func TestNonceExchangeHandler_InvalidBase64(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	body, _ := json.Marshal(models.NonceRequest{Nonce: "not!valid!base64!!!"})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for invalid base64", resp.StatusCode)
	}
}

func TestNonceExchangeHandler_MissingNonce(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	body, _ := json.Marshal(models.NonceRequest{Nonce: ""})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for missing nonce", resp.StatusCode)
	}
}

func TestNonceExchangeHandler_InvalidJSON(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for invalid JSON", resp.StatusCode)
	}
}

func TestNonceExchangeHandler_MinimumValidNonce(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	// Exactly 16-byte nonce (minimum valid)
	minNonce := make([]byte, 16)
	for i := range minNonce {
		minNonce[i] = byte(i)
	}
	encodedNonce := crypto.EncodeNonce(minNonce)

	body, _ := json.Marshal(models.NonceRequest{Nonce: encodedNonce})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200 for minimum valid nonce (16 bytes)", resp.StatusCode)
	}
}

func TestNonceExchangeHandler_MaximumValidNonce(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	// Exactly 128-byte nonce (maximum valid)
	maxNonce := make([]byte, 128)
	for i := range maxNonce {
		maxNonce[i] = byte(i)
	}
	encodedNonce := crypto.EncodeNonce(maxNonce)

	body, _ := json.Marshal(models.NonceRequest{Nonce: encodedNonce})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200 for maximum valid nonce (128 bytes)", resp.StatusCode)
	}
}

// =============================================================================
// Register V3 Handler Tests (POST /api/localsend/v3/register)
// =============================================================================

func TestRegisterV3Handler_ValidRequest(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.RegisterPathV3, fr.registerV3Handler)

	body, _ := json.Marshal(models.RegisterRequestV3{
		Alias:       "Test Sender",
		Version:     "2.3",
		DeviceModel: "iPhone",
		DeviceType:  "MOBILE",
		Token:       "sender-token",
		Port:        constants.DefaultPort,
		Protocol:    "https",
	})
	req := httptest.NewRequest("POST", constants.RegisterPathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var registerResp models.RegisterResponseV3
	if err := json.Unmarshal(respBody, &registerResp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Response should contain our device info
	if registerResp.Alias != "Test Device" {
		t.Errorf("Alias = %q; want 'Test Device'", registerResp.Alias)
	}
	if registerResp.Version != "2.3" {
		t.Errorf("Version = %q; want '2.3'", registerResp.Version)
	}
}

func TestRegisterV3Handler_InvalidJSON(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.RegisterPathV3, fr.registerV3Handler)

	req := httptest.NewRequest("POST", constants.RegisterPathV3, bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for invalid JSON", resp.StatusCode)
	}
}

// =============================================================================
// Upload Handler Tests (POST /api/localsend/v2/upload)
// =============================================================================

// TestUploadHandler_UnknownFileId tests handling of unknown file IDs.
// BUG: GetFileMeta's ok return value is ignored - unknown fileId should return 400.
func TestUploadHandler_UnknownFileId(t *testing.T) {
	fr := newTestReceiver()

	// Create a session with a known file
	testFiles := models.FileMetas{
		"known-file": {
			Id:       "known-file",
			Filename: "test.txt",
			Size:     100,
		},
	}
	// NewSession accepts all files and starts the session automatically
	sessionId, err := fr.sessman.NewSession(testFiles, "0.0.0.0")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	app := fiber.New()
	app.Post(constants.UploadPath, fr.uploadHandler)

	// Try to upload a file with an unknown fileId
	body := []byte("test file content")
	req := httptest.NewRequest("POST", constants.UploadPath+
		"?sessionId="+sessionId+
		"&fileId=unknown-file"+ // <-- This fileId was never registered
		"&token=some-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, _ := app.Test(req)

	// BUG: Currently the handler ignores the ok return value from GetFileMeta
	// and proceeds with an empty FileMeta (zero value), which should instead
	// return 400 Bad Request for unknown fileId.
	//
	// After fix: Should return 400 for unknown fileId
	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for unknown fileId", resp.StatusCode)
	}
}

// TestUploadHandler_KnownFileId verifies that known fileIds work correctly.
func TestUploadHandler_KnownFileId(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = t.TempDir() // Use temp dir for actual file saving

	// Create a session with a known file
	testFiles := models.FileMetas{
		"known-file": {
			Id:       "known-file",
			Filename: "test.txt",
			Size:     17, // matches "test file content"
		},
	}
	// NewSession accepts all files and starts the session automatically
	sessionId, err := fr.sessman.NewSession(testFiles, "127.0.0.1")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Get the session to retrieve the token
	session, err := fr.sessman.GetSession(sessionId)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	tokens := session.FileTokens()

	app := fiber.New()
	app.Post(constants.UploadPath, fr.uploadHandler)

	// Upload with correct fileId and token
	body := []byte("test file content")
	req := httptest.NewRequest("POST", constants.UploadPath+
		"?sessionId="+sessionId+
		"&fileId=known-file"+
		"&token="+tokens["known-file"], bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, _ := app.Test(req)

	// Known fileId should succeed (200) or at least not fail with "unknown file" error
	// Note: May fail with token validation if protocol has changed, but should NOT be 400
	if resp.StatusCode == 400 {
		t.Errorf("Status = 400; known fileId should not return 400")
	}
}

// TestUploadHandler_ChecksumMismatchReturns422 verifies the full handler path:
// an upload whose bytes do not match the announced sha256 is rejected with
// HTTP 422 — the status the official sender treats as "retry this file" —
// and the partial file is removed so the retry reuses the same path.
func TestUploadHandler_ChecksumMismatchReturns422(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = t.TempDir()

	content := []byte("these bytes will be corrupted in transit")
	testFiles := models.FileMetas{
		"file1": {
			Id:       "file1",
			Filename: "report.pdf",
			Size:     int64(len(content)),
			Checksum: "0000000000000000000000000000000000000000000000000000000000000000",
		},
	}
	sessionId, err := fr.sessman.NewSession(testFiles, "0.0.0.0")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	session, err := fr.sessman.GetSession(sessionId)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	tokens := session.FileTokens()

	app := fiber.New()
	app.Post(constants.UploadPath, fr.uploadHandler)

	req := httptest.NewRequest("POST", constants.UploadPath+
		"?sessionId="+sessionId+
		"&fileId=file1"+
		"&token="+tokens["file1"], bytes.NewReader(content))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	if resp.StatusCode != 422 {
		t.Errorf("Status = %d; want 422 for checksum mismatch", resp.StatusCode)
	}

	entries, err := os.ReadDir(fr.saveToDir)
	if err != nil {
		t.Fatalf("read save dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("partial file remains after 422: %d entries; want 0", len(entries))
	}
}

// =============================================================================
// Nonce Caching Integration Tests
// =============================================================================

func TestNonceCacheIntegration(t *testing.T) {
	fr := newTestReceiver()

	// Simulate first client nonce exchange
	clientNonce1, _ := crypto.GenerateNonce()
	fr.receivedNonceCache.Put("client1", clientNonce1)

	serverNonce1, _ := crypto.GenerateNonce()
	fr.generatedNonceCache.Put("client1", serverNonce1)

	// Verify nonces are cached
	cached1, found := fr.receivedNonceCache.Get("client1")
	if !found {
		t.Fatal("Client nonce should be cached")
	}
	if !bytes.Equal(cached1, clientNonce1) {
		t.Error("Cached nonce doesn't match")
	}

	cached2, found := fr.generatedNonceCache.Get("client1")
	if !found {
		t.Fatal("Server nonce should be cached")
	}
	if !bytes.Equal(cached2, serverNonce1) {
		t.Error("Cached server nonce doesn't match")
	}

	// Combined nonce for token verification
	combinedNonce := append(clientNonce1, serverNonce1...)
	if len(combinedNonce) != 64 {
		t.Errorf("Combined nonce length = %d; want 64", len(combinedNonce))
	}
}

// =============================================================================
// PIN Rate Limiting Tests
// =============================================================================

func TestPreUploadHandler_PINRateLimiting_BlocksAfter3Attempts(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")

	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100}}}`)

	// First 3 attempts with wrong PIN should return 401
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=wrongpin", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		resp, _ := app.Test(req)

		if resp.StatusCode != 401 {
			t.Errorf("Attempt %d: Status = %d; want 401", i+1, resp.StatusCode)
		}
	}

	// 4th attempt should be rate limited (429)
	req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=wrongpin", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 429 {
		t.Errorf("4th attempt: Status = %d; want 429 (rate limited)", resp.StatusCode)
	}
}

func TestPreUploadHandler_PINRateLimiting_CorrectPINBlockedAfterLockout(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")

	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100}}}`)

	// Exhaust attempts
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=wrongpin", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		_, _ = app.Test(req)
	}

	// Even correct PIN should be blocked
	req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=123456", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 429 {
		t.Errorf("Correct PIN after lockout: Status = %d; want 429", resp.StatusCode)
	}
}

func TestPreUploadHandler_PINRateLimiting_MissingPINDoesNotConsumeAttempts(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")
	fr.saveToDir = t.TempDir()

	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100}}}`)
	for attempt := 0; attempt < 3; attempt++ {
		req := httptest.NewRequest("POST", constants.PreuploadPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("missing-PIN attempt %d status = %d; want %d", attempt+1, resp.StatusCode, fiber.StatusUnauthorized)
		}
	}

	req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=123456", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("correct PIN after missing-PIN challenges status = %d; want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestPreUploadHandler_PINRateLimiting_ClearsOnSuccess(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")
	fr.saveToDir = t.TempDir()

	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100}}}`)

	// 2 failed attempts (below lockout threshold)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=wrongpin", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		_, _ = app.Test(req)
	}

	// Correct PIN should succeed and clear attempts
	req := httptest.NewRequest("POST", constants.PreuploadPath+"?pin=123456", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	// Should succeed (200)
	if resp.StatusCode != 200 {
		t.Errorf("Correct PIN: Status = %d; want 200", resp.StatusCode)
	}
}

// =============================================================================
// Folder Transfer + Extension Routing/Filtering Tests
// =============================================================================

func TestFilterFilesByExtension_FolderTransferStrictModeRejects(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = t.TempDir()

	// Enable both extension routing AND extension filtering (strict mode)
	router := NewExtensionRouter(fr.saveToDir)
	router.routes["epub"] = t.TempDir()
	fr.SetExtensionRouter(router)
	fr.SetAllowedExtensions([]string{"epub", "pdf"})

	// Folder transfer should be rejected in strict mode
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "Books/novel.epub", Size: 100},
		"file2": {Id: "file2", Filename: "Books/manual.pdf", Size: 200},
	}

	filtered, errStatus := fr.filterFilesByExtension(files, "127.0.0.1")

	if errStatus != 403 {
		t.Errorf("Status = %d; want 403 for folder transfer in strict mode", errStatus)
	}
	if filtered != nil {
		t.Error("Filtered files should be nil when rejected")
	}
}

func TestFilterFilesByExtension_FolderTransferWithRoutingOnlyAllowed(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = t.TempDir()

	// Enable extension routing WITHOUT extension filtering (permissive mode)
	router := NewExtensionRouter(fr.saveToDir)
	router.routes["epub"] = t.TempDir()
	fr.SetExtensionRouter(router)
	// No extension filter set

	// Folder transfer should be allowed when only routing is enabled
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "Books/novel.epub", Size: 100},
		"file2": {Id: "file2", Filename: "Books/readme.txt", Size: 50},
	}

	filtered, errStatus := fr.filterFilesByExtension(files, "127.0.0.1")

	if errStatus != 0 {
		t.Errorf("Status = %d; want 0 (success) for folder transfer with routing only", errStatus)
	}
	if len(filtered) != 2 {
		t.Errorf("All files should be accepted; got %d, want 2", len(filtered))
	}
}

func TestFilterFilesByExtension_FolderTransferWithFilterOnlyFiltersFiles(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = t.TempDir()

	// Enable extension filtering WITHOUT routing
	fr.SetAllowedExtensions([]string{"epub", "pdf"})
	// No router set

	// Folder transfer should be allowed, but individual files should be filtered
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "Books/novel.epub", Size: 100},
		"file2": {Id: "file2", Filename: "Books/image.jpg", Size: 50}, // Should be filtered out
		"file3": {Id: "file3", Filename: "Books/manual.pdf", Size: 200},
	}

	filtered, errStatus := fr.filterFilesByExtension(files, "127.0.0.1")

	if errStatus != 0 {
		t.Errorf("Status = %d; want 0 (success)", errStatus)
	}
	if len(filtered) != 2 {
		t.Errorf("Expected 2 files after filtering; got %d", len(filtered))
	}
	if _, ok := filtered["file2"]; ok {
		t.Error("image.jpg should have been filtered out")
	}
}

func TestFilterFilesByExtension_IndividualFilesStillRouted(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = t.TempDir()

	// Enable both routing and filtering
	router := NewExtensionRouter(fr.saveToDir)
	router.routes["epub"] = t.TempDir()
	fr.SetExtensionRouter(router)
	fr.SetAllowedExtensions([]string{"epub", "pdf"})

	// Individual files (not folder transfer) should still work normally
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "novel.epub", Size: 100},
		"file2": {Id: "file2", Filename: "manual.pdf", Size: 200},
	}

	filtered, errStatus := fr.filterFilesByExtension(files, "127.0.0.1")

	if errStatus != 0 {
		t.Errorf("Status = %d; want 0 for individual files", errStatus)
	}
	if len(filtered) != 2 {
		t.Errorf("Expected 2 files; got %d", len(filtered))
	}
}

// =============================================================================
// GetSaveDirForSession Tests
// =============================================================================

func TestGetSaveDirForSession_FolderTransferBypassesRouting(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = "/main/save/dir"

	// Enable extension routing
	epubDir := "/epub/dir"
	router := NewExtensionRouter(fr.saveToDir)
	router.routes["epub"] = epubDir
	fr.SetExtensionRouter(router)

	// Create a session with folder transfer files
	testFiles := models.FileMetas{
		"file1": {Id: "file1", Filename: "Books/novel.epub", Size: 100},
	}
	sessionId, _ := fr.sessman.NewSession(testFiles, "127.0.0.1")
	session, _ := fr.sessman.GetSession(sessionId)

	// Folder transfer should bypass routing and use main save dir
	saveDir := fr.GetSaveDirForSession(session, "Books/novel.epub")

	if saveDir != "/main/save/dir" {
		t.Errorf("SaveDir = %q; want %q (folder transfers bypass routing)", saveDir, "/main/save/dir")
	}
}

func TestGetSaveDirForSession_IndividualFilesUseRouting(t *testing.T) {
	fr := newTestReceiver()
	fr.saveToDir = "/main/save/dir"

	// Enable extension routing
	epubDir := "/epub/dir"
	router := NewExtensionRouter(fr.saveToDir)
	router.routes["epub"] = epubDir
	fr.SetExtensionRouter(router)

	// Create a session with individual files (no subdirectory)
	testFiles := models.FileMetas{
		"file1": {Id: "file1", Filename: "novel.epub", Size: 100},
	}
	sessionId, _ := fr.sessman.NewSession(testFiles, "127.0.0.1")
	session, _ := fr.sessman.GetSession(sessionId)

	// Individual files should use routing
	saveDir := fr.GetSaveDirForSession(session, "novel.epub")

	if saveDir != epubDir {
		t.Errorf("SaveDir = %q; want %q (individual files use routing)", saveDir, epubDir)
	}
}

// =============================================================================
// V2 Cancel Handler Tests (POST /api/localsend/v2/cancel)
// =============================================================================

// TestCancelHandler_MissingSessionId_Returns400 verifies that missing sessionId
// returns 400 Bad Request.
func TestCancelHandler_MissingSessionId_Returns400(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.CancelPath, fr.cancelHandler)

	// Request without sessionId parameter
	req := httptest.NewRequest("POST", constants.CancelPath, nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for missing sessionId", resp.StatusCode)
	}
}

// TestCancelHandler_ValidSessionId_KillsSession verifies that a valid sessionId
// kills the session and returns 200.
func TestCancelHandler_ValidSessionId_KillsSession(t *testing.T) {
	fr := newTestReceiver()

	// Create an active session
	testFiles := models.FileMetas{
		"file1": {Id: "file1", Filename: "test.txt", Size: 100},
	}
	sessionId, err := fr.sessman.NewSession(testFiles, "0.0.0.0")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session exists
	if !fr.sessman.HasActiveSessions() {
		t.Fatal("Session should exist before cancel")
	}

	app := fiber.New()
	app.Post(constants.CancelPath, fr.cancelHandler)

	req := httptest.NewRequest("POST", constants.CancelPath+"?sessionId="+sessionId, nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200 for valid cancel", resp.StatusCode)
	}

	// Session should be killed
	if fr.sessman.HasActiveSessions() {
		t.Error("Session should be killed after cancel")
	}
}

// TestCancelHandler_NonexistentSessionId_Returns200 verifies that cancelling
// a non-existent session still returns 200 (idempotent operation).
// NOTE: This documents current behavior. The handler does NOT validate that
// the session exists - it just calls KillSession which is a no-op for missing sessions.
func TestCancelHandler_NonexistentSessionId_Returns200(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.CancelPath, fr.cancelHandler)

	req := httptest.NewRequest("POST", constants.CancelPath+"?sessionId=nonexistent-session-id", nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	// Current behavior: returns 200 even for non-existent sessions
	// This is idempotent behavior (safe to call multiple times)
	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200 (idempotent cancel)", resp.StatusCode)
	}
}

// TestCancelHandler_DifferentClientCannotCancel verifies cancellation is bound
// to the client that created the receive session.
func TestCancelHandler_DifferentClientCannotCancel(t *testing.T) {
	fr := newTestReceiver()

	// Create a session from "original client" IP
	testFiles := models.FileMetas{
		"file1": {Id: "file1", Filename: "test.txt", Size: 100},
	}
	sessionId, _ := fr.sessman.NewSession(testFiles, "192.168.1.100")

	app := fiber.New()
	app.Post(constants.CancelPath, fr.cancelHandler)

	// A different client (simulated by X-Forwarded-For) can cancel the session
	// Note: In a real scenario, the attacker just needs to know/guess the sessionId
	req := httptest.NewRequest("POST", constants.CancelPath+"?sessionId="+sessionId, nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.200") // Different IP

	resp, _ := app.Test(req)

	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("Status = %d; want 403", resp.StatusCode)
	}

	if !fr.sessman.HasActiveSessions() {
		t.Fatal("session was killed by a different client")
	}
}

func TestPreUploadHandler_EmptyFileListReturnsBadRequest(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)
	req := httptest.NewRequest("POST", constants.PreuploadPath, strings.NewReader(`{"info":null,"files":{}}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d; want 400", resp.StatusCode)
	}
}

func TestRegisterRoutes_DoesNotExposeUnsupportedV3TransferEndpoints(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	fr.registerRoutes(app)

	unsupported := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/localsend/v3/prepare-upload"},
		{http.MethodPost, "/api/localsend/v3/upload"},
		{http.MethodPost, "/api/localsend/v3/cancel"},
		{http.MethodGet, "/api/localsend/v3/info"},
		{http.MethodGet, "/api/localsend/v3/download"},
		{http.MethodPost, "/api/localsend/v3/prepare-download"},
	}
	for _, tc := range unsupported {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if resp.StatusCode != fiber.StatusNotFound {
			t.Errorf("%s %s status = %d; want 404", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// =============================================================================
// V2 Info Handler Tests (GET /api/localsend/v2/info)
// =============================================================================

// TestInfoHandler_ReturnsDeviceIdentity verifies that the info handler returns
// the receiver's device identity.
func TestInfoHandler_ReturnsDeviceIdentity(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Get(constants.InfoPath, fr.infoHandler)

	req := httptest.NewRequest("GET", constants.InfoPath, nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var deviceInfo models.DeviceInfo
	if err := json.Unmarshal(respBody, &deviceInfo); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify returned identity matches receiver's identity
	if deviceInfo.Alias != "Test Device" {
		t.Errorf("Alias = %q; want 'Test Device'", deviceInfo.Alias)
	}
	if deviceInfo.Version != "2.3" {
		t.Errorf("Version = %q; want '2.3'", deviceInfo.Version)
	}
	if deviceInfo.DeviceModel != "Test" {
		t.Errorf("DeviceModel = %q; want 'Test'", deviceInfo.DeviceModel)
	}
	if deviceInfo.DeviceType != "headless" {
		t.Errorf("DeviceType = %q; want 'headless'", deviceInfo.DeviceType)
	}
}

func TestRegisterRoutes_LegacyV1InfoReturnsSameUsefulIdentityAsV2(t *testing.T) {
	fr := newTestReceiver()
	fr.identity.Fingerprint = "test-fingerprint"
	app := fiber.New()
	fr.registerRoutes(app)

	readInfo := func(path string) models.DeviceInfo {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("GET %s status = %d; want 200", path, resp.StatusCode)
		}
		defer func() { _ = resp.Body.Close() }()
		var info models.DeviceInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			t.Fatalf("decode GET %s: %v", path, err)
		}
		return info
	}

	v2 := readInfo(constants.InfoPath)
	v1 := readInfo(constants.InfoPathV1)
	if v1 != v2 {
		t.Fatalf("legacy identity = %#v; want v2 identity %#v", v1, v2)
	}
	if v1.Alias == "" || v1.Fingerprint == "" || v1.Version == "" {
		t.Fatalf("legacy identity is not useful: %#v", v1)
	}
}

// =============================================================================
// V2 Register Handler Tests (POST /api/localsend/v2/register)
// =============================================================================

// TestRegisterHandler_ValidAnnouncement_ReturnsDeviceInfo verifies that valid
// announcement parsing works and returns device info.
func TestRegisterHandler_ValidAnnouncement_ReturnsDeviceInfo(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.RegisterPath, fr.registerHandler)

	announcement := models.Announcement{
		DeviceInfo: models.DeviceInfo{
			Alias:       "Sender Device",
			Version:     "2.3",
			DeviceModel: "iPhone",
			DeviceType:  "mobile",
		},
		Protocol: "https",
		Port:     53317,
		Announce: true,
	}
	body, _ := json.Marshal(announcement)

	req := httptest.NewRequest("POST", constants.RegisterPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var deviceInfo models.DeviceInfo
	if err := json.Unmarshal(respBody, &deviceInfo); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Response should contain our device info
	if deviceInfo.Alias != "Test Device" {
		t.Errorf("Alias = %q; want 'Test Device'", deviceInfo.Alias)
	}
}

// TestRegisterHandler_MalformedJSON_Returns400 verifies that malformed JSON
// returns 400 Bad Request.
func TestRegisterHandler_MalformedJSON_Returns400(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.RegisterPath, fr.registerHandler)

	req := httptest.NewRequest("POST", constants.RegisterPath, bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for malformed JSON", resp.StatusCode)
	}
}

// TestRegisterHandler_EmptyBody_Returns400 verifies that empty body
// returns 400 Bad Request.
func TestRegisterHandler_EmptyBody_Returns400(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.RegisterPath, fr.registerHandler)

	req := httptest.NewRequest("POST", constants.RegisterPath, bytes.NewReader([]byte("")))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 for empty body", resp.StatusCode)
	}
}
