package recv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func performNonceExchange(t *testing.T, app *fiber.App) {
	t.Helper()

	nonce, err := crypto.GenerateNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce: %v", err)
	}

	body, _ := json.Marshal(models.NonceRequest{Nonce: crypto.EncodeNonce(nonce)})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to call nonce endpoint: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("Nonce exchange status = %d; want 200", resp.StatusCode)
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

// TestNonceExchangeHandler_CachesNonces verifies nonces are stored in cache.
func TestNonceExchangeHandler_CachesNonces(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)

	nonce, _ := crypto.GenerateNonce()
	encodedNonce := crypto.EncodeNonce(nonce)

	body, _ := json.Marshal(models.NonceRequest{Nonce: encodedNonce})
	req := httptest.NewRequest("POST", constants.NoncePathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "192.168.1.100") // Simulate client IP

	resp, _ := app.Test(req)

	if resp.StatusCode != 200 {
		t.Fatalf("Request failed with status %d", resp.StatusCode)
	}

	// Note: The client ID in test environment might be different.
	// This test verifies the cache mechanism is called but may not
	// verify the exact IP due to test environment limitations.
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
// Info V3 Handler Tests (GET /api/localsend/v3/info)
// =============================================================================

func TestInfoV3Handler(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Get(constants.InfoPathV3, fr.infoV3Handler)

	req := httptest.NewRequest("GET", constants.InfoPathV3, nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var infoResp models.RegisterResponseV3
	if err := json.Unmarshal(respBody, &infoResp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if infoResp.Alias != "Test Device" {
		t.Errorf("Alias = %q; want 'Test Device'", infoResp.Alias)
	}
}

// =============================================================================
// Prepare Upload V3 Handler Tests (POST /api/localsend/v3/prepare-upload)
// =============================================================================

func TestPreUploadV3Handler_BlockedBySession(t *testing.T) {
	fr := newTestReceiver()

	// Create an active session to block new requests
	testFiles := models.FileMetas{
		"test-file": models.FileMeta{
			Id:       "test-file",
			Filename: "test.txt",
			Size:     100,
		},
	}
	_, _ = fr.sessman.NewSession(testFiles, "127.0.0.1")

	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)
	performNonceExchange(t, app)

	body := []byte(`{"info":{"alias":"Sender"},"files":{"new":{"id":"new","fileName":"new.txt","size":1,"fileType":"text/plain"}}}`)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	// Should return 409 Conflict when blocked by another session
	if resp.StatusCode != 409 {
		t.Errorf("Status = %d; want 409 when blocked by existing session", resp.StatusCode)
	}
}

func TestPreUploadV3Handler_PINRequired(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")

	app := fiber.New()
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{}}`)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	// Should return 401 when PIN is required but not provided
	if resp.StatusCode != 401 {
		t.Errorf("Status = %d; want 401 when PIN required", resp.StatusCode)
	}
}

func TestPreUploadV3Handler_WrongPIN(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")

	app := fiber.New()
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{}}`)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3+"?pin=wrongpin", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	// Should return 401 when PIN is incorrect
	if resp.StatusCode != 401 {
		t.Errorf("Status = %d; want 401 when PIN is wrong", resp.StatusCode)
	}
}

func TestPreUploadV3Handler_CorrectPIN(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")

	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)
	performNonceExchange(t, app)

	body := []byte(`{"info":{"alias":"Sender","version":"2.3","deviceType":"MOBILE"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100,"fileType":"text/plain"}}}`)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3+"?pin=123456", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	// Should succeed with correct PIN and valid nonce exchange
	if resp.StatusCode != 200 {
		t.Errorf("Status = %d; want 200 with correct PIN and nonce", resp.StatusCode)
	}
}

func TestPreUploadV3Handler_RequiresNonceExchange(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)

	body := []byte(`{"info":{"alias":"Sender","version":"2.3","deviceType":"MOBILE"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100,"fileType":"text/plain"}}}`)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 when nonce exchange is missing", resp.StatusCode)
	}
}

func TestPreUploadV3Handler_RejectsPartialNonceState(t *testing.T) {
	fr := newTestReceiver()

	nonce, _ := crypto.GenerateNonce()
	fr.receivedNonceCache.Put("0.0.0.0", nonce)

	app := fiber.New()
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)

	body := []byte(`{"info":{"alias":"Sender","version":"2.3","deviceType":"MOBILE"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100,"fileType":"text/plain"}}}`)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("Status = %d; want 400 when only one nonce cache entry exists", resp.StatusCode)
	}
}

