package localsend

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

const (
	advInterval = 3 * time.Second
	// maxConcurrentScans limits the number of concurrent subnet scan goroutines
	// to prevent resource exhaustion on constrained devices (e.g., Raspberry Pi, e-readers)
	maxConcurrentScans = 50
	// maxConcurrentAnnouncementResponses bounds HTTP response work triggered by
	// untrusted multicast announcements on the local network.
	maxConcurrentAnnouncementResponses = 8
	// ipCacheTTL is the time-to-live for cached IP addresses
	ipCacheTTL = 30 * time.Second
	// discoveryTTL is the time-to-live for discovered device entries
	discoveryTTL = 5 * time.Minute
	// discoveryCleanupInterval is how often stale discoveries are cleaned up
	discoveryCleanupInterval  = 1 * time.Minute
	maxDiscoveredDevices      = 512
	maxDiscoveryDatagramBytes = 64 << 10
)

var multicastDiscoveryAddr = &net.UDPAddr{
	IP:   net.ParseIP("224.0.0.167"),
	Port: constants.DefaultPort,
}

var multicastDiscoveryAddrV6 = &net.UDPAddr{
	IP:   net.ParseIP("ff12::fd3a:e420"),
	Port: constants.DefaultPort,
}

var discoveryResponseHTTPClient = &fasthttp.Client{
	MaxResponseBodySize: maxDiscoveryResponseBytes,
	DialDualStack:       true,
	// #nosec G402 -- LocalSend authenticates self-signed certificates by fingerprint.
	TLSConfig: &tls.Config{InsecureSkipVerify: true},
}

const maxDiscoveryResponseBytes = 64 << 10

// verifyAnnouncedFingerprint returns a tls.Config VerifyPeerCertificate hook
// that pins the HTTPS peer to the certificate fingerprint announced in its
// multicast message (protocol spec Section 2), mirroring the official client:
// nothing is sent to a peer that does not hold the matching certificate.
func verifyAnnouncedFingerprint(expected string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("localsend: peer presented no certificate")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("localsend: parse peer certificate: %w", err)
		}
		now := time.Now()
		if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			return fmt.Errorf("localsend: peer certificate is not valid at %s", now.Format(time.RFC3339))
		}
		if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
			return fmt.Errorf("localsend: verify self-signed peer certificate: %w", err)
		}
		actual := utils.SHA256ofCert(cert)
		if expected != "" && !strings.EqualFold(actual, expected) {
			return fmt.Errorf("localsend: certificate fingerprint mismatch: announced %s, peer holds %s", expected, actual)
		}
		return nil
	}
}

// discoveryEntry wraps an Announcement with last-seen timestamp for TTL cleanup.
type discoveryEntry struct {
	anno     models.Announcement
	lastSeen time.Time
}

type Discoverer struct {
	mcastConn         *net.UDPConn
	mcastPacketConn   *ipv4.PacketConn
	mcastInterfaces   []*net.Interface
	mcastConnV6       *net.UDPConn
	mcastPacketConnV6 *ipv6.PacketConn
	mcastInterfacesV6 []*net.Interface
	selfAnno          *models.Announcement
	cert              tls.Certificate
	discovered        map[string]discoveryEntry
	mu                *sync.RWMutex
	stop              chan struct{}
	stopOnce          sync.Once
	cachedIPs         []net.IP
	ipCacheTime       time.Time
	ipCacheMu         sync.RWMutex // protects cachedIPs and ipCacheTime
	readBuf           []byte       // reusable buffer for UDP reads
	readBufV6         []byte
	responseSem       chan struct{}

	// Injectable scan settings keep scheduling and cancellation testable without
	// depending on the host's real network.
	scanHTTPClient  *http.Client
	scanConcurrency int
}

