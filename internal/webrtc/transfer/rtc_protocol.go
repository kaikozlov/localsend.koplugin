package transfer

import (
	"encoding/json"
)

// Official WebRTC protocol message types (different from our simple protocol)

// RTCNonceMessage is exchanged first by both peers.
type RTCNonceMessage struct {
	Nonce string `json:"nonce"`
}

// RTCTokenRequest is sent by the sender after nonce exchange.
type RTCTokenRequest struct {
	Token string `json:"token"`
}

// RTCTokenResponse is sent by the receiver.
type RTCTokenResponse struct {
	Status string `json:"status"` // "OK", "PIN_REQUIRED", "INVALID_SIGNATURE"
	Token  string `json:"token,omitempty"`
}

// RTCPinMessage is used for PIN exchange.
type RTCPinMessage struct {
	Pin string `json:"pin"`
}

// RTCPinReceivingResponse is sent by the receiver after PIN verification.
type RTCPinReceivingResponse struct {
	Status string `json:"status"` // "OK", "PIN_REQUIRED", "TOO_MANY_ATTEMPTS"
}

// RTCPinSendingResponse contains file list when status is OK.
type RTCPinSendingResponse struct {
	Status string       `json:"status"` // "OK", "PIN_REQUIRED", "TOO_MANY_ATTEMPTS"
	Files  []RTCFileDto `json:"files,omitempty"`
}

// RTCFileMetadata contains optional file timestamp information.
type RTCFileMetadata struct {
	Modified string `json:"modified,omitempty"`
	Accessed string `json:"accessed,omitempty"`
}

// RTCFileDto represents a file in the WebRTC protocol.
type RTCFileDto struct {
	ID       string          `json:"id"`
	FileName string          `json:"fileName"`
	Size     int64           `json:"size"`
	FileType string          `json:"fileType"`
	SHA256   string          `json:"sha256,omitempty"`
	Preview  string          `json:"preview,omitempty"`
	Metadata RTCFileMetadata `json:"metadata,omitempty"`
}

// RTCFileListResponse is sent by receiver after receiving file list.
type RTCFileListResponse struct {
	Status    string            `json:"status"`              // "OK", "PAIR", "DECLINED", "INVALID_SIGNATURE"
	Files     map[string]string `json:"files,omitempty"`     // fileId -> token
	PublicKey string            `json:"publicKey,omitempty"` // for PAIR
}

// RTCPairResponse is sent during the PAIR flow.
// Used when responding to a PAIR request from the other peer.
type RTCPairResponse struct {
	Status    string `json:"status"`              // "OK", "PAIR_DECLINED", "INVALID_SIGNATURE"
	PublicKey string `json:"publicKey,omitempty"` // Present if status == "OK"
}

// RTCSendFileHeader is sent before each file's binary data.
type RTCSendFileHeader struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

// RTCSendFileResponse is sent after receiving each file.
type RTCSendFileResponse struct {
	ID      string  `json:"id"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

// RTCErrorResponse is sent when an error occurs during handshake.
type RTCErrorResponse struct {
	Error string `json:"error"`
}

// ParseRTCMessage tries to detect what type of message this is.
func ParseRTCMessage(data []byte) (interface{}, string, error) {
	// Try each message type

	// Check for nonce first
	var nonce RTCNonceMessage
	if err := json.Unmarshal(data, &nonce); err == nil && nonce.Nonce != "" {
		return &nonce, "nonce", nil
	}

	// Check for file header (has id+token)
	var fileHeader RTCSendFileHeader
	if err := json.Unmarshal(data, &fileHeader); err == nil && fileHeader.ID != "" && fileHeader.Token != "" {
		return &fileHeader, "file_header", nil
	}

	// Check for status-based responses BEFORE token_request
	// (token response has both status AND token, while token_request only has token)
	var generic map[string]interface{}
	if err := json.Unmarshal(data, &generic); err == nil {
		if status, ok := generic["status"].(string); ok {
			// Check if it has files (RTCPinSendingResponse)
			if _, hasFiles := generic["files"]; hasFiles {
				var pinResp RTCPinSendingResponse
				if err := json.Unmarshal(data, &pinResp); err == nil {
					return &pinResp, "file_list", nil
				}
			}

			// Check for pair response (has publicKey but NO files)
			if _, hasPublicKey := generic["publicKey"]; hasPublicKey {
				if _, hasFiles := generic["files"]; !hasFiles {
					var pairResp RTCPairResponse
					if err := json.Unmarshal(data, &pairResp); err == nil {
						return &pairResp, "pair_response", nil
					}
				}
			}

			return generic, "status_" + status, nil
		}
	}

	// Check for token request (only has token field, no status)
	var tokenReq RTCTokenRequest
	if err := json.Unmarshal(data, &tokenReq); err == nil && tokenReq.Token != "" {
		return &tokenReq, "token_request", nil
	}

	// Check for PIN
	var pin RTCPinMessage
	if err := json.Unmarshal(data, &pin); err == nil && pin.Pin != "" {
		return &pin, "pin", nil
	}

	// Check for file response (success/error ack from receiver)
	var fileResp RTCSendFileResponse
	if err := json.Unmarshal(data, &fileResp); err == nil && fileResp.ID != "" {
		return &fileResp, "file_response", nil
	}

	return nil, "", nil
}