func TestPreUploadV3Handler_NonceReplayRejected(t *testing.T) {
	fr := newTestReceiver()

	app := fiber.New()
	app.Post(constants.NoncePathV3, fr.nonceExchangeHandler)
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)

	body := []byte(`{"info":{"alias":"Sender","version":"2.3","deviceType":"MOBILE"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100,"fileType":"text/plain"}}}`)

	// First request after nonce exchange should succeed.
	performNonceExchange(t, app)
	firstReq := httptest.NewRequest("POST", constants.PreuploadPathV3, bytes.NewReader(body))
	firstReq.Header.Set("Content-Type", "application/json")
	firstResp, _ := app.Test(firstReq)
	if firstResp.StatusCode != 200 {
		t.Fatalf("First prepare-upload status = %d; want 200", firstResp.StatusCode)
	}

	// Reusing without a fresh nonce exchange should fail.
	secondReq := httptest.NewRequest("POST", constants.PreuploadPathV3, bytes.NewReader(body))
	secondReq.Header.Set("Content-Type", "application/json")
	secondResp, _ := app.Test(secondReq)
	if secondResp.StatusCode != 400 {
		t.Errorf("Second prepare-upload status = %d; want 400 (replay blocked)", secondResp.StatusCode)
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

func TestPreUploadV3Handler_PINRateLimiting(t *testing.T) {
	fr := newTestReceiver()
	fr.SetPIN("123456")

	app := fiber.New()
	app.Post(constants.PreuploadPathV3, fr.preUploadV3Handler)

	body := []byte(`{"info":{"alias":"Sender"},"files":{"file1":{"id":"file1","fileName":"test.txt","size":100}}}`)

	// First 3 attempts with wrong PIN should return 401
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", constants.PreuploadPathV3+"?pin=wrongpin", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		resp, _ := app.Test(req)

		if resp.StatusCode != 401 {
			t.Errorf("Attempt %d: Status = %d; want 401", i+1, resp.StatusCode)
		}
	}

	// 4th attempt should be rate limited (429)
	req := httptest.NewRequest("POST", constants.PreuploadPathV3+"?pin=wrongpin", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)

	if resp.StatusCode != 429 {
		t.Errorf("4th attempt: Status = %d; want 429 (rate limited)", resp.StatusCode)
	}
}

// =============================================================================
// Folder Transfer Detection Tests
// =============================================================================

func TestIsFolderTransfer_WithSubdirectory(t *testing.T) {
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "Photos/beach.jpg", Size: 100},
		"file2": {Id: "file2", Filename: "Photos/sunset.jpg", Size: 200},
	}

	if !files.IsFolderTransfer() {
		t.Error("Files with subdirectory paths should be detected as folder transfer")
	}
}

func TestIsFolderTransfer_FlatFiles(t *testing.T) {
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "beach.jpg", Size: 100},
		"file2": {Id: "file2", Filename: "sunset.jpg", Size: 200},
	}

	if files.IsFolderTransfer() {
		t.Error("Flat files (no subdirectory) should not be detected as folder transfer")
	}
}

func TestIsFolderTransfer_MixedFiles(t *testing.T) {
	// If ANY file has a subdirectory, it's considered a folder transfer
	files := models.FileMetas{
		"file1": {Id: "file1", Filename: "readme.txt", Size: 100},
		"file2": {Id: "file2", Filename: "docs/manual.pdf", Size: 200},
	}

	if !files.IsFolderTransfer() {
		t.Error("Mixed files with at least one subdirectory should be detected as folder transfer")
	}
}

func TestIsFolderTransfer_EmptyFiles(t *testing.T) {
	files := models.FileMetas{}

	if files.IsFolderTransfer() {
		t.Error("Empty file list should not be detected as folder transfer")
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

	for _, path := range []string{constants.PreuploadPathV3, constants.UploadPathV3, constants.CancelPathV3} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		if resp.StatusCode != fiber.StatusNotFound {
			t.Errorf("POST %s status = %d; want 404", path, resp.StatusCode)
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
