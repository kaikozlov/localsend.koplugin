package send

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"localsend-cli/internal/crypto"
	"localsend-cli/internal/localsend"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/storage"
	"localsend-cli/internal/utils"
	"localsend-cli/internal/webrtc/signaling"
	"localsend-cli/internal/webrtc/transfer"
)

var (
	ip                string
	files             []string
	supportHttps      bool
	pin               string
	useDownloadAPI    bool
	useWebRTC         bool
	targetID          string
	preserveStructure bool
	configDir         string
	stunServers       []string
	devName           string
	text              string
)

// maxStdinTextBytes bounds text read from stdin, which is held in memory until sent.
const maxStdinTextBytes = 16_000_000

var Cmd = &cobra.Command{
	Use:   "send [files]...",
	Short: "Send files or a text message to localsend instance",
	Long:  "Send files or a text message to localsend instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		files = append(files, args...)
		var message []byte
		if cmd.Flags().Changed("text") {
			if len(files) > 0 {
				return errors.New("--text cannot be combined with files")
			}
			if text == "-" {
				if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
					fmt.Fprintln(os.Stderr, "Reading the text from stdin, end it with Ctrl+D")
				}
			}
			var err error
			if message, err = readText(text, os.Stdin); err != nil {
				return err
			}
		} else if len(files) == 0 {
			return errors.New("file is required")
		}

		// WebRTC mode
		if useWebRTC {
			return sendViaWebRTC(message)
		}

		// HTTP mode (original)
		if ip == "" && !useDownloadAPI {
			return errors.New("IP address is required")
		}

		var err error

		// only request remote device info when download api is unused
		var devinfo models.DeviceInfo
		if !useDownloadAPI {
			devinfo, err = localsend.GetDeviceInfo(ip, supportHttps)
			if err != nil {
				slog.Error("Fail to get device info", "error", err)
				return err
			}
		} else {
			// For download API, use custom device name or generate one
			alias := devName
			if alias == "" {
				alias = lsutils.GenAlias()
			}
			devinfo = models.NewDeviceInfo(alias, lsutils.GenFingerprint())
		}

		sender := localsend.NewFileSender(useDownloadAPI)
		sender.SetPIN(pin)
		if devName != "" {
			sender.SetAlias(devName)
		}
		if err := sender.Init(&devinfo, supportHttps); err != nil {
			slog.Error("Failed to initialize sender", "error", err)
			return fmt.Errorf("sender initialization failed: %w", err)
		}

		if message != nil {
			sender.AddText(message)
		}
		// try to add every file
		for _, file := range files {
			finfo, err := os.Stat(file)
			if err != nil {
				slog.Error("Fail to probe file", "file", file, "error", err)
				continue
			}
			if finfo.IsDir() {
				if preserveStructure {
					err = sender.AddDirWithStructure(file)
				} else {
					err = sender.AddDir(file)
				}
				if err != nil {
					slog.Error("Fail to add dir, skipping...", "dir", file, "error", err)
					continue
				}
			} else {
				err = sender.AddFile(file)
				if err != nil {
					slog.Error("Fail to add file, skipping...", "file", file, "error", err)
					continue

				}
			}
			slog.Info("Start sending", "file", file)
		}

		// Channel to signal when sender.Start() completes, allowing signal goroutine to exit cleanly
		done := make(chan struct{})
		go func() {
			select {
			case <-utils.WaitForSignal():
				slog.Info("Abort")
				err := sender.Cancel()
				if err != nil {
					slog.Error("Fail to cancel", "error", err)
				}
			case <-done:
				// sender.Start() completed, exit goroutine
			}
		}()

		err = sender.Start()
		close(done) // Signal the goroutine to exit
		if err != nil {
			slog.Error("Fail to send", "error", err)
			return err
		}

		slog.Info("Done")
		return nil
	},
}

