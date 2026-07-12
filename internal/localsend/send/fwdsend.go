package send

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

type ForwardSender struct {
	baseSender
	local      *models.DeviceInfo
	remote     *models.DeviceInfo
	remotePort string // Custom port (defaults to constants.DefaultPortStr)
	https      bool
	abort      atomic.Bool
}

const (
	requestTimeout          = 30 * time.Second
	maxControlResponseBytes = 1 << 20
)

func NewForwardSender() *ForwardSender {
	return &ForwardSender{
		baseSender: baseSender{
			files:  make(map[string]models.FileMeta),
			tokens: make(map[string]string),
		},
		remotePort: constants.DefaultPortStr,
	}
}

func (fsp *ForwardSender) Init(target *models.DeviceInfo, https bool) error {
	fsp.abort.Store(false)
	fsp.session = ""
	fsp.remote = target
	fsp.https = https

	// Create local device identity for sender
	// Use custom alias if set, otherwise generate random alias
	alias := fsp.alias
	if alias == "" {
		alias = lsutils.GenAlias()
	}
	localInfo := models.NewDeviceInfo(alias, lsutils.GenFingerprint())
	fsp.local = &localInfo

	fsp.reset()

	return nil
}

// SetRemotePort sets a custom port for the remote receiver.
// Must be called after Init() if you want to override the default port.
func (fsp *ForwardSender) SetRemotePort(port string) {
	fsp.remotePort = port
}

func (fsp *ForwardSender) preUploadReq() error {
	if fsp.https {
		// check fingerprint if https mode (See https://github.com/localsend/protocol section.2)
		certs, err := utils.FetchX509Cert(net.JoinHostPort(fsp.remote.IP, fsp.remotePort))
		if err != nil {
			return fmt.Errorf("failed to fetch certificate: %w", err)
		}
		if len(certs) == 0 {
			return fmt.Errorf("no certificates returned from server")
		}
		fingerprint := utils.SHA256ofCert(certs[0]) // only check the first cert
		if fingerprint != fsp.remote.Fingerprint {
			return constants.ErrFingerprint
		}
	}

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
	client := &fasthttp.Client{
		MaxResponseBodySize: maxControlResponseBytes,
		TLSConfig:           &tls.Config{InsecureSkipVerify: true},
	}
	if err := client.DoTimeout(req, resp, requestTimeout); err != nil {
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

	agent := fiber.AcquireAgent()
	defer fiber.ReleaseAgent(agent)
	response := fasthttp.AcquireResponse()
	response.SkipBody = true
	defer fasthttp.ReleaseResponse(response)
	agent.SetResponse(response)

	// prepare request
	req := agent.Request()
	fsp.prepareUri(req, constants.UploadPath)
	req.Header.SetMethod(fiber.MethodPost)
	req.URI().QueryArgs().Add("token", ftoken)
	req.URI().QueryArgs().Add("sessionId", fsp.session)
	req.URI().QueryArgs().Add("fileId", fid)
	err := agent.Parse()
	if err != nil {
		return fmt.Errorf("failed to parse upload request: %w", err)
	}

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

	// send file
	status, _, errs := agent.InsecureSkipVerify().Timeout(requestTimeout).BodyStream(fd, int(fmeta.Size)).Bytes()
	if len(errs) != 0 {
		return fmt.Errorf("failed to upload file: %w", errs[0])
	}

	return constants.ParseError(status)
}

func (fsp *ForwardSender) Start() error {
	err := fsp.preUploadReq()
	if err != nil {
		return fmt.Errorf("pre-upload failed: %w", err)
	}

	// Collect and return errors instead of just logging
	var errs []error
	for fid, ftoken := range fsp.tokens {
		err := fsp.sendFile(fid, ftoken)
		if err != nil {
			slog.Error("Fail to send file", "error", err, "fileId", fid)
			errs = append(errs, fmt.Errorf("file %s: %w", fid, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to send %d file(s): %w", len(errs), errs[0])
	}

	return nil
}

func (fsp *ForwardSender) Cancel() error {
	agent := fiber.AcquireAgent().InsecureSkipVerify()
	response := fasthttp.AcquireResponse()
	response.SkipBody = true
	agent.SetResponse(response)
	defer func() {
		fsp.abort.Store(true)
		fiber.ReleaseAgent(agent)
		fasthttp.ReleaseResponse(response)
	}()

	// prepare request
	req := agent.Request()
	fsp.prepareUri(req, constants.CancelPath)
	req.Header.SetMethod(fiber.MethodPost)
	req.URI().QueryArgs().Add("sessionId", fsp.session)
	err := agent.Parse()
	if err != nil {
		return err
	}

	// make request
	status, _, errs := agent.Timeout(requestTimeout).Bytes()
	if len(errs) != 0 {
		return errs[0]
	}

	return constants.ParseError(status)
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
