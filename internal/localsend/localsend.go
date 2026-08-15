package localsend

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/localsend/send"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

const maxDeviceInfoBytes = 1 << 20

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
	client := &http.Client{Timeout: 30 * time.Second}
	if https {
		privateKeyFile, certFile, err := lsutils.GetCertPaths()
		if err != nil {
			return models.DeviceInfo{}, err
		}
		cert, err := lsutils.LoadOrGenTLScert(privateKeyFile, certFile)
		if err != nil {
			return models.DeviceInfo{}, err
		}
		client.Transport = &http.Transport{TLSClientConfig: lsutils.TLSClientConfig(cert, "")}
	}
	url := fmt.Sprintf("%s://%s%s", scheme, remoteAddr, constants.InfoPath)
	resp, err := client.Get(url)
	if err != nil {
		return models.DeviceInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	err = constants.ParseError(resp.StatusCode)
	if err != nil {
		return models.DeviceInfo{}, err
	}

	var res models.DeviceInfo
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDeviceInfoBytes+1))
	if err != nil || len(body) > maxDeviceInfoBytes {
		return models.DeviceInfo{}, fmt.Errorf("invalid device info response")
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return models.DeviceInfo{}, err
	}
	if https {
		if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
			return models.DeviceInfo{}, fmt.Errorf("HTTPS device info response carried no peer certificate")
		}
		res.Fingerprint = utils.SHA256ofCert(resp.TLS.PeerCertificates[0])
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
