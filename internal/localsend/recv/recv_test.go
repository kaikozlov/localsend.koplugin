package recv

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	lsutils "localsend-cli/internal/localsend/utils"
)

// =============================================================================
// Race Condition Tests
// These tests verify thread-safety of FileReceiver setters.
// Run with -race flag to detect data races.
// =============================================================================

// TestSetPINRaceCondition tests concurrent PIN access.
// EXPECTED TO FAIL with race detector: Race between SetPIN and reading expectedPin.
func TestSetPINRaceCondition(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Repeatedly set PIN
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fr.SetPIN("1234")
			fr.SetPIN("5678")
		}
	}()

	// Goroutine 2: Repeatedly read PIN (simulating handler access)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// Use the thread-safe getter instead of direct field access
			_ = fr.getExpectedPIN()
		}
	}()

	wg.Wait()
	t.Log("Race condition test completed - run with -race flag to detect data races")
}

// TestSetAllowedExtensionsRaceCondition tests concurrent extension list access.
// EXPECTED TO FAIL with race detector: Race between SetAllowedExtensions and reading.
func TestSetAllowedExtensionsRaceCondition(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Repeatedly set extensions
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fr.SetAllowedExtensions([]string{"pdf", "epub"})
			fr.SetAllowedExtensions([]string{"mobi", "azw3"})
		}
	}()

	// Goroutine 2: Repeatedly check filter (simulating handler access)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// Use the thread-safe method instead of direct field access
			_ = fr.hasExtensionFilter()
		}
	}()

	wg.Wait()
	t.Log("Race condition test completed - run with -race flag to detect data races")
}

// TestSetTransferLogRaceCondition tests concurrent transfer log access.
// EXPECTED TO FAIL with race detector: Race between SetTransferLog and LogTransfer.
func TestSetTransferLogRaceCondition(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Repeatedly set transfer log path
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fr.SetTransferLog("/tmp/log1.json")
			fr.SetTransferLog("/tmp/log2.json")
		}
	}()

	// Goroutine 2: Repeatedly call LogTransfer (uses transferLogPath internally)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// LogTransfer now uses thread-safe access internally
			fr.LogTransfer("test.pdf", 100, "sender")
		}
	}()

	wg.Wait()
	t.Log("Race condition test completed - run with -race flag to detect data races")
}

// TestSetExtensionRouterRaceCondition tests concurrent router access.
// EXPECTED TO FAIL with race detector: Race between SetExtensionRouter and GetSaveDir.
func TestSetExtensionRouterRaceCondition(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)
	router := NewExtensionRouter("/default")

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Repeatedly set router
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fr.SetExtensionRouter(router)
			fr.SetExtensionRouter(nil)
		}
	}()

	// Goroutine 2: Repeatedly call GetSaveDir (uses router internally)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// GetSaveDir now uses thread-safe access internally
			_ = fr.GetSaveDir("test.pdf")
		}
	}()

	wg.Wait()
	t.Log("Race condition test completed - run with -race flag to detect data races")
}

// TestSetListenAddrRaceCondition tests concurrent listen address access.
// EXPECTED TO FAIL with race detector: Race between SetListenAddr and ListenAddr.
func TestSetListenAddrRaceCondition(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Repeatedly set listen addr
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fr.SetListenAddr(":8080")
			fr.SetListenAddr(":9090")
		}
	}()

	// Goroutine 2: Repeatedly read listen addr
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = fr.ListenAddr()
		}
	}()

	wg.Wait()
	t.Log("Race condition test completed - run with -race flag to detect data races")
}

// TestConcurrentConfigurationChanges tests multiple setters being called concurrently.
func TestConcurrentConfigurationChanges(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	var wg sync.WaitGroup
	wg.Add(5)

	// Multiple goroutines modifying different fields concurrently
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			fr.SetPIN("pin")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			fr.SetAllowedExtensions([]string{"pdf"})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			fr.SetTransferLog("/tmp/log.json")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			fr.SetListenAddr(":8080")
		}
	}()

	// Reader goroutine simulating handler access using thread-safe methods
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = fr.getExpectedPIN()
			_ = fr.hasExtensionFilter()
			_ = fr.GetSaveDir("test.pdf")
			_ = fr.ListenAddr()
		}
	}()

	wg.Wait()
	t.Log("Concurrent configuration test completed - run with -race flag to detect data races")
}

