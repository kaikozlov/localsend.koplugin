package recv

import (
	"fmt"
	"os"
	"sync"
)

// transferActivityMarker exposes aggregate receive activity to the KOReader
// integration without changing LocalSend's wire protocol. Concurrent HTTP
// uploads and WebRTC share one counter; the marker exists iff active > 0.
type transferActivityMarker struct {
	mu     sync.Mutex
	path   string
	active int
}

func newTransferActivityMarker(path string) *transferActivityMarker {
	m := &transferActivityMarker{path: path}
	if path != "" {
		_ = os.Remove(path)
	}
	return m
}

func (m *transferActivityMarker) Begin() {
	if m == nil || m.path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active++
	if m.active == 1 {
		_ = os.WriteFile(m.path, []byte("busy\n"), 0600)
	}
}

func (m *transferActivityMarker) End() {
	if m == nil || m.path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == 0 {
		return
	}
	m.active--
	if m.active == 0 {
		_ = os.Remove(m.path)
	}
}

func (m *transferActivityMarker) Close() {
	if m == nil || m.path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = 0
	_ = os.Remove(m.path)
}

func (m *transferActivityMarker) String() string {
	if m == nil {
		return "<nil>"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("transferActivityMarker{active:%d}", m.active)
}
