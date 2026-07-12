package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// Connection close wait parameters
const (
	// closeWaitIterations is the number of intervals to wait for connection to close
	closeWaitIterations = 20
	// closeWaitInterval is the time between close state checks
	closeWaitInterval = 100 * time.Millisecond
	// iceGatheringTimeout is the maximum time to wait for ICE gathering
	iceGatheringTimeout = 30 * time.Second
)

// Default STUN servers for ICE.
var DefaultSTUNServers = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun1.l.google.com:19302",
}

// PeerConnection wraps a pion/webrtc PeerConnection for file transfer.
type PeerConnection struct {
	pc            *webrtc.PeerConnection
	dataChannel   *webrtc.DataChannel
	mu            sync.Mutex
	onMessage     func([]byte)
	onDataMessage func([]byte, bool)
	onOpen        func()
	onClose       func()
}

// PeerConfig configures a new peer connection.
type PeerConfig struct {
	STUNServers []string
	IsInitiator bool // true for sender (creates offer), false for receiver (creates answer)
}

// NewPeerConnection creates a new WebRTC peer connection.
func NewPeerConnection(config PeerConfig) (*PeerConnection, error) {
	stunServers := config.STUNServers
	if len(stunServers) == 0 {
		stunServers = DefaultSTUNServers
	}

	// Create ICE servers config
	iceServers := make([]webrtc.ICEServer, len(stunServers))
	for i, server := range stunServers {
		iceServers[i] = webrtc.ICEServer{URLs: []string{server}}
	}

	// Configure settings for better ICE connectivity
	settingEngine := webrtc.SettingEngine{}

	// Restrict ICE UDP ports to a known range for firewall configuration
	// This range must match the firewall rules in the KOReader plugin
	if err := settingEngine.SetEphemeralUDPPortRange(50000, 50100); err != nil {
		return nil, fmt.Errorf("failed to set UDP port range: %w", err)
	}

	// Set ICE timeouts for better reliability on slower networks
	settingEngine.SetICETimeouts(
		5*time.Second,  // disconnectedTimeout - time before marking as disconnected
		25*time.Second, // failedTimeout - time before marking as failed
		2*time.Second,  // keepAliveInterval - STUN keepalive interval
	)

	// Create API with custom settings
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	// Create peer connection with custom API
	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
		// Use ICE candidate pool to speed up connectivity (spec recommends 2)
		ICECandidatePoolSize: 2,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %w", err)
	}

	p := &PeerConnection{
		pc: pc,
	}

	// Set up connection state handler
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		slog.Info("WebRTC connection state", "state", state.String())
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			p.mu.Lock()
			handler := p.onClose
			p.mu.Unlock()
			if handler != nil {
				handler()
			}
		}
		if state == webrtc.PeerConnectionStateConnected {
			slog.Info("WebRTC connection established!")
		}
	})

	// Set up ICE connection state handler for debugging
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		slog.Info("ICE connection state", "state", state.String())
		// Log additional details when checking or failed
		if state == webrtc.ICEConnectionStateChecking {
			slog.Info("ICE checking - connectivity checks in progress")
		}
		if state == webrtc.ICEConnectionStateFailed {
			slog.Error("ICE failed - no valid candidate pair found")
		}
	})

	// Set up ICE gathering state handler
	pc.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		slog.Info("ICE gathering state", "state", state.String())
	})

	// Set up ICE candidate handler for debugging
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			slog.Info("ICE candidate gathered", "type", candidate.Typ.String(), "address", candidate.Address, "port", candidate.Port, "protocol", candidate.Protocol.String())
		} else {
			slog.Info("ICE gathering complete (null candidate)")
		}
	})

	// Log when we receive any SCTP/data channel activity
	pc.SCTP().OnError(func(err error) {
		slog.Error("SCTP error", "error", err)
	})

	// If initiator, create data channel
	if config.IsInitiator {
		dc, err := pc.CreateDataChannel("data", nil)
		if err != nil {
			_ = pc.Close()
			return nil, fmt.Errorf("failed to create data channel: %w", err)
		}
		p.setupDataChannel(dc)
	} else {
		// If receiver, wait for data channel from peer
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			slog.Info("Received data channel", "label", dc.Label())
			p.setupDataChannel(dc)
		})
	}

	return p, nil
}

// setupDataChannel configures the data channel handlers.
func (p *PeerConnection) setupDataChannel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	p.dataChannel = dc
	p.mu.Unlock()

	dc.OnOpen(func() {
		slog.Info("Data channel opened", "label", dc.Label(), "id", dc.ID())
		p.mu.Lock()
		handler := p.onOpen
		p.mu.Unlock()
		if handler != nil {
			handler()
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		slog.Debug("Data channel message received", "isString", msg.IsString, "len", len(msg.Data))
		p.mu.Lock()
		handler := p.onMessage
		typedHandler := p.onDataMessage
		p.mu.Unlock()
		if typedHandler != nil {
			typedHandler(msg.Data, msg.IsString)
		} else if handler != nil {
			handler(msg.Data)
		}
	})

	dc.OnClose(func() {
		slog.Info("Data channel closed")
	})

	dc.OnError(func(err error) {
		slog.Error("Data channel error", "error", err)
	})
}

