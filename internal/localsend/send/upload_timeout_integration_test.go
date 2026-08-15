//go:build integration

package send

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

func TestIntegration_ForwardSenderUploadHasNoThirtySecondTotalDeadline(t *testing.T) {
	app := fiber.New()
	app.Post(constants.PreuploadPath, func(c fiber.Ctx) error {
		return c.JSON(map[string]interface{}{
			"sessionId": "slow-session",
			"files":     map[string]string{"file1": "token"},
		})
	})
	app.Post(constants.UploadPath, func(c fiber.Ctx) error {
		if string(c.Body()) != "payload" {
			t.Fatalf("upload body = %q; want payload", c.Body())
		}
		time.Sleep(31 * time.Second)
		return c.SendStatus(fiber.StatusOK)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() { _ = app.Listener(listener) }()
	defer func() { _ = app.Shutdown() }()

	path := filepath.Join(t.TempDir(), "slow.bin")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	sender.files["file1"] = models.FileMeta{Id: "file1", Filename: "slow.bin", FullPath: path, Size: 7}
	started := time.Now()
	if err := sender.Start(); err != nil {
		t.Fatalf("slow upload failed after %s: %v", time.Since(started), err)
	}
	if elapsed := time.Since(started); elapsed < 30*time.Second {
		t.Fatalf("test did not cross the former total deadline: %s", elapsed)
	}
}
