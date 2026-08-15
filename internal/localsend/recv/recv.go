package recv

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"localsend-cli/internal/localsend"
	"localsend-cli/internal/localsend/constants"
	sess "localsend-cli/internal/localsend/session"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"

	"github.com/gofiber/fiber/v3"
)

type FileReceiver struct {
	cert               tls.Certificate
	identity           models.DeviceInfo
	webServer          *fiber.App
	supportHttps       bool
	sessman            *sess.RecvSessManager
	saveToDir          string
	discoverier        *localsend.Discoverer
	discoveryMu        sync.Mutex
	expectedPin        string
	allowedExtensions  []string         // New field for extension filtering
	transferLogPath    string           // Path to transfer log file
	transferLogFile    *os.File         // Persistent file handle for transfer log
	onTransferCmd      string           // Shell command to run after each transfer
	transferNotifyPath string           // Tiny file rewritten after each completed transfer
	onTransferStart    func()           // Optional process-integration activity callback
	onTransferDone     func()           // Optional process-integration activity callback
	transferNotifySeq  uint64           // Protected by configMu
	router             *ExtensionRouter // Routes files to different dirs by extension
	listenAddr         string           // Custom listen address (defaults to constants.DefaultListenAddr)

	// configMu protects configuration fields that can be modified after creation
	// (expectedPin, allowedExtensions, transferLogPath, transferLogFile, onTransferCmd, transferNotifyPath, callbacks, router, listenAddr)
	configMu sync.RWMutex

	// PIN rate limiting (uses shared RateLimiter from utils package)
	pinRateLimiter *utils.RateLimiter

	// V3 nonce caches for token verification
	receivedNonceCache  *localsend.NonceCache // nonces received from clients
	generatedNonceCache *localsend.NonceCache // nonces generated for clients

	// cmdWg tracks running transfer command goroutines for graceful shutdown
	cmdWg    sync.WaitGroup
	cmdSlots chan struct{}
	stopped  bool
}

// PIN rate limiting constants - use shared constants from constants package
const (
	maxPINAttempts     = constants.MaxPINAttempts
	pinBlockDuration   = constants.PINBlockDuration
	pinCleanupInterval = constants.PINCleanupInterval
)

