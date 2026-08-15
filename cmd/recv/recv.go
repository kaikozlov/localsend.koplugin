package recv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"localsend-cli/internal/crypto"
	lsrecv "localsend-cli/internal/localsend/recv"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/storage"
	"localsend-cli/internal/utils"
	"localsend-cli/internal/webrtc/signaling"
	"localsend-cli/internal/webrtc/transfer"
)

var (
	devname            string
	savetodir          string
	supportHttps       bool
	pin                string
	acceptExt          string
	logFile            string
	webrtcMode         bool
	extRouting         string
	onTransferCmd      string
	transferNotifyFile string
	transferBusyFile   string
	configDir          string
	requirePairing     bool
	stunServers        []string
	signalingIDFile    string
)

const (
	// shutdownTimeout is the maximum time to wait for goroutines to exit cleanly during shutdown.
	shutdownTimeout        = 5 * time.Second
	webRTCReconnectInitial = time.Second
	webRTCReconnectMax     = 30 * time.Second
	webRTCStableSession    = 30 * time.Second
)

var Cmd = &cobra.Command{
	Use:   "recv",
	Short: "Receive files from localsend instance",
	Long:  "Receive files from localsend instance",
	Run: func(cmd *cobra.Command, args []string) {
		var wg sync.WaitGroup

		// Load extension routing config if provided
		var router *lsrecv.ExtensionRouter
		var extRoutes map[string]string
		if extRouting != "" {
			router = lsrecv.NewExtensionRouter(savetodir)
			if err := router.LoadFromFile(extRouting); err != nil {
				slog.Error("Failed to load extension routing config", "error", err)
				return
			}
			if err := router.EnsureDirectories(); err != nil {
				slog.Warn("Failed to create some routing directories", "error", err)
			}
			slog.Info("Extension routing enabled", "config", extRouting)

			// Also extract routes for WebRTC receiver
			extRoutes = make(map[string]string)
			// Re-read the config to get the raw routes
			if data, err := os.ReadFile(extRouting); err == nil {
				_ = json.Unmarshal(data, &extRoutes)
			}
		}

		activity := newTransferActivityMarker(transferBusyFile)
		defer activity.Close()

		// HTTP receiver (always start unless webrtc-only)
		recver := lsrecv.NewFileReceiver(devname, savetodir, supportHttps)
		recver.SetPIN(pin)
		recver.SetTransferLog(logFile)
		recver.SetOnTransferCmd(onTransferCmd)
		recver.SetTransferNotifyFile(transferNotifyFile)
		recver.SetTransferActivityCallbacks(activity.Begin, activity.End)

		// Set extension router if configured
		if router != nil {
			recver.SetExtensionRouter(router)
		}

		// Set allowed extensions if provided
		allowedExts := utils.ParseExtensionList(acceptExt)
		if len(allowedExts) > 0 {
			recver.SetAllowedExtensions(allowedExts)
		}

		if err := recver.Init(); err != nil {
			slog.Error("Failed to initialize receiver", "error", err)
			return
		}

		// All long-running components share one cancellation domain. A fatal HTTP
		// listener exit cancels the process instead of leaving a live PID whose
		// receive service has silently disappeared. Discovery and WebRTC handle
		// their own transient network failures internally.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		componentErr := make(chan error, 2)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := recver.Start(ctx)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				err = errors.New("HTTP receiver stopped unexpectedly")
			}
			select {
			case componentErr <- fmt.Errorf("HTTP receiver stopped: %w", err):
			default:
			}
		}()

		if webrtcMode {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := startWebRTCReceiver(ctx, devname, savetodir, pin, allowedExts, extRoutes, recver.LogTransfer, activity.Begin, activity.End, configDir, requirePairing, stunServers); err != nil && ctx.Err() == nil {
					select {
					case componentErr <- fmt.Errorf("WebRTC receiver stopped: %w", err):
					default:
					}
				}
			}()
		}

		signals := utils.WaitForSignal()
		select {
		case <-signals:
		case err := <-componentErr:
			slog.Error("Receiver component failed", "error", err)
		}
		cancel()

		// Wait for goroutines with timeout to prevent hanging on shutdown
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Clean shutdown
		case <-time.After(shutdownTimeout):
			slog.Warn("Shutdown timeout: some goroutines did not exit cleanly")
		}
	},
}

