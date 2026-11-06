// Package control connects short-lived scan commands to the process that owns
// the LocalSend listener. It never exposes a control endpoint to the LAN.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"localsend-cli/internal/localsend"
	"localsend-cli/internal/models"
)

var (
	ErrBusy        = errors.New("LocalSend backend is already starting or running")
	ErrUnavailable = errors.New("LocalSend backend is not running")
)

const maxMessageBytes = 1 << 20

// DefaultPath separates users and configured ports, including standalone CLI use.
func DefaultPath(port int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("localsend-%d-%d", os.Getuid(), port), "control.sock")
}

type ScanFunc func(context.Context, localsend.ScanOptions) (map[string]models.Announcement, error)

type response struct {
	Devices map[string]models.Announcement `json:"devices,omitempty"`
	Error   string                         `json:"error,omitempty"`
}

// Server holds an exclusive owner lock until Close. Keep the lock file's inode:
// unlinking it would let a concurrent process lock a different file at that path.
type Server struct {
	listener  net.Listener
	lock      *os.File
	closeOnce sync.Once
}

func checkDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("control directory must be private and owned by the current user: %s", dir)
	}
	return nil
}

// Listen reserves ownership before the network receiver starts. The parent must
// be private and owned by this user; never chmod or replace another user's path.
func Listen(path string) (*Server, error) {
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create control directory: %w", err)
	}
	if err := checkDirectory(dir); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "owner.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open backend owner lock: %w", err)
	}
	syscall.CloseOnExec(fd)
	lock := os.NewFile(uintptr(fd), lockPath)
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock backend owner: %w", err)
	}
	fail := func(err error) (*Server, error) {
		_ = lock.Close()
		return nil, err
	}
	if existing, err := os.Lstat(path); err == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return fail(fmt.Errorf("refusing to replace non-socket control path: %s", path))
		}
		// The owner lock proves no conforming backend owns this stale socket.
		if err := os.Remove(path); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return fail(fmt.Errorf("listen on private control socket: %w", err))
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return fail(err)
	}
	return &Server{listener: listener, lock: lock}, nil
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		_ = s.listener.Close()
		_ = s.lock.Close()
	})
}

// Serve must start only once the network listener is ready. A client disconnect
// cancels that client's scan, not the receiver or any active file transfer.
func (s *Server) Serve(ctx context.Context, scan ScanFunc) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, func() { _ = s.listener.Close() })
	defer stop()
	var handlers sync.WaitGroup
	defer func() {
		cancel()
		handlers.Wait()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			s.serveConnection(ctx, conn, scan)
		}()
	}
}

func (s *Server) serveConnection(parent context.Context, conn net.Conn, scan ScanFunc) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var options localsend.ScanOptions
	decoder := json.NewDecoder(io.LimitReader(conn, 4096))
	if err := decoder.Decode(&options); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if options.Timeout <= 0 || options.Timeout > 2*time.Minute || options.LegacyTimeout < 0 || options.LegacyTimeout > 2*time.Minute {
		_ = json.NewEncoder(conn).Encode(response{Error: "scan durations must be between zero and two minutes, with a positive collection window"})
		return
	}
	// The client sends one request and then only reads. EOF (or any unexpected
	// extra byte) ends the request; this also handles a scanner killed by Lua.
	go func() {
		var extra [1]byte
		_, _ = conn.Read(extra[:])
		cancel()
	}()
	devices, err := scan(ctx, options)
	result := response{Devices: devices}
	if err != nil {
		result.Error = err.Error()
		result.Devices = nil
	}
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = json.NewEncoder(conn).Encode(result)
}

// Scan only reports ErrUnavailable when no socket accepted the connection. A
// backend failure after connection must not silently create a competing server.
func Scan(ctx context.Context, path string, options localsend.ScanOptions) (map[string]models.Announcement, error) {
	if err := checkDirectory(filepath.Dir(path)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUnavailable
		}
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(options); err != nil {
		return nil, err
	}
	var result response
	if err := json.NewDecoder(io.LimitReader(conn, maxMessageBytes)).Decode(&result); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read backend discovery result: %w", err)
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	return result.Devices, nil
}
