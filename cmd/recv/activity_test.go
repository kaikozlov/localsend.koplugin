package recv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransferActivityMarker_TracksConcurrentReceives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy")
	m := newTransferActivityMarker(path)
	t.Cleanup(m.Close)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker exists before activity: %v", err)
	}

	m.Begin()
	m.Begin()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("marker missing with two active receives: %v", err)
	}

	m.End()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("marker removed while one receive remains active: %v", err)
	}

	m.End()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker remains after final receive ended: %v", err)
	}
}

func TestTransferActivityMarker_CloseRemovesStaleMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy")
	m := newTransferActivityMarker(path)
	m.Begin()
	m.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker remains after Close: %v", err)
	}
}