func jitterReconnectDelay(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	spread := base / 5 // ±20% prevents reconnect herds after server/network recovery.
	if spread <= 0 {
		return base
	}
	return base - spread + time.Duration(rand.Int64N(int64(2*spread)+1))
}

func nextReconnectBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return webRTCReconnectInitial
	}
	next := current * 2
	if next > webRTCReconnectMax {
		return webRTCReconnectMax
	}
	return next
}

func reconnectBackoffAfterSession(current, sessionDuration time.Duration) time.Duration {
	if sessionDuration >= webRTCStableSession {
		return webRTCReconnectInitial
	}
	return current
}

func waitReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(jitterReconnectDelay(delay))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func startWebRTCReceiver(ctx context.Context, deviceName, saveDir, pin string, allowedExts []string, extRoutes map[string]string, logTransfer func(filename string, size int64, sender string), transferStart, transferDone func(), cfgDir string, reqPairing bool, customSTUN []string) error {
	// Keep one cryptographic identity for the process lifetime. Only the
	// timestamp token and server-assigned signaling ID change on reconnect.
	key, _, err := crypto.GenerateKeyPairWithToken()
	if err != nil {
		return fmt.Errorf("generate WebRTC signing identity: %w", err)
	}

	var trustedStore *storage.TrustedDeviceStore
	if cfgDir != "" {
		trustedStore, err = storage.NewTrustedDeviceStore(cfgDir)
		if err != nil {
			slog.Error("Failed to initialize trusted device store", "error", err)
		} else {
			slog.Info("Trusted device store initialized", "config", cfgDir)
		}
	}

	if signalingIDFile != "" {
		_ = os.Remove(signalingIDFile)
		defer func() { _ = os.Remove(signalingIDFile) }()
	}

	backoff := webRTCReconnectInitial
	for ctx.Err() == nil {
		token, tokenErr := key.GenerateTokenTimestamp()
		if tokenErr != nil {
			return fmt.Errorf("generate WebRTC signaling token: %w", tokenErr)
		}
		info := signaling.NewClientInfo(deviceName, token)

		client, connectErr := signaling.ConnectWithContext(ctx, signaling.DefaultSignalingServer, info)
		if connectErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			if signalingIDFile != "" {
				_ = os.Remove(signalingIDFile)
			}
			slog.Warn("WebRTC signaling unavailable; retrying", "error", connectErr, "retry_in", backoff)
			if !waitReconnect(ctx, backoff) {
				return nil
			}
			backoff = nextReconnectBackoff(backoff)
			continue
		}

		connectedAt := time.Now()
		client.SetTokenGenerator(func() (string, error) { return key.GenerateTokenTimestamp() })

		if signalingIDFile != "" {
			if err := os.WriteFile(signalingIDFile, []byte(client.ClientID().String()), 0600); err != nil {
				slog.Warn("Failed to write signaling ID file", "path", signalingIDFile, "error", err)
			}
		}
		slog.Info("WebRTC receiver listening", "id", client.ClientID())

		receiver := transfer.NewRTCReceiver(client, key, pin, saveDir)
		if trustedStore != nil {
			receiver.SetTrustedStore(trustedStore)
		}
		if reqPairing {
			receiver.SetRequirePairing(true)
		}
		if len(customSTUN) > 0 {
			receiver.SetSTUNServers(customSTUN)
		}
		if len(extRoutes) > 0 {
			receiver.SetExtensionRoutes(extRoutes)
		}
		if logTransfer != nil {
			receiver.OnFileReceived(logTransfer)
		}
		receiver.OnTransferActivity(transferStart, transferDone)
		receiver.OnSelectFiles(func(files []transfer.RTCFileDto) []string {
			ids := make([]string, 0, len(files))
			for _, f := range files {
				if !utils.IsExtensionAllowed(f.FileName, allowedExts) {
					slog.Info("Rejecting file (extension not allowed)", "name", f.FileName)
					continue
				}
				slog.Info("Accepting file via WebRTC", "name", f.FileName, "size", f.Size)
				ids = append(ids, f.ID)
			}
			return ids
		})
		receiver.ListenForOffersWithContext(ctx, func(offer signaling.WsServerMessage) {
			peerAlias := "unknown"
			if offer.Peer != nil {
				peerAlias = offer.Peer.Alias
			}
			slog.Info("Received WebRTC offer", "peer", peerAlias)
			if err := receiver.AcceptOffer(offer); err != nil {
				if errors.Is(err, transfer.ErrReceiverBusy) {
					slog.Debug("Ignoring WebRTC offer while receiver is busy", "peer", peerAlias)
					return
				}
				slog.Error("Failed to accept offer", "peer", peerAlias, "error", err)
			}
		})

		select {
		case <-ctx.Done():
			_ = receiver.Close()
			_ = client.Close()
			if signalingIDFile != "" {
				_ = os.Remove(signalingIDFile)
			}
			return nil
		case <-client.Done():
			// Clear the stale server-assigned ID before cleanup/reconnect so a
			// concurrent scan never excludes a dead signaling identity.
			if signalingIDFile != "" {
				_ = os.Remove(signalingIDFile)
			}
			_ = receiver.Close()
			_ = client.Close()
			slog.Warn("WebRTC signaling disconnected; reconnecting")
		}

		backoff = reconnectBackoffAfterSession(backoff, time.Since(connectedAt))
		if !waitReconnect(ctx, backoff) {
			return nil
		}
		backoff = nextReconnectBackoff(backoff)
	}
	return nil
}

