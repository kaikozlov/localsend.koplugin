package utils

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
)

// MaxConnections and MaxConnectionsPerIP match LocalSend's native HTTP server.
// Real peers stay well below the per-peer limit: a sender uploads two files in
// parallel and browsers open up to six connections per host.
const (
	MaxConnections      = 64
	MaxConnectionsPerIP = 8
)

var (
	errConnectionLimit      = errors.New("total connection limit reached")
	errPerIPConnectionLimit = errors.New("per-peer connection limit reached")
)

type connLimiter struct {
	mu       sync.Mutex
	maxTotal int
	maxPerIP int
	total    int
	perIP    map[netip.Addr]int
}

func newConnLimiter(maxTotal, maxPerIP int) *connLimiter {
	return &connLimiter{maxTotal: maxTotal, maxPerIP: maxPerIP, perIP: make(map[netip.Addr]int)}
}

// acquire reserves a slot for a connection from ip and returns the function
// that frees it. Loopback peers count only towards the total: they are this
// device, and local tools connect from there many times at once.
func (l *connLimiter) acquire(ip netip.Addr) (func(), error) {
	ip = ip.Unmap().WithZone("")
	limited := !ip.IsLoopback()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal {
		return nil, errConnectionLimit
	}
	if limited {
		if l.perIP[ip] >= l.maxPerIP {
			return nil, errPerIPConnectionLimit
		}
		l.perIP[ip]++
	}
	l.total++

	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.total--
		if limited {
			if l.perIP[ip] <= 1 {
				delete(l.perIP, ip)
			} else {
				l.perIP[ip]--
			}
		}
	}, nil
}

// limitedListener closes connections over the limits before any byte is read,
// so a refused peer gets no HTTP response, as with the native server.
type limitedListener struct {
	*net.TCPListener
	limiter *connLimiter
	// atLimit is only touched by Accept, which the HTTP server calls from a
	// single goroutine.
	atLimit bool
}

// listenLimited listens on addr and serves at most MaxConnections connections
// at once, at most MaxConnectionsPerIP of them from one non-loopback peer.
func listenLimited(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return newLimitedListener(ln.(*net.TCPListener), MaxConnections, MaxConnectionsPerIP), nil
}

func newLimitedListener(ln *net.TCPListener, maxTotal, maxPerIP int) *limitedListener {
	return &limitedListener{TCPListener: ln, limiter: newConnLimiter(maxTotal, maxPerIP)}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			return nil, err
		}
		ip := conn.RemoteAddr().(*net.TCPAddr).AddrPort().Addr()
		release, err := l.limiter.acquire(ip)
		if err != nil {
			_ = conn.Close()
			if errors.Is(err, errConnectionLimit) {
				if !l.atLimit {
					slog.Warn("Refusing connections: total connection limit reached", "limit", l.limiter.maxTotal)
					l.atLimit = true
				}
			} else {
				slog.Debug("Refusing connection: per-peer connection limit reached", "remote", ip, "limit", l.limiter.maxPerIP)
			}
			continue
		}
		l.atLimit = false
		return &limitedConn{TCPConn: conn, release: release}, nil
	}
}

// limitedConn embeds *net.TCPConn so sendfile (ReadFrom) and keepalive
// controls stay available to the HTTP server.
type limitedConn struct {
	*net.TCPConn
	releaseOnce sync.Once
	release     func()
}

func (c *limitedConn) Close() error {
	err := c.TCPConn.Close()
	c.releaseOnce.Do(c.release)
	return err
}