func NewDiscoverer(devInfo models.DeviceInfo, supportHttps bool) (*Discoverer, error) {
	if !supportHttps {
		return newDiscoverer(devInfo, false, tls.Certificate{})
	}
	privateKeyFile, certFile, pathErr := lsutils.GetCertPaths()
	if pathErr != nil {
		if supportHttps {
			return nil, pathErr
		}
		return newDiscoverer(devInfo, supportHttps, tls.Certificate{})
	}
	cert, certErr := lsutils.LoadOrGenTLScert(privateKeyFile, certFile)
	if certErr != nil {
		if supportHttps {
			return nil, certErr
		}
		slog.Debug("HTTPS discovery disabled because device certificate is unavailable", "error", certErr)
	}
	return newDiscoverer(devInfo, supportHttps, cert)
}

func NewDiscovererWithCertificate(devInfo models.DeviceInfo, supportHttps bool, cert tls.Certificate) (*Discoverer, error) {
	return newDiscoverer(devInfo, supportHttps, cert)
}

func newDiscoverer(devInfo models.DeviceInfo, supportHttps bool, cert tls.Certificate) (*Discoverer, error) {
	if supportHttps {
		fingerprint, err := lsutils.CertificateFingerprint(cert)
		if err != nil {
			return nil, err
		}
		devInfo.Fingerprint = fingerprint
	}
	conn, err := net.ListenMulticastUDP("udp", nil, multicastDiscoveryAddr)
	if err != nil {
		return nil, err
	}

	protocol := "http"
	if supportHttps {
		protocol = "https"
	}

	_ = conn.SetReadBuffer(maxDiscoveryDatagramBytes)
	packetConn := ipv4.NewPacketConn(conn)
	interfaces, interfaceErr := eligibleMulticastInterfaces()
	if interfaceErr != nil {
		slog.Debug("Failed to enumerate multicast interfaces", "error", interfaceErr)
	}
	configured := make([]string, 0, len(interfaces))
	for _, ifi := range interfaces {
		configured = append(configured, ifi.Name)
		if err := packetConn.JoinGroup(ifi, multicastDiscoveryAddr); err != nil {
			// ListenMulticastUDP has already joined the system-selected interface,
			// so a duplicate-membership error for that interface is harmless.
			slog.Debug("Could not additionally join multicast interface", "interface", ifi.Name, "error", err)
			continue
		}
	}
	if len(configured) > 0 {
		slog.Info("Configured LocalSend multicast interfaces", "interfaces", configured)
	}
	connV6, packetConnV6, interfacesV6 := newIPv6MulticastListener()

	return &Discoverer{
		mcastConn:         conn,
		mcastPacketConn:   packetConn,
		mcastInterfaces:   interfaces,
		mcastConnV6:       connV6,
		mcastPacketConnV6: packetConnV6,
		mcastInterfacesV6: interfacesV6,
		cert:              cert,
		selfAnno: &models.Announcement{
			DeviceInfo: devInfo,
			Port:       constants.DefaultPort,
			Protocol:   protocol,
			Announce:   true,
		},
		stop:            make(chan struct{}, 1),
		discovered:      make(map[string]discoveryEntry),
		mu:              &sync.RWMutex{},
		readBuf:         make([]byte, maxDiscoveryDatagramBytes),
		readBufV6:       make([]byte, maxDiscoveryDatagramBytes),
		responseSem:     make(chan struct{}, maxConcurrentAnnouncementResponses),
		scanHTTPClient:  httpClientForScan,
		scanConcurrency: maxConcurrentScans,
	}, nil
}