func init() {
	Cmd.PersistentFlags().StringVarP(&devname, "devname", "n", lsutils.GenAlias(), "Device name that is advertising")
	Cmd.PersistentFlags().StringVarP(&savetodir, "dir", "d", ".", "Directory for received files")
	Cmd.PersistentFlags().StringVarP(&pin, "pin", "p", "", "PIN code")
	Cmd.PersistentFlags().BoolVar(&supportHttps, "https", true, "Do https")
	Cmd.PersistentFlags().StringVarP(&acceptExt, "accept-ext", "a", "", "Comma-separated list of allowed file extensions (e.g., epub,pdf,mobi). Empty means accept all.")
	Cmd.PersistentFlags().StringVarP(&logFile, "log", "l", "", "Path to transfer log file (JSON lines format)")
	Cmd.PersistentFlags().BoolVarP(&webrtcMode, "webrtc", "w", true, "Listen for WebRTC offers via signaling server (v3 protocol)")
	Cmd.PersistentFlags().StringVar(&extRouting, "ext-routing", "", "Path to extension routing config (JSON). Routes files to different directories by extension.")
	Cmd.PersistentFlags().StringVar(&onTransferCmd, "on-transfer", "", "Shell command to run after each file transfer completes (WARNING: executed with full shell privileges via 'sh -c')")
	Cmd.PersistentFlags().StringVar(&transferNotifyFile, "notify-file", "", "Rewrite this file after each completed transfer")
	Cmd.PersistentFlags().StringVar(&transferBusyFile, "busy-file", "", "Create this file while one or more file writes are active")
	Cmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "Config directory for trusted devices persistence")
	Cmd.PersistentFlags().BoolVar(&requirePairing, "require-pairing", false, "Require PAIR before accepting WebRTC transfers from unknown devices")
	Cmd.PersistentFlags().StringSliceVar(&stunServers, "stun-servers", nil, "Custom STUN servers for WebRTC (e.g., stun:stun.example.com:3478). Defaults to Google STUN servers if not set.")
	Cmd.PersistentFlags().StringVar(&signalingIDFile, "signaling-id-file", "", "Write WebRTC signaling client ID to this file (for self-filtering in scan)")
}
