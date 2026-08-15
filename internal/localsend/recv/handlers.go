package recv

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

	"github.com/gofiber/fiber/v3"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

const (
	maxPreUploadBodyBytes = 8 * 1024 * 1024
	maxRegisterBodyBytes  = 1 * 1024 * 1024
	maxNonceBodyBytes     = 64 * 1024
)

var errRequestBodyTooLarge = errors.New("request body too large")

// parseJSONBodyLimited parses JSON with a hard size cap to prevent oversized
// metadata requests from exhausting memory.
func parseJSONBodyLimited(c fiber.Ctx, dst interface{}, maxBytes int64) error {
	if c.RequestCtx().Request.Header.ContentLength() > int(maxBytes) {
		return errRequestBodyTooLarge
	}

	body := c.RequestCtx().RequestBodyStream()
	if body == nil {
		body = bytes.NewReader(c.Body())
	}

	payload, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(payload)) > maxBytes {
		return errRequestBodyTooLarge
	}

	return json.Unmarshal(payload, dst)
}

// filterFilesByExtension filters files based on allowed extensions.
// Returns the filtered files, or an error status code if all files were rejected.
// Folder transfers are rejected entirely when in strict routing mode
// (extension routing + extension filter both enabled).
func (fr *FileReceiver) filterFilesByExtension(files models.FileMetas, remoteIP string) (models.FileMetas, int) {
	isFolderXfer := files.IsFolderTransfer()

	// Strict mode: routing enabled + extension filter enabled
	// In this mode, reject folder transfers entirely because the user has
	// explicitly configured specific file types and routing destinations.
	if isFolderXfer && fr.hasExtensionRouter() && fr.hasExtensionFilter() {
		slog.Warn("Folder transfer rejected: strict routing mode active", "remote", remoteIP)
		return nil, 403
	}

	if !fr.hasExtensionFilter() {
		return files, 0
	}

	filteredFiles := make(models.FileMetas)
	rejectedFiles := []string{}

	for id, fileMeta := range files {
		if fr.IsExtensionAllowed(fileMeta.Filename) {
			filteredFiles[id] = fileMeta
		} else {
			rejectedFiles = append(rejectedFiles, fileMeta.Filename)
		}
	}

	// Log rejected files
	if len(rejectedFiles) > 0 {
		slog.Info("Rejected files due to extension filter", "files", rejectedFiles)
	}

	// If all files were rejected, return an error
	if len(filteredFiles) == 0 {
		slog.Warn("All files rejected by extension filter", "remote", remoteIP)
		return nil, 403
	}

	return filteredFiles, 0
}

