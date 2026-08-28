package send

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
)

type ForwardSender struct {
	baseSender
	local             *models.DeviceInfo
	remote            *models.DeviceInfo
	remotePort        string // Custom port (defaults to constants.DefaultPortStr)
	https             bool
	abort             atomic.Bool
	httpClient        *fasthttp.Client
	uploadClient      *fasthttp.Client
	activeUploadMu    sync.Mutex
	activeUploadConns map[*trackedUploadConn]struct{}
}

type trackedUploadConn struct {
	net.Conn
	owner                    *ForwardSender
	once                     sync.Once
	lastReadDeadlineRefresh  atomic.Int64
	lastWriteDeadlineRefresh atomic.Int64
	externalReadDeadline     atomic.Bool
	externalWriteDeadline    atomic.Bool
}

func (c *trackedUploadConn) Close() error {
	c.once.Do(func() {
		c.owner.activeUploadMu.Lock()
		delete(c.owner.activeUploadConns, c)
		c.owner.activeUploadMu.Unlock()
	})
	return c.Conn.Close()
}

func (c *trackedUploadConn) refreshReadDeadline() error {
	if c.externalReadDeadline.Load() {
		return nil
	}
	now := time.Now()
	last := c.lastReadDeadlineRefresh.Load()
	if last != 0 && now.UnixNano()-last < int64(uploadDeadlineRefresh) {
		return nil
	}
	if err := c.Conn.SetReadDeadline(now.Add(uploadIdleTimeout)); err != nil {
		return err
	}
	c.lastReadDeadlineRefresh.Store(now.UnixNano())
	return nil
}

func (c *trackedUploadConn) refreshWriteDeadline() error {
	if c.externalWriteDeadline.Load() {
		return nil
	}
	now := time.Now()
	last := c.lastWriteDeadlineRefresh.Load()
	if last != 0 && now.UnixNano()-last < int64(uploadDeadlineRefresh) {
		return nil
	}
	if err := c.Conn.SetWriteDeadline(now.Add(uploadIdleTimeout)); err != nil {
		return err
	}
	c.lastWriteDeadlineRefresh.Store(now.UnixNano())
	return nil
}