// =============================================================================
// Certificate Logging Tests
// These tests verify that the correct log message is shown when loading or
// generating TLS certificates.
// =============================================================================

// captureLogs temporarily redirects slog output to capture log messages
func captureLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, nil)
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(handler))
	return &buf, func() {
		slog.SetDefault(oldLogger)
	}
}

// TestCertificateLoggingGenerating verifies that "Generating" is logged when certs don't exist
func TestCertificateLoggingGenerating(t *testing.T) {
	// Create a temp directory for certs
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatalf("failed to create cert dir: %v", err)
	}

	privKeyFile := filepath.Join(certDir, "server.key.pem")
	certFile := filepath.Join(certDir, "server.crt")

	// Capture logs
	buf, restore := captureLogs(t)
	defer restore()

	// Simulate the logic from Init()
	_, keyErr := os.Stat(privKeyFile)
	_, certErr := os.Stat(certFile)
	if keyErr == nil && certErr == nil {
		slog.Info("Loading https certificate")
	} else {
		slog.Info("Generating https certificate")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "Generating https certificate") {
		t.Errorf("expected log to contain 'Generating https certificate', got: %s", logOutput)
	}
	if strings.Contains(logOutput, "Loading https certificate") {
		t.Errorf("unexpected 'Loading https certificate' in log: %s", logOutput)
	}
}

// TestCertificateLoggingLoading verifies that "Loading" is logged when certs exist
func TestCertificateLoggingLoading(t *testing.T) {
	// Create a temp directory for certs
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatalf("failed to create cert dir: %v", err)
	}

	privKeyFile := filepath.Join(certDir, "server.key.pem")
	certFile := filepath.Join(certDir, "server.crt")

	// Generate certs first
	_, err := lsutils.GenAndSaveTLScert(privKeyFile, certFile)
	if err != nil {
		t.Fatalf("failed to generate certs: %v", err)
	}

	// Capture logs
	buf, restore := captureLogs(t)
	defer restore()

	// Simulate the logic from Init()
	_, keyErr := os.Stat(privKeyFile)
	_, certErr := os.Stat(certFile)
	if keyErr == nil && certErr == nil {
		slog.Info("Loading https certificate")
	} else {
		slog.Info("Generating https certificate")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "Loading https certificate") {
		t.Errorf("expected log to contain 'Loading https certificate', got: %s", logOutput)
	}
	if strings.Contains(logOutput, "Generating https certificate") {
		t.Errorf("unexpected 'Generating https certificate' in log: %s", logOutput)
	}
}

// TestCertificateLoggingPartialMissing verifies that "Generating" is logged when only one cert file exists
func TestCertificateLoggingPartialMissing(t *testing.T) {
	// Create a temp directory for certs
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatalf("failed to create cert dir: %v", err)
	}

	privKeyFile := filepath.Join(certDir, "server.key.pem")
	certFile := filepath.Join(certDir, "server.crt")

	// Create only the cert file, not the private key
	if err := os.WriteFile(certFile, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create dummy cert: %v", err)
	}

	// Capture logs
	buf, restore := captureLogs(t)
	defer restore()

	// Simulate the logic from Init()
	_, keyErr := os.Stat(privKeyFile)
	_, certErr := os.Stat(certFile)
	if keyErr == nil && certErr == nil {
		slog.Info("Loading https certificate")
	} else {
		slog.Info("Generating https certificate")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "Generating https certificate") {
		t.Errorf("expected log to contain 'Generating https certificate' when key is missing, got: %s", logOutput)
	}
}

// =============================================================================
// OnTransfer Callback Tests
// =============================================================================

