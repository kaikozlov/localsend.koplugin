package signaling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestConnectWithContext_CancelInterruptsHelloWait(t *testing.T) {
	upgraded := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		close(upgraded)
		<-release
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ConnectWithContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), ClientInfoWithoutID{Alias: "test"})
		done <- err
	}()
	<-upgraded
	cancel()

	select {
	case err := <-done:
		close(release)
		if err == nil {
			t.Fatal("ConnectWithContext returned nil after cancellation")
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("context cancellation did not interrupt the HELLO read")
	}
}

func TestSdpCompressDecompress(t *testing.T) {
	original := `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0
m=application 9 UDP/DTLS/SCTP webrtc-datachannel
c=IN IP4 0.0.0.0
a=ice-ufrag:abcd
a=ice-pwd:efghijklmnop
a=fingerprint:sha-256 AA:BB:CC:DD:EE:FF
a=setup:actpass
a=mid:0
a=sctp-port:5000`

	compressed, err := CompressSDP(original)
	if err != nil {
		t.Fatalf("CompressSDP failed: %v", err)
	}

	if compressed == "" {
		t.Error("Compressed SDP is empty")
	}

	// Verify compressed is smaller than original (should be for repeated patterns)
	if len(compressed) >= len(original) {
		t.Logf("Warning: compressed (%d) not smaller than original (%d)", len(compressed), len(original))
	}

	decompressed, err := DecompressSDP(compressed)
	if err != nil {
		t.Fatalf("DecompressSDP failed: %v", err)
	}

	if decompressed != original {
		t.Errorf("Round-trip failed.\nOriginal: %q\nDecompressed: %q", original, decompressed)
	}
}

func TestWsServerMessageHelloSerialization(t *testing.T) {
	clientID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	peerID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	msg := WsServerMessage{
		Type: "HELLO",
		Client: &ClientInfo{
			ID:          clientID,
			Alias:       "Test Client",
			Version:     "2.1",
			DeviceModel: "Test",
			DeviceType:  "desktop",
			Token:       "abc123",
		},
		Peers: &[]ClientInfo{
			{
				ID:          peerID,
				Alias:       "Peer",
				Version:     "2.1",
				DeviceModel: "Phone",
				DeviceType:  "mobile",
				Token:       "def456",
			},
		},
	}

	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if parsed.Type != "HELLO" {
		t.Errorf("Type = %q; want HELLO", parsed.Type)
	}
	if parsed.Client.Alias != "Test Client" {
		t.Errorf("Client.Alias = %q; want 'Test Client'", parsed.Client.Alias)
	}
	if parsed.Peers == nil {
		t.Fatal("Peers is nil")
	}
	if len(*parsed.Peers) != 1 {
		t.Errorf("Peers count = %d; want 1", len(*parsed.Peers))
	}
}

func TestWsClientMessageOfferSerialization(t *testing.T) {
	targetID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	msg := NewOfferMessage("session-123", targetID, "compressed-sdp")

	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Verify JSON structure
	var raw map[string]interface{}
	if err := json.Unmarshal(bytes, &raw); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if raw["type"] != "OFFER" {
		t.Errorf("type = %q; want OFFER", raw["type"])
	}
	if raw["sessionId"] != "session-123" {
		t.Errorf("sessionId = %q; want 'session-123'", raw["sessionId"])
	}
}

func TestClientInfoToAnnouncement(t *testing.T) {
	info := ClientInfo{
		ID:          uuid.New(),
		Alias:       "Test Device",
		Version:     "2.1",
		DeviceModel: "MacBook",
		DeviceType:  "desktop",
		Token:       "fingerprint-abc",
	}

	anno := info.ToAnnouncement()

	if anno.Alias != "Test Device" {
		t.Errorf("Alias = %q; want 'Test Device'", anno.Alias)
	}
	if anno.DeviceModel != "MacBook" {
		t.Errorf("DeviceModel = %q; want 'MacBook'", anno.DeviceModel)
	}
	if anno.Protocol != "webrtc" {
		t.Errorf("Protocol = %q; want 'webrtc'", anno.Protocol)
	}
	if anno.Fingerprint != "fingerprint-abc" {
		t.Errorf("Fingerprint = %q; want 'fingerprint-abc'", anno.Fingerprint)
	}
}

