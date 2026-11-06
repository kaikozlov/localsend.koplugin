package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"localsend-cli/internal/control"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend"
	lsrecv "localsend-cli/internal/localsend/recv"
	"localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/webrtc/signaling"
)

var (
	timeout       int64
	legacyTimeout int64
	legacy        bool
	webrtc        bool
	lan           bool
	jsonOutput    bool
	excludeIDFile string
	devName       string
	port          int
	supportHTTPS  bool
	controlSocket string
)

// LANDevice represents a device discovered via LAN (multicast/HTTP)
type LANDevice struct {
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Alias    string `json:"alias"`
	Version  string `json:"version"`
	Protocol string `json:"protocol"`
}

// WebRTCDevice represents a device discovered via WebRTC signaling
type WebRTCDevice struct {
	ID      string `json:"id"`
	Alias   string `json:"alias"`
	Version string `json:"version"`
}

// ScanResult is the JSON output structure for discovered devices
type ScanResult struct {
	LAN    []LANDevice    `json:"lan"`
	WebRTC []WebRTCDevice `json:"webrtc"`
}

var Cmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan local network for localsend instance",
	Long:  "Scan local network for localsend instance",
	RunE:  runScan,
}

func runScan(cmd *cobra.Command, _ []string) error {
	if timeout <= 0 || timeout > 120 || legacyTimeout < 0 || legacyTimeout > 120 {
		return errors.New("scan durations must be between zero and 120 seconds, with a positive timeout")
	}
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if !jsonOutput {
		slog.Info("Start Scanning")
	}

	alias := devName
	if alias == "" {
		alias = utils.GenAlias()
	}
	useLAN, useLegacy, useWebRTC := lan, legacy, webrtc
	fallbackLegacy := false
	if !cmd.Flags().Changed("lan") && !cmd.Flags().Changed("legacy") && !cmd.Flags().Changed("webrtc") {
		useLAN, useWebRTC, fallbackLegacy = true, true, true
	}
	legacyDuration := legacyTimeout
	if legacyDuration == 0 {
		legacyDuration = timeout
	}
	options := localsend.ScanOptions{
		Timeout:        time.Duration(timeout) * time.Second,
		LegacyTimeout:  time.Duration(legacyDuration) * time.Second,
		LAN:            useLAN,
		Legacy:         useLegacy,
		FallbackLegacy: fallbackLegacy,
	}
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSignals()
	// Native stage deadlines start after backend readiness. This outer guard
	// includes startup without clipping the independent subnet deadline.
	ctx, cancel := context.WithTimeout(ctx, max(options.Timeout, options.LegacyTimeout)+5*time.Second)
	defer cancel()

	var signalingDone chan []signaling.ClientInfo
	if useWebRTC {
		signalingDone = make(chan []signaling.ClientInfo, 1)
		go func() {
			webCtx, cancelWeb := context.WithTimeout(ctx, options.Timeout)
			defer cancelWeb()
			signalingDone <- discoverViaSignaling(webCtx, jsonOutput, alias)
		}()
	}

	devices := make(map[string]models.Announcement)
	if useLAN || useLegacy || fallbackLegacy {
		var err error
		devices, err = scanNative(ctx, options, alias)
		if err != nil {
			cancel()
			if signalingDone != nil {
				<-signalingDone
			}
			return err
		}
	}
	var peers []signaling.ClientInfo
	if signalingDone != nil {
		peers = <-signalingDone
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !jsonOutput {
		slog.Info("Stop Scanning")
	}

	var excludeID string
	if excludeIDFile != "" {
		if data, err := os.ReadFile(excludeIDFile); err == nil {
			excludeID = strings.TrimSpace(string(data))
		}
	}
	result := ScanResult{LAN: make([]LANDevice, 0, len(devices)), WebRTC: make([]WebRTCDevice, 0, len(peers))}
	for ip, info := range devices {
		result.LAN = append(result.LAN, LANDevice{IP: ip, Port: info.Port, Alias: info.Alias, Version: info.Version, Protocol: info.Protocol})
	}
	for _, peer := range peers {
		if excludeID == "" || peer.ID.String() != excludeID {
			result.WebRTC = append(result.WebRTC, WebRTCDevice{ID: peer.ID.String(), Alias: peer.Alias, Version: peer.Version})
		}
	}
	if jsonOutput {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if len(result.LAN)+len(result.WebRTC) == 0 {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "No device found")
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Found Devices:")
	for _, device := range result.LAN {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\t[LAN] Name: %s, Version: %s, Address: %s:%d, Protocol: %s\n", device.Alias, device.Version, device.IP, device.Port, device.Protocol)
	}
	for _, device := range result.WebRTC {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\t[WebRTC] Name: %s, Version: %s, ID: %s\n", device.Alias, device.Version, device.ID)
	}
	return nil
}

func scanNative(ctx context.Context, options localsend.ScanOptions, alias string) (map[string]models.Announcement, error) {
	path := controlSocket
	if path == "" {
		path = control.DefaultPath(port)
	}
	for {
		devices, err := control.Scan(ctx, path, options)
		if !errors.Is(err, control.ErrUnavailable) {
			return devices, err
		}
		owner, err := control.Listen(path)
		if err == nil {
			defer owner.Close()
			return scanWithTemporaryBackend(ctx, owner, options, alias)
		}
		if !errors.Is(err, control.ErrBusy) {
			return nil, err
		}
		// Another command acquired ownership between our connect and lock.
		// Wait only for that startup race; never steal its socket or listener.
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func scanWithTemporaryBackend(ctx context.Context, owner *control.Server, options localsend.ScanOptions, alias string) (devices map[string]models.Announcement, err error) {
	receiver := lsrecv.NewFileReceiver(alias, "", supportHTTPS)
	receiver.SetDiscoveryOnly(true)
	receiver.SetListenAddr(net.JoinHostPort("", strconv.Itoa(port)))
	if err := receiver.Init(); err != nil {
		return nil, err
	}
	backendCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var startErr error
	go func() {
		startErr = receiver.Start(backendCtx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if startErr == nil {
			return nil, errors.New("discovery-only backend stopped before becoming ready")
		}
		return nil, fmt.Errorf("start discovery-only backend: %w", startErr)
	case <-receiver.Ready():
	}
	// This temporary backend belongs to this one scan. Concurrent commands
	// receive an explicit busy result rather than taking ownership or extending
	// its lifetime after the original caller has cancelled.
	controlDone := make(chan error, 1)
	go func() {
		controlDone <- owner.Serve(backendCtx, func(context.Context, localsend.ScanOptions) (map[string]models.Announcement, error) {
			return nil, errors.New("a discovery-only scan is already in progress")
		})
	}()
	defer func() {
		cancel()
		err = errors.Join(err, <-controlDone)
	}()
	return receiver.Scan(backendCtx, options)
}

func discoverViaSignaling(ctx context.Context, silent bool, alias string) []signaling.ClientInfo {
	// Generate signing key and token
	_, token, err := crypto.GenerateKeyPairWithToken()
	if err != nil {
		if !silent {
			slog.Error("Failed to generate key pair with token", "error", err)
		}
		return nil
	}

	// Connect to signaling server
	info := signaling.NewClientInfo(alias, token)

	client, err := signaling.ConnectWithContext(ctx, signaling.DefaultSignalingServer, info)
	if err != nil {
		if !silent {
			slog.Error("Failed to connect to signaling server", "error", err)
		}
		return nil
	}
	defer func() { _ = client.Close() }()

	if !silent {
		slog.Info("Connected to signaling server", "id", client.ClientID())
	}

	// Wait for context or collect peers
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		// Give some time to receive JOIN messages
	}

	return client.GetPeers()
}

func init() {
	Cmd.PersistentFlags().Int64VarP(&timeout, "timeout", "t", 4, "scan duration in seconds")
	Cmd.PersistentFlags().Int64Var(&legacyTimeout, "legacy-timeout", 0, "legacy subnet scan deadline in seconds (defaults to scan duration)")
	Cmd.PersistentFlags().BoolVarP(&legacy, "legacy", "l", false, "perform legacy HTTP subnet scan")
	Cmd.PersistentFlags().BoolVarP(&webrtc, "webrtc", "w", false, "discover peers via WebRTC signaling server")
	Cmd.PersistentFlags().BoolVarP(&lan, "lan", "n", false, "perform LAN discovery (multicast/UDP)")
	Cmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "output results as JSON")
	Cmd.PersistentFlags().StringVarP(&excludeIDFile, "exclude-id-file", "e", "", "file containing signaling ID to exclude (for self-filtering)")
	Cmd.PersistentFlags().StringVar(&devName, "devname", "", "device name to display to other peers")
	Cmd.PersistentFlags().IntVar(&port, "port", 53317, "LocalSend listener and discovery port")
	Cmd.PersistentFlags().BoolVar(&supportHTTPS, "https", true, "use HTTPS for a temporary discovery-only backend")
	Cmd.PersistentFlags().StringVar(&controlSocket, "control-socket", "", "private backend socket (defaults to a per-user, per-port temporary directory)")
}