func (c *trackedUploadConn) Read(p []byte) (int, error) {
	if err := c.refreshReadDeadline(); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c *trackedUploadConn) Write(p []byte) (int, error) {
	if err := c.refreshWriteDeadline(); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

// fasthttp deliberately clears deadlines when no total request timeout is set.
// Preserve that API contract, but make the next actual I/O operation re-arm our
// independent sliding idle deadline.
func (c *trackedUploadConn) SetDeadline(deadline time.Time) error {
	external := !deadline.IsZero()
	c.externalReadDeadline.Store(external)
	c.externalWriteDeadline.Store(external)
	if !external {
		c.lastReadDeadlineRefresh.Store(0)
		c.lastWriteDeadlineRefresh.Store(0)
	}
	return c.Conn.SetDeadline(deadline)
}

func (c *trackedUploadConn) SetReadDeadline(deadline time.Time) error {
	external := !deadline.IsZero()
	c.externalReadDeadline.Store(external)
	if !external {
		c.lastReadDeadlineRefresh.Store(0)
	}
	return c.Conn.SetReadDeadline(deadline)
}

func (c *trackedUploadConn) SetWriteDeadline(deadline time.Time) error {
	external := !deadline.IsZero()
	c.externalWriteDeadline.Store(external)
	if !external {
		c.lastWriteDeadlineRefresh.Store(0)
	}
	return c.Conn.SetWriteDeadline(deadline)
}

const (
	requestTimeout             = 30 * time.Second
	maxControlResponseBytes    = 1 << 20
	maxUploadAttempts          = 3
	uploadConcurrency          = 2
	httpsUploadWriteBufferSize = 64 * 1024
	uploadIdleTimeout          = 2 * time.Minute
	uploadDeadlineRefresh      = 10 * time.Second
)

func NewForwardSender() *ForwardSender {
	return &ForwardSender{
		baseSender: baseSender{
			files:  make(map[string]models.FileMeta),
			tokens: make(map[string]string),
		},
		remotePort:        constants.DefaultPortStr,
		httpClient:        &fasthttp.Client{MaxResponseBodySize: maxControlResponseBytes},
		uploadClient:      newUploadClient(nil, false),
		activeUploadConns: make(map[*trackedUploadConn]struct{}),
	}
}

func newUploadClient(tlsConfig *tls.Config, https bool) *fasthttp.Client {
	client := &fasthttp.Client{
		MaxResponseBodySize: maxControlResponseBytes,
		TLSConfig:           tlsConfig,
	}
	if https {
		// fasthttp defaults to a 4 KiB connection write buffer. TLS cannot use
		// the plain file->TCP sendfile path, and a 64 KiB buffer removes most of
		// that per-record/write overhead without spending 512 KiB per connection.
		client.WriteBufferSize = httpsUploadWriteBufferSize
	}
	return client
}

func (fsp *ForwardSender) trackUploadDial(addr string, timeout time.Duration) (net.Conn, error) {
	var conn net.Conn
	var err error
	if timeout > 0 {
		conn, err = fasthttp.DialTimeout(addr, timeout)
	} else {
		conn, err = fasthttp.Dial(addr)
	}
	if err != nil {
		return nil, err
	}
	tracked := &trackedUploadConn{Conn: conn, owner: fsp}
	fsp.activeUploadMu.Lock()
	if fsp.activeUploadConns == nil {
		fsp.activeUploadConns = make(map[*trackedUploadConn]struct{})
	}
	if fsp.abort.Load() {
		fsp.activeUploadMu.Unlock()
		_ = conn.Close()
		return nil, fmt.Errorf("upload canceled")
	}
	fsp.activeUploadConns[tracked] = struct{}{}
	fsp.activeUploadMu.Unlock()
	return tracked, nil
}

func (fsp *ForwardSender) closeActiveUploads() {
	fsp.activeUploadMu.Lock()
	conns := make([]*trackedUploadConn, 0, len(fsp.activeUploadConns))
	for conn := range fsp.activeUploadConns {
		conns = append(conns, conn)
	}
	fsp.activeUploadMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (fsp *ForwardSender) Init(target *models.DeviceInfo, https bool) error {
	// Reinitializing a sender must not retain idle/active transport sockets from
	// a previous target or transfer.
	fsp.closeActiveUploads()
	fsp.abort.Store(false)
	fsp.session = ""
	fsp.activeUploadMu.Lock()
	fsp.activeUploadConns = make(map[*trackedUploadConn]struct{})
	fsp.activeUploadMu.Unlock()
	fsp.remote = target
	fsp.https = https

	// Create local device identity for sender
	// Use custom alias if set, otherwise generate random alias
	alias := fsp.alias
	if alias == "" {
		alias = lsutils.GenAlias()
	}
	fingerprint := lsutils.GenFingerprint()
	var clientTLSConfig *tls.Config
	if https {
		privateKeyFile, certFile, err := lsutils.GetCertPaths()
		if err != nil {
			return fmt.Errorf("failed to get sender certificate paths: %w", err)
		}
		cert, err := lsutils.LoadOrGenTLScert(privateKeyFile, certFile)
		if err != nil {
			return fmt.Errorf("failed to load or generate sender TLS certificate: %w", err)
		}
		fingerprint, err = lsutils.CertificateFingerprint(cert)
		if err != nil {
			return err
		}
		clientTLSConfig = lsutils.TLSClientConfig(cert, target.Fingerprint)
	}
	localInfo := models.NewDeviceInfo(alias, fingerprint)
	fsp.local = &localInfo
	fsp.httpClient = &fasthttp.Client{MaxResponseBodySize: maxControlResponseBytes, TLSConfig: clientTLSConfig}
	fsp.uploadClient = newUploadClient(clientTLSConfig, https)
	fsp.uploadClient.DialTimeout = fsp.trackUploadDial

	fsp.reset()

	return nil
}

// SetRemotePort sets a custom port for the remote receiver.
// Must be called after Init() if you want to override the default port.
func (fsp *ForwardSender) SetRemotePort(port string) {
	fsp.remotePort = port
}

func (fsp *ForwardSender) preUploadReq() error {
	// Build request with SenderInfo per protocol spec Section 4.1
	protocol := "http"
	if fsp.https {
		protocol = "https"
	}
	var meta models.PreUploadReq
	meta.Info = &models.SenderInfo{
		DeviceInfo: *fsp.local,
		Port:       constants.DefaultPort,
		Protocol:   protocol,
	}
	meta.Files = fsp.files

	// setup request
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	fsp.prepareUri(req, constants.PreuploadPath)
	req.Header.SetMethod(fiber.MethodPost)
	if fsp.pin != "" {
		req.URI().QueryArgs().Add("pin", fsp.pin)
	}
	body, err := json.Marshal(&meta)
	if err != nil {
		return fmt.Errorf("failed to encode pre-upload request: %w", err)
	}
	req.Header.SetContentType(fiber.MIMEApplicationJSON)
	req.SetBody(body)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	if err := fsp.httpClient.DoTimeout(req, resp, requestTimeout); err != nil {
		return fmt.Errorf("pre-upload request failed: %w", err)
	}
	status, b := resp.StatusCode(), resp.Body()

	// parse error from http status
	err = constants.ParseError(status)
	if err != nil {
		return err
	}

	// decode response bytes
	var respMeta models.PreUploadResp
	err = json.Unmarshal(b, &respMeta)
	if err != nil {
		return fmt.Errorf("failed to decode pre-upload response: %w", err)
	}

	fsp.session = respMeta.SessionId
	fsp.tokens = respMeta.Tokens

	return nil
}

func (fsp *ForwardSender) sendFile(fid string, ftoken string) error {
	if fsp.abort.Load() {
		return nil
	}

	fmeta, ok := fsp.files[fid]
	if !ok {
		return constants.ErrUnknown // unlikely, but check it anyway
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	response := fasthttp.AcquireResponse()
	response.SkipBody = true
	defer fasthttp.ReleaseResponse(response)

	// prepare request
	fsp.prepareUri(req, constants.UploadPath)
	req.Header.SetMethod(fiber.MethodPost)
	req.URI().QueryArgs().Add("token", ftoken)
	req.URI().QueryArgs().Add("sessionId", fsp.session)
	req.URI().QueryArgs().Add("fileId", fid)
	// open file
	fd, err := os.Open(fmeta.FullPath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", fmeta.Filename, err)
	}
	defer func() { _ = fd.Close() }()

	// Guard against integer overflow on 32-bit systems where int is 32-bit.
	// On 64-bit systems, math.MaxInt is large enough that this check passes.
	// On 32-bit systems, files larger than ~2.15GB would overflow.
	if fmeta.Size > math.MaxInt {
		return fmt.Errorf("file too large for transfer on this system: %s (%d bytes exceeds %d limit)", fmeta.Filename, fmeta.Size, math.MaxInt)
	}

	// send file. No total deadline: a slow receiver or large transfer may
	// legitimately exceed 30s. Control requests remain bounded; the body
	// stream itself is governed by the OS connection.
	req.SetBodyStream(fd, int(fmeta.Size))
	if err := fsp.uploadClient.Do(req, response); err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	return constants.ParseError(response.StatusCode())
}

func (fsp *ForwardSender) sendFileWithRetry(fid string, ftoken string) error {
	var err error
	for attempt := 0; attempt < maxUploadAttempts; attempt++ {
		err = fsp.sendFile(fid, ftoken)
		if !errors.Is(err, constants.ErrChecksum) {
			return err
		}
	}
	return err
}

func (fsp *ForwardSender) Start() error {
	err := fsp.preUploadReq()
	if err != nil {
		return fmt.Errorf("pre-upload failed: %w", err)
	}

	// Match LocalSend 1.18's native HTTP scheduler: upload up to two files in
	// parallel. The protocol treats each file upload as an independent request
	// within the prepared session, so this changes scheduling, not wire format.
	type uploadJob struct {
		id    string
		token string
	}
	jobs := make(chan uploadJob)
	var wg sync.WaitGroup
	var errsMu sync.Mutex
	errs := make([]error, 0)
	workers := uploadConcurrency
	if len(fsp.tokens) < workers {
		workers = len(fsp.tokens)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := fsp.sendFileWithRetry(job.id, job.token); err != nil {
					slog.Error("Fail to send file", "error", err, "fileId", job.id)
					errsMu.Lock()
					errs = append(errs, fmt.Errorf("file %s: %w", job.id, err))
					errsMu.Unlock()
				}
			}
		}()
	}
	for fid, ftoken := range fsp.tokens {
		jobs <- uploadJob{id: fid, token: ftoken}
	}
	close(jobs)
	wg.Wait()

	if len(errs) > 0 {
		// Release the receiver's session so it can accept new transfers
		// instead of waiting for the session to expire. Best effort: a
		// cancel failure is logged, not fatal, since we are already
		// returning an error.
		if err := fsp.Cancel(); err != nil {
			slog.Warn("Failed to cancel remote session after send failure", "error", err)
		}
		return fmt.Errorf("failed to send %d file(s): %w", len(errs), errs[0])
	}

	return nil
}

func (fsp *ForwardSender) Cancel() error {
	// Local cancellation is immediate and independent of the protocol-level
	// cancel request. Closing the tracked transport connections interrupts any
	// in-flight streaming uploads without imposing a deadline on healthy ones.
	fsp.abort.Store(true)
	fsp.closeActiveUploads()

	if fsp.session == "" {
		return nil
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	response := fasthttp.AcquireResponse()
	response.SkipBody = true
	defer fasthttp.ReleaseResponse(response)

	fsp.prepareUri(req, constants.CancelPath)
	req.Header.SetMethod(fiber.MethodPost)
	req.URI().QueryArgs().Add("sessionId", fsp.session)
	if err := fsp.httpClient.DoTimeout(req, response, requestTimeout); err != nil {
		return err
	}
	return constants.ParseError(response.StatusCode())
}

func (fsp *ForwardSender) prepareUri(req *fasthttp.Request, path string) {
	remoteAddr := net.JoinHostPort(fsp.remote.IP, fsp.remotePort)

	req.Header.SetUserAgent("localsend-cli")
	req.URI().SetPath(path)
	if fsp.https {
		req.URI().SetScheme("https")
	} else {
		req.URI().SetScheme("http")
	}
	req.URI().SetHost(remoteAddr)
}
