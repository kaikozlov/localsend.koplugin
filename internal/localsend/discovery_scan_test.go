package localsend

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"localsend-cli/internal/models"
)

func TestScanReturnsOnlyFreshChannelsWithoutClearingStore(t *testing.T) {
	d := newTestDiscovererForSubnetScan(nil, 1, nil)
	d.advertiseRequests = make(chan struct{}, 1)
	peer := models.Announcement{DeviceInfo: models.DeviceInfo{Fingerprint: "dual", Alias: "peer"}, Port: 53317, Protocol: "https"}
	d.PutDiscovered("fe80::1%en0", peer)
	d.PutDiscovered("192.0.2.9", models.Announcement{DeviceInfo: models.DeviceInfo{Fingerprint: "stale"}})
	type outcome struct {
		peers map[string]models.Announcement
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		peers, err := d.Scan(context.Background(), ScanOptions{LAN: true, Timeout: 30 * time.Millisecond})
		done <- outcome{peers, err}
	}()
	<-d.advertiseRequests
	d.PutDiscovered("192.0.2.1", peer)
	got := <-done
	if got.err != nil || len(got.peers) != 1 || got.peers["192.0.2.1"].Fingerprint != "dual" {
		t.Fatalf("fresh scan = %v, %v; want fresh IPv4 channel only", got.peers, got.err)
	}
	if len(d.DiscoveredSince(time.Time{})) != 2 {
		t.Fatal("scan cleared the shared discovery store")
	}
	peers, err := d.Scan(context.Background(), ScanOptions{LAN: true, Timeout: time.Millisecond})
	if err != nil || len(peers) != 0 {
		t.Fatalf("next scan reused old confirmations: %v, %v", peers, err)
	}
}

func TestScanLegacyOutlivesNativeCollection(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		body, status := "", http.StatusNotFound
		if req.URL.Scheme == "http" && req.URL.Hostname() == "192.0.2.2" {
			body, status = `{"alias":"slow peer","version":"2.2","fingerprint":"slow"}`, http.StatusOK
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	d := newTestDiscovererForSubnetScan(client, 1, []net.IP{net.ParseIP("192.0.2.1")})
	done := make(chan error, 1)
	go func() {
		peers, err := d.Scan(context.Background(), ScanOptions{LAN: true, Legacy: true, Timeout: 5 * time.Millisecond, LegacyTimeout: time.Second})
		if err == nil && peers["192.0.2.2"].Alias != "slow peer" {
			err = errors.New("missing peer confirmed after native collection")
		}
		done <- err
	}()
	<-entered
	select {
	case err := <-done:
		t.Fatalf("legacy scan ended at native deadline: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestScanFallbackFindsPeerWithinNativeAndLegacyDeadlines(t *testing.T) {
	for _, tc := range []struct {
		name          string
		nativeTimeout time.Duration
		legacyTimeout time.Duration
	}{
		{"equal", 300 * time.Millisecond, 300 * time.Millisecond},
		{"shorter legacy", 600 * time.Millisecond, 300 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if err := req.Context().Err(); err != nil {
					return nil, err
				}
				body, status := "", http.StatusNotFound
				if req.URL.Scheme == "http" && req.URL.Hostname() == "192.0.2.2" {
					body, status = `{"alias":"fallback peer","version":"2.2","fingerprint":"fallback"}`, http.StatusOK
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			d := newTestDiscovererForSubnetScan(client, 1, []net.IP{net.ParseIP("192.0.2.1")})
			// A stale peer must not suppress this scan's fallback.
			d.PutDiscovered("192.0.2.9", models.Announcement{DeviceInfo: models.DeviceInfo{Fingerprint: "stale"}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			peers, err := d.Scan(ctx, ScanOptions{
				LAN: true, FallbackLegacy: true, Timeout: tc.nativeTimeout, LegacyTimeout: tc.legacyTimeout,
			})
			if err != nil || peers["192.0.2.2"].Alias != "fallback peer" {
				t.Fatalf("fallback scan = %v, %v; want peer before both deadlines", peers, err)
			}
		})
	}
}

func TestScanFallbackSkipsSubnetWhenNativePeerIsFresh(t *testing.T) {
	attempts := make(chan struct{}, 1)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case attempts <- struct{}{}:
		default:
		}
		return nil, errors.New("unexpected subnet probe")
	})}
	d := newTestDiscovererForSubnetScan(client, 1, []net.IP{net.ParseIP("192.0.2.1")})
	d.advertiseRequests = make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type outcome struct {
		peers map[string]models.Announcement
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		peers, err := d.Scan(ctx, ScanOptions{
			LAN: true, FallbackLegacy: true, Timeout: 300 * time.Millisecond, LegacyTimeout: 300 * time.Millisecond,
		})
		done <- outcome{peers, err}
	}()
	select {
	case <-d.advertiseRequests:
	case <-ctx.Done():
		t.Fatal("native discovery did not start")
	}
	d.PutDiscovered("192.0.2.2", models.Announcement{DeviceInfo: models.DeviceInfo{Alias: "native peer", Fingerprint: "native"}})
	got := <-done
	if got.err != nil || got.peers["192.0.2.2"].Alias != "native peer" {
		t.Fatalf("native scan = %v, %v; want freshly confirmed peer", got.peers, got.err)
	}
	select {
	case <-attempts:
		t.Fatal("fresh native peer did not suppress subnet fallback")
	default:
	}
}

func TestScanFallbackUsesNativeGraceForLongWindows(t *testing.T) {
	entered := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-req.Context().Done()
		select {
		case canceled <- struct{}{}:
		default:
		}
		return nil, req.Context().Err()
	})}
	d := newTestDiscovererForSubnetScan(client, 1, []net.IP{net.ParseIP("192.0.2.1")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := d.Scan(ctx, ScanOptions{
			LAN: true, FallbackLegacy: true, Timeout: time.Hour, LegacyTimeout: time.Hour,
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fallback did not start during native collection")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scan cancellation = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not join canceled fallback workers")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("scan returned before its fallback request observed cancellation")
	}
}

func TestReboundDiscovererSharesFreshCallbacksAndFiltersSelf(t *testing.T) {
	previous := newTestDiscovererForSubnetScan(nil, 1, nil)
	previous.selfAnno.Fingerprint = "self"
	rebound := newTestDiscovererForSubnetScan(nil, 1, nil)
	rebound.selfAnno.Fingerprint = "self"
	rebound.ShareDiscoveries(previous)
	started := time.Now()
	previous.RegisterDevice(models.Announcement{DeviceInfo: models.DeviceInfo{IP: "192.0.2.1", Fingerprint: "peer"}})
	rebound.RegisterDevice(models.Announcement{DeviceInfo: models.DeviceInfo{IP: "192.0.2.2", Fingerprint: "self"}})
	peers := rebound.DiscoveredSince(started)
	_, ok := peers["192.0.2.1"]
	if !ok || len(peers) != 1 {
		t.Fatalf("shared callbacks = %v; want one non-self peer", peers)
	}
}
