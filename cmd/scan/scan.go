package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend"
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
	Run: func(cmd *cobra.Command, args []string) {
		if !jsonOutput {
			slog.Info("Start Scanning")
		}

		alias := devName
		if alias == "" {
			alias = utils.GenAlias()
		}

		// Discovery HTTPS is mutual TLS in native LocalSend 1.18. The scanner
		// therefore uses the same persistent LocalSend identity as send/recv,
		// rather than an anonymous HTTP-only identity.
		privateKeyFile, certFile, err := utils.GetCertPaths()
		if err != nil {
			slog.Error("Failed to locate scanner certificate", "error", err)
			return
		}
		cert, err := utils.LoadOrGenTLScert(privateKeyFile, certFile)
		if err != nil {
			slog.Error("Failed to load scanner certificate", "error", err)
			return
		}
		fingerprint, err := utils.CertificateFingerprint(cert)
		if err != nil {
			slog.Error("Failed to fingerprint scanner certificate", "error", err)
			return
		}
		identity := models.NewDeviceInfo(alias, fingerprint)
		scanner, err := localsend.NewDiscovererWithCertificate(identity, true, cert)
		if err != nil {
			slog.Error("Fail to create advertiser", "error", err)
			return
		}
		defer func() { _ = scanner.Shutdown() }()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
		defer cancel()
		legacyDuration := timeout
		if legacyTimeout > 0 {
			legacyDuration = legacyTimeout
		}
		legacyCtx, cancelLegacy := context.WithTimeout(ctx, time.Second*time.Duration(legacyDuration))
		defer cancelLegacy()

		lanRequested := cmd.Flags().Changed("lan")
		legacyRequested := cmd.Flags().Changed("legacy")
		webrtcRequested := cmd.Flags().Changed("webrtc")
		useLAN, useLegacy, useWebRTC := lan, legacy, webrtc
		fallbackLegacy := false
		if !lanRequested && !legacyRequested && !webrtcRequested {
			// Native 1.18 discovers in stages: cheap multicast first, /24 probing
			// only when multicast produced no confirmation. Web signaling runs in
			// parallel because it is a separate compatibility surface.
			useLAN = true
			useWebRTC = true
			useLegacy = false
			fallbackLegacy = true
		}

		var callbackServer *registerCallbackServer
		if useLAN {
			callbackServer, err = startRegisterCallbackServer(ctx, scanner, cert, identity)
			if err != nil {
				slog.Warn("Could not start discovery register callback listener", "error", err)
				// Listening for announcements can still confirm peers directly, but
				// force the fallback because announcement callbacks are unavailable.
				fallbackLegacy = true
			} else {
				scanner.SetAdvertisedEndpoint(callbackServer.port, "https")
				defer func() { _ = callbackServer.shutdown() }()
			}
		}

		var wg sync.WaitGroup
		if useLAN {
			if !jsonOutput {
				slog.Info("Performing LAN discovery")
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := scanner.Listen(); err != nil && ctx.Err() == nil {
					slog.Warn("LAN discovery listener stopped", "error", err)
				}
			}()
		}

		if useLegacy {
			if !jsonOutput {
				slog.Info("Performing legacy HTTP subnet scan")
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				scanner.ScanSubnet(legacyCtx)
			}()
		} else if fallbackLegacy {
			wg.Add(1)
			go func() {
				defer wg.Done()
				timer := time.NewTimer(750 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				if len(scanner.GetAllDiscovered()) == 0 {
					if !jsonOutput {
						slog.Info("Multicast discovery was silent; falling back to subnet scan")
					}
					scanner.ScanSubnet(legacyCtx)
				}
			}()
		}

		var signalingPeers []signaling.ClientInfo
		var signalingDone chan []signaling.ClientInfo
		if useWebRTC {
			if !jsonOutput {
				slog.Info("Connecting to WebRTC signaling server")
			}
			signalingDone = make(chan []signaling.ClientInfo, 1)
			go func() { signalingDone <- discoverViaSignaling(ctx, jsonOutput, alias) }()
		}

		<-ctx.Done()
		if !jsonOutput {
			slog.Info("Stop Scanning")
		}
		_ = scanner.Shutdown()
		if callbackServer != nil {
			_ = callbackServer.shutdown()
		}
		wg.Wait()
		if signalingDone != nil {
			signalingPeers = <-signalingDone
		}

		devlist := scanner.GetAllDiscovered()

		var excludeID string
		if excludeIDFile != "" {
			if data, err := os.ReadFile(excludeIDFile); err == nil {
				excludeID = strings.TrimSpace(string(data))
			}
		}
		if excludeID != "" {
			filtered := make([]signaling.ClientInfo, 0, len(signalingPeers))
			for _, peer := range signalingPeers {
				if peer.ID.String() != excludeID {
					filtered = append(filtered, peer)
				}
			}
			signalingPeers = filtered
		}

		if jsonOutput {
			result := ScanResult{LAN: make([]LANDevice, 0, len(devlist)), WebRTC: make([]WebRTCDevice, 0, len(signalingPeers))}
			for ip, info := range devlist {
				result.LAN = append(result.LAN, LANDevice{IP: ip, Port: info.Port, Alias: info.Alias, Version: info.Version, Protocol: info.Protocol})
			}
			for _, peer := range signalingPeers {
				result.WebRTC = append(result.WebRTC, WebRTCDevice{ID: peer.ID.String(), Alias: peer.Alias, Version: peer.Version})
			}
			output, err := json.Marshal(result)
			if err != nil {
				slog.Error("Failed to marshal JSON", "error", err)
				return
			}
			fmt.Println(string(output))
		} else if len(devlist) > 0 || len(signalingPeers) > 0 {
			_, _ = fmt.Fprintln(os.Stdout, "Found Devices:")
			for ip, info := range devlist {
				_, _ = fmt.Fprintf(os.Stdout, "\t[LAN] Name: %s, Version: %s, Address: %s:%d, Protocol: %s\n", info.Alias, info.Version, ip, info.Port, info.Protocol)
			}
			for _, peer := range signalingPeers {
				_, _ = fmt.Fprintf(os.Stdout, "\t[WebRTC] Name: %s, Version: %s, ID: %s\n", peer.Alias, peer.Version, peer.ID)
			}
		} else {
			fmt.Fprintln(os.Stderr, "No device found")
		}
	},
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
}