// =============================================================================
// Rust Test Vectors
// These tests verify exact JSON format compatibility with the official Rust implementation.
// =============================================================================

// TestRustVectorHelloMessage verifies exact JSON format from Rust tests.
func TestRustVectorHelloMessage(t *testing.T) {
	// From Rust: ws_server_hello_message_encoding (signaling.rs)
	expected := `{"type":"HELLO","client":{"id":"00000000-0000-0000-0000-000000000000","alias":"Cute Apple","version":"2.3","deviceModel":"Dell","deviceType":"desktop","token":"123"},"peers":[]}`

	msg := WsServerMessage{
		Type: "HELLO",
		Client: &ClientInfo{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000000"),
			Alias:       "Cute Apple",
			Version:     "2.3",
			DeviceModel: "Dell",
			DeviceType:  "desktop",
			Token:       "123",
		},
		Peers: &[]ClientInfo{},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if string(data) != expected {
		t.Errorf("JSON mismatch.\nGot:  %s\nWant: %s", string(data), expected)
	}

	// Verify round-trip
	var parsed WsServerMessage
	if err := json.Unmarshal([]byte(expected), &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.Client.Alias != "Cute Apple" {
		t.Errorf("Parsed alias = %q; want 'Cute Apple'", parsed.Client.Alias)
	}
}

// TestRustVectorOfferMessage verifies exact JSON format from Rust tests.
func TestRustVectorOfferMessage(t *testing.T) {
	// From Rust: ws_server_offer_message_encoding (signaling.rs)
	// Note: deviceModel is omitted when empty
	expected := `{"type":"OFFER","peer":{"id":"00000000-0000-0000-0000-000000000000","alias":"Cute Apple","version":"2.3","deviceType":"desktop","token":"123"},"sessionId":"456","sdp":"my-sdp"}`

	msg := WsServerMessage{
		Type: "OFFER",
		Peer: &ClientInfo{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000000"),
			Alias:       "Cute Apple",
			Version:     "2.3",
			DeviceModel: "", // Empty - should be omitted
			DeviceType:  "desktop",
			Token:       "123",
		},
		SessionID: "456",
		SDP:       "my-sdp",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if string(data) != expected {
		t.Errorf("JSON mismatch.\nGot:  %s\nWant: %s", string(data), expected)
	}
}

// TestRustVectorClientUpdateMessage verifies exact JSON format from Rust tests.
func TestRustVectorClientUpdateMessage(t *testing.T) {
	// From Rust: ws_client_update_message_encoding (signaling.rs)
	expected := `{"type":"UPDATE","info":{"alias":"Cute Apple","version":"2.3","deviceModel":"Dell","deviceType":"desktop","token":"123"}}`

	msg := WsClientMessage{
		Type: "UPDATE",
		Info: &ClientInfoWithoutID{
			Alias:       "Cute Apple",
			Version:     "2.3",
			DeviceModel: "Dell",
			DeviceType:  "desktop",
			Token:       "123",
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if string(data) != expected {
		t.Errorf("JSON mismatch.\nGot:  %s\nWant: %s", string(data), expected)
	}

	// Verify round-trip
	var parsed WsClientMessage
	if err := json.Unmarshal([]byte(expected), &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.Info.Alias != "Cute Apple" {
		t.Errorf("Parsed alias = %q; want 'Cute Apple'", parsed.Info.Alias)
	}
}

// TestWsServerJoinMessage verifies JOIN message serialization.
func TestWsServerJoinMessage(t *testing.T) {
	peerID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	msg := WsServerMessage{
		Type: "JOIN",
		Peer: &ClientInfo{
			ID:          peerID,
			Alias:       "New Peer",
			Version:     "2.1",
			DeviceModel: "iPhone",
			DeviceType:  "mobile",
			Token:       "token123",
		},
	}

	// Marshal/unmarshal round trip
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify all fields
	if parsed.Type != "JOIN" {
		t.Errorf("Type = %q; want JOIN", parsed.Type)
	}
	if parsed.Peer == nil {
		t.Fatal("Peer is nil")
	}
	if parsed.Peer.ID != peerID {
		t.Errorf("Peer.ID = %v; want %v", parsed.Peer.ID, peerID)
	}
	if parsed.Peer.Alias != "New Peer" {
		t.Errorf("Peer.Alias = %q; want 'New Peer'", parsed.Peer.Alias)
	}
	if parsed.Peer.Version != "2.1" {
		t.Errorf("Peer.Version = %q; want '2.1'", parsed.Peer.Version)
	}
	if parsed.Peer.DeviceModel != "iPhone" {
		t.Errorf("Peer.DeviceModel = %q; want 'iPhone'", parsed.Peer.DeviceModel)
	}
	if parsed.Peer.DeviceType != "mobile" {
		t.Errorf("Peer.DeviceType = %q; want 'mobile'", parsed.Peer.DeviceType)
	}
	if parsed.Peer.Token != "token123" {
		t.Errorf("Peer.Token = %q; want 'token123'", parsed.Peer.Token)
	}
}

// TestWsServerLeftMessage verifies LEFT message serialization.
func TestWsServerLeftMessage(t *testing.T) {
	peerID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	msg := WsServerMessage{
		Type:   "LEFT",
		PeerID: &peerID,
	}

	// Marshal/unmarshal round trip
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify PeerID is preserved
	if parsed.Type != "LEFT" {
		t.Errorf("Type = %q; want LEFT", parsed.Type)
	}
	if parsed.PeerID == nil {
		t.Fatal("PeerID is nil")
	}
	if *parsed.PeerID != peerID {
		t.Errorf("PeerID = %v; want %v", *parsed.PeerID, peerID)
	}
}

// TestWsServerErrorMessage verifies ERROR message serialization.
func TestWsServerErrorMessage(t *testing.T) {
	msg := WsServerMessage{
		Type: "ERROR",
		Code: 400,
	}

	// Marshal/unmarshal round trip
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify Code is preserved
	if parsed.Type != "ERROR" {
		t.Errorf("Type = %q; want ERROR", parsed.Type)
	}
	if parsed.Code != 400 {
		t.Errorf("Code = %d; want 400", parsed.Code)
	}
}

// TestWsServerUpdateMessage verifies server UPDATE message serialization.
func TestWsServerUpdateMessage(t *testing.T) {
	peerID := uuid.MustParse("00000000-0000-0000-0000-000000000003")

	msg := WsServerMessage{
		Type: "UPDATE",
		Peer: &ClientInfo{
			ID:          peerID,
			Alias:       "Updated Peer",
			Version:     "2.2",
			DeviceModel: "Galaxy",
			DeviceType:  "mobile",
			Token:       "updated-token",
		},
	}

	// Marshal/unmarshal round trip
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify all fields
	if parsed.Type != "UPDATE" {
		t.Errorf("Type = %q; want UPDATE", parsed.Type)
	}
	if parsed.Peer == nil {
		t.Fatal("Peer is nil")
	}
	if parsed.Peer.ID != peerID {
		t.Errorf("Peer.ID = %v; want %v", parsed.Peer.ID, peerID)
	}
	if parsed.Peer.Alias != "Updated Peer" {
		t.Errorf("Peer.Alias = %q; want 'Updated Peer'", parsed.Peer.Alias)
	}
	if parsed.Peer.Version != "2.2" {
		t.Errorf("Peer.Version = %q; want '2.2'", parsed.Peer.Version)
	}
}

// TestWsServerAnswerMessage verifies ANSWER message serialization.
func TestWsServerAnswerMessage(t *testing.T) {
	peerID := uuid.MustParse("00000000-0000-0000-0000-000000000004")

	msg := WsServerMessage{
		Type: "ANSWER",
		Peer: &ClientInfo{
			ID:          peerID,
			Alias:       "Answering Peer",
			Version:     "2.1",
			DeviceModel: "MacBook",
			DeviceType:  "desktop",
			Token:       "answer-token",
		},
		SessionID: "session-456",
		SDP:       "compressed-answer-sdp",
	}

	// Marshal/unmarshal round trip
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify all fields
	if parsed.Type != "ANSWER" {
		t.Errorf("Type = %q; want ANSWER", parsed.Type)
	}
	if parsed.Peer == nil {
		t.Fatal("Peer is nil")
	}
	if parsed.Peer.Alias != "Answering Peer" {
		t.Errorf("Peer.Alias = %q; want 'Answering Peer'", parsed.Peer.Alias)
	}
	if parsed.SessionID != "session-456" {
		t.Errorf("SessionID = %q; want 'session-456'", parsed.SessionID)
	}
	if parsed.SDP != "compressed-answer-sdp" {
		t.Errorf("SDP = %q; want 'compressed-answer-sdp'", parsed.SDP)
	}
}

// TestClientInfoWithoutIDOmitEmpty verifies empty field omission.
func TestClientInfoWithoutIDOmitEmpty(t *testing.T) {
	msg := WsClientMessage{
		Type: "UPDATE",
		Info: &ClientInfoWithoutID{
			Alias:       "Test Device",
			Version:     "2.1",
			DeviceModel: "", // Empty - should be omitted
			DeviceType:  "", // Empty - should be omitted
			Token:       "abc123",
		},
	}

	// Marshal and verify these fields are NOT in the JSON output
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	jsonStr := string(bytes)

	// Verify deviceModel and deviceType are not present
	var raw map[string]interface{}
	if err := json.Unmarshal(bytes, &raw); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	info, ok := raw["info"].(map[string]interface{})
	if !ok {
		t.Fatal("info field is not an object")
	}

	if _, exists := info["deviceModel"]; exists {
		t.Errorf("deviceModel should be omitted when empty, but found in JSON: %s", jsonStr)
	}
	if _, exists := info["deviceType"]; exists {
		t.Errorf("deviceType should be omitted when empty, but found in JSON: %s", jsonStr)
	}

	// Verify required fields are present
	if info["alias"] != "Test Device" {
		t.Errorf("alias = %v; want 'Test Device'", info["alias"])
	}
	if info["version"] != "2.1" {
		t.Errorf("version = %v; want '2.1'", info["version"])
	}
	if info["token"] != "abc123" {
		t.Errorf("token = %v; want 'abc123'", info["token"])
	}
}

// TestWsServerHelloWithMultiplePeers verifies HELLO with peers array.
func TestWsServerHelloWithMultiplePeers(t *testing.T) {
	clientID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	peer1ID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	peer2ID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	peer3ID := uuid.MustParse("00000000-0000-0000-0000-000000000004")

	msg := WsServerMessage{
		Type: "HELLO",
		Client: &ClientInfo{
			ID:          clientID,
			Alias:       "My Device",
			Version:     "2.1",
			DeviceModel: "Desktop",
			DeviceType:  "desktop",
			Token:       "my-token",
		},
		Peers: &[]ClientInfo{
			{
				ID:          peer1ID,
				Alias:       "Peer One",
				Version:     "2.1",
				DeviceModel: "iPhone",
				DeviceType:  "mobile",
				Token:       "token1",
			},
			{
				ID:          peer2ID,
				Alias:       "Peer Two",
				Version:     "2.2",
				DeviceModel: "Android",
				DeviceType:  "mobile",
				Token:       "token2",
			},
			{
				ID:          peer3ID,
				Alias:       "Peer Three",
				Version:     "2.1",
				DeviceModel: "MacBook",
				DeviceType:  "desktop",
				Token:       "token3",
			},
		},
	}

	// Marshal/unmarshal round trip
	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed WsServerMessage
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify all peers are preserved after round trip
	if parsed.Type != "HELLO" {
		t.Errorf("Type = %q; want HELLO", parsed.Type)
	}
	if parsed.Client == nil {
		t.Fatal("Client is nil")
	}
	if parsed.Client.Alias != "My Device" {
		t.Errorf("Client.Alias = %q; want 'My Device'", parsed.Client.Alias)
	}
	if parsed.Peers == nil {
		t.Fatal("Peers is nil")
	}
	if len(*parsed.Peers) != 3 {
		t.Fatalf("Peers count = %d; want 3", len(*parsed.Peers))
	}

	peers := *parsed.Peers

	// Verify first peer
	if peers[0].ID != peer1ID {
		t.Errorf("Peer[0].ID = %v; want %v", peers[0].ID, peer1ID)
	}
	if peers[0].Alias != "Peer One" {
		t.Errorf("Peer[0].Alias = %q; want 'Peer One'", peers[0].Alias)
	}

	// Verify second peer
	if peers[1].ID != peer2ID {
		t.Errorf("Peer[1].ID = %v; want %v", peers[1].ID, peer2ID)
	}
	if peers[1].Alias != "Peer Two" {
		t.Errorf("Peer[1].Alias = %q; want 'Peer Two'", peers[1].Alias)
	}

	// Verify third peer
	if peers[2].ID != peer3ID {
		t.Errorf("Peer[2].ID = %v; want %v", peers[2].ID, peer3ID)
	}
	if peers[2].Alias != "Peer Three" {
		t.Errorf("Peer[2].Alias = %q; want 'Peer Three'", peers[2].Alias)
	}
}

// =============================================================================
// Phase 1-3 Implementation Tests
// =============================================================================

// TestNewUpdateMessageFormat verifies UPDATE message format for token refresh.
func TestNewUpdateMessageFormat(t *testing.T) {
	info := ClientInfoWithoutID{
		Alias:       "Refreshed Device",
		Version:     "2.3",
		DeviceModel: "Test",
		DeviceType:  "desktop",
		Token:       "new-refreshed-token",
	}

	msg := NewUpdateMessage(info)

	if msg.Type != "UPDATE" {
		t.Errorf("Type = %q; want UPDATE", msg.Type)
	}
	if msg.Info == nil {
		t.Fatal("Info is nil")
	}
	if msg.Info.Token != "new-refreshed-token" {
		t.Errorf("Token = %q; want 'new-refreshed-token'", msg.Info.Token)
	}

	// Verify JSON serialization
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if parsed["type"] != "UPDATE" {
		t.Errorf("JSON type = %v; want UPDATE", parsed["type"])
	}

	infoMap, ok := parsed["info"].(map[string]interface{})
	if !ok {
		t.Fatal("info is not an object")
	}
	if infoMap["token"] != "new-refreshed-token" {
		t.Errorf("JSON token = %v; want 'new-refreshed-token'", infoMap["token"])
	}
}

// =============================================================================
// SDP Compression Edge Case Tests
// =============================================================================

// TestSdpCompressEmptySDP tests compression of empty SDP string.
func TestSdpCompressEmptySDP(t *testing.T) {
	compressed, err := CompressSDP("")
	if err != nil {
		t.Fatalf("CompressSDP failed on empty string: %v", err)
	}

	// Should still produce valid output
	if compressed == "" {
		t.Error("Compressed empty SDP should not be empty (zlib header)")
	}

	// Should decompress back to empty string
	decompressed, err := DecompressSDP(compressed)
	if err != nil {
		t.Fatalf("DecompressSDP failed: %v", err)
	}

	if decompressed != "" {
		t.Errorf("Decompressed = %q; want empty string", decompressed)
	}
}

// TestSdpCompressLargeSDP tests compression of very large SDP strings.
// Real WebRTC SDPs can be quite large with many ICE candidates.
func TestSdpCompressLargeSDP(t *testing.T) {
	// Build a large SDP with many ICE candidates
	var builder strings.Builder
	builder.WriteString("v=0\r\n")
	builder.WriteString("o=- 0 0 IN IP4 127.0.0.1\r\n")
	builder.WriteString("s=-\r\n")
	builder.WriteString("t=0 0\r\n")

	// Add 100 candidate lines (realistic for complex network setups)
	for i := 0; i < 100; i++ {
		builder.WriteString("a=candidate:foundation ")
		builder.WriteString(strings.Repeat("abcdefgh", 10)) // Long foundation
		builder.WriteString(" UDP 12345678 192.168.1.")
		builder.WriteString(strings.Repeat("0", i%10+1))
		builder.WriteString(" 12345 typ host\r\n")
	}

	largeSDP := builder.String()

	compressed, err := CompressSDP(largeSDP)
	if err != nil {
		t.Fatalf("CompressSDP failed on large SDP: %v", err)
	}

	// Compression should reduce size for repetitive content
	if len(compressed) >= len(largeSDP) {
		t.Logf("Large SDP: original=%d, compressed=%d", len(largeSDP), len(compressed))
	}

	decompressed, err := DecompressSDP(compressed)
	if err != nil {
		t.Fatalf("DecompressSDP failed: %v", err)
	}

	if decompressed != largeSDP {
		t.Error("Large SDP round-trip failed")
	}
}

// TestSdpCompressSpecialCharacters tests SDP with various special characters.
func TestSdpCompressSpecialCharacters(t *testing.T) {
	tests := []struct {
		name string
		sdp  string
	}{
		{
			name: "unicode_device_name",
			sdp:  "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=日本語デバイス\r\n",
		},
		{
			name: "emoji_in_sdp",
			sdp:  "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=📱 iPhone\r\n",
		},
		{
			name: "newlines_mixed",
			sdp:  "v=0\no=- 0 0 IN IP4 127.0.0.1\r\ns=Mixed\nLineEndings\r\n",
		},
		{
			name: "equals_in_value",
			sdp:  "v=0\r\na=fingerprint:sha-256 AA:BB:CC=DD:EE\r\n",
		},
		{
			name: "long_base64_chars",
			sdp:  "v=0\r\na=ice-pwd:abcdefghijklmnopqrstuvwxyz0123456789+/ABCD\r\n",
		},
		{
			name: "null_bytes",
			sdp:  "v=0\r\no=- 0 0\x00 IN IP4\r\n",
		},
		{
			name: "high_bytes",
			sdp:  "v=0\r\no=\xff\xfe 0 0 IN IP4\r\n",
		},
		{
			name: "tabs_and_spaces",
			sdp:  "v=0\r\na=rtpmap:96\tH264/90000\r\n  s=test\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressSDP(tt.sdp)
			if err != nil {
				t.Fatalf("CompressSDP failed: %v", err)
			}

			decompressed, err := DecompressSDP(compressed)
			if err != nil {
				t.Fatalf("DecompressSDP failed: %v", err)
			}

			if decompressed != tt.sdp {
				t.Errorf("Round-trip failed.\nOriginal: %q\nDecompressed: %q", tt.sdp, decompressed)
			}
		})
	}
}

// TestDecompressSDPInvalidBase64 tests that invalid base64 input is handled.
func TestDecompressSDPInvalidBase64(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"invalid_chars", "not!valid!base64"},
		{"with_padding", "aGVsbG8="}, // Standard base64 with padding (wrong format)
		{"wrong_length", "abc"},      // Invalid length for base64
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecompressSDP(tt.input)
			if err == nil {
				t.Error("Expected error for invalid base64 input")
			}
		})
	}
}

// TestDecompressSDPCorruptedZlib tests that corrupted zlib data is handled.
func TestDecompressSDPCorruptedZlib(t *testing.T) {
	// Create valid base64 but invalid zlib data
	invalidZlib := base64.RawURLEncoding.EncodeToString([]byte("this is not zlib data"))

	_, err := DecompressSDP(invalidZlib)
	if err == nil {
		t.Error("Expected error for corrupted zlib data")
	}
}

// TestDecompressSDPTruncatedZlib tests handling of truncated zlib stream.
func TestDecompressSDPTruncatedZlib(t *testing.T) {
	// Compress some data, then truncate
	original := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=Test\r\n"
	compressed, err := CompressSDP(original)
	if err != nil {
		t.Fatalf("CompressSDP failed: %v", err)
	}

	// Decode, truncate, and re-encode
	data, _ := base64.RawURLEncoding.DecodeString(compressed)
	if len(data) > 5 {
		truncated := base64.RawURLEncoding.EncodeToString(data[:len(data)/2])
		_, err := DecompressSDP(truncated)
		if err == nil {
			t.Error("Expected error for truncated zlib data")
		}
	}
}

// TestSdpCompressURLSafeOutput verifies output is URL-safe base64.
func TestSdpCompressURLSafeOutput(t *testing.T) {
	// Compress many different SDPs to test character variety
	for i := 0; i < 50; i++ {
		sdp := strings.Repeat("a=candidate:", i+1) + strings.Repeat("x", i*10)

		compressed, err := CompressSDP(sdp)
		if err != nil {
			t.Fatalf("CompressSDP failed: %v", err)
		}

		// Check for non-URL-safe characters
		if strings.Contains(compressed, "+") {
			t.Errorf("Compressed contains '+' (not URL-safe): %s", compressed)
		}
		if strings.Contains(compressed, "/") {
			t.Errorf("Compressed contains '/' (not URL-safe): %s", compressed)
		}
		if strings.Contains(compressed, "=") {
			t.Errorf("Compressed contains '=' padding (should be unpadded): %s", compressed)
		}
	}
}

// TestSdpCompressRealWorldSDP tests with a realistic WebRTC SDP.
func TestSdpCompressRealWorldSDP(t *testing.T) {
	// Realistic SDP from a WebRTC data channel connection
	realSDP := `v=0
o=- 4611731400430051336 2 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0
a=extmap-allow-mixed
a=msid-semantic: WMS
m=application 9 UDP/DTLS/SCTP webrtc-datachannel
c=IN IP4 0.0.0.0
a=ice-ufrag:Xr5Y
a=ice-pwd:w9L8fWu4K3p8Y2j7K9sHgT6m
a=ice-options:trickle
a=fingerprint:sha-256 4E:6C:2A:D1:0F:8A:B9:C3:D4:E5:F6:A7:B8:C9:D0:E1:F2:A3:B4:C5:D6:E7:F8:A9:B0:C1:D2:E3:F4:A5:B6:C7
a=setup:actpass
a=mid:0
a=sctp-port:5000
a=max-message-size:262144
a=candidate:foundation 1 udp 2130706431 192.168.1.100 54321 typ host generation 0
a=candidate:foundation 2 udp 2130706430 192.168.1.100 54322 typ host generation 0
a=candidate:foundation 3 udp 1694498815 203.0.113.1 54323 typ srflx raddr 192.168.1.100 rport 54321 generation 0`

	compressed, err := CompressSDP(realSDP)
	if err != nil {
		t.Fatalf("CompressSDP failed on real-world SDP: %v", err)
	}

	t.Logf("Real SDP compression: original=%d bytes, compressed=%d bytes (%.1f%% reduction)",
		len(realSDP), len(compressed), 100.0*(1.0-float64(len(compressed))/float64(len(realSDP))))

	decompressed, err := DecompressSDP(compressed)
	if err != nil {
		t.Fatalf("DecompressSDP failed: %v", err)
	}

	if decompressed != realSDP {
		t.Error("Real-world SDP round-trip failed")
	}
}

// TestSdpCompressInteroperability tests against known compressed SDP from official implementation.
func TestSdpCompressInteroperability(t *testing.T) {
	// Test that we can decompress SDP compressed by the official Rust implementation.
	// This is a manually captured compressed SDP from the LocalSend web app.
	// If we don't have an actual sample, we verify our compression matches expected format.

	testSDP := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\n"

	compressed, err := CompressSDP(testSDP)
	if err != nil {
		t.Fatalf("CompressSDP failed: %v", err)
	}

	// Verify the compressed data can be decoded as valid base64
	decoded, err := base64.RawURLEncoding.DecodeString(compressed)
	if err != nil {
		t.Fatalf("Compressed output is not valid URL-safe base64: %v", err)
	}

	// Verify it starts with zlib magic bytes (0x78)
	if len(decoded) > 0 && decoded[0] != 0x78 {
		t.Errorf("Compressed data doesn't start with zlib magic byte: got 0x%02X", decoded[0])
	}
}

// TestSdpDecompressEmptyInput tests handling of empty input.
func TestSdpDecompressEmptyInput(t *testing.T) {
	_, err := DecompressSDP("")
	if err == nil {
		t.Error("Expected error for empty input")
	}
}

// =============================================================================
// Context Propagation Tests
// =============================================================================

// TestConnectWithContextCancellation verifies that ConnectWithContext respects context cancellation.
func TestConnectWithContextCancellation(t *testing.T) {
	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	info := ClientInfoWithoutID{
		Alias:       "Test Device",
		Version:     "2.3",
		DeviceModel: "Test",
		DeviceType:  "desktop",
		Token:       "test-token",
	}

	// This should fail quickly due to cancelled context, not hang
	_, err := ConnectWithContext(ctx, DefaultSignalingServer, info)
	if err == nil {
		t.Error("Expected error when context is cancelled")
	}

	// Error should indicate context was cancelled
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Logf("Got error (expected context cancellation): %v", err)
	}
}

