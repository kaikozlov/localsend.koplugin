package transfer

import (
	"encoding/json"
	"testing"
)

// TestParseRTCMessageNonce tests parsing of nonce messages.
func TestParseRTCMessageNonce(t *testing.T) {
	jsonData := `{"nonce":"xLJNxeKwfKvx1IYqVE_cYAUF54R547Aq6C_E_p_eilk"}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "nonce" {
		t.Errorf("msgType = %q; want 'nonce'", msgType)
	}

	nonceMsg, ok := msg.(*RTCNonceMessage)
	if !ok {
		t.Fatalf("msg is not *RTCNonceMessage")
	}

	if nonceMsg.Nonce != "xLJNxeKwfKvx1IYqVE_cYAUF54R547Aq6C_E_p_eilk" {
		t.Errorf("Nonce = %q; want expected value", nonceMsg.Nonce)
	}
}

// TestParseRTCMessageTokenRequest tests parsing of token request messages.
func TestParseRTCMessageTokenRequest(t *testing.T) {
	jsonData := `{"token":"sha256.abc123.def456.ed25519.sig789"}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "token_request" {
		t.Errorf("msgType = %q; want 'token_request'", msgType)
	}

	tokenMsg, ok := msg.(*RTCTokenRequest)
	if !ok {
		t.Fatalf("msg is not *RTCTokenRequest")
	}

	if tokenMsg.Token != "sha256.abc123.def456.ed25519.sig789" {
		t.Errorf("Token = %q; want expected value", tokenMsg.Token)
	}
}

// TestParseRTCMessageFileHeader tests parsing of file header messages.
// IMPORTANT: FileHeader has both id AND token, so it must be detected before TokenRequest.
func TestParseRTCMessageFileHeader(t *testing.T) {
	jsonData := `{"id":"0","token":"1766963574926146000"}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "file_header" {
		t.Errorf("msgType = %q; want 'file_header'", msgType)
	}

	header, ok := msg.(*RTCSendFileHeader)
	if !ok {
		t.Fatalf("msg is not *RTCSendFileHeader")
	}

	if header.ID != "0" {
		t.Errorf("ID = %q; want '0'", header.ID)
	}
	if header.Token != "1766963574926146000" {
		t.Errorf("Token = %q; want '1766963574926146000'", header.Token)
	}
}

// TestParseRTCMessageFileList tests parsing of file list messages.
func TestParseRTCMessageFileList(t *testing.T) {
	jsonData := `{"status":"OK","files":[{"id":"0","fileName":"test.md","size":1024,"fileType":"text/markdown"}]}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "file_list" {
		t.Errorf("msgType = %q; want 'file_list'", msgType)
	}

	fileList, ok := msg.(*RTCPinSendingResponse)
	if !ok {
		t.Fatalf("msg is not *RTCPinSendingResponse")
	}

	if fileList.Status != "OK" {
		t.Errorf("Status = %q; want 'OK'", fileList.Status)
	}
	if len(fileList.Files) != 1 {
		t.Errorf("len(Files) = %d; want 1", len(fileList.Files))
	}
	if fileList.Files[0].FileName != "test.md" {
		t.Errorf("Files[0].FileName = %q; want 'test.md'", fileList.Files[0].FileName)
	}
}

// TestParseRTCMessagePin tests parsing of PIN messages.
func TestParseRTCMessagePin(t *testing.T) {
	jsonData := `{"pin":"123456"}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "pin" {
		t.Errorf("msgType = %q; want 'pin'", msgType)
	}

	pinMsg, ok := msg.(*RTCPinMessage)
	if !ok {
		t.Fatalf("msg is not *RTCPinMessage")
	}

	if pinMsg.Pin != "123456" {
		t.Errorf("Pin = %q; want '123456'", pinMsg.Pin)
	}
}

// TestParseRTCMessagePinResponse tests parsing of PIN response messages.
func TestParseRTCMessagePinResponse(t *testing.T) {
	jsonData := `{"status":"OK"}`

	_, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "status_OK" {
		t.Errorf("msgType = %q; want 'status_OK'", msgType)
	}
}

// TestParseRTCMessageTooManyAttempts tests parsing of too many attempts status.
func TestParseRTCMessageTooManyAttempts(t *testing.T) {
	jsonData := `{"status":"TOO_MANY_ATTEMPTS"}`

	_, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "status_TOO_MANY_ATTEMPTS" {
		t.Errorf("msgType = %q; want 'status_TOO_MANY_ATTEMPTS'", msgType)
	}
}

// TestParseRTCMessageUnknown tests that unknown messages return nil.
func TestParseRTCMessageUnknown(t *testing.T) {
	jsonData := `{"unknown":"field"}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msg != nil {
		t.Errorf("msg = %v; want nil", msg)
	}
	if msgType != "" {
		t.Errorf("msgType = %q; want empty string", msgType)
	}
}