// CreateOffer creates an SDP offer.
func (p *PeerConnection) CreateOffer() (string, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %w", err)
	}

	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete with timeout
	select {
	case <-webrtc.GatheringCompletePromise(p.pc):
		// ICE gathering completed
	case <-time.After(iceGatheringTimeout):
		slog.Warn("ICE gathering timed out, proceeding with available candidates")
	}

	return p.pc.LocalDescription().SDP, nil
}

// AcceptOffer accepts an SDP offer and creates an answer.
func (p *PeerConnection) AcceptOffer(sdp string) (string, error) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}

	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set remote description: %w", err)
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create answer: %w", err)
	}

	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete with timeout
	select {
	case <-webrtc.GatheringCompletePromise(p.pc):
		// ICE gathering completed
	case <-time.After(iceGatheringTimeout):
		slog.Warn("ICE gathering timed out, proceeding with available candidates")
	}

	return p.pc.LocalDescription().SDP, nil
}

// SetAnswer sets the remote SDP answer.
func (p *PeerConnection) SetAnswer(sdp string) error {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	}

	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	return nil
}

// OnMessage sets the handler for incoming data channel messages.
// Thread-safe: can be called concurrently with callback invocations.
func (p *PeerConnection) OnMessage(handler func([]byte)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onMessage = handler
}

// OnDataMessage preserves whether the WebRTC frame was text or binary.
func (p *PeerConnection) OnDataMessage(handler func([]byte, bool)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onDataMessage = handler
}

// OnOpen sets the handler for when the data channel opens.
// Thread-safe: can be called concurrently with callback invocations.
func (p *PeerConnection) OnOpen(handler func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onOpen = handler
}

// OnClose sets the handler for when the connection closes.
// Thread-safe: can be called concurrently with callback invocations.
func (p *PeerConnection) OnClose(handler func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onClose = handler
}

// Send sends data through the data channel.
func (p *PeerConnection) Send(data []byte) error {
	p.mu.Lock()
	dc := p.dataChannel
	p.mu.Unlock()

	if dc == nil {
		return fmt.Errorf("data channel not ready")
	}

	return dc.Send(data)
}

// SendText sends text through the data channel.
func (p *PeerConnection) SendText(text string) error {
	p.mu.Lock()
	dc := p.dataChannel
	p.mu.Unlock()

	if dc == nil {
		return fmt.Errorf("data channel not ready")
	}

	return dc.SendText(text)
}

// BufferedAmount returns the number of bytes queued in the data channel buffer.
func (p *PeerConnection) BufferedAmount() uint64 {
	p.mu.Lock()
	dc := p.dataChannel
	p.mu.Unlock()

	if dc == nil {
		return 0
	}

	return dc.BufferedAmount()
}

// WaitBufferEmpty polls until the data channel buffer is empty.
// Uses 10ms polling interval.
// Returns error if context is cancelled.
func (p *PeerConnection) WaitBufferEmpty(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if p.BufferedAmount() == 0 {
				return nil
			}
		}
	}
}

// WaitBufferEmptyWithTimeout is WaitBufferEmpty with a timeout.
func (p *PeerConnection) WaitBufferEmptyWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return p.WaitBufferEmpty(ctx)
}

// Close closes the peer connection and waits for it to fully close.
func (p *PeerConnection) Close() error {
	if p.pc == nil {
		return nil
	}

	// Close the data channel first if it exists
	p.mu.Lock()
	if p.dataChannel != nil {
		_ = p.dataChannel.Close()
		p.dataChannel = nil
	}
	p.mu.Unlock()

	err := p.pc.Close()
	if err != nil {
		return err
	}

	// Wait for connection state to become closed
	for i := 0; i < closeWaitIterations; i++ {
		state := p.pc.ConnectionState()
		if state == webrtc.PeerConnectionStateClosed {
			break
		}
		time.Sleep(closeWaitInterval)
	}

	return nil
}

// ConnectionState returns the current connection state.
func (p *PeerConnection) ConnectionState() webrtc.PeerConnectionState {
	return p.pc.ConnectionState()
}

// IsConnected returns true if the connection is established.
func (p *PeerConnection) IsConnected() bool {
	return p.pc.ConnectionState() == webrtc.PeerConnectionStateConnected
}

// SendJSON sends a JSON-encoded message as text through the data channel.
func (p *PeerConnection) SendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.SendText(string(data))
}

// SendJSONBinary sends a JSON-encoded message as binary data through the data channel.
func (p *PeerConnection) SendJSONBinary(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	for start := 0; start < len(data); start += ChunkSize {
		end := start + ChunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := p.Send(data[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// SendDelimiter sends the "0" delimiter to signal end of a chunked message.
func (p *PeerConnection) SendDelimiter() error {
	return p.SendText("0")
}
