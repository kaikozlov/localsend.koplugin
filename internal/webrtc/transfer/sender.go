package transfer

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/storage"
	"localsend-cli/internal/utils"
	"localsend-cli/internal/webrtc/signaling"
)

const (
	// ChunkSize is the size of each binary chunk sent over WebRTC.
	// Per LocalSend protocol v3 spec section 8.2: "All binary data is chunked at 16 KiB (16,384 bytes)"
	ChunkSize = 16 * 1024 // 16 KiB

	// answerTimeout is the maximum time to wait for an SDP answer from the receiver.
	answerTimeout = 30 * time.Second

	// fileAcceptTimeout is the maximum time to wait for the receiver to accept/decline files.
	fileAcceptTimeout = 60 * time.Second

	// bufferFlushTimeout is the maximum time to wait for the send buffer to flush before closing.
	bufferFlushTimeout = 10 * time.Second

	// maxBufferedAmount matches LocalSend Web's streaming back-pressure threshold.
	maxBufferedAmount = 1024 * 1024 // 1 MiB

	// bufferBackpressureTimeout bounds a stalled WebRTC send queue.
	bufferBackpressureTimeout = 30 * time.Second
)

// fileReadBufferPool reuses LocalSend 1.18-sized physical read buffers. Data is
// still split into ChunkSize frames before it reaches the WebRTC data channel.
var fileReadBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, utils.FileIOBufferSize)
		return &buf
	},
}

// Sender handshake states
const (
	senderStateInit = iota
	senderStateWaitNonce
	senderStateWaitToken
	senderStateWaitPin
	senderStateWaitRequiredPIN
	senderStateWaitFileAccept
	senderStateSendingFiles
	senderStateDone
)

// FileMeta represents file metadata for sending.
type FileMeta struct {
	ID       string
	FileName string
	FilePath string
	Size     int64
	FileType string
	SHA256   string
	Modified time.Time
	Accessed time.Time
}

// RTCSender handles the LocalSend Web-compatible WebRTC transfer protocol.
type RTCSender struct {
	signaling           *signaling.SignalingClient
	signingKey          *crypto.SigningKey
	peer                *PeerConnection
	pin                 string
	sessionID           string
	pinAttempts         int
	requiredPINAttempts int
	requiredPIN         string
	requirePIN          bool
	mu                  sync.Mutex

	// State machine
	state       int
	localNonce  []byte
	remoteNonce []byte
	finalNonce  []byte

	// Token verification (optional, requires PAIR flow for public key)
	receiverPublicKey crypto.VerifyingKey // Set via PAIR flow
	receiverToken     string              // Stored for verification

	// PIN provider is called for every challenge attempt from the receiver. When
	// nil, pin is used as a fixed fallback (including when it is empty).
	pinProvider func(attempt int) string

	// Receiver info (for PAIR flow)
	receiverAlias string // Set via SetReceiverInfo

	// PAIR callback
	onPairRequest func(alias, fingerprint string) bool // User confirmation for PAIR

	// TrustedDeviceStore for PAIR flow persistence
	trustedStore *storage.TrustedDeviceStore

	// Files
	files       []FileMeta
	fileTokens  map[string]string // fileId -> token from receiver
	acceptedIDs []string

	// Channels
	accepted      chan map[string]string // fileId -> token
	declined      chan struct{}
	errors        chan error
	fileResults   chan RTCSendFileResponse
	controlBuffer []byte
	closed        bool      // Set to true when Close() is called
	closeOnce     sync.Once // Ensures channels are closed only once

	// Custom STUN servers (if empty, uses DefaultSTUNServers)
	stunServers []string

	// sendOpsOverride is a test seam for SendFiles' data-channel operations.
	// When nil, SendFiles uses s.peer.
	sendOpsOverride sendOps
	// controlOpsOverride records control-plane transcripts in tests.
	controlOpsOverride controlChannelOps
}

// NewRTCSender creates a new WebRTC sender.
func NewRTCSender(sig *signaling.SignalingClient, key *crypto.SigningKey, pin string) *RTCSender {
	return &RTCSender{
		signaling:   sig,
		signingKey:  key,
		pin:         pin,
		sessionID:   uuid.New().String()[:11], // Short session ID like official
		state:       senderStateInit,
		fileTokens:  make(map[string]string),
		accepted:    make(chan map[string]string, 1),
		declined:    make(chan struct{}, 1),
		errors:      make(chan error, 1),
		fileResults: make(chan RTCSendFileResponse, 1),
	}
}