// TestRTCFileListResponseSerialization tests JSON serialization of file list response.
func TestRTCFileListResponseSerialization(t *testing.T) {
	response := RTCFileListResponse{
		Status: "OK",
		Files:  map[string]string{"0": "token123", "1": "token456"},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed RTCFileListResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if parsed.Status != "OK" {
		t.Errorf("Status = %q; want 'OK'", parsed.Status)
	}
	if len(parsed.Files) != 2 {
		t.Errorf("len(Files) = %d; want 2", len(parsed.Files))
	}
	if parsed.Files["0"] != "token123" {
		t.Errorf("Files['0'] = %q; want 'token123'", parsed.Files["0"])
	}
}

// TestRTCNonceMessageSerialization tests JSON serialization of nonce message.
func TestRTCNonceMessageSerialization(t *testing.T) {
	nonce := RTCNonceMessage{Nonce: "test-nonce-base64"}

	data, err := json.Marshal(nonce)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	expected := `{"nonce":"test-nonce-base64"}`
	if string(data) != expected {
		t.Errorf("Serialized = %q; want %q", string(data), expected)
	}
}

// TestRTCTokenResponseSerialization tests JSON serialization of token response.
func TestRTCTokenResponseSerialization(t *testing.T) {
	tests := []struct {
		name     string
		response RTCTokenResponse
		expected string
	}{
		{
			name:     "OK response",
			response: RTCTokenResponse{Status: "OK", Token: "sha256.abc"},
			expected: `{"status":"OK","token":"sha256.abc"}`,
		},
		{
			name:     "PIN_REQUIRED response",
			response: RTCTokenResponse{Status: "PIN_REQUIRED", Token: "sha256.def"},
			expected: `{"status":"PIN_REQUIRED","token":"sha256.def"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.response)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("Serialized = %q; want %q", string(data), tt.expected)
			}
		})
	}
}

// TestFileHeaderVsTokenRequestParsing ensures file headers are parsed correctly
// instead of being misidentified as token requests (critical for protocol correctness).
func TestFileHeaderVsTokenRequestParsing(t *testing.T) {
	// This message has both "id" and "token" - must be file_header, not token_request
	fileHeaderJSON := `{"id":"0","token":"1766963574926146000"}`

	// This message has only "token" - must be token_request
	tokenRequestJSON := `{"token":"sha256.hash.salt.ed25519.sig"}`

	// Test file header
	msg1, type1, _ := ParseRTCMessage([]byte(fileHeaderJSON))
	if type1 != "file_header" {
		t.Errorf("File header parsed as %q; want 'file_header'", type1)
	}
	if _, ok := msg1.(*RTCSendFileHeader); !ok {
		t.Error("File header not parsed as RTCSendFileHeader")
	}

	// Test token request
	msg2, type2, _ := ParseRTCMessage([]byte(tokenRequestJSON))
	if type2 != "token_request" {
		t.Errorf("Token request parsed as %q; want 'token_request'", type2)
	}
	if _, ok := msg2.(*RTCTokenRequest); !ok {
		t.Error("Token request not parsed as RTCTokenRequest")
	}
}

// =============================================================================
// Rust Test Vectors
// These tests verify exact JSON format compatibility with the official Rust implementation.
// =============================================================================

// TestRustVectorFileListResponsePair verifies PAIR response encoding matches Rust.
// From Rust: rtc_file_list_response_encoding (webrtc.rs)
func TestRustVectorFileListResponsePair(t *testing.T) {
	expected := `{"status":"PAIR","publicKey":"123"}`

	response := RTCFileListResponse{
		Status:    "PAIR",
		PublicKey: "123",
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if string(data) != expected {
		t.Errorf("JSON mismatch.\nGot:  %s\nWant: %s", string(data), expected)
	}

	// Verify round-trip
	var parsed RTCFileListResponse
	if err := json.Unmarshal([]byte(expected), &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.Status != "PAIR" {
		t.Errorf("Parsed status = %q; want 'PAIR'", parsed.Status)
	}
	if parsed.PublicKey != "123" {
		t.Errorf("Parsed publicKey = %q; want '123'", parsed.PublicKey)
	}
}

// TestRustVectorFileListResponseOK verifies OK response encoding matches Rust.
func TestRustVectorFileListResponseOK(t *testing.T) {
	response := RTCFileListResponse{
		Status: "OK",
		Files:  map[string]string{"file1": "token1", "file2": "token2"},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Verify it has expected structure
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if parsed["status"] != "OK" {
		t.Errorf("status = %v; want 'OK'", parsed["status"])
	}

	files, ok := parsed["files"].(map[string]interface{})
	if !ok {
		t.Fatal("files is not a map")
	}
	if files["file1"] != "token1" {
		t.Errorf("files[file1] = %v; want 'token1'", files["file1"])
	}
}

// TestRustVectorChunkProcessing verifies chunking behavior matches Rust.
// From Rust: test_process_in_chunks (webrtc.rs)
// Uses ChunkSize constant from sender.go (16 KiB)
func TestRustVectorChunkProcessing(t *testing.T) {
	// ChunkSize should be 16 * 1024 = 16384 (from sender.go)
	const chunkSize = 16 * 1024 // Local constant for testing

	// Input: CHUNK_SIZE * 2 + 5 = 32773 bytes
	inputSize := chunkSize*2 + 5
	data := make([]byte, inputSize)

	// Fill with pattern (matching Rust test)
	for i := 0; i < ChunkSize; i++ {
		data[i] = 0
	}
	for i := ChunkSize; i < ChunkSize*2; i++ {
		data[i] = 1
	}
	for i := ChunkSize * 2; i < inputSize; i++ {
		data[i] = 2
	}

	// Split into chunks
	var chunks [][]byte
	for i := 0; i < len(data); i += ChunkSize {
		end := i + ChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}

	// Verify chunk count
	if len(chunks) != 3 {
		t.Errorf("Chunk count = %d; want 3", len(chunks))
	}

	// Verify chunk sizes
	if len(chunks[0]) != ChunkSize {
		t.Errorf("Chunk 0 size = %d; want %d", len(chunks[0]), ChunkSize)
	}
	if len(chunks[1]) != ChunkSize {
		t.Errorf("Chunk 1 size = %d; want %d", len(chunks[1]), ChunkSize)
	}
	if len(chunks[2]) != 5 {
		t.Errorf("Chunk 2 size = %d; want 5", len(chunks[2]))
	}

	// Verify chunk contents match Rust test pattern
	allZeros := true
	for _, b := range chunks[0] {
		if b != 0 {
			allZeros = false
			break
		}
	}
	if !allZeros {
		t.Error("Chunk 0 should contain all zeros")
	}

	allOnes := true
	for _, b := range chunks[1] {
		if b != 1 {
			allOnes = false
			break
		}
	}
	if !allOnes {
		t.Error("Chunk 1 should contain all ones")
	}

	allTwos := true
	for _, b := range chunks[2] {
		if b != 2 {
			allTwos = false
			break
		}
	}
	if !allTwos {
		t.Error("Chunk 2 should contain all twos")
	}
}

// TestRTCPairResponseSerialization tests JSON serialization of PAIR response.
func TestRTCPairResponseSerialization(t *testing.T) {
	tests := []struct {
		name     string
		response RTCPairResponse
		expected string
	}{
		{
			name:     "OK response with public key",
			response: RTCPairResponse{Status: "OK", PublicKey: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"},
			expected: `{"status":"OK","publicKey":"-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"}`,
		},
		{
			name:     "PAIR_DECLINED response",
			response: RTCPairResponse{Status: "PAIR_DECLINED"},
			expected: `{"status":"PAIR_DECLINED"}`,
		},
		{
			name:     "INVALID_SIGNATURE response",
			response: RTCPairResponse{Status: "INVALID_SIGNATURE"},
			expected: `{"status":"INVALID_SIGNATURE"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.response)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("Serialized = %q; want %q", string(data), tt.expected)
			}
		})
	}
}