// readText returns the text to send: arg itself, or stdin when arg is "-".
// A single trailing line break is dropped from stdin.
func readText(arg string, stdin io.Reader) ([]byte, error) {
	message := []byte(arg)
	if arg == "-" {
		var err error
		message, err = io.ReadAll(io.LimitReader(stdin, maxStdinTextBytes+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read text from stdin: %w", err)
		}
		if len(message) > maxStdinTextBytes {
			return nil, fmt.Errorf("stdin is longer than %d MB; save it to a file and send the file instead", maxStdinTextBytes/1_000_000)
		}
		if trimmed, ok := bytes.CutSuffix(message, []byte("\n")); ok {
			message, _ = bytes.CutSuffix(trimmed, []byte("\r"))
		}
	}
	if len(message) == 0 {
		return nil, errors.New("nothing to send: the text is empty")
	}
	if !utf8.Valid(message) {
		return nil, errors.New("text is not UTF-8; save it to a file and send the file instead")
	}
	return message, nil
}

func textFileMeta(message []byte) transfer.FileMeta {
	meta := models.GenTextFileMeta(message)
	return transfer.FileMeta{
		ID:       meta.Id,
		FileName: meta.Filename,
		Size:     meta.Size,
		FileType: meta.FileMIME,
		SHA256:   meta.Checksum,
		Preview:  meta.Preview,
		Content:  meta.Content,
	}
}

func sendViaWebRTC(message []byte) error {
	if targetID == "" {
		return errors.New("target ID is required for WebRTC mode (use --target)")
	}

	target, err := uuid.Parse(targetID)
	if err != nil {
		return fmt.Errorf("invalid target ID: %w", err)
	}

	// Generate signing key and token
	key, token, err := crypto.GenerateKeyPairWithToken()
	if err != nil {
		return fmt.Errorf("failed to generate key pair with token: %w", err)
	}

	// Connect to signaling server
	// Use custom device name or generate one
	alias := devName
	if alias == "" {
		alias = lsutils.GenAlias()
	}
	info := signaling.NewClientInfo(alias, token)

	slog.Info("Connecting to WebRTC signaling server")
	client, err := signaling.Connect(signaling.DefaultSignalingServer, info)
	if err != nil {
		return fmt.Errorf("failed to connect to signaling server: %w", err)
	}
	defer func() { _ = client.Close() }()

	slog.Info("Connected to signaling server", "id", client.ClientID())

	// Prepare file metadata
	var fileMetas []transfer.FileMeta
	if message != nil {
		fileMetas = append(fileMetas, textFileMeta(message))
	}
	for _, file := range files {
		finfo, err := os.Stat(file)
		if err != nil {
			slog.Error("Failed to stat file", "file", file, "error", err)
			continue
		}
		if finfo.IsDir() {
			// Walk directory - use parent dir as base to include directory name in path
			baseDir := filepath.Dir(file)
			_ = filepath.Walk(file, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if preserveStructure {
					fileMetas = append(fileMetas, makeFileMetaWithBase(path, info, baseDir))
				} else {
					fileMetas = append(fileMetas, makeFileMeta(path, info))
				}
				return nil
			})
		} else {
			fileMetas = append(fileMetas, makeFileMeta(file, finfo))
		}
	}

	if len(fileMetas) == 0 {
		return errors.New("no valid files to send")
	}

	slog.Info("Prepared files", "count", len(fileMetas))
	for _, f := range fileMetas {
		slog.Info("File", "name", f.FileName, "size", f.Size)
	}

	// Create sender
	sender := transfer.NewRTCSender(client, key, pin)

	// Set custom STUN servers if configured
	if len(stunServers) > 0 {
		sender.SetSTUNServers(stunServers)
		slog.Info("Using custom STUN servers", "servers", stunServers)
	}

	// Initialize trusted device store if config dir is provided
	if configDir != "" {
		trustedStore, err := storage.NewTrustedDeviceStore(configDir)
		if err != nil {
			slog.Error("Failed to initialize trusted device store", "error", err)
			// Continue without trust - just won't persist
		} else {
			sender.SetTrustedStore(trustedStore)
			slog.Info("Trusted device store initialized", "config", configDir)
		}
	}

	// Set up PAIR confirmation callback (interactive prompt)
	sender.SetOnPairRequest(func(alias, fingerprint string) bool {
		fmt.Printf("\nDevice '%s' (fingerprint: %s) wants to pair.\n", alias, fingerprint)
		fmt.Print("Accept pairing? [y/N]: ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			slog.Warn("Failed to read input", "error", err)
			return false
		}

		response = strings.TrimSpace(strings.ToLower(response))
		return response == "y" || response == "yes"
	})

	// Send to target
	slog.Info("Sending offer to target", "target", target)
	if err := sender.Send(target, fileMetas); err != nil {
		_ = sender.Close()
		return fmt.Errorf("failed to initiate transfer: %w", err)
	}

	// Send files
	slog.Info("Sending files...")
	if err := sender.SendFiles(); err != nil {
		_ = sender.Close()
		return fmt.Errorf("failed to send files: %w", err)
	}

	_ = sender.Close()
	slog.Info("Transfer complete")
	return nil
}