func newIPv6MulticastListener() (*net.UDPConn, *ipv6.PacketConn, []*net.Interface) {
	interfaces, err := eligibleIPv6MulticastInterfaces()
	if err != nil {
		slog.Debug("Failed to enumerate IPv6 multicast interfaces", "error", err)
		return nil, nil, nil
	}
	if len(interfaces) == 0 {
		return nil, nil, nil
	}

	var conn *net.UDPConn
	var first *net.Interface
	for _, ifi := range interfaces {
		firstTarget := *multicastDiscoveryAddrV6
		firstTarget.Zone = ifi.Name
		candidate, listenErr := net.ListenMulticastUDP("udp6", ifi, &firstTarget)
		if listenErr != nil {
			slog.Debug("Could not bind IPv6 multicast interface", "interface", ifi.Name, "error", listenErr)
			continue
		}
		conn, first = candidate, ifi
		break
	}
	if conn == nil {
		return nil, nil, nil
	}
	_ = conn.SetReadBuffer(maxDiscoveryDatagramBytes)
	packetConn := ipv6.NewPacketConn(conn)
	joined := make([]*net.Interface, 0, len(interfaces))
	joined = append(joined, first)
	configured := make([]string, 0, len(interfaces))
	configured = append(configured, first.Name)
	for _, ifi := range interfaces {
		if ifi.Index == first.Index {
			continue
		}
		if err := packetConn.JoinGroup(ifi, multicastDiscoveryAddrV6); err != nil {
			slog.Debug("Could not join IPv6 multicast interface", "interface", ifi.Name, "error", err)
			continue
		}
		joined = append(joined, ifi)
		configured = append(configured, ifi.Name)
	}
	slog.Info("Configured LocalSend IPv6 multicast interfaces", "interfaces", configured)
	return conn, packetConn, joined
}

func eligibleMulticastInterfaces() ([]*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	eligible := make([]*net.Interface, 0, len(interfaces))
	for i := range interfaces {
		ifi := &interfaces[i]
		required := net.FlagUp | net.FlagRunning | net.FlagMulticast
		if ifi.Flags&required != required || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := ifi.Addrs()
		if err != nil {
			continue
		}
		if interfaceHasPrivateIPv4(addresses) {
			eligible = append(eligible, ifi)
		}
	}
	return eligible, nil
}

func interfaceHasPrivateIPv4(addresses []net.Addr) bool {
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && ip.IsPrivate() {
			return true
		}
	}
	return false
}

func eligibleIPv6MulticastInterfaces() ([]*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	eligible := make([]*net.Interface, 0, len(interfaces))
	for i := range interfaces {
		ifi := &interfaces[i]
		required := net.FlagUp | net.FlagRunning | net.FlagMulticast
		if ifi.Flags&required != required || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := ifi.Addrs()
		if err != nil {
			continue
		}
		if interfaceHasUsableIPv6(addresses) {
			eligible = append(eligible, ifi)
		}
	}
	return eligible, nil
}

