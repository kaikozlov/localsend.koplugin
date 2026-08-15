package models

// NonceRequest represents a request to obtain a nonce for token generation.
// Used by the dormant official v3 HTTP nonce endpoint.
type NonceRequest struct {
	Nonce string `json:"nonce"`
}

// NonceResponse represents the server's response containing a nonce.
// Used by the dormant official v3 HTTP nonce endpoint.
type NonceResponse struct {
	Nonce string `json:"nonce"`
}

// RegisterRequestV3 represents the dormant official v3 HTTP registration DTO.
// LocalSend Web does not use this endpoint; it registers through signaling.
type RegisterRequestV3 struct {
	Alias           string `json:"alias"`
	Version         string `json:"version"`
	DeviceModel     string `json:"deviceModel,omitempty"`
	DeviceType      string `json:"deviceType,omitempty"`
	Token           string `json:"token"`
	Port            int    `json:"port"`
	Protocol        string `json:"protocol"` // "http" or "https"
	HasWebInterface bool   `json:"hasWebInterface,omitempty"`
}

// RegisterResponseV3 is returned by the dormant official v3 HTTP register endpoint.
type RegisterResponseV3 struct {
	Alias           string `json:"alias"`
	Version         string `json:"version"`
	DeviceModel     string `json:"deviceModel,omitempty"`
	DeviceType      string `json:"deviceType,omitempty"`
	Token           string `json:"token"`
	HasWebInterface bool   `json:"hasWebInterface,omitempty"`
}
