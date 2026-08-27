package send

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"

	"github.com/gofiber/fiber/v3"
)

func TestReverseSender_LegacyV1InfoReturnsSameDownloadIdentityAsV2(t *testing.T) {
	rs := NewReverseSender()
	identity := models.NewDeviceInfo("Download Device", "download-fingerprint")
	if err := rs.Init(&identity, false); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	app := fiber.New()
	rs.registerRoutes(app)

	readInfo := func(path string) models.DeviceInfo {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("GET %s status = %d; want 200", path, resp.StatusCode)
		}
		defer func() { _ = resp.Body.Close() }()
		var info models.DeviceInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			t.Fatalf("decode GET %s: %v", path, err)
		}
		return info
	}

	v2 := readInfo(constants.InfoPath)
	v1 := readInfo(constants.InfoPathV1)
	if v1 != v2 {
		t.Fatalf("legacy identity = %#v; want v2 identity %#v", v1, v2)
	}
	if !v1.Download || v1.Alias == "" || v1.Fingerprint == "" {
		t.Fatalf("legacy reverse-send identity is not useful: %#v", v1)
	}
}