func (fr *FileReceiver) preUploadHandler(c fiber.Ctx) error {
	// Check PIN authentication and rate limiting
	if status := fr.validatePIN(c); status != 0 {
		return c.SendStatus(status)
	}

	var metaReq models.PreUploadReq

	err := parseJSONBodyLimited(c, &metaReq, maxPreUploadBodyBytes)
	if err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			slog.Warn("Prepare upload body rejected", "remote", c.IP(), "status", fiber.StatusRequestEntityTooLarge, "error", err)
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		slog.Warn("Prepare upload body rejected", "remote", c.IP(), "status", fiber.StatusBadRequest, "error", err)
		return c.SendStatus(400)
	}
	if metaReq.Info == nil {
		slog.Warn("Prepare upload body rejected: missing info", "remote", c.IP())
		return c.SendStatus(fiber.StatusBadRequest)
	}
	if fingerprint, ok := tlsPeerFingerprint(c); ok {
		metaReq.Info.Fingerprint = fingerprint
	}
	for fileID, file := range metaReq.Files {
		if file.Size < 0 {
			slog.Warn("Prepare upload body rejected: negative file size", "remote", c.IP(), "fileId", fileID, "size", file.Size)
			return c.SendStatus(fiber.StatusBadRequest)
		}
	}

	// Filter files by extension if filter is enabled
	filteredFiles, errStatus := fr.filterFilesByExtension(metaReq.Files, c.IP())
	if errStatus != 0 {
		return c.SendStatus(errStatus)
	}
	metaReq.Files = filteredFiles

	var declaredBytes int64
	for _, file := range metaReq.Files {
		declaredBytes += file.Size
	}
	senderAlias, senderVersion, senderModel, senderType, senderProtocol := "", "", "", "", ""
	if metaReq.Info != nil {
		senderAlias = metaReq.Info.Alias
		senderVersion = metaReq.Info.Version
		senderModel = metaReq.Info.DeviceModel
		senderType = metaReq.Info.DeviceType
		senderProtocol = metaReq.Info.Protocol
	}
	slog.Info("Prepare upload",
		"remote", c.IP(),
		"sender", senderAlias,
		"senderProtocolVersion", senderVersion,
		"senderModel", senderModel,
		"senderType", senderType,
		"transport", senderProtocol,
		"files", len(metaReq.Files),
		"declaredBytes", declaredBytes,
	)

	// Atomically enforce admission rules + create session.
	// Prevents races between active-session checks and creation.
	sessionId, err := fr.sessman.CreateSessionIfAllowed(metaReq.Files, c.IP())
	if err != nil {
		if err == constants.ErrTooManySessions {
			slog.Warn("Prepare upload rejected", "remote", c.IP(), "status", fiber.StatusTooManyRequests, "error", err)
			return c.SendStatus(429)
		}
		slog.Warn("Prepare upload rejected", "remote", c.IP(), "status", constants.Status(err), "error", err)
		return c.SendStatus(constants.Status(err))
	}

	slog.Info("Accepting file", "remote", c.IP(), "session", sessionId, "files", len(metaReq.Files), "declaredBytes", declaredBytes)

	resp, err := fr.sessman.GeneratePreUploadResp(sessionId)
	if err != nil {
		return c.SendStatus(500)
	}

	return c.JSON(&resp)
}

func (fr *FileReceiver) uploadHandler(c fiber.Ctx) error {
	sessionId := c.Query("sessionId")
	fileId := c.Query("fileId")
	token := c.Query("token")

	slog.Info("Upload request", "remote", c.IP(), "session", sessionId, "fileId", fileId)

	if sessionId == "" || fileId == "" || token == "" {
		slog.Warn("Upload missing params", "session", sessionId, "fileId", fileId, "hasToken", token != "")
		return c.SendStatus(400)
	}

	session, err := fr.sessman.GetSession(sessionId)
	if err != nil {
		slog.Warn("Upload invalid session", "session", sessionId, "error", err)
		return c.SendStatus(403) // Invalid session = rejected per protocol spec
	}

	// Get file metadata for logging and routing
	fileMeta, ok := session.GetFileMeta(fileId)
	if !ok {
		slog.Warn("Upload for unknown fileId", "fileId", fileId, "session", sessionId)
		return c.SendStatus(400)
	}

	// Determine save directory (may be routed based on extension, unless folder transfer)
	saveDir := fr.GetSaveDirForSession(session, fileMeta.Filename)

	// Use streaming to avoid loading entire file into memory (prevents OOM on large files).
	// Note: RequestBodyStream() may return nil for small requests that were already buffered,
	// so we fall back to c.Body() in that case.
	var bodyReader io.Reader
	if stream := c.RequestCtx().RequestBodyStream(); stream != nil {
		bodyReader = stream
	} else {
		bodyReader = bytes.NewReader(c.Body())
	}

	// Pass client IP for validation per protocol spec Section 4.2
	savedFilename, err := session.SaveFile(saveDir, fileId, token, c.IP(), bodyReader)
	if err != nil {
		status := constants.Status(err)
		slog.Error("Upload error",
			"remote", c.IP(),
			"session", sessionId,
			"file", fileMeta.Filename,
			"declaredBytes", fileMeta.Size,
			"status", status,
			"error", err,
		)
		return c.SendStatus(status)
	}

	// Log the successful transfer with the actual saved filename (may differ from original if renamed)
	fr.LogTransfer(savedFilename, fileMeta.Size, c.IP())

	return c.SendStatus(200)
}

