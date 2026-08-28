package recv

import (
	"bytes"
	"testing"
	"time"
)

type recordingReadDeadlineSetter struct {
	deadlines []time.Time
}

func (r *recordingReadDeadlineSetter) SetReadDeadline(deadline time.Time) error {
	r.deadlines = append(r.deadlines, deadline)
	return nil
}

func TestIdleDeadlineReader_ArmsBeforeFirstReadAndRefreshesAfterProgress(t *testing.T) {
	deadlines := &recordingReadDeadlineSetter{}
	reader := newIdleDeadlineReader(bytes.NewReader([]byte("abcdef")), deadlines)
	if len(deadlines.deadlines) != 1 || deadlines.deadlines[0].IsZero() {
		t.Fatalf("constructor deadlines = %v; want one armed read deadline", deadlines.deadlines)
	}

	reader.lastRefresh = time.Now().Add(-uploadDeadlineRefreshInterval)
	buf := make([]byte, 3)
	if n, err := reader.Read(buf); err != nil || n != 3 {
		t.Fatalf("Read() = %d, %v; want 3, nil", n, err)
	}
	if len(deadlines.deadlines) != 2 || deadlines.deadlines[1].IsZero() {
		t.Fatalf("deadlines after progress = %v; want refreshed non-zero deadline", deadlines.deadlines)
	}
}

func TestIdleDeadlineReader_ClearRemovesConnectionDeadline(t *testing.T) {
	deadlines := &recordingReadDeadlineSetter{}
	reader := newIdleDeadlineReader(bytes.NewReader(nil), deadlines)

	reader.clearDeadline()

	if got := deadlines.deadlines[len(deadlines.deadlines)-1]; !got.IsZero() {
		t.Fatalf("last deadline = %v; want zero deadline", got)
	}
}
