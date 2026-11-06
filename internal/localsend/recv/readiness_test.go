package recv

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type readinessDiscoverer struct {
	*supervisedDiscoverer
	receiver *FileReceiver
	observed chan error
}

func (d *readinessDiscoverer) Listen() error {
	select {
	case <-d.receiver.Ready():
		d.observed <- nil
	default:
		d.observed <- errors.New("discovery started before receiver readiness")
	}
	<-d.stop
	return nil
}

func TestReceiverBindsBeforeCreatingOrAdvertisingDiscovery(t *testing.T) {
	fr := NewFileReceiver("ordering", "", false)
	fr.SetDiscoveryOnly(true)
	fr.SetListenAddr("127.0.0.1:0")
	observed := make(chan error, 1)
	fr.newDiscoverer = func() (receiverDiscoverer, error) {
		connection, err := net.DialTimeout("tcp", fr.ListenAddr(), time.Second)
		if err != nil {
			observed <- errors.New("HTTP endpoint not bound when discovery was created")
			return nil, err
		}
		_ = connection.Close()
		return &readinessDiscoverer{
			supervisedDiscoverer: &supervisedDiscoverer{stop: make(chan struct{})},
			receiver:             fr,
			observed:             observed,
		}, nil
	}
	if err := fr.Init(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fr.Start(ctx) }()
	t.Cleanup(func() { cancel(); _ = fr.Stop() })
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("receiver ended before discovery: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("discovery never started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("receiver failed to stop")
	}
}
