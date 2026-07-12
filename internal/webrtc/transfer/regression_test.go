package transfer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/models"
	"localsend-cli/internal/webrtc/signaling"
)

func TestRTCReceiver_WritesOneByteBinaryFrame(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one-byte.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRTCReceiver(nil, nil, "", dir)
	r.state = stateReceivingFiles
	r.currentFileID = "f"
	r.fileWriters["f"] = f
	r.filePaths["f"] = path
	r.fileHashers["f"] = sha256.New()
	r.files = []RTCFileDto{{ID: "f", Size: models.FlexInt(1)}}

	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		r.handleMessage([]byte{0xab})
	}()
	if panicked {
		t.Fatal("one-byte binary frame was treated as a delimiter")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string([]byte{0xab}) {
		t.Fatalf("saved data = %x; want ab", data)
	}
}

func TestRTCReceiver_InvalidHandshakeDoesNotDeadlockErrorResponse(t *testing.T) {
	r := NewRTCReceiver(nil, nil, "", t.TempDir())
	done := make(chan struct{})
	go func() {
		r.handleMessage([]byte(`{"nonce":"invalid"}`))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("invalid handshake deadlocked while sending its error response")
	}
}

func TestRTCReceiver_ReassemblesChunkedFileList(t *testing.T) {
	files := make([]RTCFileDto, 0, 500)
	for i := 0; i < 500; i++ {
		files = append(files, RTCFileDto{
			ID:       fmt.Sprintf("id-%d", i),
			FileName: fmt.Sprintf("folder/%040d.txt", i),
			Size:     1,
		})
	}
	data, err := json.Marshal(RTCPinSendingResponse{Status: "OK", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= ChunkSize {
		t.Fatalf("fixture is only %d bytes", len(data))
	}
	r := NewRTCReceiver(nil, nil, "", t.TempDir())
	r.state = stateWaitFileList
	for start := 0; start < len(data); start += ChunkSize {
		end := start + ChunkSize
		if end > len(data) {
			end = len(data)
		}
		r.handleMessage(data[start:end])
	}
	r.handleMessage([]byte("0"))
	if len(r.files) != len(files) {
		t.Fatalf("received %d files; want %d", len(r.files), len(files))
	}
}

func TestRTCReceiver_AcceptOfferResetsPreviousSenderIdentity(t *testing.T) {
	key, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRTCReceiver(nil, key, "", t.TempDir())
	r.senderPublicKey = key.ToVerifyingKey()
	r.senderPublicPEM = key.PublicKeyPEM()
	r.senderToken = "previous-token"
	offer := signaling.WsServerMessage{
		Peer: &signaling.ClientInfo{ID: uuid.New(), Alias: "new sender"},
		SDP:  "invalid",
	}
	if err := r.AcceptOffer(offer); err == nil {
		t.Fatal("invalid offer unexpectedly succeeded")
	}
	if r.senderPublicKey != nil || r.senderPublicPEM != "" || r.senderToken != "" {
		t.Fatal("new offer retained identity state from the previous sender")
	}
}

func TestRTCReceiver_RequirePairingRejectsPairDeclined(t *testing.T) {
	r := NewRTCReceiver(nil, nil, "", t.TempDir())
	r.requirePairing = true
	r.state = stateWaitPairResponse
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		r.handleMessage([]byte(`{"status":"PAIR_DECLINED"}`))
	}()
	if panicked {
		t.Fatal("PAIR_DECLINED continued into file acceptance")
	}
	if r.state != stateWaitPairResponse {
		t.Fatalf("state = %d; want stateWaitPairResponse", r.state)
	}
}

func TestRTCReceiver_PrepareFilesDoesNotOpenOrCreateEveryFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRTCReceiver(nil, nil, "", dir)
	ids := make([]string, 256)
	r.files = make([]RTCFileDto, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
		r.files[i] = RTCFileDto{ID: ids[i], FileName: fmt.Sprintf("file-%d.bin", i), Size: 1}
	}
	tokens := r.prepareFilesForReceive(ids)
	if len(tokens) != len(ids) {
		t.Fatalf("generated %d tokens; want %d", len(tokens), len(ids))
	}
	if len(r.fileWriters) != 0 {
		t.Fatalf("opened %d files before receiving a file header", len(r.fileWriters))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("created %d files before receiving data", len(entries))
	}
}

func TestRTCReceiver_CleansPartialFileWhenBodyExceedsDeclaredSize(t *testing.T) {
	dir := t.TempDir()
	r := NewRTCReceiver(nil, nil, "", dir)
	r.files = []RTCFileDto{{ID: "f", FileName: "oversized.bin", Size: 1}}
	tokens := r.prepareFilesForReceive([]string{"f"})
	header := &RTCSendFileHeader{ID: "f", Token: tokens["f"]}
	if ok := r.startReceivingFile(header); !ok {
		t.Fatal("startReceivingFile rejected prepared file")
	}
	r.handleBinaryData([]byte("too large"))
	if r.currentFileID != "" {
		t.Fatal("oversized file remained active")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized transfer left %d file artifacts", len(entries))
	}
}

func TestRTCReceiver_CloseKeepsCompletedFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRTCReceiver(nil, nil, "", dir)
	r.files = []RTCFileDto{{ID: "f", FileName: "complete.bin", Size: 1}}
	tokens := r.prepareFilesForReceive([]string{"f"})
	if !r.startReceivingFile(&RTCSendFileHeader{ID: "f", Token: tokens["f"]}) {
		t.Fatal("failed to start file")
	}
	path := r.filePaths["f"]
	r.handleBinaryData([]byte{0xab})
	r.finishCurrentFile()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("completed file removed during Close: %v", err)
	}
}

func TestRTCSender_ReportsRemoteFileFailure(t *testing.T) {
	s := NewRTCSender(nil, nil, "")
	s.state = senderStateSendingFiles
	s.handleMessage([]byte(`{"id":"f","success":false,"error":"disk full"}`))
	select {
	case err := <-s.errors:
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("error = %v; want disk full", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sender ignored remote file failure")
	}
}

func TestRTCReceiver_ExpiredBlockedPeersAreCleanedGlobally(t *testing.T) {
	ClearBlockedPeers()
	t.Cleanup(ClearBlockedPeers)
	blockedPeersMu.Lock()
	for i := 0; i < 1000; i++ {
		blockedPeers[fmt.Sprintf("expired-%d", i)] = time.Now().Add(-time.Minute)
	}
	blockedPeersMu.Unlock()
	_ = isPeerBlocked("unrelated")
	blockedPeersMu.RLock()
	got := len(blockedPeers)
	blockedPeersMu.RUnlock()
	if got != 0 {
		t.Fatalf("retained %d expired blocked peers", got)
	}
}
