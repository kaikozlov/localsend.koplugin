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

	// v3 paths
	NoncePathV3       = "/api/localsend/v3/nonce"
	RegisterPathV3    = "/api/localsend/v3/register"
	PreuploadPathV3   = "/api/localsend/v3/prepare-upload"
	UploadPathV3      = "/api/localsend/v3/upload"
	CancelPathV3      = "/api/localsend/v3/cancel"
	InfoPathV3        = "/api/localsend/v3/info"
	DownloadPathV3    = "/api/localsend/v3/download"
	PreDownloadPathV3 = "/api/localsend/v3/prepare-download"
)

// DeviceTypeToV3 converts lowercase device type to SCREAMING_SNAKE_CASE for v3 HTTP API.
// v2.1 uses lowercase ("mobile"), v3 HTTP uses uppercase ("MOBILE").
// Note: v3 WebRTC signaling still uses lowercase.
func DeviceTypeToV3(dt string) string {
	return strings.ToUpper(dt)
}

// DeviceTypeFromV3 converts SCREAMING_SNAKE_CASE device type to lowercase.
func DeviceTypeFromV3(dt string) string {
	return strings.ToLower(dt)
}

// ProtocolToV3 converts lowercase protocol to uppercase for v3 HTTP API.
// v2.1 uses lowercase ("https"), v3 HTTP uses uppercase ("HTTPS").
func ProtocolToV3(p string) string {
	return strings.ToUpper(p)
}

// ProtocolFromV3 converts uppercase protocol to lowercase.
func ProtocolFromV3(p string) string {
	return strings.ToLower(p)
}
