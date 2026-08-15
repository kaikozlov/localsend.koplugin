package recv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"localsend-cli/internal/localsend/constants"
)

func TestPreUploadHandler_RejectsMissingInfo(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)
	req := httptest.NewRequest(http.MethodPost, constants.PreuploadPath,
		strings.NewReader(`{"files":{"f":{"id":"f","fileName":"f.bin","size":1}}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d; want 400", resp.StatusCode)
	}
	if fr.sessman.HasActiveSessions() {
		t.Fatal("missing info created an active session")
	}
}

func TestPreUploadHandler_RejectsNegativeFileSize(t *testing.T) {
	fr := newTestReceiver()
	app := fiber.New()
	app.Post(constants.PreuploadPath, fr.preUploadHandler)
	req := httptest.NewRequest(http.MethodPost, constants.PreuploadPath,
		strings.NewReader(`{"info":{"alias":"Sender"},"files":{"f":{"id":"f","fileName":"f.bin","size":-1}}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d; want 400", resp.StatusCode)
	}
	if fr.sessman.HasActiveSessions() {
		t.Fatal("negative size created an active session")
	}
}