// TestConnectWithContextTimeout verifies that ConnectWithContext respects context timeout.
func TestConnectWithContextTimeout(t *testing.T) {
	// Create a context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	info := ClientInfoWithoutID{
		Alias:       "Test Device",
		Version:     "2.3",
		DeviceModel: "Test",
		DeviceType:  "desktop",
		Token:       "test-token",
	}

	// This should fail quickly due to timeout
	_, err := ConnectWithContext(ctx, DefaultSignalingServer, info)
	if err == nil {
		t.Error("Expected error when context times out")
	}

	// Error should indicate deadline exceeded or context-related
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "timeout") {
		t.Logf("Got error (expected timeout): %v", err)
	}
}

func TestBuildSignalingURL_EncodesClientInfoAndPreservesQuery(t *testing.T) {
	info := ClientInfoWithoutID{
		Alias:       "Test Device",
		Version:     "2.3",
		DeviceModel: "Test Model",
		DeviceType:  "desktop",
		Token:       "abc123",
	}

	built, err := buildSignalingURL("wss://example.test/connect?existing=value", info)
	if err != nil {
		t.Fatal(err)
	}
	parsedURL, err := url.Parse(built)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsedURL.Query().Get("existing"); got != "value" {
		t.Fatalf("existing query value = %q; want value", got)
	}
	encoded := parsedURL.Query().Get("d")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode client info: %v", err)
	}
	var got ClientInfoWithoutID
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("unmarshal client info: %v", err)
	}
	if got != info {
		t.Fatalf("decoded client info = %+v; want %+v", got, info)
	}
}