func interfaceHasUsableIPv6(addresses []net.Addr) bool {
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() == nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func (ma *Discoverer) Listen() error {
	// Receiving must remain independent from advertising. In particular, a short
	// CLI scan must be able to drain every response triggered by its announcement
	// instead of reading only one datagram per advertisement interval.
	go ma.announcementTask()
	go ma.discoveryCleanupTask()
	if ma.mcastConnV6 != nil {
		go func() {
			if err := ma.listenOn(ma.mcastConnV6, ma.readBufV6); err != nil && !isClosedConnError(err) {
				slog.Debug("IPv6 multicast listener stopped", "error", err)
			}
		}()
	}
	return ma.listenOn(ma.mcastConn, ma.readBuf)
}

func (ma *Discoverer) listenOn(conn *net.UDPConn, readBuf []byte) error {
	for {
		err := ma.readAndRegisterFrom(conn, readBuf)
		if err == nil {
			continue
		}
		if isClosedConnError(err) {
			return nil
		}
		select {
		case <-ma.stop:
			return nil
		default:
			slog.Debug("Failed to read multicast announcement", "error", err)
		}
	}
}

func (ma *Discoverer) announcementTask() {
	// Repeat quickly so a sleeping Wi-Fi peer has several chances to receive a
	// short scan, then continue at the normal receiver advertisement interval.
	delays := []time.Duration{0, 100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ma.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if err := ma.advertise(); err != nil && !isClosedConnError(err) {
			slog.Warn("Failed to send multicast announcement", "error", err)
		}
	}

	ticker := time.NewTicker(advInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ma.stop:
			return
		case <-ticker.C:
			if err := ma.advertise(); err != nil && !isClosedConnError(err) {
				slog.Warn("Failed to send multicast announcement", "error", err)
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
	v4Err := ma.advertiseV4(b)
	if ma.mcastConnV6 == nil {
		return v4Err
	}
	v6Err := ma.advertiseV6(b)
	if v4Err == nil || v6Err == nil {
		return nil
	}
	return errors.Join(v4Err, v6Err)
}

func (ma *Discoverer) advertiseV4(b []byte) error {
	if ma.mcastPacketConn == nil || len(ma.mcastInterfaces) == 0 {
		_, err := ma.mcastConn.WriteToUDP(b, multicastDiscoveryAddr)
		return err
	}

	var lastErr error
	sent := false
	for _, ifi := range ma.mcastInterfaces {
		if err := ma.mcastPacketConn.SetMulticastInterface(ifi); err != nil {
			lastErr = err
			continue
		}
		if _, err := ma.mcastConn.WriteToUDP(b, multicastDiscoveryAddr); err != nil {
			lastErr = err
			continue
		}
		sent = true
	}
	if sent {
		return nil
	}
	return lastErr
}

func (ma *Discoverer) advertiseV6(b []byte) error {
	var lastErr error
	sent := false
	for _, ifi := range ma.mcastInterfacesV6 {
		if err := ma.mcastPacketConnV6.SetMulticastInterface(ifi); err != nil {
			lastErr = err
			continue
		}
		target := *multicastDiscoveryAddrV6
		target.Zone = ifi.Name
		if _, err := ma.mcastConnV6.WriteToUDP(b, &target); err != nil {
			lastErr = err
			continue
		}
		sent = true
	}
	if sent {
		return nil
	}
	if lastErr == nil {
		return errors.New("localsend: no IPv6 multicast interface available")
	}
	return lastErr
}

func (ma *Discoverer) Shutdown() error {
	ma.stopOnce.Do(func() {
		// Close connections first to unblock pending multicast reads,
		// allowing Listen() to return to the select and receive the stop signal
		_ = ma.mcastConn.Close()
		if ma.mcastConnV6 != nil {
			_ = ma.mcastConnV6.Close()
		}
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

func (mcs *Discoverer) readAndRegisterFrom(conn *net.UDPConn, readBuf []byte) error {
	n, remoteAddr, err := conn.ReadFromUDP(readBuf)
	if err != nil {
		return err
	}

	var anno models.Announcement
	err = json.Unmarshal(readBuf[:n], &anno)
	if err != nil {
		return err
	}

	// Avoid self discovery using fingerprint per protocol spec Section 2 & 3.1
	if anno.Fingerprint == mcs.selfAnno.Fingerprint {
		return nil
	}

	// Per protocol spec Section 3.1: respond when we receive an announcement with announce:true
	// First try HTTP POST (primary method), then UDP fallback. Announcements do
	// not enter the store until /register confirms the peer is reachable.
	if anno.Announce {
		mcs.respondToAnnouncement(remoteAddr, anno)
	}

	return nil
}

func discoveryHost(addr *net.UDPAddr) string {
	host := addr.IP.String()
	if addr.Zone != "" {
		host += "%" + addr.Zone
	}
	return host
}

func (mcs *Discoverer) respondToAnnouncement(remoteAddr *net.UDPAddr, anno models.Announcement) {
	host := discoveryHost(remoteAddr)
	if mcs.responseSem == nil {
		mcs.responseSem = make(chan struct{}, maxConcurrentAnnouncementResponses)
	}
	select {
	case mcs.responseSem <- struct{}{}:
		go func() {
			defer func() { <-mcs.responseSem }()
			if confirmed, ok := mcs.sendHTTPResponse(host, anno); ok {
				mcs.PutDiscovered(host, confirmed)
			}
			mcs.sendUDPResponse(remoteAddr)
		}()
	default:
		slog.Debug("Dropping multicast response while response limit is full", "remote", host)
	}
}

// sendHTTPResponse sends our device info via HTTP POST to /api/localsend/v2/register
// per protocol spec Section 3.1: "First, an HTTP/TCP request is sent to the origin"
func (mcs *Discoverer) sendHTTPResponse(ip string, anno models.Announcement) (models.Announcement, bool) {
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
		return models.Announcement{}, false
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

	remoteAddr := net.JoinHostPort(ip, strconv.Itoa(port))

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.URI().SetScheme(scheme)
	req.URI().SetHost(remoteAddr)
	req.URI().SetPath(constants.RegisterPath)
	req.Header.SetMethod(fiber.MethodPost)
	req.Header.SetContentType(fiber.MIMEApplicationJSON)
	req.SetBody(bodyBytes)

	// Skip TLS verification for self-signed certs
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	client := discoveryResponseHTTPClient
	if scheme == "https" {
		// Validate the self-signed certificate during the handshake and, when
		// announced, pin its fingerprint before sending any request bytes.
		// Each announcement gets its own client because the pin is peer-specific.
		client = &fasthttp.Client{
			MaxResponseBodySize: maxDiscoveryResponseBytes,
			DialDualStack:       true,
			TLSConfig:           lsutils.TLSClientConfig(mcs.cert, anno.Fingerprint),
		}
	}
	if err := client.DoTimeout(req, resp, 2*time.Second); err != nil {
		slog.Debug("Failed to send HTTP register response", "remote", remoteAddr, "error", err)
		return models.Announcement{}, false
	}
	if resp.StatusCode() != fiber.StatusOK {
		slog.Debug("HTTP register response was rejected", "remote", remoteAddr, "status", resp.StatusCode())
		return models.Announcement{}, false
	}

	var confirmed models.Announcement
	if err := json.Unmarshal(resp.Body(), &confirmed); err != nil {
		slog.Debug("Failed to decode HTTP register response", "remote", remoteAddr, "error", err)
		return models.Announcement{}, false
	}
	if confirmed.Alias == "" || confirmed.Version == "" {
		slog.Debug("HTTP register response omitted required identity fields", "remote", remoteAddr)
		return models.Announcement{}, false
	}
	if scheme == "https" {
		// The peer authenticated with the certificate pinned during the
		// handshake. Per protocol v2.2 / official discovery, the register
		// body fingerprint is ignored in HTTPS mode: retain the announced
		// certificate fingerprint as the device identity and never replace
		// it with the peer-controlled response-body value.
		confirmed.Fingerprint = anno.Fingerprint
	} else {
		// HTTP mode has no certificate identity; the body fingerprint is
		// the device identity and must be present.
		if confirmed.Fingerprint == "" {
			slog.Debug("HTTP register response omitted fingerprint", "remote", remoteAddr)
			return models.Announcement{}, false
		}
	}
	confirmed.Protocol = scheme
	confirmed.Port = port

	slog.Debug("Sent HTTP register response", "remote", remoteAddr)
	return confirmed, true
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

	// Send directly to the remote address (unicast response) on the same address
	// family that carried the announcement.
	conn := mcs.mcastConn
	if remoteAddr.IP.To4() == nil {
		conn = mcs.mcastConnV6
	}
	if conn == nil {
		slog.Warn("Failed to send UDP response", "error", "matching UDP socket unavailable")
		return
	}
	_, err = conn.WriteToUDP(b, remoteAddr)
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

// buildSubnetTargets returns every non-local host in each unique /24. Hosts are
// interleaved across subnets so one slow network cannot starve the others.
func buildSubnetTargets(ips []net.IP) []string {
	type subnet [3]byte
	subnets := make([]subnet, 0, len(ips))
	seenSubnets := make(map[subnet]struct{}, len(ips))
	localAddresses := make(map[string]struct{}, len(ips))

	for _, ip := range ips {
		ipv4 := ip.To4()
		if ipv4 == nil {
			continue
		}
		prefix := subnet{ipv4[0], ipv4[1], ipv4[2]}
		if _, exists := seenSubnets[prefix]; !exists {
			seenSubnets[prefix] = struct{}{}
			subnets = append(subnets, prefix)
		}
		localAddresses[ipv4.String()] = struct{}{}
	}

	capacity := len(subnets)*254 - len(localAddresses)
	if capacity < 0 {
		capacity = 0
	}
	targets := make([]string, 0, capacity)
	for host := 1; host < 255; host++ {
		for _, prefix := range subnets {
			target := net.IPv4(prefix[0], prefix[1], prefix[2], byte(host)).String()
			if _, local := localAddresses[target]; local {
				continue
			}
			targets = append(targets, target)
		}
	}
	return targets
}

// ScanSubnet performs legacy HTTP discovery by scanning the subnet of all private IPv4 interfaces
// per protocol spec Section 3.2.
func (mcs *Discoverer) ScanSubnet(ctx context.Context) {
	started := time.Now()
	ips, err := mcs.getCachedIPs()
	if err != nil {
		slog.Error("Failed to get local IPs for subnet scan", "error", err)
		return
	}

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
	httpsRegBody := regBody
	httpsRegBody.Protocol = "https"
	if fingerprint, fingerprintErr := lsutils.CertificateFingerprint(mcs.cert); fingerprintErr == nil {
		httpsRegBody.Fingerprint = fingerprint
	}
	httpsBodyBytes, err := json.Marshal(httpsRegBody)
	if err != nil {
		slog.Error("Failed to marshal HTTPS registration body", "error", err)
		return
	}

	targets := buildSubnetTargets(ips)
	concurrency := mcs.scanConcurrency
	if concurrency <= 0 {
		concurrency = maxConcurrentScans
	}

	type scanJob struct {
		ip     string
		scheme string
		body   []byte
	}
	jobs := make(chan scanJob)
	var wg sync.WaitGroup
	var attempted atomic.Int64
	var found atomic.Int64

	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				attempted.Add(1)
				if mcs.tryScanIP(ctx, job.ip, job.scheme, job.body) {
					found.Add(1)
				}
			}
		}()
	}

enqueue:
	for _, target := range targets {
		for _, scheme := range []string{"https", "http"} {
			jobBody := bodyBytes
			if scheme == "https" {
				jobBody = httpsBodyBytes
			}
			select {
			case <-ctx.Done():
				break enqueue
			case jobs <- scanJob{ip: target, scheme: scheme, body: jobBody}:
			}
		}
	}
	close(jobs)
	wg.Wait()

	slog.Info("Legacy subnet scan finished",
		"local_ips", ips,
		"targets", len(targets),
		"attempts", attempted.Load(),
		"found", found.Load(),
		"canceled", ctx.Err() != nil,
		"duration", time.Since(started),
	)
}

// httpClientForScan is a shared HTTP client for subnet scanning.
//
// SECURITY NOTE: InsecureSkipVerify is set to true because LocalSend uses
// self-signed certificates. The protocol handles trust via fingerprint
// verification instead of CA-based PKI. See protocol spec Section 2.
// This is intentional and matches the official LocalSend implementation.
//
// Redirects are never followed: a peer answering a scan with a 3xx must not be
// able to bounce the register POST to an arbitrary destination. This matches
// the official client hardening in LocalSend 1.18.2.
var httpClientForScan = &http.Client{
	Timeout: 500 * time.Millisecond,
	Transport: &http.Transport{
		// #nosec G402 - Self-signed certs expected per LocalSend protocol
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConnsPerHost: 0, // Don't keep connections open
		DisableKeepAlives:   true,
	},
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// tryScanIP attempts to discover a device at the given IP using the specified protocol.
// Returns true if a device was found and registered.
func (mcs *Discoverer) tryScanIP(ctx context.Context, ip, scheme string, bodyBytes []byte) bool {
	remoteAddr := net.JoinHostPort(ip, constants.DefaultPortStr)
	url := fmt.Sprintf("%s://%s%s", scheme, remoteAddr, constants.RegisterPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	client := mcs.scanHTTPClient
	if client == nil && scheme == "https" && len(mcs.cert.Certificate) > 0 {
		client = &http.Client{
			Timeout: 500 * time.Millisecond,
			Transport: &http.Transport{
				TLSClientConfig:   lsutils.TLSClientConfig(mcs.cert, ""),
				DisableKeepAlives: true,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if client == nil {
		client = httpClientForScan
	}
	resp, err := client.Do(req)
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
	if scheme == "https" {
		if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
			return false
		}
		deviceInfo.Fingerprint = utils.SHA256ofCert(resp.TLS.PeerCertificates[0])
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
