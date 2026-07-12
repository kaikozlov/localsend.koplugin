package localsend

import (
	"crypto/tls"
	"encoding/json"
	"net"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/localsend/send"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

var deviceInfoHTTPClient = &fasthttp.Client{
	// #nosec G402 -- LocalSend authenticates self-signed certificates by fingerprint.
	TLSConfig: &tls.Config{InsecureSkipVerify: true},
}

// validDeviceTypes are the allowed deviceType values per protocol spec Section 7.1
var validDeviceTypes = map[string]bool{
	"mobile":   true,
	"desktop":  true,
	"web":      true,
	"headless": true,
	"server":   true,
}

// normalizeDeviceType validates deviceType and falls back to "desktop" for unknown values
// per protocol spec: "The official implementation falls back to desktop"
func normalizeDeviceType(deviceType string) string {
	if validDeviceTypes[deviceType] {
		return deviceType
	}
	return "desktop"
}

func GetDeviceInfo(ip string, https bool) (models.DeviceInfo, error) {
	remoteAddr := net.JoinHostPort(ip, constants.DefaultPortStr)
	scheme := utils.GetProtocolScheme(https)

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.URI().SetScheme(scheme)
	req.URI().SetHost(remoteAddr)
	req.URI().SetPath(constants.InfoPath)
	req.Header.SetMethod(fiber.MethodGet)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	if err := deviceInfoHTTPClient.Do(req, resp); err != nil {
		return models.DeviceInfo{}, err
	}

	err := constants.ParseError(resp.StatusCode())
	if err != nil {
		return models.DeviceInfo{}, err
	}

	var res models.DeviceInfo
	err = json.Unmarshal(resp.Body(), &res)
	if err != nil {
		return models.DeviceInfo{}, err
	}
	res.IP = ip
	res.DeviceType = normalizeDeviceType(res.DeviceType)

	return res, nil
}

func NewFileSender(useDownloadAPI ...bool) send.FileSender {
	if len(useDownloadAPI) > 0 {
		if useDownloadAPI[0] {
			return send.NewReverseSender()
		}
	}
	return send.NewForwardSender()
}
