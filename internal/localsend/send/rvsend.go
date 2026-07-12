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

	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
	"localsend-cli/templates"

	"github.com/gofiber/fiber/v2"
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
}

func NewReverseSender() *ReverseSender {
	return &ReverseSender{
		baseSender: baseSender{
			tokens: make(map[string]string),
			files:  make(map[string]models.FileMeta),
		},
		webServer: lsutils.NewWebServer(true),
		downloads: make([]DownloadEntry, 0),
	}
}

func (rs *ReverseSender) Init(target *models.DeviceInfo, https bool) error {
	rs.local = target
	rs.session = uuid.NewString()
	rs.https = https

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

func (rs *ReverseSender) predownloadHandler(c *fiber.Ctx) error {
	// Check PIN if set (constant-time comparison to prevent timing attacks)
	if rs.pin != "" {
		pin := c.Query("pin")
		if subtle.ConstantTimeCompare([]byte(pin), []byte(rs.pin)) != 1 {
			return c.SendStatus(401)
		}
	}

	// Support session refresh - if sessionId provided, validate it matches
	// Use constant-time comparison to prevent timing attacks on session IDs
	sessionId := c.Query("sessionId")
	if sessionId != "" && subtle.ConstantTimeCompare([]byte(sessionId), []byte(rs.session)) != 1 {
		return c.SendStatus(403)
	}

	var resp models.PreDownloadResp
	resp.SessionId = rs.session
	resp.Files = rs.files
	resp.Info = rs.local

	return c.JSON(&resp)
}

func (rs *ReverseSender) downloadHandler(c *fiber.Ctx) error {
	if rs.pin != "" && subtle.ConstantTimeCompare([]byte(c.Query("pin")), []byte(rs.pin)) != 1 {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	sessionId := c.Query("sessionId")
	fileId := c.Query("fileId")

	if sessionId == "" || fileId == "" {
		return c.SendStatus(400)
	}

	// Use constant-time comparison to prevent timing attacks on session IDs
	if subtle.ConstantTimeCompare([]byte(sessionId), []byte(rs.session)) != 1 {
		return c.SendStatus(403)
	}

	fileMeta, exist := rs.files[fileId]
	if !exist {
		return c.SendStatus(404)
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

func (rs *ReverseSender) downloadListHandler(c *fiber.Ctx) error {
	pin := c.Query("pin")
	if rs.pin != "" && subtle.ConstantTimeCompare([]byte(pin), []byte(rs.pin)) != 1 {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	downloads := rs.downloads
	if pin != "" {
		downloads = make([]DownloadEntry, len(rs.downloads))
		for i, entry := range rs.downloads {
			entry.Url += "&pin=" + url.QueryEscape(pin)
			downloads[i] = entry
		}
	}
	return c.Render(templates.DownloadListTemp, fiber.Map{"Files": downloads})
}

func (rs *ReverseSender) Start() error {
	server := rs.webServer
	server.Post(constants.PreDownloadPath, rs.predownloadHandler)
	server.Get(constants.DownloadPath, rs.downloadHandler)
	server.Get("/", rs.downloadListHandler)

	ip, err := utils.GetMyIPv4Addr()
	if err != nil {
		return err
	}

	scheme := utils.GetProtocolScheme(rs.https)

	slog.Info("Start reverse sending server", "https", rs.https)

	// build downloads list
	for idx := range ip {
		host := net.JoinHostPort(ip[idx].String(), constants.DefaultPortStr)

		for fileId, fileMeta := range rs.files {
			rs.downloads = append(rs.downloads, DownloadEntry{
				Filename: fileMeta.Filename,
				Url: fmt.Sprintf("%s://%s%s?sessionId=%s&fileId=%s",
					scheme, host, constants.DownloadPath, rs.session, fileId),
			})
		}

		_, _ = fmt.Fprintf(os.Stdout, "Visit %s://%s to download files\n", scheme, host)
	}

	return lsutils.ListenWithTLS(server, constants.DefaultListenAddr, rs.cert, rs.https)
}

func (rs *ReverseSender) Cancel() error {
	slog.Info("Shutdown reverse sending server")
	return rs.webServer.Shutdown()
}
