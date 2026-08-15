package scan

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"localsend-cli/internal/localsend"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
)

func testTLSCert(t *testing.T, name string) tls.Certificate {
	t.Helper()
	dir := t.TempDir()
	cert, err := lsutils.GenAndSaveTLScert(filepath.Join(dir, name+".key"), filepath.Join(dir, name+".crt"))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestRegisterCallbackServer_AuthenticatesAndStoresHTTPSPeer(t *testing.T) {
	serverCert := testTLSCert(t, "server")
	clientCert := testTLSCert(t, "client")
	serverFP, err := lsutils.CertificateFingerprint(serverCert)
	if err != nil {
		t.Fatal(err)
	}
	clientFP, err := lsutils.CertificateFingerprint(clientCert)
	if err != nil {
		t.Fatal(err)
	}
	identity := models.NewDeviceInfo("Scanner", serverFP)
	scanner, err := localsend.NewDiscovererWithCertificate(identity, true, serverCert)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scanner.Shutdown() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callback, err := startRegisterCallbackServer(ctx, scanner, serverCert, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = callback.shutdown() }()

	body, _ := json.Marshal(models.Announcement{
		DeviceInfo: models.DeviceInfo{Alias: "Peer", Version: "2.2", Fingerprint: clientFP},
		Protocol:   "https",
		Port:       53317,
	})
	req, err := http.NewRequest(http.MethodPost, "https://127.0.0.1:"+fmt.Sprint(callback.port)+"/api/localsend/v2/register", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: lsutils.TLSClientConfig(clientCert, serverFP)}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, peer := range scanner.GetAllDiscovered() {
			if peer.Fingerprint == clientFP {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("authenticated callback was not stored: %#v", scanner.GetAllDiscovered())
}
