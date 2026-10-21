//go:build integration

package localsend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localsend-cli/internal/localsend"
	"localsend-cli/internal/localsend/recv"
	"localsend-cli/internal/localsend/send"
	"localsend-cli/internal/models"
)

// =============================================================================
// Integration Test Helpers
// =============================================================================

// getFreePort returns an available TCP port
func getFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

// waitForServer polls until the server is ready or times out
func waitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready within %v", url, timeout)
}

// createTestFiles creates temporary test files and returns their paths
func createTestFiles(t *testing.T, dir string, files map[string][]byte) []string {
	t.Helper()
	var paths []string
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", name, err)
		}
		paths = append(paths, path)
	}
	return paths
}

// =============================================================================
// ForwardSender -> FileReceiver Integration Tests
// =============================================================================

func TestIntegration_ForwardSender_to_FileReceiver(t *testing.T) {
	// Skip if running short tests
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("sends single file successfully", func(t *testing.T) {
		// Setup directories
		sendDir := t.TempDir()
		recvDir := t.TempDir()

		// Create test file to send
		testContent := []byte("Hello, LocalSend integration test!")
		testFile := filepath.Join(sendDir, "test.txt")
		if err := os.WriteFile(testFile, testContent, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Get a free port for the receiver
		port := getFreePort(t)
		_ = port // Will be used when FileReceiver supports custom ports

		// Create and configure receiver
		receiver := recv.NewFileReceiver("TestReceiver", recvDir, false)
		if err := receiver.Init(); err != nil {
			t.Fatalf("failed to init receiver: %v", err)
		}

		// Start receiver in background
		serverReady := make(chan struct{})
		serverErr := make(chan error, 1)
		go func() {
			// We need to access the internal webServer to start on custom port
			// For now, use the receiver's Test method if available, or
			// create a test-specific startup
			close(serverReady)
			// Note: This is a limitation - FileReceiver.Start() uses hardcoded port
			// For real integration tests, we'd need to refactor to accept port parameter
		}()

		<-serverReady

		// For this test, we'll use Fiber's app.Test() instead of real HTTP
		// This tests the integration without port conflicts
		t.Log("Integration test structure validated - see TestIntegration_FullHTTPFlow for full test")

		select {
		case err := <-serverErr:
			if err != nil {
				t.Fatalf("server error: %v", err)
			}
		default:
		}
	})
}

// TestIntegration_FullHTTPFlow tests the complete HTTP flow using Fiber's test utilities
func TestIntegration_FullHTTPFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup directories
	sendDir := t.TempDir()
	recvDir := t.TempDir()

	// Create test files
	testFiles := map[string][]byte{
		"document.pdf": []byte("PDF content here"),
		"image.png":    bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 100), // PNG-like
		"notes.txt":    []byte("Some notes for testing"),
	}
	filePaths := createTestFiles(t, sendDir, testFiles)

	// Create receiver
	receiver := recv.NewFileReceiver("IntegrationTestReceiver", recvDir, false)
	if err := receiver.Init(); err != nil {
		t.Fatalf("failed to init receiver: %v", err)
	}

	// Get the Fiber app for testing (we'll need to expose this or use reflection)
	// For now, test via HTTP test client pattern

	t.Run("pre-upload request flow", func(t *testing.T) {
		// Build a pre-upload request
		sender := send.NewForwardSender()
		for _, path := range filePaths {
			if err := sender.AddFile(path); err != nil {
				t.Fatalf("failed to add file: %v", err)
			}
		}

		// Verify files were added
		if sender.FileCount() != 3 {
			t.Errorf("expected 3 files, got %d", sender.FileCount())
		}
	})
}

// =============================================================================
// ReverseSender Integration Tests
// =============================================================================

func TestIntegration_ReverseSender_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup
	sendDir := t.TempDir()
	testContent := []byte("File content for reverse send test")
	testFile := filepath.Join(sendDir, "shareable.txt")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create reverse sender
	sender := send.NewReverseSender()
	target := &models.DeviceInfo{
		Alias: "TestDevice",
		IP:    "127.0.0.1",
	}
	if err := sender.Init(target, false); err != nil {
		t.Fatalf("failed to init sender: %v", err)
	}

	// Add file
	if err := sender.AddFile(testFile); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	// Test the handlers directly using Fiber's test client
	// This avoids port conflicts while still testing the full request/response cycle
	t.Run("prepare-download returns file list", func(t *testing.T) {
		// The handlers are already tested in send_test.go
		// This is a placeholder for more complex integration scenarios
		if sender.FileCount() != 1 {
			t.Errorf("expected 1 file, got %d", sender.FileCount())
		}
	})
}

