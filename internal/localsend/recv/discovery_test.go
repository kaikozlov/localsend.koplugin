package recv

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localsend-cli/internal/localsend/constants"
)

func startDiscoveryTestReceiver(t *testing.T, discoveryOnly bool) (*FileReceiver, string, context.CancelFunc, <-chan error) {
	t.Helper()
	saveDir := filepath.Join(t.TempDir(), "must-not-exist")
	fr := NewFileReceiver("discovery-test", saveDir, false)
	fr.SetDiscoveryOnly(discoveryOnly)
	fr.SetListenAddr("127.0.0.1:0")
	if err := fr.Init(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = fr.Stop()
	})
	select {
	case <-fr.Ready():
	case err := <-done:
		t.Fatalf("receiver startup failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("receiver never became ready")
	}
	return fr, "http://" + fr.ListenAddr(), cancel, done
}

func postDiscoveryTest(t *testing.T, endpoint, body string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post(endpoint, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestDiscoveryOnlyRejectsTransfersButRegistersPeers(t *testing.T) {
	fr, endpoint, cancel, done := startDiscoveryTestReceiver(t, true)
	if _, err := os.Stat(fr.saveToDir); !os.IsNotExist(err) {
		t.Fatalf("discovery-only Init touched save directory: %v", err)
	}
	body := `{"info":{"alias":"sender","version":"2.2","fingerprint":"sender"},"files":{"file":{"id":"file","fileName":"book.epub","size":4,"fileType":"application/epub+zip"}}}`
	if resp := postDiscoveryTest(t, endpoint+constants.PreuploadPath, body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("prepare status = %d", resp.StatusCode)
	}
	if fr.sessman.HasActiveSessions() {
		t.Fatal("discovery-only prepare accepted a transfer session")
	}
	if resp := postDiscoveryTest(t, endpoint+constants.UploadPath+"?sessionId=x&fileId=x&token=x", "book"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	registration := `{"alias":"peer","version":"2.2","fingerprint":"peer","port":53317,"protocol":"http"}`
	if resp := postDiscoveryTest(t, endpoint+constants.RegisterPath, registration); resp.StatusCode != http.StatusOK {
		t.Fatalf("registration status = %d", resp.StatusCode)
	}
	peers := fr.currentDiscoverer().DiscoveredSince(time.Time{})
	if peers["127.0.0.1"].Alias != "peer" {
		t.Fatalf("register callback was not stored by the shared discoverer: %v", peers)
	}
	if _, err := os.Stat(fr.saveToDir); !os.IsNotExist(err) {
		t.Fatalf("discovery-only upload touched save directory: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("receiver did not shut down")
	}
}

func TestReceiverReadinessDoesNotCloseOnBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	fr := NewFileReceiver("busy", "", false)
	fr.SetDiscoveryOnly(true)
	fr.SetListenAddr(occupied.Addr().String())
	if err := fr.Init(); err != nil {
		t.Fatal(err)
	}
	if err := fr.Start(context.Background()); err == nil {
		t.Fatal("receiver started on an occupied port")
	}
	select {
	case <-fr.Ready():
		t.Fatal("receiver announced readiness after bind failure")
	default:
	}
	if err := fr.Stop(); err != nil {
		t.Fatal(err)
	}
}
