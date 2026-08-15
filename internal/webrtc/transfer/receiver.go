package transfer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/storage"
	"localsend-cli/internal/utils"
	"localsend-cli/internal/webrtc/signaling"
)

// Receiver handshake states
const (
	stateWaitNonce = iota
	stateWaitToken
	stateWaitPin
	stateWaitFileList
	stateWaitPairResponse // Waiting for sender's PAIR response
	stateWaitFiles
	stateReceivingFiles
	stateDone
)

// Configuration constants
const (
	maxPINAttempts         = constants.MaxPINAttempts   // Maximum incorrect PIN attempts before closing connection
	tokenPreviewLength     = 30                         // Max characters to show in token preview logs
	pinBlockDuration       = constants.PINBlockDuration // How long a peer is blocked after max PIN attempts
	maxControlMessageBytes = 2 * 1024 * 1024
)

// Package-level blocked peers map (persists across receiver instances)
// This ensures attackers can't bypass rate limiting by reconnecting.
// Cleanup is done lazily in isPeerBlocked() to avoid background CPU usage on e-readers.
var (
	ErrReceiverBusy = errors.New("WebRTC receiver is busy")
	blockedPeers    = make(map[string]time.Time) // signaling ID -> blocked until
	blockedPeersMu  sync.RWMutex
)

// RTCReceiver handles receiving files over WebRTC.
type RTCReceiver struct {
	signaling           *signaling.SignalingClient
	signingKey          *crypto.SigningKey
	peer                *PeerConnection
	accepting           bool
	pin                 string
	pinAttempts         int
	providedPINAttempts int
	saveDir             string
	mu                  sync.Mutex

	// Extension routing
	extRoutes map[string]string // lowercase ext -> directory

	// Folder remapping for unique folder names
	folderRemapper *utils.FolderRemapper

	// Handshake state
	state       int
	remoteNonce []byte
	localNonce  []byte
	finalNonce  []byte

	// Token verification (optional, requires PAIR flow for public key)
	senderPublicKey crypto.VerifyingKey // Set via PAIR flow
	requirePairing  bool                // If true, require PAIR before accepting files
	pendingFiles    []RTCFileDto        // Files pending while waiting for PAIR response
	pinProvider     func(attempt int) string

	// Files
	files               []RTCFileDto
	fileByID            map[string]RTCFileDto // fileId -> metadata for O(1) hot-path lookup
	fileTokens          map[string]string     // fileId -> token
	acceptedIDs         []string
	folderTransferKnown bool
	folderTransfer      bool

	// File writers
	currentFileID string
	fileWriters   map[string]*os.File
	fileBuffers   map[string]*bufio.Writer
	filePaths     map[string]string // fileId -> actual saved path
	fileHashers   map[string]hash.Hash
	currentBytes  int64
	controlBuffer []byte
	ensuredDirs   map[string]struct{}
	uniqueFiles   utils.UniqueFileAllocator

	// Callbacks
	onSelectFiles  func([]RTCFileDto) []string
	onFileReceived func(filename string, size int64, sender string)

	// TrustedDeviceStore for PAIR flow persistence
	trustedStore *storage.TrustedDeviceStore

	// Sender info (set before AcceptOffer)
	senderAlias       string // Alias from signaling offer
	senderPublicPEM   string // Sender's public key PEM from PAIR flow (for persistence)
	senderToken       string // Sender's token from token exchange (for PAIR verification)
	senderSignalingID string // Signaling ID for rate limiting across connections

	// Custom STUN servers (if empty, uses DefaultSTUNServers)
	stunServers []string

	// controlOpsOverride records control-plane transcripts in tests.
	controlOpsOverride controlChannelOps
}

// isPeerBlocked checks if a peer is currently blocked due to too many PIN attempts.
// Thread-safe: uses package-level mutex.
func isPeerBlocked(peerID string) bool {
	now := time.Now()
	blockedPeersMu.Lock()
	defer blockedPeersMu.Unlock()
	for id, until := range blockedPeers {
		if now.After(until) {
			delete(blockedPeers, id)
		}
	}
	until, exists := blockedPeers[peerID]
	return exists && now.Before(until)
}

// blockPeer blocks a peer for pinBlockDuration.
// Thread-safe: uses package-level mutex.
func blockPeer(peerID string) {
	blockedPeersMu.Lock()
	blockedPeers[peerID] = time.Now().Add(pinBlockDuration)
	blockedPeersMu.Unlock()
	slog.Info("Peer blocked due to PIN attempts", "id", peerID, "duration", pinBlockDuration)
}

// ClearBlockedPeers clears all blocked peers (for testing).
func ClearBlockedPeers() {
	blockedPeersMu.Lock()
	blockedPeers = make(map[string]time.Time)
	blockedPeersMu.Unlock()
}

// NewRTCReceiver creates a new WebRTC receiver.
func NewRTCReceiver(sig *signaling.SignalingClient, key *crypto.SigningKey, pin, saveDir string) *RTCReceiver {
	return &RTCReceiver{
		signaling:   sig,
		signingKey:  key,
		pin:         pin,
		saveDir:     saveDir,
		state:       stateWaitNonce,
		fileByID:    make(map[string]RTCFileDto),
		fileTokens:  make(map[string]string),
		fileWriters: make(map[string]*os.File),
		fileBuffers: make(map[string]*bufio.Writer),
		filePaths:   make(map[string]string),
		fileHashers: make(map[string]hash.Hash),
		ensuredDirs: make(map[string]struct{}),
	}
}

