package localsend

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

const (
	advInterval = 3 * time.Second
	// maxConcurrentScans limits the number of concurrent subnet scan goroutines
	// to prevent resource exhaustion on constrained devices (e.g., Raspberry Pi, e-readers)
	maxConcurrentScans = 50
	// ipCacheTTL is the time-to-live for cached IP addresses
	ipCacheTTL = 30 * time.Second
	// discoveryTTL is the time-to-live for discovered device entries
	discoveryTTL = 5 * time.Minute
	// discoveryCleanupInterval is how often stale discoveries are cleaned up
	discoveryCleanupInterval = 1 * time.Minute
	maxDiscoveredDevices     = 512
)

var multicastDiscoveryAddr = &net.UDPAddr{
	IP:   net.ParseIP("224.0.0.167"),
	Port: constants.DefaultPort,
}

// discoveryEntry wraps an Announcement with last-seen timestamp for TTL cleanup.
type discoveryEntry struct {
	anno     models.Announcement
	lastSeen time.Time
}

type Discoverer struct {
	mcastConn   *net.UDPConn
	selfAnno    *models.Announcement
	discovered  map[string]discoveryEntry
	mu          *sync.RWMutex
	stop        chan struct{}
	stopOnce    sync.Once
	cachedIPs   []net.IP
	ipCacheTime time.Time
	ipCacheMu   sync.RWMutex // protects cachedIPs and ipCacheTime
	readBuf     []byte       // reusable buffer for UDP reads
}

func NewDiscoverer(devInfo models.DeviceInfo, supportHttps bool) (*Discoverer, error) {
	conn, err := net.ListenMulticastUDP("udp", nil, multicastDiscoveryAddr)
	if err != nil {
		return nil, err
	}

	protocol := "http"
	if supportHttps {
		protocol = "https"
	}

	_ = conn.SetReadBuffer(512)

	return &Discoverer{
		mcastConn: conn,
		selfAnno: &models.Announcement{
			DeviceInfo: devInfo,
			Port:       constants.DefaultPort,
			Protocol:   protocol,
			Announce:   true,
		},
		stop:       make(chan struct{}, 1),
		discovered: make(map[string]discoveryEntry),
		mu:         &sync.RWMutex{},
		readBuf:    make([]byte, 512),
	}, nil
}

func (ma *Discoverer) Listen() error {
	ticker := time.NewTicker(advInterval)
	defer ticker.Stop()

	// Start cleanup task in background
	go ma.discoveryCleanupTask()

	_ = ma.advertise()

	for {
		select {
		case <-ma.stop:
			return nil
		case <-ticker.C:
			// Check if stopped before attempting network operations
			select {
			case <-ma.stop:
				return nil
			default:
			}
			err := ma.advertise()
			if err != nil {
				// If connection is closed, exit gracefully
				if isClosedConnError(err) {
					return nil
				}
				slog.Warn("Fail to send announcement", "error", err)
				continue
			}
			err = ma.readAndRegister()
			if err != nil {
				continue
			}
		}
	}
}

// isClosedConnError checks if the error is due to a closed network connection
func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "use of closed network connection") ||
		err == net.ErrClosed
}

// discoveryCleanupTask periodically removes stale discovered device entries.
// This prevents unbounded memory growth from devices that appear once and disappear.
func (ma *Discoverer) discoveryCleanupTask() {
	ticker := time.NewTicker(discoveryCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ma.stop:
			return
		case <-ticker.C:
			ma.cleanupStaleDiscovered()
		}
	}
}

// cleanupStaleDiscovered removes discovered entries older than discoveryTTL.
func (ma *Discoverer) cleanupStaleDiscovered() {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	if ma.discovered == nil {
		return
	}

	now := time.Now()
	cleaned := 0
	for ip, entry := range ma.discovered {
		if now.Sub(entry.lastSeen) > discoveryTTL {
			delete(ma.discovered, ip)
			cleaned++
		}
	}
	if cleaned > 0 {
		slog.Debug("Cleaned up stale discovered devices", "count", cleaned)
	}
}

func (ma *Discoverer) advertise() error {
	b, err := json.Marshal(ma.selfAnno)
	if err != nil {
		return err
	}

	_, err = ma.mcastConn.WriteToUDP(b, multicastDiscoveryAddr)
	if err != nil {
		return err
	}

	return nil
}

func (ma *Discoverer) Shutdown() error {
	ma.stopOnce.Do(func() {
		// Close connection first to unblock any pending reads in readAndRegister(),
		// allowing Listen() to return to the select and receive the stop signal
		_ = ma.mcastConn.Close()
		close(ma.stop) // Close the channel so all goroutines watching it exit
	})
	return nil
}

