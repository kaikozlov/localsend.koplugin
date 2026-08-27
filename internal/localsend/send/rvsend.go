package send

import (
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
	"localsend-cli/templates"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type DownloadEntry struct {
	Filename string
	Url      string
}

type ReverseSender struct {
	baseSender
	local     *models.DeviceInfo
	webServer *fiber.App
	downloads []DownloadEntry
	https     bool
	cert      tls.Certificate
	// PIN rate limiting uses the same policy as the ordinary receiver.
	pinRateLimiter *utils.RateLimiter
	sessionMu      sync.Mutex
	sessions       map[string]string
	sessionByIP    map[string]string
}

func NewReverseSender() *ReverseSender {
	return &ReverseSender{
		baseSender: baseSender{
			tokens: make(map[string]string),
			files:  make(map[string]models.FileMeta),
		},
		webServer:      lsutils.NewWebServer(true),
		downloads:      make([]DownloadEntry, 0),
		pinRateLimiter: utils.NewRateLimiter(constants.MaxPINAttempts, constants.PINBlockDuration),
		sessions:       make(map[string]string),
		sessionByIP:    make(map[string]string),
	}
}

func (rs *ReverseSender) Init(target *models.DeviceInfo, https bool) error {
	rs.local = target
	rs.https = https
	rs.pinRateLimiter = utils.NewRateLimiter(constants.MaxPINAttempts, constants.PINBlockDuration)
	rs.sessionMu.Lock()
	rs.sessions = make(map[string]string)
	rs.sessionByIP = make(map[string]string)
	rs.sessionMu.Unlock()

	// The reverse sender IS the download API, so set Download to true
	rs.local.Download = true

	if https {
		privkeyFile, certFile, err := lsutils.GetCertPaths()
		if err != nil {
			return fmt.Errorf("failed to get certificate paths: %w", err)
		}

		// Check if certs already exist
		_, keyErr := os.Stat(privkeyFile)
		_, certErr := os.Stat(certFile)
		if keyErr == nil && certErr == nil {
			slog.Info("Loading https certificate")
		} else {
			slog.Info("Generating https certificate")
		}

		cert, err := lsutils.LoadOrGenTLScert(privkeyFile, certFile)
		if err != nil {
			return err
		}
		rs.cert = cert
		rs.local.Fingerprint = utils.SHA256ofCert(cert.Leaf)
	}

	rs.reset()

	return nil
}

func (rs *ReverseSender) predownloadHandler(c fiber.Ctx) error {
	clientIP := c.IP()
	if sessionID := c.Query("sessionId"); sessionID != "" && rs.validSession(sessionID, clientIP) {
		return c.JSON(&models.PreDownloadResp{
			SessionId: sessionID,
			Files:     rs.files,
			Info:      rs.local,
		})
	}

	if status := rs.validatePIN(c); status != 0 {
		return c.SendStatus(status)
	}

	var resp models.PreDownloadResp
	resp.SessionId = rs.createSession(clientIP)
	resp.Files = rs.files
	resp.Info = rs.local

	return c.JSON(&resp)
}

func (rs *ReverseSender) downloadHandler(c fiber.Ctx) error {
	sessionId := c.Query("sessionId")
	fileId := c.Query("fileId")

	if sessionId == "" || fileId == "" {
		return c.SendStatus(400)
	}

	if !rs.validSession(sessionId, c.IP()) {
		return c.SendStatus(403)
	}

	fileMeta, exist := rs.files[fileId]
	if !exist {
		return c.SendStatus(403)
	}

	// Set Content-Disposition header BEFORE sending file
	// Use mime.FormatMediaType to properly encode the filename (RFC 5987)
	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": fileMeta.Filename,
	})
	c.Set(fiber.HeaderContentDisposition, disposition)

	err := c.SendFile(fileMeta.FullPath)
	if err != nil {
		slog.Info("Fail to send file", "file", fileMeta.Filename)
		return c.SendStatus(500)
	}

	slog.Info("File sent", "file", fileMeta.Filename, "recv", c.IP())
	return nil
}

