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

		// Use custom device name or generate one
		alias := devName
		if alias == "" {
			alias = utils.GenAlias()
		}

		scanner, err := localsend.NewDiscoverer(
			models.NewDeviceInfo(alias, utils.GenFingerprint()),
			false)
		if err != nil {
			slog.Error("Fail to create advertiser", "error", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
		defer cancel()
		legacyDuration := timeout
		if legacyTimeout > 0 {
			legacyDuration = legacyTimeout
		}
		legacyCtx, cancelLegacy := context.WithTimeout(context.Background(), time.Second*time.Duration(legacyDuration))
		defer cancelLegacy()

		// If no protocol flags are set, enable all discovery methods
		if !cmd.Flags().Changed("lan") && !cmd.Flags().Changed("legacy") && !cmd.Flags().Changed("webrtc") {
			lan = true
			legacy = true
			webrtc = true
		}

		var wg sync.WaitGroup
		if lan {
			if !jsonOutput {
				slog.Info("Performing LAN discovery")
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = scanner.Listen()
			}()
		}

		if legacy {
			if !jsonOutput {
				slog.Info("Performing legacy HTTP subnet scan")
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				scanner.ScanSubnet(legacyCtx)
			}()
		}

		// WebRTC signaling discovery
		var signalingPeers []signaling.ClientInfo
		if webrtc {
			if !jsonOutput {
				slog.Info("Connecting to WebRTC signaling server")
			}
			signalingPeers = discoverViaSignaling(ctx, jsonOutput, alias)
		}

		<-ctx.Done()
		if !jsonOutput {
			slog.Info("Stop Scanning")
		}
		_ = scanner.Shutdown()
		wg.Wait()

		devlist := scanner.GetAllDiscovered()

		// Read exclude ID from file if specified (for self-filtering)
		var excludeID string
		if excludeIDFile != "" {
			if data, err := os.ReadFile(excludeIDFile); err == nil {
				excludeID = strings.TrimSpace(string(data))
			}
		}

		// Filter out excluded WebRTC peer
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
			// JSON output mode
			result := ScanResult{
				LAN:    make([]LANDevice, 0, len(devlist)),
				WebRTC: make([]WebRTCDevice, 0, len(signalingPeers)),
			}

			for ip, info := range devlist {
				result.LAN = append(result.LAN, LANDevice{
					IP:       ip,
					Port:     info.Port,
					Alias:    info.Alias,
					Version:  info.Version,
					Protocol: info.Protocol,
				})
			}

			for _, peer := range signalingPeers {
				result.WebRTC = append(result.WebRTC, WebRTCDevice{
					ID:      peer.ID.String(),
					Alias:   peer.Alias,
					Version: peer.Version,
				})
			}

			output, err := json.Marshal(result)
			if err != nil {
				slog.Error("Failed to marshal JSON", "error", err)
				return
			}
			fmt.Println(string(output))
		} else {
			// Human-readable output mode
			if len(devlist) > 0 || len(signalingPeers) > 0 {
				_, _ = fmt.Fprintf(os.Stdout, "Found Devices: \n")

				// LAN devices
				for ip, info := range devlist {
					_, _ = fmt.Fprintf(os.Stdout, "\t[LAN] Name: %s, Version: %s, Address: %s:%d, Protocol: %s\n",
						info.Alias, info.Version, ip, info.Port, info.Protocol)
				}

				// WebRTC signaling peers
				for _, peer := range signalingPeers {
					_, _ = fmt.Fprintf(os.Stdout, "\t[WebRTC] Name: %s, Version: %s, ID: %s\n",
						peer.Alias, peer.Version, peer.ID)
				}
			} else {
				fmt.Fprintln(os.Stderr, "No device found")
			}
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