// SetReceiverPublicKey sets the receiver's public key for token verification.
// This is typically obtained through the PAIR flow.
func (s *RTCSender) SetReceiverPublicKey(key crypto.VerifyingKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiverPublicKey = key
}

// SetRequiredPIN enables a sender-owned PIN challenge before the file list is
// published. Calling it enables the challenge even when pin is empty, matching
// the official PinConfig/Option distinction.
func (s *RTCSender) SetRequiredPIN(pin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requiredPIN = pin
	s.requirePIN = true
}

// SetPINProvider supplies a fresh response for each receiver-owned PIN
// challenge. The callback may return an empty string; that is a real attempt.
func (s *RTCSender) SetPINProvider(provider func(attempt int) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinProvider = provider
}

func (s *RTCSender) controlOps() controlChannelOps {
	if s.controlOpsOverride != nil {
		return s.controlOpsOverride
	}
	if s.peer != nil {
		return s.peer
	}
	return nil
}

func (s *RTCSender) sendJSON(v interface{}) error {
	if ops := s.controlOps(); ops != nil {
		return ops.SendJSON(v)
	}
	return fmt.Errorf("data channel not initialized")
}

func (s *RTCSender) sendJSONBinary(v interface{}) error {
	if ops := s.controlOps(); ops != nil {
		return ops.SendJSONBinary(v)
	}
	return fmt.Errorf("data channel not initialized")
}

func (s *RTCSender) sendDelimiter() error {
	if ops := s.controlOps(); ops != nil {
		return ops.SendDelimiter()
	}
	return fmt.Errorf("data channel not initialized")
}

// SetReceiverInfo sets receiver information for the PAIR flow.
// The alias is used when prompting for PAIR confirmation.
func (s *RTCSender) SetReceiverInfo(alias string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiverAlias = alias
}

