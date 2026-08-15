package scan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"localsend-cli/internal/localsend"
	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	apputils "localsend-cli/internal/utils"
)

const scanRegisterBodyLimit = 64 << 10

type registerCallbackServer struct {
	port     int
	addr     string
	server   *http.Server
	done     chan error
	listener net.Listener
}

func startRegisterCallbackServer(ctx context.Context, scanner *localsend.Discoverer, cert tls.Certificate, identity models.DeviceInfo) (*registerCallbackServer, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("listen for discovery register callbacks: %w", err)
	}
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(constants.RegisterPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		r.Body = http.MaxBytesReader(w, r.Body, scanRegisterBodyLimit)
		var peer models.Announcement
		if err := json.NewDecoder(r.Body).Decode(&peer); err != nil {
			http.Error(w, "invalid registration", http.StatusBadRequest)
			return
		}
		if peer.Alias == "" || peer.Version == "" {
			http.Error(w, "invalid registration", http.StatusBadRequest)
			return
		}

		certFingerprint := apputils.SHA256ofCert(r.TLS.PeerCertificates[0])
		if peer.Fingerprint != "" && !strings.EqualFold(peer.Fingerprint, certFingerprint) {
			http.Error(w, "fingerprint does not match client certificate", http.StatusForbidden)
			return
		}
		peer.Fingerprint = certFingerprint
		if peer.Protocol == "" {
			peer.Protocol = "https"
		}
		if peer.Port == 0 {
			peer.Port = constants.DefaultPort
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		peer.IP = host
		scanner.RegisterDevice(peer)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identity)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       2 * time.Second,
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates:          []tls.Certificate{cert},
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: lsutils.VerifyClientCertificate(),
	})
	result := &registerCallbackServer{
		port:     port,
		addr:     listener.Addr().String(),
		server:   server,
		done:     make(chan error, 1),
		listener: listener,
	}
	go func() {
		err := server.Serve(tlsListener)
		if err == http.ErrServerClosed {
			err = nil
		}
		result.done <- err
		close(result.done)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return result, nil
}

func (s *registerCallbackServer) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}
