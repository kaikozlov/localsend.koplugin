package localsend

import (
	"crypto/tls"
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

func newDeviceInfoHTTPClient(tlsConfig *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// LocalSend peers are local-network endpoints. Never route discovery/info
	// traffic through HTTP_PROXY/HTTPS_PROXY, matching LocalSend 1.18.2.
	transport.Proxy = nil
	transport.TLSClientConfig = tlsConfig

	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		// A peer must not be able to redirect LocalSend control traffic to an
		// arbitrary destination. Return the 3xx response to the caller instead.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func GetDeviceInfo(ip string, https bool) (models.DeviceInfo, error) {
	remoteAddr := net.JoinHostPort(ip, constants.DefaultPortStr)
	scheme := utils.GetProtocolScheme(https)
	var tlsConfig *tls.Config
	if https {
		privateKeyFile, certFile, err := lsutils.GetCertPaths()
		if err != nil {
			return models.DeviceInfo{}, err
		}
		cert, err := lsutils.LoadOrGenTLScert(privateKeyFile, certFile)
		if err != nil {
			return models.DeviceInfo{}, err
		}
		tlsConfig = lsutils.TLSClientConfig(cert, "")
	}
	client := newDeviceInfoHTTPClient(tlsConfig)
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
