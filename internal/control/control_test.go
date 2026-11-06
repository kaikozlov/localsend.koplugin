package control

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localsend-cli/internal/localsend"
	"localsend-cli/internal/models"
)

func privateDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOwnerExcludesConcurrentBackend(t *testing.T) {
	path := filepath.Join(privateDirectory(t), "control.sock")
	owner, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	if other, err := Listen(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second backend was not excluded: %v", err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("failed contender removed the active socket: %v", err)
	}
	_ = conn.Close()
	owner.Close()
	replacement, err := Listen(path)
	if err != nil {
		t.Fatalf("owner was not released: %v", err)
	}
	replacement.Close()
}

func TestOwnerRecoversStaleSocket(t *testing.T) {
	path := filepath.Join(privateDirectory(t), "control.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()
	owner, err := Listen(path)
	if err != nil {
		t.Fatalf("stale socket prevented recovery: %v", err)
	}
	owner.Close()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shutdown left its socket behind: %v", err)
	}
}

func TestControlRejectsUnsafeDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "control.sock")
	if owner, err := Listen(path); err == nil {
		owner.Close()
		t.Fatal("backend accepted a publicly writable control directory")
	}
	if _, err := Scan(context.Background(), path, localsend.ScanOptions{Timeout: time.Second}); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("client must reject an unsafe directory rather than start a backend: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0777 {
		t.Fatalf("foreign directory permissions changed: %v", err)
	}
}

func TestOwnerPreservesNonSocketPath(t *testing.T) {
	path := filepath.Join(privateDirectory(t), "control.sock")
	if err := os.WriteFile(path, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if owner, err := Listen(path); err == nil {
		owner.Close()
		t.Fatal("backend replaced an unrelated file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "unrelated" {
		t.Fatalf("file changed: %q, %v", data, err)
	}
}

func TestClientCancellationKeepsBackendAlive(t *testing.T) {
	path := filepath.Join(privateDirectory(t), "control.sock")
	owner, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	entered := make(chan struct{}, 2)
	cancelled := make(chan struct{}, 2)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- owner.Serve(ctx, func(scanCtx context.Context, _ localsend.ScanOptions) (map[string]models.Announcement, error) {
			entered <- struct{}{}
			<-scanCtx.Done()
			cancelled <- struct{}{}
			return nil, scanCtx.Err()
		})
	}()
	// Two independent requests prove disconnect cancels only its own work,
	// while the same backend continues serving the next scan.
	for range 2 {
		requestCtx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := Scan(requestCtx, path, localsend.ScanOptions{Timeout: time.Second})
			result <- err
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("backend did not accept scan")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("client cancellation lost: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("client did not stop")
		}
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("disconnected client's scan kept running")
		}
	}
	stop()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not shut down")
	}
}