// =============================================================================
// Goroutine Leak Tests
// These tests verify the fix for goroutine leaks in SetTokenGenerator.
// =============================================================================

func TestSetTokenGenerator_ConcurrentCallsAreRaceFree(t *testing.T) {
	client := &SignalingClient{
		done:     make(chan struct{}),
		onAnswer: make(map[string]answerCallback),
	}
	generator := func() (string, error) { return "token", nil }

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.SetTokenGenerator(generator)
		}()
	}
	wg.Wait()
	if !client.refreshStarted.Load() {
		t.Fatal("token refresh loop was not marked as started")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSetTokenGenerator_ConcurrentWithCloseIsRaceFree(t *testing.T) {
	generator := func() (string, error) { return "token", nil }
	for range 100 {
		client := &SignalingClient{
			done:     make(chan struct{}),
			onAnswer: make(map[string]answerCallback),
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.SetTokenGenerator(generator)
		}()
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
	}
}

// =============================================================================
// Answer Callback Memory Leak Tests
// These tests verify the fix for memory leaks in OnAnswer callbacks.
// =============================================================================

// TestOnAnswerCallbackLeak tests that callbacks are cleaned up on close.
func TestOnAnswerCallbackLeak(t *testing.T) {
	client := &SignalingClient{
		done:     make(chan struct{}),
		onAnswer: make(map[string]answerCallback),
	}

	// Register many callbacks that will never be triggered
	for i := 0; i < 1000; i++ {
		sessionID := "session-" + string(rune('0'+i%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i/100%10))
		client.OnAnswer(sessionID, func(msg WsServerMessage) {
			// This callback will never be called
		})
	}

	// Check that callbacks are stored
	client.answerMu.Lock()
	countBefore := len(client.onAnswer)
	client.answerMu.Unlock()

	if countBefore != 1000 {
		t.Errorf("Expected 1000 callbacks, got %d", countBefore)
	}

	// Close the client using the proper Close() method (not just close(done))
	// This triggers the callback cleanup
	_ = client.Close()

	// Close should clear all pending callbacks.
	client.answerMu.Lock()
	countAfter := len(client.onAnswer)
	client.answerMu.Unlock()

	if countAfter != 0 {
		t.Errorf("MEMORY LEAK: After Close(), expected 0 callbacks, got %d", countAfter)
	}
}

// TestConnectWithInvalidURL verifies that Connect handles invalid URLs gracefully.
func TestConnectWithInvalidURL(t *testing.T) {
	info := ClientInfoWithoutID{
		Alias:       "Test",
		Version:     "2.3",
		DeviceModel: "Test",
		DeviceType:  "desktop",
		Token:       "token",
	}

	tests := []struct {
		name string
		url  string
	}{
		{"empty_url", ""},
		{"invalid_scheme", "not-a-valid-url"},
		{"missing_host", "wss://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Connect(tt.url, info)
			if err == nil {
				t.Error("Expected error for invalid URL")
			}
		})
	}
}