// OnSelectFiles sets the callback for selecting which files to accept.
func (r *RTCReceiver) OnSelectFiles(handler func([]RTCFileDto) []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onSelectFiles = handler
}

// OnFileReceived sets the callback for when a file is received.
func (r *RTCReceiver) OnFileReceived(handler func(filename string, size int64, sender string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onFileReceived = handler
}

// SetSenderPublicKey sets the sender's public key for token verification.
// This is typically obtained through the PAIR flow.
func (r *RTCReceiver) SetSenderPublicKey(key crypto.VerifyingKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.senderPublicKey = key
}

// SetPINProvider supplies a fresh response for each sender-owned PIN challenge.
// Empty strings are sent and counted as real attempts.
func (r *RTCReceiver) SetPINProvider(provider func(attempt int) string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pinProvider = provider
}

func (r *RTCReceiver) controlOps() controlChannelOps {
	if r.controlOpsOverride != nil {
		return r.controlOpsOverride
	}
	if r.peer != nil {
		return r.peer
	}
	return nil
}

func (r *RTCReceiver) sendJSON(v interface{}) error {
	if ops := r.controlOps(); ops != nil {
		return ops.SendJSON(v)
	}
	return fmt.Errorf("data channel not initialized")
}

func (r *RTCReceiver) sendJSONBinary(v interface{}) error {
	if ops := r.controlOps(); ops != nil {
		return ops.SendJSONBinary(v)
	}
	return fmt.Errorf("data channel not initialized")
}

func (r *RTCReceiver) sendDelimiter() error {
	if ops := r.controlOps(); ops != nil {
		return ops.SendDelimiter()
	}
	return fmt.Errorf("data channel not initialized")
}

func (r *RTCReceiver) cleanupPartialFilesLocked() {
	for _, file := range r.fileWriters {
		_ = file.Close()
	}
	for _, path := range r.filePaths {
		_ = os.Remove(path)
	}
	r.fileWriters = make(map[string]*os.File)
	r.fileBuffers = make(map[string]*bufio.Writer)
	r.filePaths = make(map[string]string)
	r.fileHashers = make(map[string]hash.Hash)
	r.currentFileID = ""
	r.currentBytes = 0
}

func (r *RTCReceiver) terminateLocked() {
	r.cleanupPartialFilesLocked()
	r.state = stateDone
	if r.peer == nil {
		return
	}
	peer := r.peer
	r.peer = nil
	go func() { _ = peer.Close() }()
}

func (r *RTCReceiver) handlePeerClosed(peer *PeerConnection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.peer != peer {
		return
	}
	r.cleanupPartialFilesLocked()
	r.peer = nil
	r.accepting = false
	r.state = stateDone
}

// SetRequirePairing enables pairing requirement.
// When enabled, the receiver will request PAIR before accepting files from unknown senders.
func (r *RTCReceiver) SetRequirePairing(require bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requirePairing = require
}

// SetExtensionRoutes sets extension-to-directory routing.
// Keys should be lowercase extensions without dots (e.g., "epub", "pdf").
func (r *RTCReceiver) SetExtensionRoutes(routes map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extRoutes = routes
}

// SetTrustedStore sets the trusted device store for PAIR flow persistence.
// When set, devices paired during the PAIR flow are persisted for future sessions.
func (r *RTCReceiver) SetTrustedStore(store *storage.TrustedDeviceStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trustedStore = store
}

// SetSenderInfo sets sender information for the current transfer.
// The alias is used when persisting the trusted device after PAIR.
// Call this before AcceptOffer with information from the signaling offer.
func (r *RTCReceiver) SetSenderInfo(alias string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.senderAlias = alias
}

// SetSTUNServers sets custom STUN servers for ICE negotiation.
// If not set or empty, DefaultSTUNServers will be used.
func (r *RTCReceiver) SetSTUNServers(servers []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stunServers = servers
}

func (r *RTCReceiver) rebuildFileIndex() {
	r.fileByID = make(map[string]RTCFileDto, len(r.files))
	for _, file := range r.files {
		// Preserve the old linear-scan behavior for duplicate IDs: first wins.
		if _, exists := r.fileByID[file.ID]; !exists {
			r.fileByID[file.ID] = file
		}
	}
}

func (r *RTCReceiver) fileMetaByID(id string) (RTCFileDto, bool) {
	if meta, ok := r.fileByID[id]; ok {
		return meta, true
	}
	// Tests and a few internal setup paths assign r.files directly. Lazily fill
	// the index once so those callers retain their behavior without putting a
	// linear scan back into the per-frame hot path.
	for _, file := range r.files {
		if file.ID == id {
			if r.fileByID == nil {
				r.fileByID = make(map[string]RTCFileDto)
			}
			r.fileByID[id] = file
			return file, true
		}
	}
	return RTCFileDto{}, false
}

// prepareFolderRemap computes folder remapping for unique folder names.
// This finds unique names for root folders that already exist in saveDir.
func (r *RTCReceiver) prepareFolderRemap() {
	// Collect filenames from files
	filenames := make([]string, len(r.files))
	filenames = filenames[:0]
	for _, f := range r.files {
		if strings.ContainsRune(f.FileName, '\x00') {
			continue
		}
		if sanitized, err := utils.SanitizePathWithFallback(f.FileName); err == nil {
			filenames = append(filenames, sanitized)
		}
	}

	remapper, err := utils.NewFolderRemapper(r.saveDir, filenames)
	if err != nil {
		slog.Warn("Failed to create folder remapper", "error", err)
		return
	}

	// Log remapped folders
	for orig, unique := range remapper.GetRemap() {
		slog.Info("Remapping folder for uniqueness", "original", orig, "unique", unique)
	}

	r.folderRemapper = remapper
}

// applyFolderRemap applies the folder remap to a sanitized path.
func (r *RTCReceiver) applyFolderRemap(sanitizedPath string) string {
	if r.folderRemapper == nil {
		return sanitizedPath
	}
	return r.folderRemapper.Apply(sanitizedPath)
}

// prepareFilesForReceive validates files and generates tokens. It opens each
// destination lazily when the corresponding file header arrives.
// Returns a map of fileId -> token for successfully prepared files.
func (r *RTCReceiver) prepareFilesForReceive(acceptedIDs []string) map[string]string {
	// Prepare folder remap for unique folder names (computed once)
	r.prepareFolderRemap()

	fileTokens := make(map[string]string)
	for _, id := range acceptedIDs {
		targetFile, ok := r.fileMetaByID(id)
		if !ok {
			slog.Warn("File ID not found in file list", "id", id)
			continue
		}
		if strings.ContainsRune(targetFile.FileName, '\x00') {
			slog.Warn("Filename contains NUL byte", "id", id)
			continue
		}

		// Sanitize filename to allow subdirectories but prevent path traversal attacks.
		// A malicious sender could send "../../../etc/passwd" to write outside saveDir.
		_, sanitizeErr := utils.SanitizePathWithFallback(targetFile.FileName)
		if sanitizeErr != nil {
			slog.Warn("Invalid filename rejected", "filename", targetFile.FileName, "id", id, "error", sanitizeErr)
			continue
		}

		// Use crypto/rand for unpredictable tokens instead of time-based
		token := crypto.GenerateSecureToken()
		fileTokens[id] = token
		r.fileTokens[id] = token
	}
	return fileTokens
}

func (r *RTCReceiver) sendError(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sendErrorLocked(message)
}

// sendErrorLocked sends an error response while the receiver mutex is held.
func (r *RTCReceiver) sendErrorLocked(message string) {
	if r.controlOps() == nil {
		return
	}
	_ = r.sendJSON(RTCErrorResponse{Error: message})
}

// getSaveDir returns the appropriate save directory for a filename.
// For folder transfers, returns the main save dir to keep folders together.
// For individual files, applies extension routing if configured.
func (r *RTCReceiver) getSaveDir(filename string) string {
	// Folder transfers bypass extension routing to keep folder contents together
	if r.isFolderTransfer() {
		return r.saveDir
	}

	if r.extRoutes == nil {
		return r.saveDir
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		return r.saveDir
	}

	// Remove leading dot and lowercase
	ext = strings.ToLower(ext[1:])

	if dir, ok := r.extRoutes[ext]; ok {
		return dir
	}

	// Check for "default" route
	if dir, ok := r.extRoutes["default"]; ok {
		return dir
	}

	return r.saveDir
}

// isFolderTransfer checks if any file in the current transfer has subdirectory structure.
// The result is cached because getSaveDir runs once per file in large batches.
func (r *RTCReceiver) isFolderTransfer() bool {
	if r.folderTransferKnown {
		return r.folderTransfer
	}
	for _, file := range r.files {
		if strings.Contains(filepath.ToSlash(file.FileName), "/") {
			r.folderTransfer = true
			break
		}
	}
	r.folderTransferKnown = true
	return r.folderTransfer
}

func (r *RTCReceiver) ensureSaveDir(dir string) error {
	if r.ensuredDirs == nil {
		r.ensuredDirs = make(map[string]struct{})
	}
	if _, ok := r.ensuredDirs[dir]; ok {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	r.ensuredDirs[dir] = struct{}{}
	return nil
}

// AcceptOffer accepts an incoming WebRTC offer.
func (r *RTCReceiver) AcceptOffer(offer signaling.WsServerMessage) error {
	if offer.Peer == nil {
		return fmt.Errorf("offer missing peer info")
	}

	peerID := offer.Peer.ID.String()
	if isPeerBlocked(peerID) {
		slog.Warn("Rejecting offer from blocked peer", "peer", offer.Peer.Alias, "id", peerID)
		return fmt.Errorf("too many failed PIN attempts, please try again later")
	}

	// Reserve the singleton receive session before doing ICE work. A duplicate or
	// second sender must never tear down an in-progress transfer.
	r.mu.Lock()
	if r.accepting || (r.peer != nil && r.state != stateDone) {
		r.mu.Unlock()
		return ErrReceiverBusy
	}
	oldPeer := r.peer
	r.peer = nil
	r.accepting = true
	r.cleanupPartialFilesLocked()
	r.fileTokens = make(map[string]string)
	r.files = nil
	r.fileByID = make(map[string]RTCFileDto)
	r.acceptedIDs = nil
	r.folderTransferKnown = false
	r.folderTransfer = false
	r.ensuredDirs = make(map[string]struct{})
	r.uniqueFiles.Reset()
	r.controlBuffer = nil
	r.remoteNonce = nil
	r.localNonce = nil
	r.finalNonce = nil
	r.pinAttempts = 0
	r.providedPINAttempts = 0
	r.senderPublicKey = nil
	r.senderPublicPEM = ""
	r.senderToken = ""
	r.pendingFiles = nil
	r.senderAlias = offer.Peer.Alias
	r.senderSignalingID = peerID
	r.state = stateWaitNonce
	r.mu.Unlock()

	if oldPeer != nil {
		oldPeer.OnClose(nil)
		_ = oldPeer.Close()
	}

	failAccept := func() {
		r.mu.Lock()
		r.accepting = false
		r.state = stateDone
		r.mu.Unlock()
	}

	sdp, err := signaling.DecompressSDP(offer.SDP)
	if err != nil {
		failAccept()
		return fmt.Errorf("failed to decompress SDP: %w", err)
	}

	r.mu.Lock()
	stunServers := append([]string(nil), r.stunServers...)
	r.mu.Unlock()
	if len(stunServers) == 0 {
		stunServers = DefaultSTUNServers
	}

	peer, err := NewPeerConnection(PeerConfig{STUNServers: stunServers, IsInitiator: false})
	if err != nil {
		failAccept()
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	peer.OnDataMessage(r.handleDataMessage)
	peer.OnClose(func() { r.handlePeerClosed(peer) })

	r.mu.Lock()
	if !r.accepting {
		r.mu.Unlock()
		_ = peer.Close()
		return ErrReceiverBusy
	}
	r.peer = peer
	r.accepting = false
	r.mu.Unlock()

	answer, err := peer.AcceptOffer(sdp)
	if err != nil {
		_ = peer.Close()
		return fmt.Errorf("failed to accept offer: %w", err)
	}

	if err := r.signaling.SendAnswer(offer.SessionID, offer.Peer.ID, answer); err != nil {
		_ = peer.Close()
		return fmt.Errorf("failed to send answer: %w", err)
	}

	slog.Info("Sent answer", "peer", offer.Peer.Alias, "session", offer.SessionID)
	return nil
}

// handleMessage processes incoming data channel messages.
func (r *RTCReceiver) handleMessage(data []byte) {
	isString := bytes.Equal(data, []byte("0")) || json.Valid(data)
	r.handleDataMessage(data, isString)
}

func (r *RTCReceiver) handleDataMessage(data []byte, isString bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	slog.Debug("Message received", "state", r.state, "len", len(data))

	// While receiving file data, every text frame is a boundary. Exact text "0"
	// ends the transfer; every other text frame must parse as the next header.
	// A one-byte binary frame remains ordinary file content.
	if r.state == stateReceivingFiles && r.currentFileID != "" {
		if !isString {
			r.handleBinaryData(data)
			return
		}

		r.finishCurrentFile()
		if bytes.Equal(data, []byte("0")) {
			slog.Info("All files received, transfer complete")
			r.state = stateDone
			peer := r.peer
			r.peer = nil
			if peer != nil {
				go func(p *PeerConnection) {
					time.Sleep(100 * time.Millisecond)
					_ = p.Close()
				}(peer)
			}
			return
		}

		var header RTCSendFileHeader
		if err := json.Unmarshal(data, &header); err != nil || header.ID == "" {
			slog.Error("Failed to parse next file header", "error", err)
			r.terminateLocked()
			return
		}
		_ = r.startReceivingFile(&header)
		return
	}

	// A control-message delimiter is specifically the text frame "0".
	if isString && bytes.Equal(data, []byte("0")) {
		if len(r.controlBuffer) > 0 {
			data = append([]byte(nil), r.controlBuffer...)
			r.controlBuffer = nil
			isString = true
		} else {
			return
		}
	}

	// Chunked control messages are binary frames terminated by a text "0".
	if !isString {
		if len(r.controlBuffer)+len(data) > maxControlMessageBytes {
			r.controlBuffer = nil
			slog.Warn("Chunked control message exceeds limit")
			return
		}
		r.controlBuffer = append(r.controlBuffer, data...)
		return
	}

	// Parse message type
	msg, msgType, err := ParseRTCMessage(data)
	if err != nil || msg == nil {
		// Could be binary data in wrong state, or malformed JSON
		if r.state == stateWaitFiles && data[0] != '{' {
			// Binary data without header - might be continuation
			slog.Debug("Possible binary data, treating as file content")
			r.handleBinaryData(data)
			return
		}
		slog.Warn("Failed to parse RTC message", "error", err)
		return
	}

	slog.Debug("Parsed RTC message", "type", msgType)

	switch r.state {
	case stateWaitNonce:
		r.handleNonce(msg, msgType)
	case stateWaitToken:
		r.handleToken(msg, msgType)
	case stateWaitPin:
		r.handlePin(msg, msgType)
	case stateWaitFileList:
		r.handleFileList(msg, msgType, data)
	case stateWaitPairResponse:
		r.handlePairResponse(msg, msgType, data)
	case stateWaitFiles:
		r.handleFileHeader(msg, msgType)
	}
}

// handleNonce processes the nonce message from sender.
func (r *RTCReceiver) handleNonce(msg interface{}, msgType string) {
	if msgType != "nonce" {
		slog.Warn("Expected nonce, got", "type", msgType)
		return
	}

	nonceMsg, ok := msg.(*RTCNonceMessage)
	if !ok {
		slog.Error("Invalid message type for nonce", "got", fmt.Sprintf("%T", msg))
		r.sendErrorLocked("internal error: message type mismatch")
		return
	}
	remoteNonce, err := crypto.DecodeNonce(nonceMsg.Nonce)
	if err != nil {
		slog.Error("Failed to decode remote nonce", "error", err)
		r.sendErrorLocked("invalid nonce format")
		return
	}
	r.remoteNonce = remoteNonce

	// Generate and send our nonce
	localNonce, err := crypto.GenerateNonce()
	if err != nil {
		slog.Error("Failed to generate nonce", "error", err)
		r.sendErrorLocked("internal error: nonce generation failed")
		return
	}
	r.localNonce = localNonce

	// Final nonce = sender_nonce || receiver_nonce
	r.finalNonce = crypto.CombineNonces(r.remoteNonce, r.localNonce)

	response := RTCNonceMessage{
		Nonce: crypto.EncodeNonce(localNonce),
	}
	if err := r.sendJSON(response); err != nil {
		slog.Error("Failed to send nonce response", "error", err)
		return
	}

	slog.Info("Nonce exchange complete")
	r.state = stateWaitToken
}

// handleToken processes the token message from sender.
func (r *RTCReceiver) handleToken(msg interface{}, msgType string) {
	if msgType != "token_request" {
		slog.Warn("Expected token_request, got", "type", msgType)
		return
	}

	tokenReq, ok := msg.(*RTCTokenRequest)
	if !ok {
		slog.Error("Invalid message type for token_request", "got", fmt.Sprintf("%T", msg))
		r.sendErrorLocked("internal error: message type mismatch")
		return
	}
	tokenPreview := tokenReq.Token
	if len(tokenPreview) > tokenPreviewLength {
		tokenPreview = tokenPreview[:tokenPreviewLength] + "..."
	}
	slog.Info("Received token from sender", "token", tokenPreview)

	// Store sender's token for PAIR verification (if we don't have their public key yet)
	r.senderToken = tokenReq.Token

	// Try to find sender in trusted device store (skip PAIR for previously paired devices)
	if r.senderPublicKey == nil && r.trustedStore != nil && r.requirePairing {
		if key, pem := r.findTrustedSender(tokenReq.Token); key != nil {
			r.senderPublicKey = key
			r.senderPublicPEM = pem
			slog.Info("Sender found in trusted devices, PAIR will be skipped")
		}
	}

	// An expected key is an explicit identity requirement. Verification failure is
	// always terminal; opportunistic operation is represented by no expected key.
	if r.senderPublicKey != nil {
		if err := crypto.VerifyTokenNonce(r.senderPublicKey, tokenReq.Token, r.finalNonce); err != nil {
			_ = r.sendJSON(RTCTokenResponse{Status: StatusInvalidSignature})
			slog.Error("Rejecting sender due to invalid token signature", "error", err)
			r.terminateLocked()
			return
		}
		slog.Info("Sender token verified successfully")
	}

	// Generate our token
	token, err := r.signingKey.GenerateTokenWithNonce(r.finalNonce)
	if err != nil {
		slog.Error("Failed to generate token", "error", err)
		r.sendErrorLocked("internal error: token generation failed")
		return
	}

	// Send token response (with or without PIN requirement)
	var response RTCTokenResponse
	if r.pin != "" {
		response = RTCTokenResponse{Status: "PIN_REQUIRED", Token: token}
	} else {
		response = RTCTokenResponse{Status: "OK", Token: token}
	}

	_ = r.sendJSON(response)

	slog.Info("Token exchange complete", "status", response.Status)
	if response.Status == "PIN_REQUIRED" {
		r.state = stateWaitPin
	} else {
		r.state = stateWaitFileList
	}
}

// handlePin processes the PIN message from sender.
func (r *RTCReceiver) handlePin(msg interface{}, msgType string) {
	if msgType != "pin" {
		slog.Warn("Expected pin, got", "type", msgType)
		return
	}

	pinMsg, ok := msg.(*RTCPinMessage)
	if !ok {
		slog.Error("Invalid message type for pin", "got", fmt.Sprintf("%T", msg))
		r.sendErrorLocked("internal error: message type mismatch")
		return
	}
	slog.Info("Received PIN challenge")

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(pinMsg.Pin), []byte(r.pin)) == 1 {
		slog.Info("PIN correct")
		response := RTCPinReceivingResponse{Status: "OK"}
		_ = r.sendJSON(response)
		r.state = stateWaitFileList
		return
	}

	r.pinAttempts++
	slog.Warn("Incorrect PIN", "attempt", r.pinAttempts)

	if r.pinAttempts >= maxPINAttempts {
		slog.Error("Too many PIN attempts, blocking peer and closing connection")
		// Block this peer from reconnecting for a duration
		if r.senderSignalingID != "" {
			blockPeer(r.senderSignalingID)
		}
		response := RTCPinReceivingResponse{Status: StatusTooManyAttempts}
		_ = r.sendJSON(response)
		r.terminateLocked()
		return
	}

	response := RTCPinReceivingResponse{Status: "PIN_REQUIRED"}
	_ = r.sendJSON(response)
}

// handleFileList processes the file list or sender-owned PIN challenge.
func (r *RTCReceiver) handleFileList(_ interface{}, msgType string, data []byte) {
	if msgType == "status_PIN_REQUIRED" {
		r.providedPINAttempts++
		pin := r.pin
		if r.pinProvider != nil {
			provider := r.pinProvider
			attempt := r.providedPINAttempts
			r.mu.Unlock()
			pin = provider(attempt)
			r.mu.Lock()
		}
		if err := r.sendJSON(RTCPinMessage{Pin: pin}); err != nil {
			slog.Error("Failed to answer sender PIN challenge", "error", err)
			r.terminateLocked()
		}
		return
	}
	if msgType == "status_TOO_MANY_ATTEMPTS" {
		slog.Error("Sender rejected too many PIN attempts")
		r.terminateLocked()
		return
	}

	// File list comes as RTCPinSendingResponse with status OK.
	if msgType != "file_list" && msgType != "status_OK" {
		slog.Warn("Expected file_list or sender PIN status, got", "type", msgType)
		return
	}

	// Parse as RTCPinSendingResponse
	var fileListMsg RTCPinSendingResponse
	if err := json.Unmarshal(data, &fileListMsg); err != nil {
		slog.Error("Failed to parse file list", "error", err)
		return
	}

	// Validate file count to prevent DoS via excessive file metadata
	if len(fileListMsg.Files) > constants.MaxFilesPerSession {
		slog.Error("Too many files in transfer", "count", len(fileListMsg.Files), "max", constants.MaxFilesPerSession)
		response := RTCFileListResponse{Status: "DECLINED"}
		_ = r.sendJSONBinary(response)
		_ = r.sendDelimiter()
		return
	}

	r.files = fileListMsg.Files
	r.rebuildFileIndex()
	r.folderTransferKnown = false
	_ = r.isFolderTransfer()
	slog.Info("Received file list", "count", len(r.files))

	// A large transfer can contain thousands of files. Keep the aggregate at
	// Info and individual metadata at Debug so logging cannot become the
	// many-file hot path that upstream LocalSend 1.18 explicitly removed.
	for _, f := range r.files {
		slog.Debug("File offered", "name", f.FileName, "size", f.Size)
	}

	// Select files to accept
	// IMPORTANT: Release mutex before calling callback to prevent deadlock.
	// The callback may call receiver methods that need the mutex.
	var acceptedIDs []string
	if r.onSelectFiles != nil {
		files := r.files            // Copy reference before unlocking
		callback := r.onSelectFiles // Copy callback reference
		r.mu.Unlock()
		acceptedIDs = callback(files)
		r.mu.Lock()
	} else {
		// Accept all by default
		for _, f := range r.files {
			acceptedIDs = append(acceptedIDs, f.ID)
		}
	}
	r.acceptedIDs = acceptedIDs
	if r.controlOps() == nil {
		return
	}

	if len(acceptedIDs) == 0 {
		response := RTCFileListResponse{Status: "DECLINED"}
		if err := r.sendJSONBinary(response); err != nil {
			slog.Error("Failed to send decline response", "error", err)
		}
		if err := r.sendDelimiter(); err != nil {
			slog.Error("Failed to send delimiter", "error", err)
		}
		slog.Info("Declined all files")
		return
	}

	// Check if PAIR is required
	if r.requirePairing && r.senderPublicKey == nil {
		// Initiate PAIR flow
		slog.Info("Initiating PAIR flow - sender not yet trusted")
		r.pendingFiles = r.files // Store files for after PAIR completes

		response := RTCFileListResponse{
			Status:    "PAIR",
			PublicKey: r.signingKey.PublicKeyPEM(),
		}
		if err := r.sendJSONBinary(response); err != nil {
			slog.Error("Failed to send PAIR request", "error", err)
			return
		}
		if err := r.sendDelimiter(); err != nil {
			slog.Error("Failed to send delimiter after PAIR request", "error", err)
			return
		}
		slog.Info("Sent PAIR request with our public key, waiting for sender's response")
		r.state = stateWaitPairResponse
		return
	}

	// Prepare files and generate tokens
	fileTokens := r.prepareFilesForReceive(acceptedIDs)

	// Send acceptance with file tokens as binary (official protocol uses chunked binary)
	response := RTCFileListResponse{
		Status: "OK",
		Files:  fileTokens,
	}
	if err := r.sendJSONBinary(response); err != nil {
		slog.Error("Failed to send file acceptance", "error", err)
		return
	}

	// Send delimiter to signal end of our response (required by protocol)
	if err := r.sendDelimiter(); err != nil {
		slog.Error("Failed to send delimiter", "error", err)
		return
	}

	slog.Info("Sent file acceptance and delimiter", "count", len(fileTokens))
	r.state = stateWaitFiles
}

// handlePairResponse processes the PAIR response from sender.
func (r *RTCReceiver) handlePairResponse(_ interface{}, msgType string, data []byte) {
	if msgType != "pair_response" && msgType != "status_OK" && msgType != "status_PAIR_DECLINED" && msgType != "status_INVALID_SIGNATURE" {
		slog.Warn("Expected pair response, got", "type", msgType)
		return
	}

	var resp RTCPairResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		slog.Error("Failed to parse PAIR response", "error", err)
		return
	}

	switch resp.Status {
	case "OK":
		slog.Info("PAIR accepted by sender", "hasPublicKey", resp.PublicKey != "")
		if resp.PublicKey != "" {
			// Parse sender's public key
			key, err := crypto.ParsePublicKeyPEM(resp.PublicKey)
			if err != nil {
				slog.Error("Failed to parse sender's public key", "error", err)
				return
			}

			// CRITICAL: Verify sender's token was signed with this public key
			// This ensures the PAIR public key matches the identity from token exchange
			if r.senderToken != "" {
				if err := crypto.VerifyTokenNonce(key, r.senderToken, r.finalNonce); err != nil {
					slog.Error("Sender token does not match PAIR public key - potential identity mismatch", "error", err)
					// Reject the pairing by not storing the key and not proceeding
					return
				}
				slog.Info("Sender token verified against PAIR public key")
			}

			// Verification passed - safe to store
			r.senderPublicKey = key
			r.senderPublicPEM = resp.PublicKey
			slog.Info("Stored sender's public key for verification")

			// Persist to TrustedDeviceStore if configured
			if r.trustedStore != nil {
				alias := r.senderAlias
				if alias == "" {
					alias = "Unknown Device"
				}
				device := storage.TrustedDevice{
					Alias:     alias,
					PublicKey: resp.PublicKey,
					AddedAt:   time.Now().Unix(),
				}
				if err := r.trustedStore.Add(device); err != nil {
					slog.Warn("Failed to persist trusted device", "error", err)
				} else {
					slog.Info("Device paired and trusted", "alias", alias)
				}
			}
		}

		// Now proceed with accepting files - send OK response with file tokens
		r.acceptFilesAfterPair()

	case "PAIR_DECLINED":
		slog.Warn("PAIR declined by sender")
		if r.requirePairing {
			// Pairing is mandatory, so there is no valid post-decline file-list
			// response to send. Close locally; the official sender observes EOF/error
			// instead of receiving a non-enum extension or hanging indefinitely.
			r.terminateLocked()
			return
		}
		r.acceptFilesAfterPair()

	case "INVALID_SIGNATURE":
		slog.Error("Sender rejected our signature")
		// Close the connection
		return
	}
}

// acceptFilesAfterPair sends the file acceptance response after PAIR flow completes.
func (r *RTCReceiver) acceptFilesAfterPair() {
	// Prepare files and generate tokens
	fileTokens := r.prepareFilesForReceive(r.acceptedIDs)

	// Send acceptance with file tokens
	response := RTCFileListResponse{
		Status: "OK",
		Files:  fileTokens,
	}
	if err := r.sendJSONBinary(response); err != nil {
		slog.Error("Failed to send file acceptance after PAIR", "error", err)
		return
	}

	if err := r.sendDelimiter(); err != nil {
		slog.Error("Failed to send delimiter after PAIR acceptance", "error", err)
		return
	}

	slog.Info("Sent file acceptance after PAIR", "count", len(fileTokens))
	r.state = stateWaitFiles
}

// handleFileHeader processes file header before binary data.
func (r *RTCReceiver) handleFileHeader(msg interface{}, msgType string) {
	if msgType != "file_header" {
		slog.Debug("Non-file-header in file receive state", "type", msgType)
		return
	}

	header, ok := msg.(*RTCSendFileHeader)
	if !ok {
		slog.Error("Invalid message type for file_header", "got", fmt.Sprintf("%T", msg))
		return
	}
	_ = r.startReceivingFile(header)
}

func (r *RTCReceiver) startReceivingFile(header *RTCSendFileHeader) bool {
	if err := r.validateFileHeader(header); err != nil {
		slog.Warn("Rejected file header", "id", header.ID, "error", err)
		r.rejectFileTransfer(err.Error())
		return false
	}
	meta, ok := r.fileMetaByID(header.ID)
	if !ok {
		r.rejectFileTransfer("unknown file ID")
		return false
	}
	sanitizedPath, err := utils.SanitizePathWithFallback(meta.FileName)
	if err != nil {
		r.rejectFileTransfer("invalid filename")
		return false
	}
	sanitizedPath = r.applyFolderRemap(sanitizedPath)
	subDir, baseName := filepath.Dir(sanitizedPath), filepath.Base(sanitizedPath)
	saveDir := r.getSaveDir(meta.FileName)
	if subDir != "." && subDir != "" {
		saveDir = filepath.Join(saveDir, subDir)
	}
	if err := r.ensureSaveDir(saveDir); err != nil {
		r.rejectFileTransfer("failed to create destination")
		return false
	}
	file, path, err := r.uniqueFiles.Create(saveDir, baseName)
	if err != nil {
		r.rejectFileTransfer("failed to create file")
		return false
	}
	if r.fileWriters == nil {
		r.fileWriters = make(map[string]*os.File)
	}
	if r.fileBuffers == nil {
		r.fileBuffers = make(map[string]*bufio.Writer)
	}
	if r.filePaths == nil {
		r.filePaths = make(map[string]string)
	}
	if r.fileHashers == nil {
		r.fileHashers = make(map[string]hash.Hash)
	}
	r.fileWriters[header.ID] = file
	r.fileBuffers[header.ID] = bufio.NewWriterSize(file, utils.FileIOBufferSize)
	r.filePaths[header.ID] = path
	if meta.SHA256 != "" {
		r.fileHashers[header.ID] = sha256.New()
	}

	r.currentFileID = header.ID
	r.currentBytes = 0
	r.state = stateReceivingFiles
	slog.Info("Receiving file", "id", header.ID)
	return true
}

func (r *RTCReceiver) validateFileHeader(header *RTCSendFileHeader) error {
	expectedToken, ok := r.fileTokens[header.ID]
	if !ok {
		return fmt.Errorf("unknown file ID")
	}

	if subtle.ConstantTimeCompare([]byte(header.Token), []byte(expectedToken)) != 1 {
		return fmt.Errorf("invalid file token")
	}

	return nil
}

func (r *RTCReceiver) rejectFileTransfer(reason string) {
	if r.peer != nil {
		peer := r.peer
		r.peer = nil
		go func(p *PeerConnection) {
			_ = p.Close()
		}(peer)
	}
	r.currentFileID = ""
	r.state = stateWaitFiles
}

// handleBinaryData writes received file data.
func (r *RTCReceiver) handleBinaryData(data []byte) {
	meta, ok := r.fileMetaByID(r.currentFileID)
	if !ok || int64(len(data)) > meta.Size-r.currentBytes {
		slog.Error("Received more data than declared", "fileId", r.currentFileID)
		r.cleanupCurrentFile()
		r.rejectFileTransfer("file exceeds declared size")
		return
	}

	var (
		n   int
		err error
	)
	if buffered := r.fileBuffers[r.currentFileID]; buffered != nil {
		n, err = buffered.Write(data)
	} else if file := r.fileWriters[r.currentFileID]; file != nil {
		// Compatibility fallback for tests/internal callers that install a raw
		// writer directly instead of going through startReceivingFile.
		n, err = file.Write(data)
	} else {
		slog.Warn("No file writer for current file", "fileId", r.currentFileID)
		return
	}
	if err != nil {
		slog.Error("Failed to write data", "error", err)
		return
	}

	r.currentBytes += int64(n)
	slog.Debug("Buffered file data", "fileId", r.currentFileID, "bytes", n)
	if h := r.fileHashers[r.currentFileID]; h != nil && n > 0 {
		_, _ = h.Write(data[:n])
	}
}

func (r *RTCReceiver) cleanupCurrentFile() {
	id := r.currentFileID
	delete(r.fileBuffers, id) // discard buffered partial data; the file is removed below
	if f := r.fileWriters[id]; f != nil {
		_ = f.Close()
	}
	if path := r.filePaths[id]; path != "" {
		_ = os.Remove(path)
	}
	delete(r.fileWriters, id)
	delete(r.filePaths, id)
	delete(r.fileHashers, id)
	r.currentFileID = ""
	r.currentBytes = 0
}

// finishCurrentFile closes the current file and sends a success response to the sender.
func (r *RTCReceiver) finishCurrentFile() {
	if r.currentFileID == "" {
		return
	}

	fileID := r.currentFileID
	meta, metaOK := r.fileMetaByID(fileID)
	success := metaOK && r.currentBytes == meta.Size
	var errorMsg *string
	if !success {
		msg := "file size does not match declaration"
		errorMsg = &msg
	}

	// Flush the 512 KiB userspace buffer before acknowledging the file. Like
	// upstream LocalSend, this does not fsync every file: forcing flash durability
	// per file is disproportionately expensive on slow e-reader storage.
	if buffered := r.fileBuffers[fileID]; buffered != nil {
		if err := buffered.Flush(); err != nil {
			slog.Error("Failed to flush file data", "fileId", fileID, "error", err)
			success = false
			msg := "failed to save file"
			errorMsg = &msg
		}
		delete(r.fileBuffers, fileID)
	}
	if file := r.fileWriters[fileID]; file != nil {
		if err := file.Close(); err != nil {
			slog.Error("Failed to close file", "fileId", fileID, "error", err)
			success = false
			msg := "failed to save file"
			errorMsg = &msg
		}
		delete(r.fileWriters, fileID)
	}

	path, pathOK := r.filePaths[fileID]
	if h := r.fileHashers[fileID]; h != nil {
		checksum := hex.EncodeToString(h.Sum(nil))
		if metaOK && meta.SHA256 != "" && checksum != meta.SHA256 {
			slog.Error("Checksum mismatch", "file", filepath.Base(path), "expected", meta.SHA256, "got", checksum)
			success = false
			msg := "checksum mismatch"
			errorMsg = &msg
		}
	}
	delete(r.fileHashers, fileID)

	if !success {
		if pathOK {
			_ = os.Remove(path)
		}
	} else if pathOK {
		savedFilename := filepath.Base(path)
		slog.Info("File received successfully", "file", savedFilename)
		if r.onFileReceived != nil {
			r.onFileReceived(savedFilename, meta.Size, "WebRTC")
		}
	}
	delete(r.filePaths, fileID)

	response := RTCSendFileResponse{
		ID:      fileID,
		Success: success,
		Error:   errorMsg,
	}
	if r.controlOps() != nil {
		if err := r.sendJSON(response); err != nil {
			slog.Error("Failed to send file response", "error", err)
		}
	}

	r.currentFileID = ""
	r.currentBytes = 0
}

// Close closes the receiver.
func (r *RTCReceiver) Close() error {
	r.mu.Lock()
	r.cleanupPartialFilesLocked()
	peer := r.peer
	r.peer = nil
	r.accepting = false
	r.state = stateDone
	r.mu.Unlock()

	if peer != nil {
		peer.OnClose(nil)
		return peer.Close()
	}
	return nil
}

// ListenForOffersWithContext listens for incoming WebRTC offers with context support.
// The listener stops when the context is cancelled or the signaling channel closes.
func (r *RTCReceiver) ListenForOffersWithContext(ctx context.Context, onOffer func(offer signaling.WsServerMessage)) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-r.signaling.Offers():
				if !ok {
					return
				}
				if msg.Type == "OFFER" {
					onOffer(msg)
				}
			}
		}
	}()
}

// findTrustedSender searches the trusted device store for a public key that can verify
// the sender's token. This allows skipping the PAIR flow for previously paired devices.
// Returns the verifying key and PEM if found, nil otherwise.
func (r *RTCReceiver) findTrustedSender(token string) (crypto.VerifyingKey, string) {
	if r.trustedStore == nil {
		return nil, ""
	}

	// Get all trusted public keys
	pubKeys := r.trustedStore.ListPublicKeys()
	if len(pubKeys) == 0 {
		return nil, ""
	}

	// Try to verify the token against each trusted public key
	for _, pemStr := range pubKeys {
		key, err := crypto.ParsePublicKeyPEM(pemStr)
		if err != nil {
			slog.Debug("Failed to parse trusted public key", "error", err)
			continue
		}

		// Try to verify the sender's token with this key
		if err := crypto.VerifyTokenNonce(key, token, r.finalNonce); err == nil {
			slog.Info("Found matching trusted device for sender")
			return key, pemStr
		}
	}

	return nil, ""
}
