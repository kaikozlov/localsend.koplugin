package models

type Announcement struct {
	DeviceInfo
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Announce bool   `json:"announce"`
}

func (anno Announcement) GetDeviceInfo() DeviceInfo {
	return anno.DeviceInfo
}

type DeviceInfo struct {
	IP          string `json:"-"` // not part of the protocol
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"` // nullable per protocol
	DeviceType  string `json:"deviceType,omitempty"`  // nullable per protocol
	Fingerprint string `json:"fingerprint,omitempty"` // v2.1 field
	Token       string `json:"token,omitempty"`       // v3 field - replaces fingerprint
	Download    bool   `json:"download,omitempty"`    // optional, default false
	// v3 fields
	HasWebInterface bool `json:"hasWebInterface,omitempty"` // v3: whether device has web UI
}

// SenderInfo extends DeviceInfo with port and protocol fields.
// Used in prepare-upload requests per protocol spec Section 4.1.
type SenderInfo struct {
	DeviceInfo
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // "http" or "https"
}

// NewDeviceInfo creates a DeviceInfo for HTTP/multicast discovery (V2 protocol).
// Note: Version is "2.2" for V2 HTTP endpoints (LocalSend 1.18+, protocol
// revision of Aug 2026; wire format unchanged from 2.1). WebRTC signaling uses
// "2.3" (see internal/webrtc/signaling/messages.go) to indicate V3 WebRTC
// capability.
func NewDeviceInfo(alias string, fingerprint string) DeviceInfo {
	return DeviceInfo{
		Alias:       alias,
		Version:     "2.2", // V2 HTTP protocol version
		DeviceModel: "LocalSend-CLI",
		DeviceType:  "headless",
		Fingerprint: fingerprint,
		Download:    false,
	}
}