// TestParseRTCMessagePairResponse tests parsing of PAIR response messages.
func TestParseRTCMessagePairResponse(t *testing.T) {
	// PAIR response has status and publicKey but NO files field
	jsonData := `{"status":"OK","publicKey":"-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"}`

	msg, msgType, err := ParseRTCMessage([]byte(jsonData))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if msgType != "pair_response" {
		t.Errorf("msgType = %q; want 'pair_response'", msgType)
	}

	pairResp, ok := msg.(*RTCPairResponse)
	if !ok {
		t.Fatalf("msg is not *RTCPairResponse")
	}

	if pairResp.Status != "OK" {
		t.Errorf("Status = %q; want 'OK'", pairResp.Status)
	}
	if pairResp.PublicKey == "" {
		t.Error("PublicKey should not be empty")
	}
}

// TestPairResponseVsFileListResponse ensures PAIR response is not confused with file list response.
func TestPairResponseVsFileListResponse(t *testing.T) {
	// File list from sender has status and files as array
	fileListJSON := `{"status":"OK","files":[{"id":"0","fileName":"test.txt","size":100,"fileType":"text/plain"}]}`

	// PAIR response has status and publicKey but NO files
	pairRespJSON := `{"status":"OK","publicKey":"test-key"}`

	// PAIR status with publicKey but no files
	pairStatusJSON := `{"status":"PAIR","publicKey":"test-key"}`

	// Test file list response
	_, type1, _ := ParseRTCMessage([]byte(fileListJSON))
	if type1 != "file_list" {
		t.Errorf("File list response parsed as %q; want 'file_list'", type1)
	}

	// Test PAIR response (OK status with publicKey, no files)
	_, type2, _ := ParseRTCMessage([]byte(pairRespJSON))
	if type2 != "pair_response" {
		t.Errorf("PAIR response parsed as %q; want 'pair_response'", type2)
	}

	// Test PAIR status (PAIR status with publicKey)
	_, type3, _ := ParseRTCMessage([]byte(pairStatusJSON))
	if type3 != "pair_response" {
		t.Errorf("PAIR status parsed as %q; want 'pair_response'", type3)
	}
}

