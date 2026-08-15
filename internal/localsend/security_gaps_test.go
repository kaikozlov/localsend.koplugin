package localsend

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

func TestDiscovererReadAndRegisterFrom_DropsUnsolicitedFallbackResponse(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	d := &Discoverer{
		selfAnno:   &models.Announcement{DeviceInfo: models.DeviceInfo{Fingerprint: "self"}},
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}
	packet, err := json.Marshal(models.Announcement{
		DeviceInfo: models.DeviceInfo{Alias: "Spoof", Version: "2.2", Fingerprint: "spoof"},
		Protocol:   "http",
		Port:       1,
		Announce:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sender.Close() }()
	if _, err := sender.Write(packet); err != nil {
		t.Fatal(err)
	}
	if err := d.readAndRegisterFrom(conn, make([]byte, 512)); err != nil {
		t.Fatal(err)
	}
	if got := d.GetAllDiscovered(); len(got) != 0 {
		t.Fatalf("unsolicited announce:false device was accepted: %#v", got)
	}
}

func TestTryScanIP_HTTPSUsesCertificateFingerprint(t *testing.T) {
	der := makeSelfSignedTestCertificate(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	claimed := strings.Repeat("A", 64)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"alias":"TLS peer","version":"2.2","fingerprint":"` + claimed + `"}`)),
			TLS:        &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}},
			Request:    req,
		}, nil
	})}
	d := &Discoverer{discovered: make(map[string]discoveryEntry), mu: &sync.RWMutex{}, scanHTTPClient: client}
	if !d.tryScanIP(context.Background(), "192.0.2.20", "https", []byte(`{}`)) {
		t.Fatal("HTTPS scan did not register response")
	}
	got := d.GetAllDiscovered()["192.0.2.20"].Fingerprint
	want := utils.SHA256ofCert(cert)
	if got != want {
		t.Fatalf("stored fingerprint = %q; want TLS certificate fingerprint %q", got, want)
	}
}

func TestDeviceInfoHTTPClient_DoesNotFollowPeerRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/first" {
			http.Redirect(w, r, "/second", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := newDeviceInfoHTTPClient(nil).Get(server.URL + "/first")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d; want 302 without following peer redirect", resp.StatusCode)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d; want exactly 1", got)
	}
}

func TestDeviceInfoHTTPClient_IgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("http_proxy", "http://proxy.invalid:3128")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	serverAddr := strings.TrimPrefix(server.URL, "http://")

	client := newDeviceInfoHTTPClient(nil)
	transport := client.Transport.(*http.Transport)
	var dialed string
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return (&net.Dialer{}).DialContext(ctx, network, serverAddr)
	}

	resp, err := client.Get("http://peer.localsend.invalid/info")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if dialed != "peer.localsend.invalid:80" {
		t.Fatalf("dialed %q; environment proxy must be ignored", dialed)
	}
}