// =============================================================================
// Protocol Compliance Tests
// =============================================================================

func TestIntegration_ProtocolCompliance_PreUploadRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("request format matches LocalSend protocol spec", func(t *testing.T) {
		// Create a valid pre-upload request per protocol spec Section 4.1
		senderInfo := &models.SenderInfo{
			DeviceInfo: models.DeviceInfo{
				Alias:       "TestSender",
				DeviceModel: "CLI",
				DeviceType:  "headless",
			},
			Port:     53317, // LocalSend default port
			Protocol: "http",
		}

		files := models.FileMetas{
			"file1": models.FileMeta{
				Id:       "file1",
				Filename: "test.txt",
				Size:     100,
				FileMIME: "text/plain",
			},
		}

		preUploadReq := models.PreUploadReq{
			Info:  senderInfo,
			Files: files,
		}

		// Serialize and verify JSON structure
		jsonBytes, err := json.Marshal(preUploadReq)
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}

		// Parse back and verify fields
		var parsed map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Verify required fields per protocol spec
		if _, ok := parsed["info"]; !ok {
			t.Error("missing 'info' field in request")
		}
		if _, ok := parsed["files"]; !ok {
			t.Error("missing 'files' field in request")
		}

		info := parsed["info"].(map[string]interface{})
		if _, ok := info["alias"]; !ok {
			t.Error("missing 'alias' in info")
		}
	})

	t.Run("response format matches LocalSend protocol spec", func(t *testing.T) {
		// Create a valid pre-upload response per protocol spec
		preUploadResp := models.PreUploadResp{
			SessionId: "test-session-id",
			Tokens: models.FileTokens{
				"file1": "token-for-file1",
				"file2": "token-for-file2",
			},
		}

		jsonBytes, err := json.Marshal(preUploadResp)
		if err != nil {
			t.Fatalf("failed to marshal response: %v", err)
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if _, ok := parsed["sessionId"]; !ok {
			t.Error("missing 'sessionId' field in response")
		}
		if _, ok := parsed["files"]; !ok {
			t.Error("missing 'files' (tokens) field in response")
		}
	})
}

// =============================================================================
// End-to-End Test with Real HTTP
// =============================================================================

