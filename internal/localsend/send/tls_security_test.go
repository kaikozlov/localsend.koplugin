package send

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	coreutils "localsend-cli/internal/utils"
)

func TestForwardSender_Init_HTTPSUsesCertificateIdentity(t *testing.T) {
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{IP: "192.0.2.1", Fingerprint: "peer"}, true); err != nil {
		t.Fatal(err)
	}
	if sender.httpClient.TLSConfig.GetClientCertificate == nil {
		t.Fatal("TLS GetClientCertificate not configured; client certificate would be dropped on CA-hint mismatch")
	}
	clientCert, err := sender.httpClient.TLSConfig.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if len(clientCert.Certificate) != 1 {
		t.Fatalf("TLS client certificates = %d; want 1", len(clientCert.Certificate))
	}
	cert := *clientCert
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	want := coreutils.SHA256ofCert(leaf)
	if sender.local.Fingerprint != want {
		t.Fatalf("local fingerprint = %q; want certificate fingerprint %q", sender.local.Fingerprint, want)
	}
}

func TestForwardSender_PreUploadPinsFingerprintInRequestHandshake(t *testing.T) {
	tmp := t.TempDir()
	expectedCert, err := lsutils.GenAndSaveTLScert(filepath.Join(tmp, "expected.key"), filepath.Join(tmp, "expected.crt"))
	if err != nil {
		t.Fatal(err)
	}
	impostorCert, err := lsutils.GenAndSaveTLScert(filepath.Join(tmp, "impostor.key"), filepath.Join(tmp, "impostor.crt"))
	if err != nil {
		t.Fatal(err)
	}
	expectedLeaf, err := x509.ParseCertificate(expectedCert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	var connections atomic.Int32
	var impostorReceivedBody atomic.Bool
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			index := connections.Add(1)
			cert := expectedCert
			if index > 1 {
				cert = impostorCert
			}
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
			if tlsConn.Handshake() != nil {
				_ = tlsConn.Close()
				continue
			}
			req, readErr := http.ReadRequest(bufio.NewReader(tlsConn))
			if readErr != nil {
				_ = tlsConn.Close()
				continue
			}
			if index > 1 {
				impostorReceivedBody.Store(true)
			}
			responseBody, _ := json.Marshal(models.PreUploadResp{SessionId: "session", Tokens: models.FileTokens{}})
			resp := &http.Response{
				StatusCode:    http.StatusOK,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Body:          io.NopCloser(bytes.NewReader(responseBody)),
				ContentLength: int64(len(responseBody)),
				Header:        make(http.Header),
			}
			_ = resp.Write(tlsConn)
			_ = req.Body.Close()
			_ = tlsConn.Close()
		}
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{IP: host, Fingerprint: coreutils.SHA256ofCert(expectedLeaf)}, true); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(port)
	if err := sender.preUploadReq(); err != nil {
		t.Fatalf("pre-upload failed: %v", err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("TLS connections = %d; want one pinned request handshake", got)
	}
	if impostorReceivedBody.Load() {
		t.Fatal("prepare-upload body was sent on a later impostor connection")
	}
	_ = listener.Close()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("rotating TLS server did not stop")
	}
}
