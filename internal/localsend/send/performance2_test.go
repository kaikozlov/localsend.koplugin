package send

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

func TestForwardSender_Start_UploadsAtMostTwoFilesConcurrently(t *testing.T) {
	app := fiber.New()
	app.Post(constants.PreuploadPath, func(c fiber.Ctx) error {
		var req struct {
			Files map[string]interface{} `json:"files"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		tokens := make(map[string]string, len(req.Files))
		for id := range req.Files {
			tokens[id] = "token-" + id
		}
		return c.JSON(map[string]interface{}{"sessionId": "parallel", "files": tokens})
	})

	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32
	app.Post(constants.UploadPath, func(c fiber.Ctx) error {
		now := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if now <= old || maxActive.CompareAndSwap(old, now) {
				break
			}
		}
		started <- struct{}{}
		<-release
		return c.SendStatus(fiber.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	dir := t.TempDir()
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{Alias: "Test", IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port))
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("file-%d", i)
		path := filepath.Join(dir, id)
		if err := os.WriteFile(path, []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		sender.files[id] = models.FileMeta{Id: id, Filename: id, FullPath: path, Size: int64(len(id))}
	}

	done := make(chan error, 1)
	go func() { done <- sender.Start() }()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("second upload did not start concurrently")
		}
	}
	if got := maxActive.Load(); got != 2 {
		close(release)
		t.Fatalf("max concurrent uploads = %d; want 2", got)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parallel upload did not complete")
	}
	if got := maxActive.Load(); got != 2 {
		t.Fatalf("max concurrent uploads = %d after completion; want 2", got)
	}
}

func TestNewUploadClient_Uses64KiBWriteBufferOnlyForHTTPS(t *testing.T) {
	httpsClient := newUploadClient(nil, true)
	if got := httpsClient.WriteBufferSize; got != 64*1024 {
		t.Fatalf("HTTPS upload write buffer = %d; want %d", got, 64*1024)
	}
	httpClient := newUploadClient(nil, false)
	if got := httpClient.WriteBufferSize; got != 0 {
		t.Fatalf("HTTP upload write buffer = %d; want default/zero to preserve zero-copy path", got)
	}
}

func BenchmarkHTTPSUploadWriteBuffer_1MiB(b *testing.B) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	path := filepath.Join(b.TempDir(), "payload.bin")
	payload := make([]byte, 1024*1024)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		b.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		size int
	}{
		{name: "4KiB", size: 4 * 1024},
		{name: "64KiB", size: 64 * 1024},
	} {
		b.Run(tc.name, func(b *testing.B) {
			client := newUploadClient(&tls.Config{InsecureSkipVerify: true}, true) //nolint:gosec -- loopback benchmark server
			client.WriteBufferSize = tc.size
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fd, err := os.Open(path)
				if err != nil {
					b.Fatal(err)
				}
				req := fasthttp.AcquireRequest()
				resp := fasthttp.AcquireResponse()
				req.SetRequestURI(server.URL)
				req.Header.SetMethod(fiber.MethodPost)
				req.SetBodyStream(fd, len(payload))
				err = client.Do(req, resp)
				_ = fd.Close()
				fasthttp.ReleaseRequest(req)
				fasthttp.ReleaseResponse(resp)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