func (rs *ReverseSender) downloadListHandler(c fiber.Ctx) error {
	if status := rs.validatePIN(c); status != 0 {
		return c.SendStatus(status)
	}
	sessionID := rs.createSession(c.IP())
	downloads := make([]DownloadEntry, len(rs.downloads))
	for i, entry := range rs.downloads {
		entry.Url += "&sessionId=" + url.QueryEscape(sessionID)
		downloads[i] = entry
	}
	return c.Render(templates.DownloadListTemp, fiber.Map{"Files": downloads})
}

func (rs *ReverseSender) infoHandler(c fiber.Ctx) error {
	return c.JSON(rs.local)
}

func (rs *ReverseSender) createSession(clientIP string) string {
	rs.sessionMu.Lock()
	defer rs.sessionMu.Unlock()

	if oldSessionID := rs.sessionByIP[clientIP]; oldSessionID != "" {
		delete(rs.sessions, oldSessionID)
	}
	sessionID := uuid.NewString()
	rs.sessions[sessionID] = clientIP
	rs.sessionByIP[clientIP] = sessionID
	return sessionID
}

func (rs *ReverseSender) validSession(sessionID, clientIP string) bool {
	rs.sessionMu.Lock()
	defer rs.sessionMu.Unlock()
	return rs.sessions[sessionID] == clientIP
}

// validatePIN returns 0 on success, 401 for an incorrect PIN, or 429 while the
// caller is blocked. A successful prepare-download exchanges the PIN for a
// session bound to the caller's IP address.
func (rs *ReverseSender) validatePIN(c fiber.Ctx) int {
	if rs.pin == "" {
		return 0
	}

	clientIP := c.IP()
	if rs.pinRateLimiter.IsBlocked(clientIP) {
		return fiber.StatusTooManyRequests
	}
	// A missing PIN is an authentication challenge, not a failed guess.
	if !c.Request().URI().QueryArgs().Has("pin") {
		return fiber.StatusUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(c.Query("pin")), []byte(rs.pin)) != 1 {
		rs.pinRateLimiter.RecordAttempt(clientIP)
		return fiber.StatusUnauthorized
	}

	rs.pinRateLimiter.Clear(clientIP)
	return 0
}

func (rs *ReverseSender) pinCleanupTask(stop <-chan struct{}) {
	ticker := time.NewTicker(constants.PINCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			rs.pinRateLimiter.CleanupExpired()
		}
	}
}

func (rs *ReverseSender) registerRoutes(server *fiber.App) {
	server.Post(constants.PreDownloadPath, rs.predownloadHandler)
	server.Get(constants.DownloadPath, rs.downloadHandler)
	server.Get(constants.InfoPath, rs.infoHandler)
	server.Get(constants.InfoPathV1, rs.infoHandler)
	server.Get("/", rs.downloadListHandler)
}

func (rs *ReverseSender) Start() error {
	server := rs.webServer
	rs.registerRoutes(server)
	stopPINCleanup := make(chan struct{})
	go rs.pinCleanupTask(stopPINCleanup)
	defer close(stopPINCleanup)

	ip, err := utils.GetMyIPv4Addr()
	if err != nil {
		return err
	}

	scheme := utils.GetProtocolScheme(rs.https)

	slog.Info("Start reverse sending server", "https", rs.https)

	// Keep file links relative so the browser downloads through the same server
	// address it used to authenticate. Crossing to another local interface can
	// change the client's source IP and invalidate the IP-bound session.
	for fileId, fileMeta := range rs.files {
		rs.downloads = append(rs.downloads, DownloadEntry{
			Filename: fileMeta.Filename,
			Url:      fmt.Sprintf("%s?fileId=%s", constants.DownloadPath, url.QueryEscape(fileId)),
		})
	}

	for idx := range ip {
		host := net.JoinHostPort(ip[idx].String(), constants.DefaultPortStr)
		_, _ = fmt.Fprintf(os.Stdout, "Visit %s://%s to download files\n", scheme, host)
	}

	return lsutils.ListenWithTLS(server, constants.DefaultListenAddr, rs.cert, rs.https)
}

func (rs *ReverseSender) Cancel() error {
	slog.Info("Shutdown reverse sending server")
	return rs.webServer.Shutdown()
}
