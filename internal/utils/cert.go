package utils

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"strings"
	"time"
)

func SHA256ofCert(cert *x509.Certificate) string {
	hasher := sha256.New()
	hasher.Write(cert.Raw)

	return strings.ToUpper(hex.EncodeToString(hasher.Sum(nil)))
}

func FetchX509Cert(addr string) ([]*x509.Certificate, error) {
	conf := &tls.Config{
		InsecureSkipVerify: true,
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, conf)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	return conn.ConnectionState().PeerCertificates, nil
}