func makeFileMeta(path string, info os.FileInfo) transfer.FileMeta {
	fileType := mime.TypeByExtension(filepath.Ext(path))
	if fileType == "" {
		fileType = "application/octet-stream"
	}
	checksum, accessed := fileIntegrityMetadata(path)
	return transfer.FileMeta{
		ID:       uuid.New().String(),
		FileName: info.Name(),
		FilePath: path,
		Size:     info.Size(),
		FileType: fileType,
		SHA256:   checksum,
		Modified: info.ModTime(),
		Accessed: accessed,
	}
}

// makeFileMetaWithBase creates FileMeta with relative path from baseDir for subdirectory preservation.
func makeFileMetaWithBase(path string, info os.FileInfo, baseDir string) transfer.FileMeta {
	fileType := mime.TypeByExtension(filepath.Ext(path))
	if fileType == "" {
		fileType = "application/octet-stream"
	}

	// Calculate relative path from baseDir
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		// Fall back to base name if relative path calculation fails
		relPath = info.Name()
	} else {
		// Normalize to forward slashes for protocol compatibility
		relPath = filepath.ToSlash(relPath)
	}
	checksum, accessed := fileIntegrityMetadata(path)

	return transfer.FileMeta{
		ID:       uuid.New().String(),
		FileName: relPath,
		FilePath: path,
		Size:     info.Size(),
		FileType: fileType,
		SHA256:   checksum,
		Modified: info.ModTime(),
		Accessed: accessed,
	}
}

func fileIntegrityMetadata(path string) (string, time.Time) {
	meta, err := models.GenFileMeta(path)
	if err != nil || meta.Metadata == nil {
		return "", time.Time{}
	}
	accessed, err := time.Parse(time.RFC3339Nano, meta.Metadata.Accessed)
	if err != nil {
		return meta.Checksum, time.Time{}
	}
	return meta.Checksum, accessed
}

func init() {
	Cmd.PersistentFlags().StringVar(&ip, "ip", "", "IP address of remote localsend instance")
	Cmd.PersistentFlags().StringSliceVarP(&files, "file", "f", []string{}, "File/Directory to be sent")
	Cmd.PersistentFlags().BoolVar(&supportHttps, "https", true, "Do https")
	Cmd.PersistentFlags().BoolVar(&useDownloadAPI, "dapi", false, "Use Download API(Reverse File Transfer)")
	Cmd.PersistentFlags().StringVarP(&pin, "pin", "p", "", "PIN code")
	Cmd.PersistentFlags().BoolVarP(&useWebRTC, "webrtc", "w", false, "Send via WebRTC signaling server")
	Cmd.PersistentFlags().StringVarP(&targetID, "target", "t", "", "Target peer ID (from scan --webrtc)")
	Cmd.PersistentFlags().BoolVar(&preserveStructure, "preserve-structure", true, "Preserve subdirectory structure when sending directories")
	Cmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "Config directory for trusted devices persistence")
	Cmd.PersistentFlags().StringSliceVar(&stunServers, "stun-servers", nil, "Custom STUN servers for WebRTC (e.g., stun:stun.example.com:3478). Defaults to Google STUN servers if not set.")
	Cmd.PersistentFlags().StringVarP(&devName, "devname", "n", "", "Device name to display to receiver")
	Cmd.PersistentFlags().StringVar(&text, "text", "", "Send this text as a message instead of files; - reads it from stdin")
}