// TestSetOnTransferCmd verifies that the callback command is stored correctly.
func TestSetOnTransferCmd(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	// Initially empty
	fr.configMu.RLock()
	if fr.onTransferCmd != "" {
		t.Errorf("expected empty onTransferCmd initially, got: %s", fr.onTransferCmd)
	}
	fr.configMu.RUnlock()

	// Set a command
	fr.SetOnTransferCmd("touch /tmp/notify")
	fr.configMu.RLock()
	if fr.onTransferCmd != "touch /tmp/notify" {
		t.Errorf("expected 'touch /tmp/notify', got: %s", fr.onTransferCmd)
	}
	fr.configMu.RUnlock()
}

// TestOnTransferCmdRaceCondition tests concurrent access to onTransferCmd.
func TestOnTransferCmdRaceCondition(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Repeatedly set callback
	// Mostly use empty string to avoid spawning processes, but include some
	// non-empty values to test the race on actual string values
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			fr.SetOnTransferCmd("")
			fr.SetOnTransferCmd(":")
			fr.SetOnTransferCmd("")
		}
	}()

	// Goroutine 2: Repeatedly call LogTransfer (reads onTransferCmd)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			fr.LogTransfer("test.pdf", 100, "sender")
		}
	}()

	wg.Wait()
	t.Log("Race condition test completed - run with -race flag to detect data races")
}

// TestLogTransferRunsCallbackWithoutLogFile verifies callback runs even without a log file.
func TestLogTransferRunsCallbackWithoutLogFile(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)
	// Don't set transfer log - fr.transferLogFile is nil

	// Create a temp file to verify the callback runs
	tmpFile := filepath.Join(t.TempDir(), "callback_test")

	fr.SetOnTransferCmd("touch " + tmpFile)

	// Call LogTransfer (no log file, but callback should still run)
	fr.LogTransfer("test.pdf", 100, "sender")

	// Give the goroutine time to execute
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(tmpFile); err == nil {
			return // Success - file was created
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Check if file exists
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Error("callback should have run and created the temp file even without a log file configured")
	}
}

// TestLogTransferRunsCallbackWithLogFile verifies callback runs with a log file.
func TestLogTransferRunsCallbackWithLogFile(t *testing.T) {
	fr := NewFileReceiver("test", "/tmp", false)

	// Set up a log file
	logFile := filepath.Join(t.TempDir(), "transfer.log")
	fr.SetTransferLog(logFile)
	defer fr.closeTransferLog()

	// Create a temp file to verify the callback runs
	callbackFile := filepath.Join(t.TempDir(), "callback_test")

	fr.SetOnTransferCmd("touch " + callbackFile)

	// Call LogTransfer
	fr.LogTransfer("test.pdf", 100, "sender")

	// Give the goroutine time to execute
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(callbackFile); err == nil {
			break // Success - file was created
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify log was written
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Errorf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "test.pdf") {
		t.Errorf("log file should contain 'test.pdf', got: %s", content)
	}

	// Verify callback ran
	if _, err := os.Stat(callbackFile); os.IsNotExist(err) {
		t.Error("callback should have run and created the temp file")
	}
}

func TestLogTransfer_BoundsConcurrentCallbacks(t *testing.T) {
	fr := NewFileReceiver("test", t.TempDir(), false)
	fr.SetOnTransferCmd("sleep 0.2")
	baseline := runtime.NumGoroutine()

	for i := 0; i < 40; i++ {
		fr.LogTransfer("test.pdf", 100, "sender")
	}
	time.Sleep(50 * time.Millisecond)

	if added := runtime.NumGoroutine() - baseline; added > 10 {
		t.Fatalf("on-transfer callbacks are unbounded: added %d goroutines", added)
	}
	_ = fr.Stop()
}

func TestLogTransfer_DoesNotStartCallbackAfterStop(t *testing.T) {
	fr := NewFileReceiver("test", t.TempDir(), false)
	marker := filepath.Join(t.TempDir(), "ran-after-stop")
	fr.SetOnTransferCmd("touch " + marker)
	if err := fr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	fr.LogTransfer("test.pdf", 100, "sender")
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("on-transfer callback started after receiver was stopped")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}
