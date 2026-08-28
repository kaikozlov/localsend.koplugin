package send

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type recordingConn struct {
	mu             sync.Mutex
	readDeadlines  []time.Time
	writeDeadlines []time.Time
}

func (c *recordingConn) Read(p []byte) (int, error)  { return 0, io.EOF }
func (c *recordingConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *recordingConn) Close() error                { return nil }
func (c *recordingConn) LocalAddr() net.Addr         { return dummyAddr("local") }
func (c *recordingConn) RemoteAddr() net.Addr        { return dummyAddr("remote") }
func (c *recordingConn) SetDeadline(time.Time) error { return nil }
func (c *recordingConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadlines = append(c.readDeadlines, t)
	return nil
}
func (c *recordingConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadlines = append(c.writeDeadlines, t)
	return nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

func TestTrackedUploadConn_RestoresIdleWriteDeadlineAfterClientClearsIt(t *testing.T) {
	raw := &recordingConn{}
	sender := NewForwardSender()
	conn := &trackedUploadConn{Conn: raw, owner: sender}

	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}

	raw.mu.Lock()
	defer raw.mu.Unlock()
	if len(raw.writeDeadlines) != 2 {
		t.Fatalf("write deadline calls = %d; want clear + idle deadline", len(raw.writeDeadlines))
	}
	if raw.writeDeadlines[1].IsZero() {
		t.Fatal("Write() did not arm an idle write deadline")
	}
}

func TestTrackedUploadConn_RestoresIdleReadDeadlineAfterClientClearsIt(t *testing.T) {
	raw := &recordingConn{}
	sender := NewForwardSender()
	conn := &trackedUploadConn{Conn: raw, owner: sender}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Read(make([]byte, 1))

	raw.mu.Lock()
	defer raw.mu.Unlock()
	if len(raw.readDeadlines) != 2 {
		t.Fatalf("read deadline calls = %d; want clear + idle deadline", len(raw.readDeadlines))
	}
	if raw.readDeadlines[1].IsZero() {
		t.Fatal("Read() did not arm an idle read deadline")
	}
}

func TestTrackedUploadConn_DoesNotOverrideExplicitWriteDeadline(t *testing.T) {
	raw := &recordingConn{}
	sender := NewForwardSender()
	conn := &trackedUploadConn{Conn: raw, owner: sender}
	explicit := time.Now().Add(15 * time.Second)

	if err := conn.SetWriteDeadline(explicit); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}

	raw.mu.Lock()
	defer raw.mu.Unlock()
	if len(raw.writeDeadlines) != 1 {
		t.Fatalf("write deadline calls = %d; explicit deadline was overwritten", len(raw.writeDeadlines))
	}
	if !raw.writeDeadlines[0].Equal(explicit) {
		t.Fatalf("write deadline = %v; want explicit %v", raw.writeDeadlines[0], explicit)
	}
}
