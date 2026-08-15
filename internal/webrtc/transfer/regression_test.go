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
	"localsend-cli/internal/webrtc/signaling"
)

type controlFrame struct {
	kind string
	data string
}

type recordingControlOps struct {
	frames []controlFrame
	closed int
}

func (r *recordingControlOps) record(kind string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.frames = append(r.frames, controlFrame{kind: kind, data: string(data)})
	return nil
}

func (r *recordingControlOps) SendJSON(v interface{}) error       { return r.record("text", v) }
func (r *recordingControlOps) SendJSONBinary(v interface{}) error { return r.record("binary", v) }
func (r *recordingControlOps) SendDelimiter() error {
	r.frames = append(r.frames, controlFrame{kind: "text", data: "0"})
	return nil
}
func (r *recordingControlOps) Close() error { r.closed++; return nil }

func assertControlFrames(t *testing.T, got []controlFrame, want ...controlFrame) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("frames = %#v; want %#v", got, want)
	}
}

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
	r.files = []RTCFileDto{{ID: "f", Size: 1}}

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
	rec := &recordingControlOps{}
	r.controlOpsOverride = rec
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
	if r.state != stateDone {
		t.Fatalf("state = %d; want terminal stateDone", r.state)
	}
	if len(rec.frames) != 0 {
		t.Fatalf("PAIR decline emitted nonstandard response frames: %#v", rec.frames)
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

// recordingSendOps records the wire sequence produced by SendFiles and
// auto-acknowledges each file (by header id) when the next header or the final
// delimiter is sent, mirroring a cooperative receiver. Single-goroutine use.
type recordingSendOps struct {
	events  []string // trace entries: "header:<id>", "data:<id>:<n>", "delimiter"
	current string   // id of the file whose data is currently being received
	results chan RTCSendFileResponse
}

func (r *recordingSendOps) SendJSON(v interface{}) error {
	header, ok := v.(RTCSendFileHeader)
	if !ok {
		return nil
	}
	// A new header means the previous file's data is complete: ack it.
	r.ackCurrent()
	r.current = header.ID
	r.events = append(r.events, "header:"+header.ID)
	return nil
}

func (r *recordingSendOps) Send(data []byte) error {
	r.events = append(r.events, fmt.Sprintf("data:%s:%d", r.current, len(data)))
	return nil
}

func (r *recordingSendOps) SendDelimiter() error {
	r.ackCurrent()
	r.events = append(r.events, "delimiter")
	return nil
}

func (r *recordingSendOps) WaitBufferBelowWithTimeout(limit uint64, _ time.Duration) error {
	r.events = append(r.events, fmt.Sprintf("backpressure:%d", limit))
	return nil
}

func (r *recordingSendOps) WaitBufferEmptyWithTimeout(time.Duration) error { return nil }

func (r *recordingSendOps) ackCurrent() {
	if r.current == "" {
		return
	}
	r.results <- RTCSendFileResponse{ID: r.current, Success: true}
	r.current = ""
}

func writeSendFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func hasEventPrefix(events []string, prefix string) bool {
	for _, e := range events {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func countExactEvent(events []string, want string) int {
	n := 0
	for _, e := range events {
		if e == want {
			n++
		}
	}
	return n
}

// TestRTCSender_PrepareSendQueue_FiltersMissingTokenAndMissingLocalFile
// verifies the pre-filter that makes SendFiles' pipelining safe: an accepted id
// is only queued when it has both a receiver token and a local FileMeta, and the
// acceptance order is preserved.
func TestRTCSender_PrepareSendQueue_FiltersMissingTokenAndMissingLocalFile(t *testing.T) {
	s := NewRTCSender(nil, nil, "")
	s.files = []FileMeta{
		{ID: "A", FileName: "a", FilePath: "a"},
		{ID: "C", FileName: "c", FilePath: "c"},
	}
	// B has a token but no local file; D has neither token nor file.
	s.fileTokens = map[string]string{"A": "ta", "B": "tb", "C": "tc"}
	s.acceptedIDs = []string{"A", "B", "C", "D"}

	queue := s.prepareSendQueue()

	if len(queue) != 2 {
		t.Fatalf("queue length = %d, want 2 (A and C only)", len(queue))
	}
	if queue[0].id != "A" || queue[1].id != "C" {
		t.Fatalf("queue order = %s, %s; want A then C", queue[0].id, queue[1].id)
	}
	if queue[0].token != "ta" || queue[1].token != "tc" {
		t.Fatalf("queue tokens = %q, %q; want ta, tc", queue[0].token, queue[1].token)
	}
}

// TestRTCSender_SendFiles_PreFiltersAcceptedIDsWithNoLocalFile is the regression
// test for the pipelined header desync. The receiver accepted an id ("B") that
// has no local FileMeta. On the old code the loop pre-announced B's header, then
// `continue`d past B leaving headerAlreadySent set, so C's data was sent under
// B's header. With the pre-filter, B is dropped up front and the wire trace
// contains exactly headers [A, C] with each file's data under its own header.
func TestRTCSender_SendFiles_PreFiltersAcceptedIDsWithNoLocalFile(t *testing.T) {
	dir := t.TempDir()
	pathA := writeSendFile(t, filepath.Join(dir, "a.txt"), "hello-A")
	pathC := writeSendFile(t, filepath.Join(dir, "c.txt"), "hello-C")

	s := NewRTCSender(nil, nil, "")
	s.files = []FileMeta{
		{ID: "A", FileName: "a.txt", FilePath: pathA, Size: int64(len("hello-A"))},
		{ID: "C", FileName: "c.txt", FilePath: pathC, Size: int64(len("hello-C"))},
	}
	s.fileTokens = map[string]string{"A": "ta", "B": "tb", "C": "tc"}
	s.acceptedIDs = []string{"A", "B", "C"}

	rec := &recordingSendOps{results: s.fileResults}
	s.sendOpsOverride = rec

	if err := s.SendFiles(); err != nil {
		t.Fatalf("SendFiles returned error: %v", err)
	}

	var headers []string
	for _, e := range rec.events {
		if strings.HasPrefix(e, "header:") {
			headers = append(headers, strings.TrimPrefix(e, "header:"))
		}
	}
	if fmt.Sprint(headers) != "[A C]" {
		t.Fatalf("headers sent = %v, want [A C] (B must be filtered before sending)", headers)
	}

	// C's data must be recorded under C, never attributed to the skipped B.
	if !hasEventPrefix(rec.events, "data:C:") {
		t.Fatalf("expected a data event for C; trace=%v", rec.events)
	}
	if hasEventPrefix(rec.events, "data:B:") {
		t.Fatalf("data must not be attributed to skipped B (header desync); trace=%v", rec.events)
	}

	if countExactEvent(rec.events, "delimiter") != 1 {
		t.Fatalf("expected exactly one final delimiter; trace=%v", rec.events)
	}
}

func TestWebCompat_SendFilesUsesNextHeaderBoundaryAndPerFileAck(t *testing.T) {
	dir := t.TempDir()
	pathA := writeSendFile(t, filepath.Join(dir, "a.bin"), "A")
	pathB := writeSendFile(t, filepath.Join(dir, "b.bin"), "BB")

	s := NewRTCSender(nil, nil, "")
	s.files = []FileMeta{
		{ID: "A", FileName: "a.bin", FilePath: pathA, Size: 1},
		{ID: "B", FileName: "b.bin", FilePath: pathB, Size: 2},
	}
	s.fileTokens = map[string]string{"A": "ta", "B": "tb"}
	s.acceptedIDs = []string{"A", "B"}
	rec := &recordingSendOps{results: s.fileResults}
	s.sendOpsOverride = rec

	if err := s.SendFiles(); err != nil {
		t.Fatalf("SendFiles returned error: %v", err)
	}

	want := []string{
		"header:A", "backpressure:1048576", "data:A:1",
		"header:B", "backpressure:1048576", "data:B:2", "delimiter",
	}
	if fmt.Sprint(rec.events) != fmt.Sprint(want) {
		t.Fatalf("wire trace = %v; want %v", rec.events, want)
	}
}

func TestWebCompat_SendFilesAppliesOneMiBBackpressureBeforeEachChunk(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", ChunkSize*2+1)
	path := writeSendFile(t, filepath.Join(dir, "large.bin"), content)

	s := NewRTCSender(nil, nil, "")
	s.files = []FileMeta{{ID: "A", FileName: "large.bin", FilePath: path, Size: int64(len(content))}}
	s.fileTokens = map[string]string{"A": "ta"}
	s.acceptedIDs = []string{"A"}
	rec := &recordingSendOps{results: s.fileResults}
	s.sendOpsOverride = rec

	if err := s.SendFiles(); err != nil {
		t.Fatalf("SendFiles returned error: %v", err)
	}

	waits, chunks := 0, 0
	for _, event := range rec.events {
		if event == "backpressure:1048576" {
			waits++
		}
		if strings.HasPrefix(event, "data:A:") {
			chunks++
		}
	}
	if chunks != 3 {
		t.Fatalf("data chunks = %d; want 3; trace=%v", chunks, rec.events)
	}
	if waits != chunks {
		t.Fatalf("backpressure waits = %d; want one before each of %d chunks; trace=%v", waits, chunks, rec.events)
	}
}

func TestRTCSender_SenderOwnedPINTranscript_RetriesEmptyAttemptThenSendsFiles(t *testing.T) {
	rec := &recordingControlOps{}
	s := NewRTCSender(nil, nil, "")
	s.controlOpsOverride = rec
	s.SetRequiredPIN("2468")
	s.state = senderStateWaitToken

	s.handleDataMessage([]byte(`{"status":"OK"}`), true)
	s.handleDataMessage([]byte(`{"pin":""}`), true)
	s.handleDataMessage([]byte(`{"pin":"2468"}`), true)

	assertControlFrames(t, rec.frames,
		controlFrame{kind: "binary", data: `{"status":"PIN_REQUIRED"}`},
		controlFrame{kind: "text", data: "0"},
		controlFrame{kind: "binary", data: `{"status":"PIN_REQUIRED"}`},
		controlFrame{kind: "text", data: "0"},
		controlFrame{kind: "binary", data: `{"status":"OK"}`},
		controlFrame{kind: "text", data: "0"},
	)
	if s.state != senderStateWaitFileAccept {
		t.Fatalf("state = %d; want senderStateWaitFileAccept", s.state)
	}
}

func TestRTCSender_SenderOwnedPINTranscript_TooManyAttemptsIsTerminal(t *testing.T) {
	rec := &recordingControlOps{}
	s := NewRTCSender(nil, nil, "")
	s.controlOpsOverride = rec
	s.SetRequiredPIN("2468")
	s.state = senderStateWaitToken

	s.handleDataMessage([]byte(`{"status":"OK"}`), true)
	for i := 0; i < maxPINAttempts; i++ {
		s.handleDataMessage([]byte(`{"pin":"wrong"}`), true)
	}

	if s.state != senderStateDone {
		t.Fatalf("state = %d; want senderStateDone", s.state)
	}
	assertControlFrames(t, rec.frames[len(rec.frames)-2:],
		controlFrame{kind: "binary", data: `{"status":"TOO_MANY_ATTEMPTS"}`},
		controlFrame{kind: "text", data: "0"},
	)
}

func TestRTCSender_ReceiverOwnedPINTranscript_ProviderRetriesAndSendsEmptyAttempt(t *testing.T) {
	rec := &recordingControlOps{}
	s := NewRTCSender(nil, nil, "fallback")
	s.controlOpsOverride = rec
	attempts := []string{"", "correct"}
	s.SetPINProvider(func(attempt int) string { return attempts[attempt-1] })
	s.state = senderStateWaitToken

	s.handleDataMessage([]byte(`{"status":"PIN_REQUIRED"}`), true)
	s.handleDataMessage([]byte(`{"status":"PIN_REQUIRED"}`), true)
	s.handleDataMessage([]byte(`{"status":"OK"}`), true)

	assertControlFrames(t, rec.frames,
		controlFrame{kind: "text", data: `{"pin":""}`},
		controlFrame{kind: "text", data: `{"pin":"correct"}`},
		controlFrame{kind: "binary", data: `{"status":"OK"}`},
		controlFrame{kind: "text", data: "0"},
	)
}

func TestRTCReceiver_SenderOwnedPINTranscript_RetriesThenAcceptsFileList(t *testing.T) {
	rec := &recordingControlOps{}
	r := NewRTCReceiver(nil, nil, "fallback", t.TempDir())
	r.controlOpsOverride = rec
	attempts := []string{"", "2468"}
	r.SetPINProvider(func(attempt int) string { return attempts[attempt-1] })
	r.OnSelectFiles(func(files []RTCFileDto) []string { return []string{files[0].ID} })
	r.state = stateWaitFileList

	for i := 0; i < 2; i++ {
		r.handleDataMessage([]byte(`{"status":"PIN_REQUIRED"}`), false)
		r.handleDataMessage([]byte("0"), true)
	}
	r.handleDataMessage([]byte(`{"status":"OK","files":[{"id":"f","fileName":"f.bin","size":0}]}`), false)
	r.handleDataMessage([]byte("0"), true)

	if r.state != stateWaitFiles {
		t.Fatalf("state = %d; want stateWaitFiles", r.state)
	}
	if len(rec.frames) < 2 || rec.frames[0] != (controlFrame{kind: "text", data: `{"pin":""}`}) || rec.frames[1] != (controlFrame{kind: "text", data: `{"pin":"2468"}`}) {
		t.Fatalf("PIN frames = %#v", rec.frames)
	}
}

func TestRTCReceiver_SenderOwnedPINTranscript_TooManyAttemptsIsTerminal(t *testing.T) {
	rec := &recordingControlOps{}
	r := NewRTCReceiver(nil, nil, "", t.TempDir())
	r.controlOpsOverride = rec
	r.state = stateWaitFileList

	r.handleDataMessage([]byte(`{"status":"TOO_MANY_ATTEMPTS"}`), false)
	r.handleDataMessage([]byte("0"), true)

	if r.state != stateDone {
		t.Fatalf("state = %d; want stateDone", r.state)
	}
}

func TestRTCReceiver_AnyTextFrameFinishesFileThenMalformedHeaderIsTerminal(t *testing.T) {
	dir := t.TempDir()
	r := NewRTCReceiver(nil, nil, "", dir)
	rec := &recordingControlOps{}
	r.controlOpsOverride = rec
	r.files = []RTCFileDto{{ID: "f", FileName: "boundary.bin", Size: 3}}
	tokens := r.prepareFilesForReceive([]string{"f"})
	if !r.startReceivingFile(&RTCSendFileHeader{ID: "f", Token: tokens["f"]}) {
		t.Fatal("failed to start file")
	}
	path := r.filePaths["f"]
	r.handleDataMessage([]byte("abc"), false)
	r.handleDataMessage([]byte("not-a-header"), true)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abc" {
		t.Fatalf("saved data = %q; text boundary was appended", data)
	}
	if r.state != stateDone {
		t.Fatalf("state = %d; want stateDone after malformed text header", r.state)
	}
	if len(rec.frames) != 1 || rec.frames[0].kind != "text" || !strings.Contains(rec.frames[0].data, `"success":true`) {
		t.Fatalf("file acknowledgement frames = %#v", rec.frames)
	}
}

func TestRTCReceiver_OneByteNonDelimiterTextIsTerminalHeaderError(t *testing.T) {
	dir := t.TempDir()
	r := NewRTCReceiver(nil, nil, "", dir)
	r.controlOpsOverride = &recordingControlOps{}
	r.files = []RTCFileDto{{ID: "f", FileName: "delimiter.bin", Size: 1}}
	tokens := r.prepareFilesForReceive([]string{"f"})
	if !r.startReceivingFile(&RTCSendFileHeader{ID: "f", Token: tokens["f"]}) {
		t.Fatal("failed to start file")
	}
	r.handleDataMessage([]byte{0xab}, false)
	r.handleDataMessage([]byte("x"), true)
	if r.state != stateDone {
		t.Fatalf("state = %d; want stateDone", r.state)
	}
}

func TestRTCSender_SendFiles_EmptyQueueSendsExactlyOneDelimiter(t *testing.T) {
	s := NewRTCSender(nil, nil, "")
	rec := &recordingSendOps{results: s.fileResults}
	s.sendOpsOverride = rec

	if err := s.SendFiles(); err != nil {
		t.Fatalf("SendFiles returned error: %v", err)
	}
	if countExactEvent(rec.events, "delimiter") != 1 {
		t.Fatalf("expected exactly one final delimiter; trace=%v", rec.events)
	}
}