// TransferLogEntry represents a single transfer log entry
type TransferLogEntry struct {
	Timestamp string `json:"timestamp"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	Sender    string `json:"sender"`
}

func NewFileReceiver(devname string, saveToDir string, supportHttps bool) *FileReceiver {
	return &FileReceiver{
		identity:            models.NewDeviceInfo(devname, lsutils.GenFingerprint()),
		webServer:           lsutils.NewWebServer(),
		supportHttps:        supportHttps,
		saveToDir:           saveToDir,
		sessman:             sess.NewRecvSessManager(),
		allowedExtensions:   nil, // nil means accept all
		listenAddr:          constants.DefaultListenAddr,
		pinRateLimiter:      utils.NewRateLimiter(maxPINAttempts, pinBlockDuration),
		receivedNonceCache:  localsend.NewNonceCache(200),
		generatedNonceCache: localsend.NewNonceCache(200),
		cmdSlots:            make(chan struct{}, 4),
	}
}

func (fr *FileReceiver) SetPIN(pin string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.expectedPin = pin
}

// SetListenAddr sets a custom listen address (e.g., "127.0.0.1:0" for random port).
// Must be called before Start().
func (fr *FileReceiver) SetListenAddr(addr string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.listenAddr = addr
}

// ListenAddr returns the configured listen address.
func (fr *FileReceiver) ListenAddr() string {
	fr.configMu.RLock()
	defer fr.configMu.RUnlock()
	return fr.listenAddr
}

func (fr *FileReceiver) SetTransferLog(path string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()

	// Close existing file handle if any
	if fr.transferLogFile != nil {
		_ = fr.transferLogFile.Close()
		fr.transferLogFile = nil
	}

	fr.transferLogPath = path

	// Open the new log file if path is provided
	if path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			slog.Error("Failed to open transfer log", "path", path, "error", err)
			return
		}
		fr.transferLogFile = f
	}
}

// closeTransferLog closes the transfer log file handle.
func (fr *FileReceiver) closeTransferLog() {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	if fr.transferLogFile != nil {
		_ = fr.transferLogFile.Close()
		fr.transferLogFile = nil
	}
}

// SetOnTransferCmd sets a shell command to run after each file transfer.
// The command runs asynchronously to avoid blocking the transfer.
func (fr *FileReceiver) SetOnTransferCmd(cmd string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.onTransferCmd = cmd
}

// SetTransferNotifyFile configures a tiny file whose contents change after each
// successful transfer. This avoids depending on vendor-shell date(1) features.
func (fr *FileReceiver) SetTransferNotifyFile(path string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.transferNotifyPath = path
}

// SetTransferActivityCallbacks reports the lifetime of an HTTP file write.
// Callbacks must be lightweight and concurrency-safe: uploads may overlap.
func (fr *FileReceiver) SetTransferActivityCallbacks(start, done func()) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.onTransferStart = start
	fr.onTransferDone = done
}

func (fr *FileReceiver) transferActivityCallbacks() (func(), func()) {
	fr.configMu.RLock()
	defer fr.configMu.RUnlock()
	return fr.onTransferStart, fr.onTransferDone
}

func (fr *FileReceiver) LogTransfer(filename string, size int64, sender string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()

	// Always run callback, even if logging is disabled
	cmd := fr.onTransferCmd
	if fr.stopped {
		cmd = ""
	}
	if cmd != "" && !fr.stopped {
		select {
		case fr.cmdSlots <- struct{}{}:
		default:
			slog.Warn("on-transfer callback limit reached; dropping callback")
			cmd = ""
		}
	}
	if cmd != "" {
		fr.cmdWg.Add(1)
		go func() {
			defer fr.cmdWg.Done()
			defer func() { <-fr.cmdSlots }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := exec.CommandContext(ctx, "sh", "-c", cmd).Run(); err != nil {
				slog.Warn("on-transfer command failed", "cmd", cmd, "error", err)
			}
		}()
	}

	if fr.transferNotifyPath != "" {
		fr.transferNotifySeq++
		if err := os.WriteFile(fr.transferNotifyPath, []byte(fmt.Sprintf("%d\n", fr.transferNotifySeq)), 0644); err != nil {
			slog.Warn("Failed to update transfer notification file", "path", fr.transferNotifyPath, "error", err)
		}
	}

	if fr.transferLogFile == nil {
		return
	}

	// Sanitize inputs to prevent log injection
	entry := TransferLogEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Filename:  utils.SanitizeForLog(filename),
		Size:      size,
		Sender:    utils.SanitizeForLog(sender),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		slog.Error("Failed to marshal transfer log entry", "error", err)
		return
	}

	_, _ = fr.transferLogFile.Write(data)
	_, _ = fr.transferLogFile.WriteString("\n")
}

// SetAllowedExtensions sets the list of allowed file extensions.
// Extensions should be lowercase without the leading dot (e.g., "pdf", "epub").
// If empty or nil, all extensions are accepted.
func (fr *FileReceiver) SetAllowedExtensions(extensions []string) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.allowedExtensions = extensions
	if len(extensions) > 0 {
		slog.Info("File extension filter enabled", "allowed", extensions)
	}
}

// SetExtensionRouter sets the router for extension-based directory routing.
func (fr *FileReceiver) SetExtensionRouter(router *ExtensionRouter) {
	fr.configMu.Lock()
	defer fr.configMu.Unlock()
	fr.router = router
}

// GetSaveDir returns the appropriate save directory for a file.
// Uses the router if configured, otherwise falls back to the default saveToDir.
func (fr *FileReceiver) GetSaveDir(filename string) string {
	fr.configMu.RLock()
	router := fr.router
	fr.configMu.RUnlock()

	if router != nil {
		return router.GetSaveDir(filename)
	}
	return fr.saveToDir
}

// GetSaveDirForSession returns the appropriate save directory for a file,
// taking into account whether the session is a folder transfer.
// Folder transfers bypass extension routing and always use the main save dir
// to keep the folder contents together.
func (fr *FileReceiver) GetSaveDirForSession(session *sess.RecvSession, filename string) string {
	if session.IsFolderTransfer() {
		return fr.saveToDir // Folder transfers bypass routing
	}
	return fr.GetSaveDir(filename) // Individual files use routing
}

// IsExtensionAllowed checks if a filename has an allowed extension.
// Returns true if no filter is set or if the extension is in the allowed list.
func (fr *FileReceiver) IsExtensionAllowed(filename string) bool {
	fr.configMu.RLock()
	allowed := fr.allowedExtensions
	fr.configMu.RUnlock()
	return utils.IsExtensionAllowed(filename, allowed)
}

// getExpectedPIN returns the expected PIN in a thread-safe manner.
// Used by handlers to check PIN without races.
func (fr *FileReceiver) getExpectedPIN() string {
	fr.configMu.RLock()
	defer fr.configMu.RUnlock()
	return fr.expectedPin
}

// isPINBlocked returns true if the IP is currently blocked due to too many failed PIN attempts.
func (fr *FileReceiver) isPINBlocked(ip string) bool {
	return fr.pinRateLimiter.IsBlocked(ip)
}

// recordPINAttempt records a failed PIN attempt for an IP.
func (fr *FileReceiver) recordPINAttempt(ip string) {
	fr.pinRateLimiter.RecordAttempt(ip)
}

// clearPINAttempts clears failed PIN attempts for an IP (on successful auth).
func (fr *FileReceiver) clearPINAttempts(ip string) {
	fr.pinRateLimiter.Clear(ip)
}

// validatePIN checks PIN authentication and rate limiting.
// Returns HTTP status code: 0 = success, 401 = wrong PIN, 429 = rate limited.
func (fr *FileReceiver) validatePIN(c fiber.Ctx) int {
	expectedPin := fr.getExpectedPIN()
	if expectedPin == "" {
		return 0 // No PIN required
	}

	// Check if IP is blocked due to too many failed attempts
	if fr.isPINBlocked(c.IP()) {
		slog.Warn("PIN attempt blocked - too many failures", "remote", c.IP())
		return 429 // Too Many Requests
	}

	// A missing PIN is an authentication challenge, not a failed guess.
	if !c.Request().URI().QueryArgs().Has("pin") {
		return 401
	}

	pin := c.Query("pin")
	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(pin), []byte(expectedPin)) != 1 {
		fr.recordPINAttempt(c.IP())
		return 401
	}

	// Clear attempts on successful PIN
	fr.clearPINAttempts(c.IP())
	return 0
}

// hasExtensionFilter returns true if an extension filter is configured.
// Used by handlers to check if filtering is needed.
func (fr *FileReceiver) hasExtensionFilter() bool {
	fr.configMu.RLock()
	defer fr.configMu.RUnlock()
	return len(fr.allowedExtensions) > 0
}

// hasExtensionRouter returns true if an extension router is configured.
// Used by handlers to check routing mode.
func (fr *FileReceiver) hasExtensionRouter() bool {
	fr.configMu.RLock()
	defer fr.configMu.RUnlock()
	return fr.router != nil
}

func (fr *FileReceiver) Init() error {
	// ensure save directory exists
	err := os.MkdirAll(fr.saveToDir, fs.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

	if fr.supportHttps {
		// Get cert paths from the certs directory next to the binary
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

		fr.cert, err = lsutils.LoadOrGenTLScert(privkeyFile, certFile)
		if err != nil {
			return fmt.Errorf("failed to load or generate TLS certificate: %w", err)
		}

		// See https://github.com/localsend/protocol section. 2
		fr.identity.Fingerprint, err = lsutils.CertificateFingerprint(fr.cert)
		if err != nil {
			return fmt.Errorf("failed to fingerprint TLS certificate: %w", err)
		}
	}

	// Create the initial discovery instance. Failure is non-fatal: the runtime
	// supervisor below retries when a network interface becomes available.
	discoverer, discoveryErr := localsend.NewDiscovererWithCertificate(fr.identity, fr.supportHttps, fr.cert)
	if discoveryErr != nil {
		slog.Warn("Failed to create discoverer; will retry", "error", discoveryErr)
	} else {
		fr.discoveryMu.Lock()
		fr.discoverier = discoverer
		fr.discoveryMu.Unlock()
	}

	// start session cleanup task
	fr.sessman.Start()

	return nil
}

func (fr *FileReceiver) Start(ctx context.Context) error {
	server := fr.webServer
	fr.registerRoutes(server)

	slog.Info("Waiting for files (Ctrl-C to terminate)")

	// Start PIN attempts cleanup goroutine (prevents unbounded memory growth)
	go fr.pinCleanupTask(ctx)

	// Start discovery/advertisement (with retry if network wasn't available at init)
	go fr.startDiscoveryWithRetry(ctx)

	// Listen for context cancellation to trigger graceful shutdown
	go func() {
		<-ctx.Done()
		_ = fr.Stop()
	}()

	return lsutils.ListenWithMutualTLS(fr.webServer, fr.listenAddr, fr.cert, fr.supportHttps)
}

func (fr *FileReceiver) registerRoutes(server *fiber.App) {

	// V2 routes
	server.Post(constants.PreuploadPath, fr.preUploadHandler)
	server.Post(constants.UploadPath, fr.uploadHandler)
	server.Post(constants.CancelPath, fr.cancelHandler)
	server.Get(constants.InfoPath, fr.infoHandler)
	server.Get(constants.InfoPathV1, fr.infoHandler)
	server.Post(constants.RegisterPath, fr.registerHandler)
	server.Post(constants.RegisterPathV1, fr.registerHandler)

	// Dormant official v3 HTTP scaffolding. The pinned 1.18.x server exposes
	// only nonce and register; LocalSend Web uses signaling + WebRTC instead.
	server.Post(constants.NoncePathV3, fr.nonceExchangeHandler)
	server.Post(constants.RegisterPathV3, fr.registerV3Handler)
}

// currentDiscoverer returns the currently owned discovery instance.
func (fr *FileReceiver) currentDiscoverer() *localsend.Discoverer {
	fr.discoveryMu.Lock()
	defer fr.discoveryMu.Unlock()
	return fr.discoverier
}

func (fr *FileReceiver) installDiscoverer(discoverer *localsend.Discoverer) {
	fr.discoveryMu.Lock()
	fr.discoverier = discoverer
	fr.discoveryMu.Unlock()
}

func (fr *FileReceiver) clearDiscoverer(discoverer *localsend.Discoverer) {
	fr.discoveryMu.Lock()
	if fr.discoverier == discoverer {
		fr.discoverier = nil
	}
	fr.discoveryMu.Unlock()
}

func waitDiscoveryRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// startDiscoveryWithRetry owns the native multicast discovery lifecycle. A
// socket can become permanently unusable after a Wi-Fi/VPN/interface change;
// when Listen returns unexpectedly, discard the instance and bind a fresh set
// of sockets instead of leaving the HTTP receiver alive but undiscoverable.
func (fr *FileReceiver) startDiscoveryWithRetry(ctx context.Context) {
	const retryDelay = 2 * time.Second

	for ctx.Err() == nil {
		discoverer := fr.currentDiscoverer()
		if discoverer == nil {
			var err error
			discoverer, err = localsend.NewDiscovererWithCertificate(fr.identity, fr.supportHttps, fr.cert)
			if err != nil {
				slog.Debug("Discovery bind unavailable; retrying", "error", err)
				if !waitDiscoveryRetry(ctx, retryDelay) {
					return
				}
				continue
			}
			fr.installDiscoverer(discoverer)
			slog.Info("Discovery bound to current network interfaces")
		}

		stopShutdown := context.AfterFunc(ctx, func() { _ = discoverer.Shutdown() })
		err := discoverer.Listen()
		_ = stopShutdown()
		fr.clearDiscoverer(discoverer)
		_ = discoverer.Shutdown()

		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("Discovery listener stopped; rebinding", "error", err)
		} else {
			slog.Warn("Discovery listener stopped unexpectedly; rebinding")
		}
		if !waitDiscoveryRetry(ctx, retryDelay) {
			return
		}
	}
}

func (fr *FileReceiver) Stop() error {
	slog.Info("Stop receiving")
	fr.configMu.Lock()
	fr.stopped = true
	fr.configMu.Unlock()

	fr.sessman.Stop()
	fr.discoveryMu.Lock()
	discoverer := fr.discoverier
	fr.discoveryMu.Unlock()
	if discoverer != nil {
		_ = discoverer.Shutdown()
	}
	fr.closeTransferLog()

	// Wait for transfer command goroutines to finish (with timeout)
	done := make(chan struct{})
	go func() {
		fr.cmdWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All commands finished
	case <-time.After(5 * time.Second):
		slog.Warn("Transfer commands still running at shutdown")
	}

	// Graceful shutdown with 5 second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return fr.webServer.ShutdownWithContext(ctx)
}

// pinCleanupTask periodically removes expired PIN attempt entries.
// This prevents unbounded memory growth from attackers using rotating IPs.
func (fr *FileReceiver) pinCleanupTask(ctx context.Context) {
	ticker := time.NewTicker(pinCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fr.cleanupExpiredPINAttempts()
		}
	}
}

// cleanupExpiredPINAttempts removes PIN attempt entries that have expired.
// This includes both blocked IPs whose block has expired and stale partial-attempt
// entries from IPs that haven't made attempts recently (handles rotating IP attacks).
func (fr *FileReceiver) cleanupExpiredPINAttempts() {
	fr.pinRateLimiter.CleanupExpired()
}