func (mcs *Discoverer) getCachedIPs() ([]net.IP, error) {
	// Use double-checked locking for thread-safe cache access
	mcs.ipCacheMu.RLock()
	if time.Since(mcs.ipCacheTime) <= ipCacheTTL && mcs.cachedIPs != nil {
		ips := mcs.cachedIPs
		mcs.ipCacheMu.RUnlock()
		return ips, nil
	}
	mcs.ipCacheMu.RUnlock()

	// Need to refresh - acquire write lock
	mcs.ipCacheMu.Lock()
	defer mcs.ipCacheMu.Unlock()

	// Double-check after acquiring write lock (another goroutine may have refreshed)
	if time.Since(mcs.ipCacheTime) <= ipCacheTTL && mcs.cachedIPs != nil {
		return mcs.cachedIPs, nil
	}

	ips, err := utils.GetMyIPv4Addr()
	if err != nil {
		return nil, err
	}
	mcs.cachedIPs = ips
	mcs.ipCacheTime = time.Now()
	return ips, nil
}

func (mcs *Discoverer) readAndRegister() error {
	_ = mcs.mcastConn.SetReadDeadline(time.Now().Add(1 * time.Second))

	n, remoteAddr, err := mcs.mcastConn.ReadFromUDP(mcs.readBuf)
	if err != nil {
		return err
	}

	var anno models.Announcement
	err = json.Unmarshal(mcs.readBuf[:n], &anno)
	if err != nil {
		return err
	}

	// Avoid self discovery using fingerprint per protocol spec Section 2 & 3.1
	if anno.Fingerprint == mcs.selfAnno.Fingerprint {
		return nil
	}

	// Register the discovered device (IPv4 only - LocalSend protocol uses IPv4)
	ip4 := remoteAddr.IP.To4()
	if ip4 == nil {
		// Skip IPv6 addresses - LocalSend discovery is IPv4 only
		return nil
	}
	mcs.PutDiscovered(ip4.String(), anno)

	// Per protocol spec Section 3.1: respond when we receive an announcement with announce:true
	// First try HTTP POST (primary method), then UDP fallback
	if anno.Announce {
		mcs.sendHTTPResponse(remoteAddr.IP.String(), anno)
		mcs.sendUDPResponse(remoteAddr)
	}

	return nil
}

// sendHTTPResponse sends our device info via HTTP POST to /api/localsend/v2/register
// per protocol spec Section 3.1: "First, an HTTP/TCP request is sent to the origin"
func (mcs *Discoverer) sendHTTPResponse(ip string, anno models.Announcement) {
	// Build the registration request body (same fields as announcement, without announce)
	regBody := models.Announcement{
		DeviceInfo: mcs.selfAnno.DeviceInfo,
		Protocol:   mcs.selfAnno.Protocol,
		Port:       mcs.selfAnno.Port,
		Announce:   false, // Not used in HTTP request per spec
	}

	bodyBytes, err := json.Marshal(regBody)
	if err != nil {
		slog.Debug("Failed to marshal HTTP response body", "error", err)
		return
	}

	// Use the protocol and port from the received announcement
	scheme := anno.Protocol
	if scheme == "" {
		scheme = "http"
	}
	port := anno.Port
	if port == 0 {
		port = constants.DefaultPort
	}

	remoteAddr := fmt.Sprintf("%s:%d", ip, port)

	agent := fiber.AcquireAgent()
	defer fiber.ReleaseAgent(agent)

	req := agent.Request()
	req.URI().SetScheme(scheme)
	req.URI().SetHost(remoteAddr)
	req.URI().SetPath(constants.RegisterPath)
	req.Header.SetMethod(fiber.MethodPost)
	req.Header.SetContentType(fiber.MIMEApplicationJSON)
	req.SetBody(bodyBytes)

	if err := agent.Parse(); err != nil {
		slog.Debug("Failed to parse HTTP register request", "error", err)
		return
	}

	// Skip TLS verification for self-signed certs
	_, _, errs := agent.InsecureSkipVerify().Timeout(2 * time.Second).Bytes()
	if len(errs) > 0 {
		slog.Debug("Failed to send HTTP register response", "remote", remoteAddr, "error", errs[0])
		return
	}

	slog.Debug("Sent HTTP register response", "remote", remoteAddr)
}

// sendUDPResponse sends our device info via UDP as a fallback response
// per protocol spec Section 3.1: "As fallback, members can also respond
// with a Multicast/UDP message" with announce:false
func (mcs *Discoverer) sendUDPResponse(remoteAddr *net.UDPAddr) {
	response := *mcs.selfAnno
	response.Announce = false

	b, err := json.Marshal(response)
	if err != nil {
		slog.Warn("Failed to marshal UDP response", "error", err)
		return
	}

	// Send directly to the remote address (unicast response)
	_, err = mcs.mcastConn.WriteToUDP(b, remoteAddr)
	if err != nil {
		slog.Warn("Failed to send UDP response", "error", err)
	}
}

