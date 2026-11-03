package utils

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func mustAcquire(t *testing.T, l *connLimiter, ip string) func() {
	t.Helper()
	release, err := l.acquire(netip.MustParseAddr(ip))
	if err != nil {
		t.Fatalf("acquire(%s) error = %v, want a slot", ip, err)
	}
	return release
}

func TestConnLimiter_RefusesPeerAtPerIPLimit_UntilItsConnectionCloses(t *testing.T) {
	l := newConnLimiter(10, 2)
	mustAcquire(t, l, "192.168.1.2")
	release := mustAcquire(t, l, "192.168.1.2")

	if _, err := l.acquire(netip.MustParseAddr("192.168.1.2")); !errors.Is(err, errPerIPConnectionLimit) {
		t.Fatalf("third connection error = %v, want per-peer limit", err)
	}
	mustAcquire(t, l, "192.168.1.3")

	release()
	mustAcquire(t, l, "192.168.1.2")
}

func TestConnLimiter_CountsIPv4MappedAddressAsItsIPv4Peer(t *testing.T) {
	l := newConnLimiter(10, 1)
	mustAcquire(t, l, "192.168.1.2")

	if _, err := l.acquire(netip.MustParseAddr("::ffff:192.168.1.2")); !errors.Is(err, errPerIPConnectionLimit) {
		t.Fatalf("IPv4-mapped connection error = %v, want per-peer limit", err)
	}
}

func TestConnLimiter_CountsScopedIPv6AddressAsOnePeer(t *testing.T) {
	l := newConnLimiter(10, 1)
	mustAcquire(t, l, "fe80::1%eth0")

	if _, err := l.acquire(netip.MustParseAddr("fe80::1%wlan0")); !errors.Is(err, errPerIPConnectionLimit) {
		t.Fatalf("same address on another zone error = %v, want per-peer limit", err)
	}
}

func TestConnLimiter_RefusesEveryPeerAtTotalLimit_UntilAConnectionCloses(t *testing.T) {
	l := newConnLimiter(2, 2)
	release := mustAcquire(t, l, "192.168.1.2")
	mustAcquire(t, l, "fe80::1")

	if _, err := l.acquire(netip.MustParseAddr("192.168.1.4")); !errors.Is(err, errConnectionLimit) {
		t.Fatalf("connection over total error = %v, want total limit", err)
	}

	release()
	mustAcquire(t, l, "192.168.1.4")
}

func TestConnLimiter_CountsLoopbackOnlyTowardsTotal(t *testing.T) {
	l := newConnLimiter(3, 1)
	releases := []func(){
		mustAcquire(t, l, "127.0.0.1"),
		mustAcquire(t, l, "127.0.0.1"),
		mustAcquire(t, l, "::1"),
	}

	if _, err := l.acquire(netip.MustParseAddr("127.0.0.1")); !errors.Is(err, errConnectionLimit) {
		t.Fatalf("fourth loopback connection error = %v, want total limit", err)
	}

	for _, release := range releases {
		release()
	}
	if l.total != 0 || len(l.perIP) != 0 {
		t.Fatalf("after releasing every slot: total = %d, peers = %d; want 0, 0", l.total, len(l.perIP))
	}
}

func TestLimitedListener_ClosesConnectionOverLimitWithoutResponse_UntilASlotFrees(t *testing.T) {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(ln, 1, 1)
	defer func() { _ = limited.Close() }()

	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			conn, err := limited.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()

	dial := func() net.Conn {
		t.Helper()
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	waitAccepted := func() net.Conn {
		t.Helper()
		select {
		case conn := <-accepted:
			return conn
		case <-time.After(5 * time.Second):
			t.Fatal("connection within the limit was not accepted")
			return nil
		}
	}

	dial()
	served := waitAccepted()

	refused := dial()
	_ = refused.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := refused.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("refused connection read = %d, %v; want closed without a response", n, err)
	}

	_ = served.Close()
	dial()
	waitAccepted()
}
