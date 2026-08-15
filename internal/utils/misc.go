package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

func WaitForSignal() chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)

	return ch
}

func ForEachAsync[T any](arr []T, wg *sync.WaitGroup, do func(value T)) {
	for _, val := range arr {
		wg.Add(1)
		go func(val T) {
			defer wg.Done()

			do(val)
		}(val)
	}
}

// FileIOBufferSize matches LocalSend 1.18's physical file/hash I/O buffer.
// It is intentionally larger than WebRTC's 16 KiB wire frame size.
const FileIOBufferSize = 512 * 1024

var fileIOBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, FileIOBufferSize)
		return &buf
	},
}

type readerOnly struct{ io.Reader }
type writerOnly struct{ io.Writer }

// CopyWithFileIOBuffer copies using the shared 512 KiB file-I/O buffer pool.
// This amortizes read/write syscalls without forcing callers to allocate a large
// scratch buffer for every file in a many-file transfer.
func CopyWithFileIOBuffer(dst io.Writer, src io.Reader) (int64, error) {
	bufPtr := fileIOBufferPool.Get().(*[]byte)
	defer fileIOBufferPool.Put(bufPtr)
	// Hide optional WriterTo/ReaderFrom methods so io.CopyBuffer cannot bypass
	// the supplied buffer. In particular, *os.File may expose WriterTo on newer
	// Go versions, which would otherwise make the 512 KiB hash buffer cosmetic.
	return io.CopyBuffer(writerOnly{dst}, readerOnly{src}, *bufPtr)
}

func SHA256ofFile(fpath string) (string, error) {
	fd, err := os.Open(fpath)
	if err != nil {
		return "", err
	}
	defer func() { _ = fd.Close() }()

	hasher := sha256.New()
	_, err = CopyWithFileIOBuffer(hasher, fd)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// getMyIPv4Addr get ipv4 address of every RUNNING interfaces on the host
// Note: ipv6, loopback and non-private addressess are ignored
func GetMyIPv4Addr() ([]net.IP, error) {
	intfs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	res := make([]net.IP, 0, len(intfs))

	for _, intf := range intfs {
		addrs, _ := intf.Addrs()
		for idx := range addrs {
			ip, _, _ := net.ParseCIDR(addrs[idx].String())
			if ip.To4() != nil && !ip.IsLoopback() && ip.IsPrivate() && (intf.Flags&net.FlagRunning != 0) {
				res = append(res, ip)
			}
		}
	}
	return res, nil
}

// RandChoice returns a random element from the slice.
// Uses math/rand/v2 which is automatically seeded with a cryptographically
// secure seed in Go 1.22+. Safe for non-cryptographic randomness.
func RandChoice[T any](l []T) T {
	if len(l) == 0 {
		var zero T
		return zero
	}
	randIndex := rand.IntN(len(l))

	return l[randIndex]
}

// GetProtocolScheme returns "https" or "http" based on the useHTTPS flag.
func GetProtocolScheme(useHTTPS bool) string {
	if useHTTPS {
		return "https"
	}
	return "http"
}

// ParseExtensionList parses a comma-separated list of file extensions,
// normalizes them to lowercase and trims whitespace.
// Returns nil if input is empty.
func ParseExtensionList(extString string) []string {
	if extString == "" {
		return nil
	}
	parts := strings.Split(extString, ",")
	result := make([]string, 0, len(parts))
	for _, ext := range parts {
		ext = strings.TrimSpace(strings.ToLower(ext))
		if ext != "" {
			result = append(result, ext)
		}
	}
	return result
}

// EnsureDirectory creates a directory if it doesn't exist.
// Returns nil if the directory already exists or was created successfully.
func EnsureDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("Failed to create directory", "dir", dir, "error", err)
		return err
	}
	return nil
}

// IsExtensionAllowed checks if a filename has an extension in the allowed list.
// Returns true if allowedExtensions is empty (accept all) or if the extension is found.
func IsExtensionAllowed(filename string, allowedExtensions []string) bool {
	if len(allowedExtensions) == 0 {
		return true
	}

	// Get the extension (without the dot, lowercase)
	ext := GetFileExtension(filename)
	if ext == "" {
		return false // No extension, reject
	}

	// Check if it's in the allowed list
	for _, allowed := range allowedExtensions {
		if ext == allowed {
			return true
		}
	}

	return false
}

// GetFileExtension returns the lowercase extension without the leading dot.
// Returns empty string if no extension.
func GetFileExtension(filename string) string {
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			if i == len(filename)-1 {
				return "" // Ends with dot, no extension
			}
			return strings.ToLower(filename[i+1:])
		}
		if filename[i] == '/' || filename[i] == '\\' {
			return "" // Hit path separator before dot
		}
	}
	return ""
}

// SanitizeForLog removes or escapes control characters that could cause issues in logs.
// Preserves printable characters and common whitespace (space, tab).
func SanitizeForLog(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	for _, r := range s {
		if r >= 32 || r == '\t' {
			result.WriteRune(r)
		}
		// Control characters are simply omitted
	}
	return result.String()
}