func (fr *FileReceiver) cancelHandler(c fiber.Ctx) error {
	sessionId := c.Query("sessionId")
	if sessionId == "" {
		return c.SendStatus(400)
	}

	if err := fr.sessman.KillSessionForClient(sessionId, c.IP()); err != nil {
		return c.SendStatus(constants.Status(err))
	}
	return c.SendStatus(200)
}

func (fr *FileReceiver) infoHandler(c fiber.Ctx) error {
	return c.JSON(&fr.identity)
}

func (fr *FileReceiver) registerHandler(c fiber.Ctx) error {
	var announcement models.Announcement
	if err := parseJSONBodyLimited(c, &announcement, maxRegisterBodyBytes); err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		return c.SendStatus(400)
	}
	if fingerprint, ok := tlsPeerFingerprint(c); ok {
		announcement.Fingerprint = fingerprint
	}

	// Register the discovered device
	announcement.IP = c.IP()
	if fr.discoverier != nil {
		fr.discoverier.RegisterDevice(announcement)
	}

	// Respond with our device info
	return c.JSON(&fr.identity)
}

func tlsPeerFingerprint(c fiber.Ctx) (string, bool) {
	state := c.RequestCtx().TLSConnectionState()
	if state == nil || len(state.PeerCertificates) == 0 {
		return "", false
	}
	return utils.SHA256ofCert(state.PeerCertificates[0]), true
}

// nonceExchangeHandler implements POST /api/localsend/v3/nonce
// This exchanges nonces for secure token verification in v3 protocol.
func (fr *FileReceiver) nonceExchangeHandler(c fiber.Ctx) error {
	var req models.NonceRequest
	if err := parseJSONBodyLimited(c, &req, maxNonceBodyBytes); err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		slog.Warn("Invalid nonce request", "error", err, "remote", c.IP())
		return c.SendStatus(400)
	}

	// Decode nonce from base64
	nonce, err := crypto.DecodeNonce(req.Nonce)
	if err != nil {
		slog.Warn("Invalid nonce format", "error", err, "remote", c.IP())
		return c.SendStatus(400)
	}

	// Validate nonce length (16-128 bytes per protocol spec)
	if !crypto.ValidateNonce(nonce) {
		slog.Warn("Invalid nonce length", "length", len(nonce), "remote", c.IP())
		return c.SendStatus(400)
	}

	// Get client identifier (IP for now, could be cert public key for HTTPS)
	clientID := c.IP()

	// Store received nonce from client
	fr.receivedNonceCache.Put(clientID, nonce)

	// Generate new nonce for client
	newNonce, err := crypto.GenerateNonce()
	if err != nil {
		slog.Error("Failed to generate nonce", "error", err)
		return c.SendStatus(500)
	}

	// Store generated nonce for later verification
	fr.generatedNonceCache.Put(clientID, newNonce)

	// Return response with base64-encoded nonce
	resp := models.NonceResponse{
		Nonce: crypto.EncodeNonce(newNonce),
	}

	slog.Info("Nonce exchange successful",
		"remote", clientID,
		"clientNonceLen", len(nonce),
		"serverNonceLen", len(newNonce))

	return c.JSON(&resp)
}

// registerV3Handler implements POST /api/localsend/v3/register
// This handles device registration with v3 protocol fields.
func (fr *FileReceiver) registerV3Handler(c fiber.Ctx) error {
	var req models.RegisterRequestV3
	if err := parseJSONBodyLimited(c, &req, maxRegisterBodyBytes); err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		slog.Error("Failed to parse v3 register request", "error", err)
		return c.SendStatus(400)
	}

	// Build response from our identity
	resp := models.RegisterResponseV3{
		Alias:           fr.identity.Alias,
		Version:         fr.identity.Version,
		DeviceModel:     fr.identity.DeviceModel,
		DeviceType:      constants.DeviceTypeToV3(fr.identity.DeviceType),
		Token:           fr.identity.Token,
		HasWebInterface: false, // CLI doesn't have web interface
	}

	slog.Info("V3 register received", "remote", c.IP(), "sender", req.Alias)

	return c.JSON(&resp)
}