// SetOnPairRequest sets the callback for PAIR confirmation.
// When the receiver requests pairing, this callback is invoked with the
// receiver's alias and public key fingerprint. Return true to accept pairing.
// If no callback is set, pairing is automatically accepted.
func (s *RTCSender) SetOnPairRequest(callback func(alias, fingerprint string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onPairRequest = callback
}

// SetTrustedStore sets the trusted device store for PAIR flow persistence.
// When set, devices paired during the PAIR flow are persisted for future sessions.
func (s *RTCSender) SetTrustedStore(store *storage.TrustedDeviceStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trustedStore = store
}

// SetSTUNServers sets custom STUN servers for ICE negotiation.
// If not set or empty, DefaultSTUNServers will be used.
func (s *RTCSender) SetSTUNServers(servers []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stunServers = servers
}

// Send initiates a file transfer to the target peer.
func (s *RTCSender) Send(target uuid.UUID, files []FileMeta) error {
	s.files = files

	// Use custom STUN servers if set, otherwise use defaults
	stunServers := s.stunServers
	if len(stunServers) == 0 {
		stunServers = DefaultSTUNServers
	}

	// Create peer connection as initiator
	peer, err := NewPeerConnection(PeerConfig{
		STUNServers: stunServers,
		IsInitiator: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	s.peer = peer

	// Set up message handler
	peer.OnDataMessage(s.handleDataMessage)
	peer.OnOpen(func() {
		slog.Info("Data channel opened, starting nonce exchange")
		s.startNonceExchange()
	})

	// Create offer
	sdp, err := peer.CreateOffer()
	if err != nil {
		_ = peer.Close()
		return fmt.Errorf("failed to create offer: %w", err)
	}

	// Set up answer handler
	answerChan := make(chan string, 1)
	s.signaling.OnAnswer(s.sessionID, func(msg signaling.WsServerMessage) {
		answer, err := signaling.DecompressSDP(msg.SDP)
		if err != nil {
			slog.Error("Failed to decompress SDP answer", "error", err)
			return
		}
		answerChan <- answer
	})

	// Send offer via signaling
	if err := s.signaling.SendOffer(s.sessionID, target, sdp); err != nil {
		_ = peer.Close()
		return fmt.Errorf("failed to send offer: %w", err)
	}

	slog.Info("Sent offer, waiting for answer", "target", target, "session", s.sessionID)

	// Wait for answer with timeout
	select {
	case answer := <-answerChan:
		if err := peer.SetAnswer(answer); err != nil {
			_ = peer.Close()
			return fmt.Errorf("failed to set answer: %w", err)
		}
		slog.Info("Received answer, waiting for connection")
	case <-time.After(answerTimeout):
		_ = peer.Close()
		return fmt.Errorf("timeout waiting for answer")
	}

	// Wait for file acceptance
	select {
	case tokens, ok := <-s.accepted:
		if !ok {
			_ = peer.Close()
			return fmt.Errorf("sender closed")
		}
		s.fileTokens = tokens
		for id := range tokens {
			s.acceptedIDs = append(s.acceptedIDs, id)
		}
		slog.Info("Files accepted", "count", len(tokens))
	case _, ok := <-s.declined:
		_ = peer.Close()
		if !ok {
			return fmt.Errorf("sender closed")
		}
		return fmt.Errorf("transfer declined by receiver")
	case err, ok := <-s.errors:
		_ = peer.Close()
		if !ok {
			return fmt.Errorf("sender closed")
		}
		return err
	case <-time.After(fileAcceptTimeout):
		_ = peer.Close()
		return fmt.Errorf("timeout waiting for file acceptance")
	}

	return nil
}

// startNonceExchange begins the official protocol handshake.
func (s *RTCSender) startNonceExchange() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate our nonce
	nonce, err := crypto.GenerateNonce()
	if err != nil {
		if !s.closed {
			s.errors <- fmt.Errorf("failed to generate nonce: %w", err)
		}
		return
	}
	s.localNonce = nonce

	// Send nonce
	msg := RTCNonceMessage{Nonce: crypto.EncodeNonce(nonce)}
	if err := s.sendJSON(msg); err != nil {
		if !s.closed {
			s.errors <- fmt.Errorf("failed to send nonce: %w", err)
		}
		return
	}

	slog.Info("Sent nonce, waiting for receiver's nonce")
	s.state = senderStateWaitNonce
}

// handleMessage processes incoming data channel messages.
func (s *RTCSender) handleMessage(data []byte) {
	isString := bytes.Equal(data, []byte("0")) || json.Valid(data)
	s.handleDataMessage(data, isString)
}

func (s *RTCSender) handleDataMessage(data []byte, isString bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Debug("Message received", "state", s.state, "len", len(data))

	if isString && bytes.Equal(data, []byte("0")) {
		if len(s.controlBuffer) == 0 {
			return
		}
		data = append([]byte(nil), s.controlBuffer...)
		s.controlBuffer = nil
		isString = true
	}
	if !isString {
		if len(s.controlBuffer)+len(data) > maxControlMessageBytes {
			s.controlBuffer = nil
			return
		}
		s.controlBuffer = append(s.controlBuffer, data...)
		return
	}

	// Parse message
	msg, msgType, err := ParseRTCMessage(data)
	if err != nil || msg == nil {
		slog.Warn("Failed to parse RTC message", "error", err)
		return
	}

	slog.Debug("Parsed RTC message", "type", msgType)
	if msgType == "file_response" {
		resp, ok := msg.(*RTCSendFileResponse)
		if !ok {
			return
		}
		if s.closed {
			return
		}
		select {
		case s.fileResults <- *resp:
		default:
		}
		if !resp.Success && !s.closed {
			message := "receiver failed to save file"
			if resp.Error != nil {
				message = *resp.Error
			}
			select {
			case s.errors <- fmt.Errorf("file %s: %s", resp.ID, message):
			default:
			}
		}
		return
	}

	switch s.state {
	case senderStateWaitNonce:
		s.handleNonceResponse(msg, msgType)
	case senderStateWaitToken, senderStateWaitPin:
		s.handleTokenResponse(msg, msgType, data)
	case senderStateWaitRequiredPIN:
		s.handleRequiredPIN(msg, msgType)
	case senderStateWaitFileAccept:
		s.handleFileAcceptance(msg, msgType, data)
	}
}

// handleNonceResponse processes the nonce from receiver.
func (s *RTCSender) handleNonceResponse(msg interface{}, msgType string) {
	if msgType != "nonce" {
		slog.Warn("Expected nonce, got", "type", msgType)
		return
	}

	nonceMsg, ok := msg.(*RTCNonceMessage)
	if !ok {
		slog.Error("Invalid message type for nonce", "got", fmt.Sprintf("%T", msg))
		return
	}
	remoteNonce, err := crypto.DecodeNonce(nonceMsg.Nonce)
	if err != nil {
		slog.Error("Failed to decode remote nonce", "error", err)
		return
	}
	s.remoteNonce = remoteNonce

	// Final nonce = sender_nonce || receiver_nonce
	s.finalNonce = crypto.CombineNonces(s.localNonce, s.remoteNonce)

	slog.Info("Nonce exchange complete, sending token")

	// Generate and send token
	token, err := s.signingKey.GenerateTokenWithNonce(s.finalNonce)
	if err != nil {
		slog.Error("Failed to generate token", "error", err)
		return
	}

	tokenMsg := RTCTokenRequest{Token: token}
	if err := s.sendJSON(tokenMsg); err != nil {
		slog.Error("Failed to send token", "error", err)
		return
	}

	s.state = senderStateWaitToken
}

// handleTokenResponse processes the token response from receiver.
func (s *RTCSender) handleTokenResponse(_ interface{}, msgType string, data []byte) {
	// Status responses: OK, PIN_REQUIRED, TOO_MANY_ATTEMPTS, INVALID_SIGNATURE.
	if msgType == "status_TOO_MANY_ATTEMPTS" {
		s.failLocked(fmt.Errorf("too many PIN attempts, receiver blocked transfer"))
		return
	}
	if msgType == "status_INVALID_SIGNATURE" {
		s.failLocked(fmt.Errorf("receiver rejected our token signature"))
		return
	}
	if msgType != "status_OK" && msgType != "status_PIN_REQUIRED" {
		slog.Warn("Expected token response, got", "type", msgType)
		return
	}

	var tokenResp RTCTokenResponse
	if err := json.Unmarshal(data, &tokenResp); err != nil {
		slog.Error("Failed to parse token response", "error", err)
		return
	}
	s.receiverToken = tokenResp.Token

	// Supplying an expected key is an explicit identity requirement. A missing or
	// invalid token is therefore always terminal; there is no second leniency flag.
	if s.receiverPublicKey != nil {
		if err := crypto.VerifyTokenNonce(s.receiverPublicKey, tokenResp.Token, s.finalNonce); err != nil {
			s.failLocked(fmt.Errorf("receiver token verification failed: %w", err))
			return
		}
		slog.Info("Receiver token verified successfully")
	}

	if tokenResp.Status == "PIN_REQUIRED" {
		s.pinAttempts++
		pin := s.pin
		if s.pinProvider != nil {
			provider := s.pinProvider
			attempt := s.pinAttempts
			s.mu.Unlock()
			pin = provider(attempt)
			s.mu.Lock()
		}
		if err := s.sendJSON(RTCPinMessage{Pin: pin}); err != nil {
			s.failLocked(fmt.Errorf("failed to send PIN: %w", err))
			return
		}
		s.state = senderStateWaitPin
		return
	}

	s.beginFileListPhase()
}

func (s *RTCSender) beginFileListPhase() {
	if !s.requirePIN {
		s.sendFileList()
		return
	}
	if err := s.sendPinSendingStatus(StatusPINRequired); err != nil {
		s.failLocked(fmt.Errorf("failed to send sender PIN challenge: %w", err))
		return
	}
	s.state = senderStateWaitRequiredPIN
}

func (s *RTCSender) handleRequiredPIN(msg interface{}, msgType string) {
	if msgType != "pin" {
		slog.Warn("Expected pin for sender challenge", "type", msgType)
		return
	}
	pinMsg, ok := msg.(*RTCPinMessage)
	if !ok {
		s.failLocked(fmt.Errorf("invalid PIN message type %T", msg))
		return
	}
	if subtle.ConstantTimeCompare([]byte(pinMsg.Pin), []byte(s.requiredPIN)) == 1 {
		s.sendFileList()
		return
	}

	s.requiredPINAttempts++
	status := StatusPINRequired
	if s.requiredPINAttempts >= maxPINAttempts {
		status = StatusTooManyAttempts
	}
	if err := s.sendPinSendingStatus(status); err != nil {
		s.failLocked(fmt.Errorf("failed to send sender PIN result: %w", err))
		return
	}
	if status == StatusTooManyAttempts {
		s.failLocked(fmt.Errorf("too many incorrect PIN attempts"))
	}
}

func (s *RTCSender) sendPinSendingStatus(status string) error {
	if err := s.sendJSONBinary(RTCPinSendingResponse{Status: status}); err != nil {
		return err
	}
	return s.sendDelimiter()
}

func (s *RTCSender) failLocked(err error) {
	s.state = senderStateDone
	if s.closed {
		return
	}
	select {
	case s.errors <- err:
	default:
	}
}

func (s *RTCSender) fileDTOs() []RTCFileDto {
	files := make([]RTCFileDto, len(s.files))
	for i, f := range s.files {
		metadata := RTCFileMetadata{}
		if !f.Modified.IsZero() {
			metadata.Modified = f.Modified.Format(time.RFC3339Nano)
		}
		if !f.Accessed.IsZero() {
			metadata.Accessed = f.Accessed.Format(time.RFC3339Nano)
		}
		files[i] = RTCFileDto{
			ID:       f.ID,
			FileName: f.FileName,
			Size:     f.Size,
			FileType: f.FileType,
			SHA256:   f.SHA256,
			Metadata: metadata,
		}
	}
	return files
}

// sendFileList sends the list of files to transfer.
func (s *RTCSender) sendFileList() {
	files := s.fileDTOs()

	fileList := RTCPinSendingResponse{
		Status: "OK",
		Files:  files,
	}

	// Send as binary + delimiter
	if err := s.sendJSONBinary(fileList); err != nil {
		slog.Error("Failed to send file list", "error", err)
		return
	}
	if err := s.sendDelimiter(); err != nil {
		slog.Error("Failed to send delimiter", "error", err)
		return
	}

	slog.Info("Sent file list", "count", len(files))
	s.state = senderStateWaitFileAccept
}

// handleFileAcceptance processes the file acceptance from receiver.
func (s *RTCSender) handleFileAcceptance(_ interface{}, msgType string, data []byte) {
	if msgType != "status_OK" && msgType != "status_DECLINED" && msgType != "status_PAIR" {
		slog.Warn("Expected file acceptance, got", "type", msgType)
		return
	}

	var resp RTCFileListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		slog.Error("Failed to parse file acceptance", "error", err)
		return
	}

	switch resp.Status {
	case "DECLINED":
		if !s.closed {
			select {
			case s.declined <- struct{}{}:
			default:
			}
		}
		return

	case "PAIR":
		// Receiver wants to pair - they've sent their public key
		slog.Info("Receiver requested pairing", "hasPublicKey", resp.PublicKey != "")

		// Parse and verify receiver's public key against their token FIRST
		var receiverKey crypto.VerifyingKey
		if resp.PublicKey != "" {
			key, err := crypto.ParsePublicKeyPEM(resp.PublicKey)
			if err != nil {
				slog.Error("Failed to parse receiver's public key", "error", err)
				// Send INVALID_SIGNATURE response
				pairResponse := RTCPairResponse{Status: "INVALID_SIGNATURE"}
				_ = s.sendJSON(pairResponse)
				s.failLocked(fmt.Errorf("invalid receiver PAIR public key: %w", err))
				return
			}

			// CRITICAL: Verify receiver's token was signed with this public key
			// This ensures the PAIR public key matches the identity from token exchange
			if s.receiverToken != "" {
				if err := crypto.VerifyTokenNonce(key, s.receiverToken, s.finalNonce); err != nil {
					slog.Error("Receiver token does not match PAIR public key - potential identity mismatch", "error", err)
					pairResponse := RTCPairResponse{Status: "INVALID_SIGNATURE"}
					_ = s.sendJSON(pairResponse)
					s.failLocked(fmt.Errorf("receiver PAIR identity mismatch: %w", err))
					return
				}
				slog.Info("Receiver token verified against PAIR public key")
			}

			// Verification passed - store the key
			receiverKey = key
			s.receiverPublicKey = key
		}

		// Compute fingerprint for user confirmation display
		fingerprint := ""
		if resp.PublicKey != "" {
			fingerprint = computeFingerprint(resp.PublicKey)
		}

		// Check user confirmation callback
		accepted := true // Default to accept if no callback
		if s.onPairRequest != nil {
			alias := s.receiverAlias
			if alias == "" {
				alias = "Unknown Device"
			}
			// Release lock before calling callback to prevent deadlock
			s.mu.Unlock()
			accepted = s.onPairRequest(alias, fingerprint)
			s.mu.Lock()
		}

		if !accepted {
			slog.Info("User declined PAIR request")
			// Clear the stored key since user declined
			s.receiverPublicKey = nil
			pairResponse := RTCPairResponse{
				Status: "PAIR_DECLINED",
			}
			if err := s.sendJSON(pairResponse); err != nil {
				slog.Error("Failed to send PAIR decline", "error", err)
				return
			}
			// Continue without pairing - receiver will send a new file list response
			return
		}

		// Accept: send our public key
		pairResponse := RTCPairResponse{
			Status:    "OK",
			PublicKey: s.signingKey.PublicKeyPEM(),
		}
		if err := s.sendJSON(pairResponse); err != nil {
			slog.Error("Failed to send PAIR response", "error", err)
			return
		}
		slog.Info("Sent PAIR response with our public key")

		// Persist receiver's public key to TrustedDeviceStore if configured
		if s.trustedStore != nil && receiverKey != nil && resp.PublicKey != "" {
			alias := s.receiverAlias
			if alias == "" {
				alias = "Unknown Device"
			}
			device := storage.TrustedDevice{
				Alias:     alias,
				PublicKey: resp.PublicKey,
				AddedAt:   time.Now().Unix(),
			}
			if err := s.trustedStore.Add(device); err != nil {
				slog.Warn("Failed to persist trusted device", "error", err)
			} else {
				slog.Info("Device paired and trusted", "alias", alias)
			}
		}

		// After PAIR, receiver will send a new file list response
		// We stay in senderStateWaitFileAccept to receive it
		return

	case "OK":
		// Files accepted
		if !s.closed {
			select {
			case s.accepted <- resp.Files:
			default:
			}
		}
		s.state = senderStateSendingFiles
	}
}

// sendOps abstracts the data-channel send operations used by SendFiles. The real
// *PeerConnection satisfies it; tests may substitute a recording fake via
// RTCSender.sendOpsOverride.
type sendOps interface {
	SendJSON(v interface{}) error
	Send(data []byte) error
	SendDelimiter() error
	WaitBufferBelowWithTimeout(limit uint64, timeout time.Duration) error
	WaitBufferEmptyWithTimeout(timeout time.Duration) error
}

// sendable is a file that has both a receiver-supplied token and local metadata,
// queued for sending by SendFiles.
type sendable struct {
	id    string
	token string
	file  *FileMeta
}

// prepareSendQueue filters acceptedIDs down to files we can actually send: the
// id must have a token from the receiver AND a local FileMeta. Filtering up front
// (mirroring the official web client, which filters fileDtoList to the accepted
// tokens before sending) keeps the pipelined send loop free of skip paths, so a
// pre-announced "next header" can never be left dangling for a file we skip.
// See docs/localsend_protocol_v3.md §C.3.
func (s *RTCSender) prepareSendQueue() []sendable {
	// Build the index once. The old nested scan made queue construction O(n*m)
	// for many-file transfers; accepted IDs now resolve in O(1).
	fileByID := make(map[string]*FileMeta, len(s.files))
	for i := range s.files {
		file := &s.files[i]
		if _, exists := fileByID[file.ID]; !exists {
			fileByID[file.ID] = file
		}
	}

	queue := make([]sendable, 0, len(s.acceptedIDs))
	for _, id := range s.acceptedIDs {
		token, ok := s.fileTokens[id]
		if !ok {
			slog.Warn("Skipping accepted file with no token", "id", id)
			continue
		}
		file := fileByID[id]
		if file == nil {
			slog.Warn("Skipping accepted file with no local metadata", "id", id)
			continue
		}
		queue = append(queue, sendable{id: id, token: token, file: file})
	}
	return queue
}

// SendFiles sends all accepted files using the pipelined LocalSend Web protocol:
// each file's header precedes its data, and the next file's header is
// pre-announced before waiting for the current file's acknowledgement (matching
// the official web client). The send queue is pre-filtered by prepareSendQueue
// so the loop never skips an entry, which keeps the pipelining desync-free even
// when the receiver accepts an id with no local file.
func (s *RTCSender) SendFiles() error {
	if s.peer == nil && s.sendOpsOverride == nil {
		return fmt.Errorf("data channel not initialized")
	}
	var ops sendOps = s.peer
	if s.sendOpsOverride != nil {
		ops = s.sendOpsOverride
	}

	queue := s.prepareSendQueue()
	if len(queue) == 0 {
		if err := ops.SendDelimiter(); err != nil {
			return fmt.Errorf("failed to send final delimiter: %w", err)
		}
	}

	// Read from disk in 512 KiB blocks, then split each block into the 16 KiB
	// WebRTC frames required by LocalSend Web. This reduces disk syscalls without
	// changing the wire protocol.
	bufPtr := fileReadBufferPool.Get().(*[]byte)
	defer fileReadBufferPool.Put(bufPtr)
	buf := *bufPtr

	headerAlreadySent := false
	for index, item := range queue {
		id := item.id
		token := item.token
		file := item.file

		slog.Info("Sending file", "id", id, "name", file.FileName)

		// Open file
		f, err := os.Open(file.FilePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", file.FilePath, err)
		}

		// Send file header
		if !headerAlreadySent {
			header := RTCSendFileHeader{ID: id, Token: token}
			if err := ops.SendJSON(header); err != nil {
				_ = f.Close()
				return fmt.Errorf("failed to send file header: %w", err)
			}
		}
		headerAlreadySent = false

		// Send file data. Physical reads are large, but every data-channel frame
		// remains at most ChunkSize for LocalSend Web compatibility.
		for {
			n, readErr := f.Read(buf)
			if n > 0 {
				for start := 0; start < n; start += ChunkSize {
					end := start + ChunkSize
					if end > n {
						end = n
					}
					if err := ops.WaitBufferBelowWithTimeout(maxBufferedAmount, bufferBackpressureTimeout); err != nil {
						_ = f.Close()
						return fmt.Errorf("timed out waiting for WebRTC send buffer: %w", err)
					}
					if err := ops.Send(buf[start:end]); err != nil {
						_ = f.Close()
						return fmt.Errorf("failed to send data: %w", err)
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = f.Close()
				return fmt.Errorf("failed to read file: %w", readErr)
			}
		}
		_ = f.Close()

		if index+1 < len(queue) {
			next := queue[index+1]
			nextHeader := RTCSendFileHeader{ID: next.id, Token: next.token}
			if err := ops.SendJSON(nextHeader); err != nil {
				return fmt.Errorf("failed to send next file header: %w", err)
			}
			headerAlreadySent = true
		} else if err := ops.SendDelimiter(); err != nil {
			return fmt.Errorf("failed to send final delimiter: %w", err)
		}

		select {
		case result := <-s.fileResults:
			if result.ID != id {
				return fmt.Errorf("unexpected file acknowledgement for %s", result.ID)
			}
			if !result.Success {
				message := "receiver failed to save file"
				if result.Error != nil {
					message = *result.Error
				}
				return fmt.Errorf("file %s: %s", id, message)
			}
		case <-time.After(fileAcceptTimeout):
			return fmt.Errorf("timeout waiting for acknowledgement for file %s", id)
		}

		slog.Info("File sent", "id", id, "name", file.FileName)
	}

	// Wait for buffer to actually flush instead of fixed sleep
	// This is critical per protocol spec to ensure all data is delivered
	slog.Info("Waiting for buffer to flush...")
	if err := ops.WaitBufferEmptyWithTimeout(bufferFlushTimeout); err != nil {
		slog.Warn("Timeout waiting for buffer flush, continuing anyway", "error", err)
		// Don't return error - allow graceful degradation
	}

	s.state = senderStateDone
	return nil
}

// Close closes the sender and peer connection.
// Channels are closed exactly once using sync.Once to prevent panic.
// Mutex is held during channel close to prevent race with handleMessage.
func (s *RTCSender) Close() error {
	// Acquire mutex to synchronize with handleMessage
	s.mu.Lock()
	// Set closed flag before closing channels so handleMessage knows not to send
	s.closed = true
	// Close channels safely (only once)
	s.closeOnce.Do(func() {
		close(s.accepted)
		close(s.declined)
		close(s.errors)
		close(s.fileResults)
	})
	s.mu.Unlock()

	if s.peer != nil {
		return s.peer.Close()
	}
	return nil
}

// computeFingerprint computes the SHA256 fingerprint of a public key PEM string.
// Returns a hex-encoded truncated fingerprint suitable for display.
func computeFingerprint(publicKeyPEM string) string {
	hash := sha256.Sum256([]byte(publicKeyPEM))
	fullHash := hex.EncodeToString(hash[:])
	// Return first 16 hex chars for display (8 bytes)
	if len(fullHash) > 16 {
		return fullHash[:16]
	}
	return fullHash
}