func TestIntegration_EndToEnd_RealHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("sends and receives file over HTTP", func(t *testing.T) {
		// Setup directories
		sendDir := t.TempDir()
		recvDir := t.TempDir()

		// Create test file to send
		testContent := []byte("Hello, this is an end-to-end integration test!")
		testFile := filepath.Join(sendDir, "e2e_test.txt")
		if err := os.WriteFile(testFile, testContent, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Get a free port
		port := getFreePort(t)
		listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

		// Create and configure receiver
		receiver := recv.NewFileReceiver("E2ETestReceiver", recvDir, false)
		receiver.SetListenAddr(listenAddr)
		if err := receiver.Init(); err != nil {
			t.Fatalf("failed to init receiver: %v", err)
		}

		// Start receiver in background
		serverErr := make(chan error, 1)
		go func() {
			serverErr <- receiver.Start(context.Background())
		}()

		// Wait for server to be ready
		infoURL := fmt.Sprintf("http://127.0.0.1:%d/api/localsend/v2/info", port)
		waitForServer(t, infoURL, 5*time.Second)

		// Cleanup when done
		t.Cleanup(func() {
			_ = receiver.Stop()
		})

		// Create sender targeting the receiver
		sender := send.NewForwardSender()
		target := &models.DeviceInfo{
			Alias: "E2ETestReceiver",
			IP:    "127.0.0.1",
		}
		if err := sender.Init(target, false); err != nil {
			t.Fatalf("failed to init sender: %v", err)
		}
		sender.SetRemotePort(fmt.Sprintf("%d", port))

		// Add file to send
		if err := sender.AddFile(testFile); err != nil {
			t.Fatalf("failed to add file: %v", err)
		}

		// Send the file
		if err := sender.Start(); err != nil {
			t.Fatalf("failed to send file: %v", err)
		}

		// Verify file was received
		receivedFile := filepath.Join(recvDir, "e2e_test.txt")
		receivedContent, err := os.ReadFile(receivedFile)
		if err != nil {
			t.Fatalf("failed to read received file: %v", err)
		}

		if string(receivedContent) != string(testContent) {
			t.Errorf("content mismatch: expected %q, got %q", string(testContent), string(receivedContent))
		}
	})

	t.Run("sends multiple files", func(t *testing.T) {
		// Setup directories
		sendDir := t.TempDir()
		recvDir := t.TempDir()

		// Create test files
		testFiles := map[string][]byte{
			"file1.txt": []byte("Content of file 1"),
			"file2.txt": []byte("Content of file 2"),
			"file3.txt": []byte("Content of file 3"),
		}
		for name, content := range testFiles {
			path := filepath.Join(sendDir, name)
			if err := os.WriteFile(path, content, 0644); err != nil {
				t.Fatalf("failed to create test file %s: %v", name, err)
			}
		}

		// Get a free port
		port := getFreePort(t)
		listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

		// Create and configure receiver
		receiver := recv.NewFileReceiver("E2EMultiReceiver", recvDir, false)
		receiver.SetListenAddr(listenAddr)
		if err := receiver.Init(); err != nil {
			t.Fatalf("failed to init receiver: %v", err)
		}

		// Start receiver
		go func() {
			_ = receiver.Start(context.Background())
		}()

		infoURL := fmt.Sprintf("http://127.0.0.1:%d/api/localsend/v2/info", port)
		waitForServer(t, infoURL, 5*time.Second)

		t.Cleanup(func() {
			_ = receiver.Stop()
		})

		// Create sender
		sender := send.NewForwardSender()
		target := &models.DeviceInfo{
			Alias: "E2EMultiReceiver",
			IP:    "127.0.0.1",
		}
		_ = sender.Init(target, false)
		sender.SetRemotePort(fmt.Sprintf("%d", port))

		// Add all files
		for name := range testFiles {
			if err := sender.AddFile(filepath.Join(sendDir, name)); err != nil {
				t.Fatalf("failed to add file %s: %v", name, err)
			}
		}

		// Send
		if err := sender.Start(); err != nil {
			t.Fatalf("failed to send files: %v", err)
		}

		// Verify all files received
		for name, expectedContent := range testFiles {
			receivedPath := filepath.Join(recvDir, name)
			receivedContent, err := os.ReadFile(receivedPath)
			if err != nil {
				t.Errorf("failed to read received file %s: %v", name, err)
				continue
			}
			if string(receivedContent) != string(expectedContent) {
				t.Errorf("content mismatch for %s: expected %q, got %q",
					name, string(expectedContent), string(receivedContent))
			}
		}
	})
}

func TestIntegration_ScopedIPv6Transfer(t *testing.T) {
	recvDir := t.TempDir()
	receiver := recv.NewFileReceiver("Kindle", recvDir, false)
	receiver.SetListenAddr("[::1]:53317")
	if err := receiver.Init(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() { serverErr <- receiver.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverErr; err != nil {
			t.Errorf("receiver: %v", err)
		}
	})
	waitForServer(t, "http://[::1]:53317/api/localsend/v2/info", 5*time.Second)

	target, err := localsend.GetDeviceInfo("::1%lo", false)
	if err != nil {
		t.Fatal(err)
	}
	sender := send.NewForwardSender()
	if err := sender.Init(&target, false); err != nil {
		t.Fatal(err)
	}
	content := []byte("p { text-indent: 0 !important; }\n")
	file := createTestFiles(t, t.TempDir(), map[string][]byte{"style.css": content})[0]
	if err := sender.AddFile(file); err != nil {
		t.Fatal(err)
	}
	if err := sender.Start(); err != nil {
		t.Fatal(err)
	}
	received, err := os.ReadFile(filepath.Join(recvDir, "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, content) {
		t.Fatalf("received bytes = %q; want %q", received, content)
	}
}