// TestRTCErrorResponseSerialization tests JSON serialization of error response.
func TestRTCErrorResponseSerialization(t *testing.T) {
	tests := []struct {
		name     string
		response RTCErrorResponse
		expected string
	}{
		{
			name:     "invalid nonce format",
			response: RTCErrorResponse{Error: "invalid nonce format"},
			expected: `{"error":"invalid nonce format"}`,
		},
		{
			name:     "internal error",
			response: RTCErrorResponse{Error: "internal error: nonce generation failed"},
			expected: `{"error":"internal error: nonce generation failed"}`,
		},
		{
			name:     "token generation failed",
			response: RTCErrorResponse{Error: "internal error: token generation failed"},
			expected: `{"error":"internal error: token generation failed"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.response)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("Serialized = %q; want %q", string(data), tt.expected)
			}

			// Verify round-trip
			var parsed RTCErrorResponse
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			if parsed.Error != tt.response.Error {
				t.Errorf("Parsed error = %q; want %q", parsed.Error, tt.response.Error)
			}
		})
	}
}

// TestRTCErrorResponseUsage verifies the error response type.
func TestRTCErrorResponseUsage(t *testing.T) {
	// Verify RTCErrorResponse can be created and serialized
	errResp := RTCErrorResponse{Error: "test error message"}

	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("Failed to marshal error response: %v", err)
	}

	// Verify JSON structure
	expected := `{"error":"test error message"}`
	if string(data) != expected {
		t.Errorf("Serialized = %q; want %q", string(data), expected)
	}
}
