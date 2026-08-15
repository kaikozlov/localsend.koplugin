package transfer

import (
	"sync/atomic"
	"testing"
	"time"
)

type fakeBufferedAmountChannel struct {
	amount    atomic.Uint64
	threshold atomic.Uint64
}

func (f *fakeBufferedAmountChannel) BufferedAmount() uint64 { return f.amount.Load() }
func (f *fakeBufferedAmountChannel) SetBufferedAmountLowThreshold(v uint64) {
	f.threshold.Store(v)
}

func TestWaitForBufferedAmountBelow_FastPathAllocatesNothing(t *testing.T) {
	var dc fakeBufferedAmountChannel
	dc.amount.Store(1024)
	signal := make(chan struct{}, 1)
	allocs := testing.AllocsPerRun(1000, func() {
		if err := waitForBufferedAmountBelow(&dc, signal, 1024*1024, time.Second); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("fast-path allocations = %f; want 0", allocs)
	}
}

func TestWaitForBufferedAmountBelow_BlocksUntilLowSignal(t *testing.T) {
	var dc fakeBufferedAmountChannel
	dc.amount.Store(2 * 1024 * 1024)
	signal := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- waitForBufferedAmountBelow(&dc, signal, 1024*1024, time.Second)
	}()

	deadline := time.After(time.Second)
	for dc.threshold.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("buffer threshold was not installed")
		default:
		}
	}
	dc.amount.Store(512 * 1024)
	signal <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not resume after low-buffer signal")
	}
}

func BenchmarkWaitForBufferedAmountBelow_FastPath(b *testing.B) {
	var dc fakeBufferedAmountChannel
	dc.amount.Store(1024)
	signal := make(chan struct{}, 1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := waitForBufferedAmountBelow(&dc, signal, 1024*1024, time.Second); err != nil {
			b.Fatal(err)
		}
	}
}
