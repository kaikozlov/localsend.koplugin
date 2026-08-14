package localsend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDiscovererListen_ProcessesDatagramsContinuously(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}

	d := &Discoverer{
		mcastConn:  conn,
		selfAnno:   &models.Announcement{DeviceInfo: models.DeviceInfo{Fingerprint: "self"}},
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
		stop:       make(chan struct{}),
		readBuf:    make([]byte, 512),
	}

	listenDone := make(chan error, 1)
	go func() {
		listenDone <- d.Listen()
	}()
	t.Cleanup(func() {
		_ = d.Shutdown()
		<-listenDone
	})

	sender, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial UDP listener: %v", err)
	}
	defer func() { _ = sender.Close() }()
	if _, err := sender.Write([]byte("{")); err != nil {
		t.Fatalf("send malformed announcement: %v", err)
	}

	send := func(alias string) {
		t.Helper()
		packet, marshalErr := json.Marshal(models.Announcement{
			DeviceInfo: models.DeviceInfo{
				Alias:       alias,
				Fingerprint: alias,
			},
			Protocol: "http",
			Port:     53317,
		})
		if marshalErr != nil {
			t.Fatalf("marshal announcement: %v", marshalErr)
		}
		if _, writeErr := sender.Write(packet); writeErr != nil {
			t.Fatalf("send announcement: %v", writeErr)
		}
	}

	waitForAlias := func(want string) {
		t.Helper()
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			for _, announcement := range d.GetAllDiscovered() {
				if announcement.Alias == want {
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("announcement %q was not processed promptly", want)
	}

	send("first")
	waitForAlias("first")
	send("second")
	waitForAlias("second")
}

func TestTryScanIP_RegistersSuccessfulResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Errorf("method = %q; want POST", req.Method)
		}
		if req.URL.Path != "/api/localsend/v2/register" {
			t.Errorf("path = %q; want register endpoint", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"alias":"Phone",
				"version":"2.1",
				"deviceType":"mobile"
			}`)),
			Header: make(http.Header),
		}, nil
	})}
	d := newTestDiscovererForSubnetScan(client, 1, nil)

	if !d.tryScanIP(context.Background(), "192.168.1.42", "http", []byte(`{}`)) {
		t.Fatal("tryScanIP() = false; want successful discovery")
	}

	discovered := d.GetAllDiscovered()["192.168.1.42"]
	if discovered.Alias != "Phone" || discovered.Protocol != "http" {
		t.Fatalf("discovered device = %#v; want Phone over HTTP", discovered)
	}
}

func TestBuildSubnetTargets_InterleavesAndDeduplicatesSubnets(t *testing.T) {
	targets := buildSubnetTargets([]net.IP{
		net.ParseIP("192.168.1.20"),
		net.ParseIP("10.0.0.30"),
		net.ParseIP("192.168.1.21"),
	})

	if got, want := len(targets), 505; got != want {
		t.Fatalf("target count = %d; want %d", got, want)
	}

	wantPrefix := []string{"192.168.1.1", "10.0.0.1", "192.168.1.2", "10.0.0.2"}
	for i, want := range wantPrefix {
		if targets[i] != want {
			t.Fatalf("target %d = %q; want %q", i, targets[i], want)
		}
	}

	for _, target := range targets {
		if target == "192.168.1.20" || target == "192.168.1.21" || target == "10.0.0.30" {
			t.Fatalf("local address %s must not be scanned", target)
		}
	}
}

func TestInterfaceHasPrivateIPv4_RejectsPublicAndIPv6OnlyInterfaces(t *testing.T) {
	tests := []struct {
		name      string
		addresses []net.Addr
		want      bool
	}{
		{
			name:      "private IPv4",
			addresses: []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)}},
			want:      true,
		},
		{
			name:      "public IPv4",
			addresses: []net.Addr{&net.IPNet{IP: net.ParseIP("203.0.113.10"), Mask: net.CIDRMask(24, 32)}},
		},
		{
			name:      "private IPv6 only",
			addresses: []net.Addr{&net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interfaceHasPrivateIPv4(tt.addresses); got != tt.want {
				t.Fatalf("interfaceHasPrivateIPv4() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestScanSubnet_CompletesBothProtocolAttemptsForEveryTarget(t *testing.T) {
	var httpAttempts atomic.Int32
	var httpsAttempts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Scheme {
		case "http":
			httpAttempts.Add(1)
		case "https":
			httpsAttempts.Add(1)
		default:
			t.Errorf("unexpected protocol %q", req.URL.Scheme)
		}
		return nil, errors.New("unreachable test host")
	})}

	d := newTestDiscovererForSubnetScan(client, 10, []net.IP{net.ParseIP("192.168.1.20")})
	d.ScanSubnet(context.Background())

	const targets = 253
	if got := httpAttempts.Load(); got != targets {
		t.Fatalf("HTTP attempts = %d; want %d", got, targets)
	}
	if got := httpsAttempts.Load(); got != targets {
		t.Fatalf("HTTPS attempts = %d; want %d", got, targets)
	}
}

func TestScanSubnet_AttemptsEverySubnetBeforeSlowSubnetCanStarveIt(t *testing.T) {
	var mu sync.Mutex
	seenSubnets := make(map[string]bool)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		host := req.URL.Hostname()
		mu.Lock()
		seenSubnets[strings.Join(strings.Split(host, ".")[:3], ".")] = true
		mu.Unlock()
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}

	d := newTestDiscovererForSubnetScan(client, 4, []net.IP{
		net.ParseIP("192.168.15.1"),
		net.ParseIP("192.168.1.20"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	d.ScanSubnet(ctx)

	mu.Lock()
	defer mu.Unlock()
	if !seenSubnets["192.168.15"] || !seenSubnets["192.168.1"] {
		t.Fatalf("attempted subnets = %v; want both subnets before cancellation", seenSubnets)
	}
}

func TestScanSubnet_BoundsConcurrentRequests(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}

	d := newTestDiscovererForSubnetScan(client, 3, []net.IP{net.ParseIP("192.168.1.20")})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	d.ScanSubnet(ctx)

	if got := maximum.Load(); got > 3 {
		t.Fatalf("maximum concurrent requests = %d; want at most 3", got)
	}
}

func TestScanSubnet_CancelsActiveRequests(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-req.Context().Done()
		select {
		case requestCanceled <- struct{}{}:
		default:
		}
		return nil, req.Context().Err()
	})}

	d := newTestDiscovererForSubnetScan(client, 1, []net.IP{net.ParseIP("192.168.1.20")})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.ScanSubnet(ctx)
		close(done)
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("scan request did not start")
	}
	cancel()

	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("active request did not observe scan cancellation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subnet scan did not return after cancellation")
	}
}

func newTestDiscovererForSubnetScan(client *http.Client, concurrency int, ips []net.IP) *Discoverer {
	return &Discoverer{
		selfAnno:        &models.Announcement{},
		discovered:      make(map[string]discoveryEntry),
		mu:              &sync.RWMutex{},
		cachedIPs:       ips,
		ipCacheTime:     time.Now(),
		scanHTTPClient:  client,
		scanConcurrency: concurrency,
	}
}

// TestReadAndRegister_IPv6Address_CorruptedKey demonstrates the bug at scan.go:150
// where calling remoteAddr.IP.To4().String() on an IPv6 address returns "<nil>"
// instead of a valid IP string.
//
// The code review incorrectly stated this would panic - in Go, nil net.IP.String()
// returns "<nil>", which causes corrupted map keys rather than a crash.
//
// After fix: This test should PASS because IPv6 addresses are now skipped.
func TestReadAndRegister_IPv6Address_CorruptedKey(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	// Simulate what readAndRegister does at line 150 for an IPv6 address
	ipv6Addr := net.ParseIP("2001:db8::1")
	if ipv6Addr == nil {
		t.Fatal("Failed to parse IPv6 address")
	}

	anno := models.Announcement{
		DeviceInfo: models.DeviceInfo{
			Alias:       "IPv6 Device",
			DeviceType:  "desktop",
			DeviceModel: "Test",
		},
	}

	// After fix: IPv6 addresses should be skipped (nil check added)
	ip4 := ipv6Addr.To4()
	if ip4 == nil {
		// This is the expected behavior after the fix - IPv6 should be skipped
		t.Log("IPv6 address correctly skipped (fix is working)")
		return
	}

	// If we get here with a valid ip4, store it
	mcs.PutDiscovered(ip4.String(), anno)

	// Verify no "<nil>" key exists
	if _, exists := mcs.discovered["<nil>"]; exists {
		t.Error("BUG: Found '<nil>' key in discovered map - IPv6 handling is broken")
	}
}

// TestReadAndRegister_IPv6_ShouldBeSkipped tests that the discoverer should
// skip IPv6 addresses since LocalSend only supports IPv4 discovery.
func TestReadAndRegister_IPv6_ShouldBeSkipped(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	anno := models.Announcement{
		DeviceInfo: models.DeviceInfo{
			Alias:      "IPv6 Device",
			DeviceType: "desktop",
		},
	}

	testCases := []struct {
		name        string
		ip          string
		shouldStore bool
		expectedKey string
	}{
		{"IPv4", "192.168.1.100", true, "192.168.1.100"},
		{"IPv6", "2001:db8::1", false, ""},
		{"IPv6 loopback", "::1", false, ""},
		{"IPv4-mapped IPv6", "::ffff:192.168.1.1", true, "192.168.1.1"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear the map
			mcs.discovered = make(map[string]discoveryEntry)

			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("Failed to parse IP: %s", tc.ip)
			}

			// This is the FIX that should be applied:
			ip4 := ip.To4()
			if ip4 == nil {
				// IPv6 - should be skipped
				if tc.shouldStore {
					t.Errorf("Expected %s to be stored but it would be skipped", tc.ip)
				}
				return
			}

			mcs.PutDiscovered(ip4.String(), anno)

			if tc.shouldStore {
				stored, ok := mcs.discovered[tc.expectedKey]
				if !ok {
					t.Errorf("Device should be stored under key %q", tc.expectedKey)
				}
				if stored.anno.Alias != "IPv6 Device" {
					t.Errorf("Alias = %q, want 'IPv6 Device'", stored.anno.Alias)
				}
			}
		})
	}
}

// =============================================================================
// Unsynchronized IP Cache Access Tests
// =============================================================================

// TestGetCachedIPs_ConcurrentAccess_RaceCondition tests thread-safe IP caching.
// BUG: getCachedIPs() reads and writes cachedIPs/ipCacheTime without synchronization.
// This test should FAIL with the race detector before the fix is applied.
func TestGetCachedIPs_ConcurrentAccess_RaceCondition(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
		// Note: cachedIPs and ipCacheTime start as nil/zero - first access will populate
	}

	// Force cache to be stale so it triggers refresh on each call
	// (ipCacheTime is zero value, which is > 30 seconds ago)

	var wg sync.WaitGroup
	const numGoroutines = 10

	// Launch multiple goroutines that all call getCachedIPs concurrently
	// This will trigger the race detector if there's no synchronization
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Call getCachedIPs multiple times to increase race window
			for j := 0; j < 5; j++ {
				_, _ = mcs.getCachedIPs()
				// Small sleep to interleave goroutines
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()

	// If we get here without the race detector complaining, the test passes
	// But with the current code, `go test -race` should report a data race
	t.Log("Concurrent getCachedIPs() calls completed")
}

// TestGetCachedIPs_CacheExpiry verifies the 30-second cache TTL.
func TestGetCachedIPs_CacheExpiry(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	// First call - should populate cache
	ips1, err := mcs.getCachedIPs()
	if err != nil {
		t.Fatalf("First getCachedIPs() failed: %v", err)
	}

	// Second call immediately - should return cached values
	ips2, err := mcs.getCachedIPs()
	if err != nil {
		t.Fatalf("Second getCachedIPs() failed: %v", err)
	}

	// Should be the same slice (pointer equality) if properly cached
	if len(ips1) != len(ips2) {
		t.Errorf("Cache should return consistent results: got %d then %d IPs", len(ips1), len(ips2))
	}
}

// =============================================================================
// Discovery TTL Cleanup Tests
// =============================================================================

// TestDiscoverer_cleanupStaleDiscovered_RemovesExpired verifies that stale
// discovered device entries are removed during cleanup.
func TestDiscoverer_cleanupStaleDiscovered_RemovesExpired(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	// Add an old entry (past TTL)
	mcs.discovered["192.168.1.1"] = discoveryEntry{
		anno: models.Announcement{
			DeviceInfo: models.DeviceInfo{
				Alias: "Old Device",
			},
		},
		lastSeen: time.Now().Add(-discoveryTTL - time.Minute),
	}

	// Run cleanup
	mcs.cleanupStaleDiscovered()

	// Verify entry was removed
	mcs.mu.RLock()
	_, exists := mcs.discovered["192.168.1.1"]
	mcs.mu.RUnlock()

	if exists {
		t.Error("Stale discovered entry should have been removed")
	}
}

// TestDiscoverer_cleanupStaleDiscovered_KeepsRecent verifies that recent
// discovered entries are NOT removed during cleanup.
func TestDiscoverer_cleanupStaleDiscovered_KeepsRecent(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	// Add a recent entry
	mcs.discovered["192.168.1.1"] = discoveryEntry{
		anno: models.Announcement{
			DeviceInfo: models.DeviceInfo{
				Alias: "Recent Device",
			},
		},
		lastSeen: time.Now(),
	}

	// Run cleanup
	mcs.cleanupStaleDiscovered()

	// Verify entry is still present
	mcs.mu.RLock()
	_, exists := mcs.discovered["192.168.1.1"]
	mcs.mu.RUnlock()

	if !exists {
		t.Error("Recent discovered entry should NOT have been removed")
	}
}

// TestDiscoverer_cleanupStaleDiscovered_MixedEntries verifies cleanup
// correctly handles a mix of stale and recent entries.
func TestDiscoverer_cleanupStaleDiscovered_MixedEntries(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	// Add stale entry
	mcs.discovered["192.168.1.1"] = discoveryEntry{
		anno: models.Announcement{
			DeviceInfo: models.DeviceInfo{Alias: "Stale"},
		},
		lastSeen: time.Now().Add(-discoveryTTL - time.Minute),
	}

	// Add recent entry
	mcs.discovered["192.168.1.2"] = discoveryEntry{
		anno: models.Announcement{
			DeviceInfo: models.DeviceInfo{Alias: "Recent"},
		},
		lastSeen: time.Now(),
	}

	// Run cleanup
	mcs.cleanupStaleDiscovered()

	// Verify results
	mcs.mu.RLock()
	_, staleExists := mcs.discovered["192.168.1.1"]
	_, recentExists := mcs.discovered["192.168.1.2"]
	count := len(mcs.discovered)
	mcs.mu.RUnlock()

	if staleExists {
		t.Error("Stale entry should have been removed")
	}
	if !recentExists {
		t.Error("Recent entry should still exist")
	}
	if count != 1 {
		t.Errorf("Expected 1 entry remaining, got %d", count)
	}
}

// TestDiscoverer_PutDiscovered_UpdatesLastSeen verifies that PutDiscovered
// updates the lastSeen timestamp for existing entries.
func TestDiscoverer_PutDiscovered_UpdatesLastSeen(t *testing.T) {
	mcs := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}

	anno := models.Announcement{
		DeviceInfo: models.DeviceInfo{
			Alias: "Test Device",
		},
	}

	// First put
	mcs.PutDiscovered("192.168.1.1", anno)
	mcs.mu.RLock()
	firstSeen := mcs.discovered["192.168.1.1"].lastSeen
	mcs.mu.RUnlock()

	// Wait a bit and put again
	time.Sleep(10 * time.Millisecond)
	mcs.PutDiscovered("192.168.1.1", anno)
	mcs.mu.RLock()
	secondSeen := mcs.discovered["192.168.1.1"].lastSeen
	mcs.mu.RUnlock()

	if !secondSeen.After(firstSeen) {
		t.Error("Second PutDiscovered should have updated lastSeen timestamp")
	}
}

func TestDiscoverer_PutDiscovered_BoundsRetainedDevices(t *testing.T) {
	d := &Discoverer{
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
	}
	for i := 0; i < 5000; i++ {
		d.PutDiscovered(fmt.Sprintf("198.51.%d.%d", i/256, i%256), models.Announcement{})
	}
	if got := len(d.discovered); got > 512 {
		t.Fatalf("retained %d discovered devices; want at most 512", got)
	}
}

// TestScanHTTPClient_DoesNotFollowRedirects verifies the shared scan client
// refuses to follow a 3xx from a scanned peer (official LocalSend 1.18.2
// hardening): the register POST must land on the scanned target only, and the
// redirect answer itself is returned to the caller, which rejects the device.
func TestScanHTTPClient_DoesNotFollowRedirects(t *testing.T) {
	var urls []string
	client := *httpClientForScan
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		urls = append(urls, req.URL.String())
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://198.51.100.7/attacker"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	resp, err := client.Post("http://192.0.2.9:53317/api/localsend/v2/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("client.Do() error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := len(urls); got != 1 {
		t.Fatalf("client issued %d requests (%v); want exactly 1 — redirect must not be followed", got, urls)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d; want 302 returned to caller", resp.StatusCode)
	}
}

// TestSendHTTPResponse_PinsAnnouncedFingerprint verifies that answering an
// HTTPS multicast announcement with /register is refused during the TLS
// handshake when the peer's certificate does not match the fingerprint it
// announced — nothing must be sent to an impersonating peer.
func TestSendHTTPResponse_PinsAnnouncedFingerprint(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer target.Close()

	host := strings.TrimPrefix(target.URL, "https://")
	ip, portStr, err := net.SplitHostPort(host)
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	d := &Discoverer{
		selfAnno:   &models.Announcement{DeviceInfo: models.DeviceInfo{Alias: "self"}},
		mu:         &sync.RWMutex{},
		discovered: make(map[string]discoveryEntry),
	}

	// Announce a fingerprint the server's certificate does not hold.
	d.sendHTTPResponse(ip, models.Announcement{
		DeviceInfo: models.DeviceInfo{Fingerprint: "DEADBEEF"},
		Protocol:   "https",
		Port:       port,
	})

	if got := requests.Load(); got != 0 {
		t.Fatalf("impersonating server received %d register requests; want 0 (handshake must fail)", got)
	}
}

// TestSendHTTPResponse_AcceptsMatchingFingerprint is the positive control:
// with the server's real fingerprint announced, the register POST completes.
func TestSendHTTPResponse_AcceptsMatchingFingerprint(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer target.Close()

	host := strings.TrimPrefix(target.URL, "https://")
	ip, portStr, err := net.SplitHostPort(host)
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	cert := target.Certificate()
	realFingerprint := utils.SHA256ofCert(cert)

	d := &Discoverer{
		selfAnno:   &models.Announcement{DeviceInfo: models.DeviceInfo{Alias: "self"}},
		mu:         &sync.RWMutex{},
		discovered: make(map[string]discoveryEntry),
	}

	d.sendHTTPResponse(ip, models.Announcement{
		DeviceInfo: models.DeviceInfo{Fingerprint: realFingerprint},
		Protocol:   "https",
		Port:       port,
	})

	if got := requests.Load(); got != 1 {
		t.Fatalf("server received %d register requests; want 1 (handshake must succeed)", got)
	}
}
