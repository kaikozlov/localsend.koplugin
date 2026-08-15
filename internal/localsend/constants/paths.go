package constants

import "strings"

const (
	// DefaultPort is the standard LocalSend port per protocol specification.
	DefaultPort = 53317
	// DefaultPortStr is the string representation of DefaultPort.
	DefaultPortStr = "53317"
	// DefaultListenAddr listens on both IPv4 and IPv6 where the platform supports it.
	DefaultListenAddr = ":53317"
)

const (
	// v2 paths
	UploadPath      = "/api/localsend/v2/upload"
	PreuploadPath   = "/api/localsend/v2/prepare-upload"
	CancelPath      = "/api/localsend/v2/cancel"
	InfoPath        = "/api/localsend/v2/info"
	InfoPathV1      = "/api/localsend/v1/info"
	RegisterPath    = "/api/localsend/v2/register"
	RegisterPathV1  = "/api/localsend/v1/register"
	DownloadPath    = "/api/localsend/v2/download"
	PreDownloadPath = "/api/localsend/v2/prepare-download"

	// Dormant official v3 HTTP scaffolding. At the pinned LocalSend 1.18.x
	// source, these are the only v3 routes registered by the server. LocalSend
	// Web does not use this HTTP surface; its transfers run over WebRTC.
	NoncePathV3    = "/api/localsend/v3/nonce"
	RegisterPathV3 = "/api/localsend/v3/register"
)

// DeviceTypeToV3 converts the lowercase v2 device type to the
// SCREAMING_SNAKE_CASE representation used by v3 HTTP and WebRTC signaling.
func DeviceTypeToV3(dt string) string {
	return strings.ToUpper(dt)
}
