package recv

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	lsutils "localsend-cli/internal/localsend/utils"
)

func TestFileReceiver_HTTPSRequiresClientCertificate(t *testing.T) {
	tmp := t.TempDir()
	serverCert, err := lsutils.GenAndSaveTLScert(filepath.Join(tmp, "server.key"), filepath.Join(tmp, "server.crt"))
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := lsutils.GenAndSaveTLScert(filepath.Join(tmp, "client.key"), filepath.Join(tmp, "client.crt"))
	if err != nil {
		t.Fatal(err)
	}
	fr := NewFileReceiver("Test Device", tmp, false)
	if err := fr.Init(); err != nil {
		t.Fatal(err)
	}
	fr.supportHttps = true
	fr.cert = serverCert
	fr.SetListenAddr("127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("receiver failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("receiver did not stop")
		}
	})
	select {
	case <-fr.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("receiver did not become ready")
	}
	addr := fr.ListenAddr()

	withoutCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if resp, err := withoutCert.Get(fmt.Sprintf("https://%s/api/localsend/v2/info", addr)); err == nil {
		_ = resp.Body.Close()
		t.Fatal("HTTPS receiver accepted a client without a certificate")
	}

	withCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{clientCert},
	}}}
	resp, err := withCert.Get(fmt.Sprintf("https://%s/api/localsend/v2/info", addr))
	if err != nil {
		t.Fatalf("HTTPS receiver rejected certificate-bearing client: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
}
