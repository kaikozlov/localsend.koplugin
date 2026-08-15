package recv

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
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
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	fr := newTestReceiver()
	fr.supportHttps = true
	fr.cert = serverCert
	fr.listenAddr = addr
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fr.Start(ctx) }()
	waitForTCPListener(t, addr)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("receiver did not stop")
		}
	})

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

func waitForTCPListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 20*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener %s did not start", addr)
}
