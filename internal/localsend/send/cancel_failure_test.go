package send

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

func TestForwardSender_Start_CancelsReceiverAfterLocalSendFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vanishes.bin")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post(constants.PreuploadPath, func(c fiber.Ctx) error {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return c.JSON(map[string]interface{}{
			"sessionId": "must-cancel",
			"files":     map[string]string{"file1": "token"},
		})
	})
	var cancels atomic.Int32
	app.Post(constants.CancelPath, func(c fiber.Ctx) error {
		if got := c.Query("sessionId"); got != "must-cancel" {
			t.Fatalf("cancel sessionId = %q; want must-cancel", got)
		}
		cancels.Add(1)
		return c.SendStatus(fiber.StatusOK)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() { _ = app.Listener(listener) }()
	defer func() { _ = app.Shutdown() }()

	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{IP: "127.0.0.1"}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	sender.files["file1"] = models.FileMeta{Id: "file1", Filename: "vanishes.bin", FullPath: path, Size: 7}
	if err := sender.Start(); err == nil {
		t.Fatal("Start unexpectedly succeeded after source file disappeared")
	}
	if got := cancels.Load(); got != 1 {
		t.Fatalf("cancel requests = %d; want 1", got)
	}
}
