package localsend

import (
	"context"
	"errors"
	"time"

	"localsend-cli/internal/models"
)

var (
	ErrScanBusy         = errors.New("discovery scan already in progress")
	ErrDiscoveryStopped = errors.New("discovery listener stopped")
)

// ScanOptions keeps native collection and legacy probing deadlines independent.
// Both deadlines start at Scan entry.
type ScanOptions struct {
	Timeout        time.Duration
	LegacyTimeout  time.Duration
	LAN            bool
	Legacy         bool
	FallbackLegacy bool
}

// ShareDiscoveries retains confirmed channels across a socket rebind. Call only
// before this discoverer begins listening or scanning.
func (mcs *Discoverer) ShareDiscoveries(previous *Discoverer) {
	mcs.mu = previous.mu
	mcs.discovered = previous.discovered
}

// DiscoveredSince snapshots only channels confirmed since the supplied instant.
func (mcs *Discoverer) DiscoveredSince(since time.Time) map[string]models.Announcement {
	mcs.mu.RLock()
	defer mcs.mu.RUnlock()
	result := make(map[string]models.Announcement)
	for _, entry := range mcs.discovered {
		best, found := bestDiscoveryChannel(entry.channels, since)
		if found {
			anno := entry.anno
			anno.Protocol, anno.Port = best.protocol, best.port
			result[best.host] = anno
		}
	}
	return result
}

// Scan never clears the receiver's shared store. Cancellation stops only this
// scan's work; it does not shut down the multicast or HTTP listeners. The receiver
// serializes scans across socket rebinds.
func (mcs *Discoverer) Scan(ctx context.Context, opts ScanOptions) (map[string]models.Announcement, error) {
	started := time.Now()
	select {
	case <-mcs.stop:
		return nil, ErrDiscoveryStopped
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	legacyCtx, cancelLegacy := context.WithDeadline(ctx, started.Add(opts.LegacyTimeout))
	defer cancelLegacy()
	var legacyDone <-chan struct{}
	var legacyErr error
	var legacyWait chan struct{}
	startLegacy := func() {
		legacyWait = make(chan struct{})
		legacyDone = legacyWait
		go func() {
			defer close(legacyWait)
			legacyErr = mcs.scanSubnet(legacyCtx)
		}()
	}
	defer func() {
		cancelLegacy()
		if legacyWait != nil {
			<-legacyWait
		}
	}()
	if opts.Legacy {
		startLegacy()
	}
	nativeDeadline := started.Add(opts.Timeout)
	var collection, fallback <-chan time.Time
	if opts.LAN {
		timer := time.NewTimer(time.Until(nativeDeadline))
		defer timer.Stop()
		collection = timer.C
		select {
		case mcs.advertiseRequests <- struct{}{}:
		default:
		}
		if opts.FallbackLegacy && !opts.Legacy {
			// Leave both windows room for probes, even when the CLI gives
			// native collection and legacy fallback the same deadline.
			grace := min(750*time.Millisecond, opts.Timeout/2, opts.LegacyTimeout/2)
			fallbackTimer := time.NewTimer(time.Until(started.Add(grace)))
			defer fallbackTimer.Stop()
			fallback = fallbackTimer.C
		}
	} else if opts.FallbackLegacy && !opts.Legacy {
		startLegacy()
	}
	for collection != nil || fallback != nil || legacyDone != nil {
		select {
		case <-ctx.Done():
			return mcs.DiscoveredSince(started), ctx.Err()
		case <-mcs.stop:
			return mcs.DiscoveredSince(started), ErrDiscoveryStopped
		case <-collection:
			collection = nil
			fallback = nil
		case <-fallback:
			fallback = nil
			if time.Now().Before(nativeDeadline) && legacyCtx.Err() == nil && len(mcs.DiscoveredSince(started)) == 0 {
				startLegacy()
			}
		case <-legacyDone:
			legacyDone = nil
		}
	}
	return mcs.DiscoveredSince(started), errors.Join(ctx.Err(), legacyErr)
}
