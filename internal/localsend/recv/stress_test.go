//go:build stress

package recv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

func TestStress_HTTPReceive_1000FilesOf1MiB(t *testing.T) {
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	const (
		fileCount = 1000
		fileSize  = 1024 * 1024
	)

	fr := newTestReceiver()
	fr.saveToDir = t.TempDir()
	app := fiber.New()
	fr.registerRoutes(app)

	files := make(models.FileMetas, fileCount)
	for i := 0; i < fileCount; i++ {
		id := fmt.Sprintf("file-%04d", i)
		files[id] = models.FileMeta{
			Id:       id,
			Filename: fmt.Sprintf("stress/dir%02d/file-%04d.bin", i%20, i),
			Size:     fileSize,
			FileMIME: "application/octet-stream",
		}
	}
	prepare := models.PreUploadReq{
		Info: &models.SenderInfo{
			DeviceInfo: models.DeviceInfo{Alias: "Stress Sender", Version: "2.2", DeviceType: "headless"},
			Port:       constants.DefaultPort,
			Protocol:   "http",
		},
		Files: files,
	}
	body, err := json.Marshal(prepare)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", constants.PreuploadPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("prepare status = %d; want 200", resp.StatusCode)
	}
	var prepared models.PreUploadResp
	if err := json.NewDecoder(resp.Body).Decode(&prepared); err != nil {
		t.Fatal(err)
	}
	if len(prepared.Tokens) != fileCount {
		t.Fatalf("accepted files = %d; want %d", len(prepared.Tokens), fileCount)
	}

	payload := make([]byte, fileSize)
	started := time.Now()
	for i := 0; i < fileCount; i++ {
		id := fmt.Sprintf("file-%04d", i)
		token := prepared.Tokens[id]
		url := fmt.Sprintf("%s?sessionId=%s&fileId=%s&token=%s", constants.UploadPath, prepared.SessionId, id, token)
		req := httptest.NewRequest("POST", url, bytes.NewReader(payload))
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("upload %s: %v", id, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("upload %s status = %d; want 200", id, resp.StatusCode)
		}
	}

	elapsed := time.Since(started)
	mib := float64(fileCount*fileSize) / (1024 * 1024)
	t.Logf("received %.0f MiB across %d files in %s (%.1f MiB/s)", mib, fileCount, elapsed, mib/elapsed.Seconds())
}
