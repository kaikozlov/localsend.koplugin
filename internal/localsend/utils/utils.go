package utils

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"localsend-cli/internal/utils"
	"localsend-cli/templates"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/template/html/v3"
	"github.com/google/uuid"
)

var aliasAdj = []string{
	"Adorable",
	"Beautiful",
	"Big",
	"Bright",
	"Clean",
	"Clever",
	"Cool",
	"Cute",
	"Cunning",
	"Determined",
	"Energetic",
	"Efficient",
	"Fantastic",
	"Fast",
	"Fine",
	"Fresh",
	"Good",
	"Gorgeous",
	"Great",
	"Handsome",
	"Hot",
	"Kind",
	"Lovely",
	"Mystic",
	"Neat",
	"Nice",
	"Patient",
	"Pretty",
	"Powerful",
	"Rich",
	"Secret",
	"Smart",
	"Solid",
	"Special",
	"Strategic",
	"Strong",
	"Tidy",
	"Wise",
}

var aliasFruit = []string{
	"Apple",
	"Avocado",
	"Banana",
	"Blackberry",
	"Blueberry",
	"Broccoli",
	"Carrot",
	"Cherry",
	"Coconut",
	"Grape",
	"Lemon",
	"Lettuce",
	"Mango",
	"Melon",
	"Mushroom",
	"Onion",
	"Orange",
	"Papaya",
	"Peach",
	"Pear",
	"Pineapple",
	"Potato",
	"Pumpkin",
	"Raspberry",
	"Strawberry",
	"Tomato",
}

// GetCertDir returns the directory for storing TLS certificates.
// It creates a "certs" subdirectory next to the binary executable.
// Returns an error if the directory cannot be created (e.g., read-only filesystem).
func GetCertDir() (string, error) {
	// Get the path to the executable
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve any symlinks to get the actual binary location
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable path: %w", err)
	}

	// Create certs directory next to the binary
	certDir := filepath.Join(filepath.Dir(exePath), "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create certs directory at %s: %w", certDir, err)
	}

	return certDir, nil
}

// GetCertPaths returns the paths for the TLS private key and certificate files.
// It uses GetCertDir() to determine the directory.
func GetCertPaths() (privKeyFile, certFile string, err error) {
	certDir, err := GetCertDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(certDir, "server.key.pem"), filepath.Join(certDir, "server.crt"), nil
}

// GenAndSaveTLScert generates an Ed25519 TLS certificate (per protocol spec v3:
// "Key Algorithm: RSA 2048 or Ed25519"). Ed25519 is preferred for constrained
// devices as it generates almost instantly compared to RSA.
func GenAndSaveTLScert(privKeyFile, certFile string) (tls.Certificate, error) {
	// Generate Ed25519 key pair - much faster than RSA on constrained devices
	pubkey, privkey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "LocalSend User",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years per spec
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		// SAN is required by modern TLS clients (iOS, etc.)
		DNSNames:    []string{"localhost", "localsend"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("0.0.0.0")},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, pubkey, privkey)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Marshal Ed25519 private key to PKCS8 format
	privBytes, err := x509.MarshalPKCS8PrivateKey(privkey)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPrivKeyPem := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	// save certificate (public, can be group-readable)
	err = os.WriteFile(certFile, certPem, 0o644)
	if err != nil {
		return tls.Certificate{}, err
	}

	// save private key (restricted permissions - owner only)
	err = os.WriteFile(privKeyFile, certPrivKeyPem, 0o600)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.X509KeyPair(certPem, certPrivKeyPem)
}

func LoadOrGenTLScert(privKeyFile, certFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, privKeyFile)
	if err == nil {
		return cert, nil
	}

	return GenAndSaveTLScert(privKeyFile, certFile)
}

func GenAlias() string {
	adj := utils.RandChoice(aliasAdj)
	fruit := utils.RandChoice(aliasFruit)

	return adj + " " + fruit
}

// GenFingerprint generates a random fingerprint for HTTP mode.
// In HTTPS mode, the fingerprint is derived from the TLS certificate instead.
func GenFingerprint() string {
	return uuid.NewString()
}

func NewWebServer(withTemplateEngine ...bool) *fiber.App {
	config := fiber.Config{
		StreamRequestBody: true,
		//	BodyLimit:             100 * 1024 * 1024 * 1024, // 100G
		BodyLimit: 1 * 1024 * 1024 * 1024, // 1G (for 32-bit)
	}

	if len(withTemplateEngine) > 0 {
		if withTemplateEngine[0] {
			config.Views = html.NewFileSystem(http.FS(templates.TemplatesFS), ".html")
		}
	}

	return fiber.New(config)
}

// ListenWithTLS starts the Fiber server with optional TLS support.
func ListenWithTLS(server *fiber.App, addr string, cert tls.Certificate, useHTTPS bool) error {
	config := fiber.ListenConfig{
		DisableStartupMessage: true,
		ListenerNetwork:       "tcp",
	}
	if useHTTPS {
		config.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	return server.Listen(addr, config)
}
