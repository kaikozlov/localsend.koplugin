package transfer

import (
	"encoding/json"

	"localsend-cli/internal/models"
)

// Data channel message types matching official LocalSend protocol.
const (
	DCTypeHandshake = "HANDSHAKE"
	DCTypePin       = "PIN"
	DCTypeFiles     = "FILES"
	DCTypeAccept    = "ACCEPT"
	DCTypeDecline   = "DECLINE"
	DCTypeBinary    = "BINARY"
	DCTypeFinish    = "FINISH"
	DCTypeError     = "ERROR"
)

// DCMessage is the base structure for data channel messages.
type DCMessage struct {
	Type string `json:"type"`
}

// DCHandshakeMessage is sent to establish connection and verify identity.
type DCHandshakeMessage struct {
	Type  string `json:"type"`
	Token string `json:"token"` // Signed token for verification
}

// DCPinMessage is used for PIN protection.
type DCPinMessage struct {
	Type string `json:"type"`
	Pin  string `json:"pin,omitempty"`
	Try  int    `json:"try,omitempty"` // Current attempt number
	Max  int    `json:"max,omitempty"` // Maximum attempts
}

// DCFilesMessage contains file metadata from sender.
type DCFilesMessage struct {
	Type  string   `json:"type"`
	Files []DCFile `json:"files"`
}

// DCFile represents file metadata.
type DCFile struct {
	ID       string         `json:"id"`
	FileName string         `json:"fileName"`
	Size     models.FlexInt `json:"size"`
	FileType string         `json:"fileType"`
	SHA256   string         `json:"sha256,omitempty"`
	Preview  string         `json:"preview,omitempty"`
}

// DCAcceptMessage is sent by receiver to accept selected files.
type DCAcceptMessage struct {
	Type    string   `json:"type"`
	FileIDs []string `json:"fileIds"`
}

// DCDeclineMessage is sent by receiver to decline all files.
type DCDeclineMessage struct {
	Type string `json:"type"`
}

// DCBinaryHeader is sent before binary file data.
type DCBinaryHeader struct {
	Type   string `json:"type"`
	FileID string `json:"fileId"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
}

// DCFinishMessage is sent when a file transfer is complete.
type DCFinishMessage struct {
	Type   string `json:"type"`
	FileID string `json:"fileId"`
}

// DCErrorMessage reports an error.
type DCErrorMessage struct {
	Type    string `json:"type"`
	FileID  string `json:"fileId,omitempty"`
	Message string `json:"message"`
}

// NewHandshakeMessage creates a handshake message.
func NewHandshakeMessage(token string) DCHandshakeMessage {
	return DCHandshakeMessage{
		Type:  DCTypeHandshake,
		Token: token,
	}
}

// NewFilesMessage creates a files metadata message.
func NewFilesMessage(files []DCFile) DCFilesMessage {
	return DCFilesMessage{
		Type:  DCTypeFiles,
		Files: files,
	}
}

// NewAcceptMessage creates an accept message.
func NewAcceptMessage(fileIDs []string) DCAcceptMessage {
	return DCAcceptMessage{
		Type:    DCTypeAccept,
		FileIDs: fileIDs,
	}
}

// NewDeclineMessage creates a decline message.
func NewDeclineMessage() DCDeclineMessage {
	return DCDeclineMessage{
		Type: DCTypeDecline,
	}
}

// NewFinishMessage creates a finish message.
func NewFinishMessage(fileID string) DCFinishMessage {
	return DCFinishMessage{
		Type:   DCTypeFinish,
		FileID: fileID,
	}
}

// NewErrorMessage creates an error message.
func NewErrorMessage(fileID, message string) DCErrorMessage {
	return DCErrorMessage{
		Type:    DCTypeError,
		FileID:  fileID,
		Message: message,
	}
}

// ParseMessageType extracts the message type from raw JSON.
func ParseMessageType(data []byte) (string, error) {
	var msg DCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", err
	}
	return msg.Type, nil
}
