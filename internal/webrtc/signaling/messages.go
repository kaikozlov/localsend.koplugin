package signaling

import (
	"github.com/google/uuid"
	"localsend-cli/internal/models"
)

// WsServerMessage represents messages from the signaling server.
type WsServerMessage struct {
	Type      string        `json:"type"` // HELLO, JOIN, UPDATE, LEFT, OFFER, ANSWER, ERROR
	Client    *ClientInfo   `json:"client,omitempty"`
	Peers     *[]ClientInfo `json:"peers,omitempty"` // Use pointer to allow [] when empty but present
	Peer      *ClientInfo   `json:"peer,omitempty"`
	PeerID    *uuid.UUID    `json:"peerId,omitempty"`
	SessionID string        `json:"sessionId,omitempty"`
	SDP       string        `json:"sdp,omitempty"`
	Code      int           `json:"code,omitempty"`
}

// WsClientMessage represents messages sent to the signaling server.
type WsClientMessage struct {
	Type      string               `json:"type"` // UPDATE, OFFER, ANSWER
	Info      *ClientInfoWithoutID `json:"info,omitempty"`
	SessionID string               `json:"sessionId,omitempty"`
	Target    *uuid.UUID           `json:"target,omitempty"`
	SDP       string               `json:"sdp,omitempty"`
}

// ClientInfo represents peer information with a server-assigned ID.
type ClientInfo struct {
	ID          uuid.UUID `json:"id"`
	Alias       string    `json:"alias"`
	Version     string    `json:"version"`
	DeviceModel string    `json:"deviceModel,omitempty"`
	DeviceType  string    `json:"deviceType,omitempty"`
	Token       string    `json:"token"`
}

// ClientInfoWithoutID is client info for initial connection (ID assigned by server).
type ClientInfoWithoutID struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Token       string `json:"token"`
}

// NewClientInfo creates the signaling identity used by recv, scan, and send.
// Version 2.3 and SCREAMING_SNAKE_CASE device type intentionally match the
// pinned LocalSend Web client, which is a WebRTC interoperability target.
func NewClientInfo(alias, token string) ClientInfoWithoutID {
	return ClientInfoWithoutID{
		Alias:       alias,
		Version:     "2.3",
		DeviceModel: "LocalSend-CLI",
		DeviceType:  "HEADLESS",
		Token:       token,
	}
}

// ToAnnouncement converts ClientInfo to a models.Announcement for display.
func (c *ClientInfo) ToAnnouncement() models.Announcement {
	return models.Announcement{
		DeviceInfo: models.DeviceInfo{
			Alias:       c.Alias,
			Version:     c.Version,
			DeviceModel: c.DeviceModel,
			DeviceType:  c.DeviceType,
			Fingerprint: c.Token,
		},
		Protocol: "webrtc",
		Port:     0,
		Announce: false,
	}
}

// NewUpdateMessage creates an UPDATE message.
func NewUpdateMessage(info ClientInfoWithoutID) WsClientMessage {
	return WsClientMessage{
		Type: "UPDATE",
		Info: &info,
	}
}

// NewOfferMessage creates an OFFER message.
func NewOfferMessage(sessionID string, target uuid.UUID, sdp string) WsClientMessage {
	return WsClientMessage{
		Type:      "OFFER",
		SessionID: sessionID,
		Target:    &target,
		SDP:       sdp,
	}
}

// NewAnswerMessage creates an ANSWER message.
func NewAnswerMessage(sessionID string, target uuid.UUID, sdp string) WsClientMessage {
	return WsClientMessage{
		Type:      "ANSWER",
		SessionID: sessionID,
		Target:    &target,
		SDP:       sdp,
	}
}