func (mcs *Discoverer) GetAllDiscovered() map[string]models.Announcement {
	mcs.mu.RLock()
	defer mcs.mu.RUnlock()

	result := make(map[string]models.Announcement, len(mcs.discovered))
	for k, entry := range mcs.discovered {
		result[k] = entry.anno
	}
	return result
}

func (mcs *Discoverer) PutDiscovered(ip string, anno models.Announcement) {
	mcs.mu.Lock()
	defer mcs.mu.Unlock()

	// Normalize deviceType per protocol spec Section 7.1
	anno.DeviceType = normalizeDeviceType(anno.DeviceType)
	if _, exists := mcs.discovered[ip]; !exists && len(mcs.discovered) >= maxDiscoveredDevices {
		var oldestIP string
		var oldest time.Time
		for candidateIP, entry := range mcs.discovered {
			if oldestIP == "" || entry.lastSeen.Before(oldest) {
				oldestIP, oldest = candidateIP, entry.lastSeen
			}
		}
		delete(mcs.discovered, oldestIP)
	}
	mcs.discovered[ip] = discoveryEntry{
		anno:     anno,
		lastSeen: time.Now(),
	}
}

func (mcs *Discoverer) RegisterDevice(anno models.Announcement) {
	if anno.IP != "" {
		mcs.PutDiscovered(anno.IP, anno)
	}
}

// ScanSubnet performs legacy HTTP discovery by scanning the subnet of all private IPv4 interfaces
// per protocol spec Section 3.2.
func (mcs *Discoverer) ScanSubnet(ctx context.Context) {
	ips, err := mcs.getCachedIPs()
	if err != nil {
		slog.Error("Failed to get local IPs for subnet scan", "error", err)
		return
	}

	// Pre-marshal the registration body once (avoids JSON marshaling per IP)
	regBody := models.Announcement{
		DeviceInfo: mcs.selfAnno.DeviceInfo,
		Protocol:   mcs.selfAnno.Protocol,
		Port:       mcs.selfAnno.Port,
		Announce:   false,
	}
	bodyBytes, err := json.Marshal(regBody)
	if err != nil {
		slog.Error("Failed to marshal registration body", "error", err)
		return
	}

	var wg sync.WaitGroup
	// Semaphore to limit concurrent scans and prevent resource exhaustion
	sem := make(chan struct{}, maxConcurrentScans)

	for _, ip := range ips {
		// Only scan /24 subnets for simplicity and common home network usage
		ipv4 := ip.To4()
		if ipv4 == nil {
			continue
		}

		for i := 1; i < 255; i++ {
			targetIP := net.IPv4(ipv4[0], ipv4[1], ipv4[2], byte(i))
			if targetIP.Equal(ipv4) {
				continue
			}

			// Acquire semaphore slot (blocks if at capacity)
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(targetIP net.IP) {
				defer wg.Done()
				defer func() { <-sem }() // Release semaphore slot
				select {
				case <-ctx.Done():
					return
				default:
					mcs.scanIP(targetIP.String(), bodyBytes)
				}
			}(targetIP)
		}
	}
	wg.Wait()
}

// httpClientForScan is a shared HTTP client for subnet scanning.
//
// SECURITY NOTE: InsecureSkipVerify is set to true because LocalSend uses
// self-signed certificates. The protocol handles trust via fingerprint
// verification instead of CA-based PKI. See protocol spec Section 2.
// This is intentional and matches the official LocalSend implementation.
var httpClientForScan = &http.Client{
	Timeout: 1 * time.Second,
	Transport: &http.Transport{
		// #nosec G402 - Self-signed certs expected per LocalSend protocol
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConnsPerHost: 0, // Don't keep connections open
		DisableKeepAlives:   true,
	},
}

// tryScanIP attempts to discover a device at the given IP using the specified protocol.
// Returns true if a device was found and registered.
func (mcs *Discoverer) tryScanIP(ip, scheme string, bodyBytes []byte) bool {
	remoteAddr := net.JoinHostPort(ip, constants.DefaultPortStr)
	url := fmt.Sprintf("%s://%s%s", scheme, remoteAddr, constants.RegisterPath)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClientForScan.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return false
	}

	var deviceInfo models.DeviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&deviceInfo); err != nil {
		return false
	}

	deviceInfo.IP = ip
	mcs.PutDiscovered(ip, models.Announcement{
		DeviceInfo: deviceInfo,
		Protocol:   scheme,
		Port:       constants.DefaultPort,
		Announce:   false,
	})
	return true
}

func (mcs *Discoverer) scanIP(ip string, bodyBytes []byte) {
	// Try both HTTPS and HTTP as we don't know the receiver's preference
	// Protocol spec 3.2 says to send to all local IP addresses.
	for _, scheme := range []string{"https", "http"} {
		if mcs.tryScanIP(ip, scheme, bodyBytes) {
			return
		}
	}
}
