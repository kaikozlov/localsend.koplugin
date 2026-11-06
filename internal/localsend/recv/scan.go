package recv

import (
	"context"
	"errors"
	"time"

	"localsend-cli/internal/localsend"
	"localsend-cli/internal/models"
)

// Scan uses the receiver's own register callbacks and multicast sockets. It
// waits through startup/rebinding without changing listener ownership or TLS.
func (fr *FileReceiver) Scan(ctx context.Context, opts localsend.ScanOptions) (map[string]models.Announcement, error) {
	if !fr.scanMu.TryLock() {
		return nil, localsend.ErrScanBusy
	}
	defer fr.scanMu.Unlock()
	started := time.Now()
	nativeDeadline := started.Add(opts.Timeout)
	legacyDeadline := started.Add(opts.LegacyTimeout)
	deadline := nativeDeadline
	if (opts.Legacy || opts.FallbackLegacy) && legacyDeadline.After(deadline) {
		deadline = legacyDeadline
	}
	scanCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result := make(map[string]models.Announcement)
	finish := func(err error) (map[string]models.Announcement, error) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			if len(result) == 0 && fr.currentDiscoverer() == nil {
				return result, fr.discoveryUnavailableError()
			}
			return result, nil
		}
		return result, err
	}
	select {
	case <-scanCtx.Done():
		return finish(scanCtx.Err())
	case <-fr.done:
		return result, localsend.ErrDiscoveryStopped
	case <-fr.ready:
	}
	for {
		fr.discoveryMu.Lock()
		discoverer, changed := fr.discoverier, fr.discoveryChanged
		fr.discoveryMu.Unlock()
		if discoverer != nil {
			remaining := opts
			remaining.Timeout = time.Until(nativeDeadline)
			if remaining.Timeout <= 0 {
				remaining.LAN = false
			}
			remaining.LegacyTimeout = time.Until(legacyDeadline)
			if remaining.LegacyTimeout <= 0 {
				remaining.Legacy, remaining.FallbackLegacy = false, false
			}
			_, err := discoverer.Scan(scanCtx, remaining)
			result = discoverer.DiscoveredSince(started)
			if !errors.Is(err, localsend.ErrDiscoveryStopped) {
				return finish(err)
			}
		}
		select {
		case <-scanCtx.Done():
			return finish(scanCtx.Err())
		case <-fr.done:
			return result, localsend.ErrDiscoveryStopped
		case <-changed:
		}
	}
}
