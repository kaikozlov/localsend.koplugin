package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenAndSaveTLScertPermissions verifies private key permissions
// Private keys should be saved with 0600 permissions (owner only)
func TestGenAndSaveTLScertPermissions(t *testing.T) {
	dir := t.TempDir()
	privKeyFile := filepath.Join(dir, "key.pem")
	certFile := filepath.Join(dir, "cert.pem")

	_, err := GenAndSaveTLScert(privKeyFile, certFile)
	if err != nil {
		t.Fatalf("GenAndSaveTLScert failed: %v", err)
	}

	t.Run("private key has 0600 permissions", func(t *testing.T) {
		info, err := os.Stat(privKeyFile)
		if err != nil {
			t.Fatalf("failed to stat private key file: %v", err)
		}

		mode := info.Mode().Perm()
		expected := os.FileMode(0o600)
		if mode != expected {
			t.Errorf("private key permissions: expected %o, got %o", expected, mode)
		}
	})

	t.Run("certificate has 0644 permissions", func(t *testing.T) {
		info, err := os.Stat(certFile)
		if err != nil {
			t.Fatalf("failed to stat certificate file: %v", err)
		}

		mode := info.Mode().Perm()
		expected := os.FileMode(0o644)
		if mode != expected {
			t.Errorf("certificate permissions: expected %o, got %o", expected, mode)
		}
	})
}

// TestGenAndSaveTLScertGeneratesValidCert verifies the certificate is valid
func TestGenAndSaveTLScertGeneratesValidCert(t *testing.T) {
	dir := t.TempDir()
	privKeyFile := filepath.Join(dir, "key.pem")
	certFile := filepath.Join(dir, "cert.pem")

	cert, err := GenAndSaveTLScert(privKeyFile, certFile)
	if err != nil {
		t.Fatalf("GenAndSaveTLScert failed: %v", err)
	}

	// Verify certificate has at least one certificate in chain
	if len(cert.Certificate) == 0 {
		t.Error("certificate chain is empty")
	}

	// Verify private key is present
	if cert.PrivateKey == nil {
		t.Error("private key is nil")
	}
}

// TestLoadOrGenTLScert tests loading existing or generating new certs
func TestLoadOrGenTLScert(t *testing.T) {
	t.Run("generates new cert when files don't exist", func(t *testing.T) {
		dir := t.TempDir()
		privKeyFile := filepath.Join(dir, "key.pem")
		certFile := filepath.Join(dir, "cert.pem")

		cert, err := LoadOrGenTLScert(privKeyFile, certFile)
		if err != nil {
			t.Fatalf("LoadOrGenTLScert failed: %v", err)
		}

		if len(cert.Certificate) == 0 {
			t.Error("certificate chain is empty")
		}

		// Verify files were created
		if _, err := os.Stat(privKeyFile); os.IsNotExist(err) {
			t.Error("private key file was not created")
		}
		if _, err := os.Stat(certFile); os.IsNotExist(err) {
			t.Error("certificate file was not created")
		}
	})

	t.Run("loads existing cert when files exist", func(t *testing.T) {
		dir := t.TempDir()
		privKeyFile := filepath.Join(dir, "key.pem")
		certFile := filepath.Join(dir, "cert.pem")

		// Generate first
		cert1, err := GenAndSaveTLScert(privKeyFile, certFile)
		if err != nil {
			t.Fatalf("GenAndSaveTLScert failed: %v", err)
		}

		// Load existing
		cert2, err := LoadOrGenTLScert(privKeyFile, certFile)
		if err != nil {
			t.Fatalf("LoadOrGenTLScert failed: %v", err)
		}

		// Should be the same certificate
		if len(cert1.Certificate) != len(cert2.Certificate) {
			t.Error("loaded certificate differs from generated certificate")
		}
	})
}

// TestGenAlias tests alias generation
func TestGenAlias(t *testing.T) {
	t.Run("generates non-empty alias", func(t *testing.T) {
		alias := GenAlias()
		if alias == "" {
			t.Error("alias should not be empty")
		}
	})

	t.Run("alias contains space", func(t *testing.T) {
		alias := GenAlias()
		hasSpace := false
		for _, c := range alias {
			if c == ' ' {
				hasSpace = true
				break
			}
		}
		if !hasSpace {
			t.Errorf("alias should contain a space: %q", alias)
		}
	})

	t.Run("generates different aliases", func(t *testing.T) {
		seen := make(map[string]bool)
		// Generate many aliases
		for i := 0; i < 100; i++ {
			alias := GenAlias()
			seen[alias] = true
		}
		// With 38 adjectives and 26 fruits = 988 combinations
		// 100 attempts should generate at least 10 unique aliases
		if len(seen) < 10 {
			t.Errorf("expected at least 10 unique aliases, got %d", len(seen))
		}
	})
}

// TestGenFingerprint tests fingerprint generation
func TestGenFingerprint(t *testing.T) {
	t.Run("generates non-empty fingerprint", func(t *testing.T) {
		fp := GenFingerprint()
		if fp == "" {
			t.Error("fingerprint should not be empty")
		}
	})

	t.Run("generates unique fingerprints", func(t *testing.T) {
		fp1 := GenFingerprint()
		fp2 := GenFingerprint()
		if fp1 == fp2 {
			t.Error("fingerprints should be unique")
		}
	})

	t.Run("fingerprint is UUID format", func(t *testing.T) {
		fp := GenFingerprint()
		// UUID format: 8-4-4-4-12 = 36 characters
		if len(fp) != 36 {
			t.Errorf("fingerprint should be 36 characters (UUID), got %d", len(fp))
		}
	})
}

// TestNewWebServer tests web server creation
func TestNewWebServer(t *testing.T) {
	t.Run("creates server without template engine", func(t *testing.T) {
		app := NewWebServer()
		if app == nil {
			t.Error("should return non-nil app")
		}
		cfg := app.Config()
		if !cfg.StreamRequestBody {
			t.Error("StreamRequestBody should be enabled")
		}
		if cfg.BodyLimit != 1*1024*1024*1024 {
			t.Errorf("BodyLimit = %d; want %d", cfg.BodyLimit, 1*1024*1024*1024)
		}
		_ = app.Shutdown()
	})

	t.Run("creates server with template engine", func(t *testing.T) {
		app := NewWebServer(true)
		if app == nil {
			t.Error("should return non-nil app")
		}
		_ = app.Shutdown()
	})
}

// TestGetCertDir tests the certificate directory location
// Verifies that certificates are stored next to the binary, not in /tmp
func TestGetCertDir(t *testing.T) {
	t.Run("returns path ending with certs", func(t *testing.T) {
		dir, err := GetCertDir()
		if err != nil {
			t.Fatalf("GetCertDir failed: %v", err)
		}

		if filepath.Base(dir) != "certs" {
			t.Errorf("expected path to end with 'certs', got %q", dir)
		}
	})

	t.Run("does not use tmp directory", func(t *testing.T) {
		// During `go test`, the test binary is compiled to /tmp/go-build.../
		// so GetCertDir() will return a path in /tmp. This is expected
		// because GetCertDir() returns a path next to the executable.
		// In production, the binary will be in a persistent location.
		exePath, err := os.Executable()
		if err != nil {
			t.Fatalf("failed to get executable path: %v", err)
		}
		exePath, _ = filepath.EvalSymlinks(exePath)
		tmpDir := os.TempDir()
		if strings.HasPrefix(exePath, tmpDir) || strings.HasPrefix(exePath, "/tmp") {
			t.Skip("skipping: test binary is in temp directory (go test environment)")
		}

		dir, err := GetCertDir()
		if err != nil {
			t.Fatalf("GetCertDir failed: %v", err)
		}

		// Should NOT be in /tmp or os.TempDir()
		if strings.HasPrefix(dir, tmpDir) {
			t.Errorf("cert directory should NOT be in temp dir, got %q", dir)
		}

		// Also check for common tmp patterns
		if strings.HasPrefix(dir, "/tmp") || strings.HasPrefix(dir, "/var/tmp") {
			t.Errorf("cert directory should NOT be in /tmp or /var/tmp, got %q", dir)
		}
	})

	t.Run("creates directory with 0700 permissions", func(t *testing.T) {
		dir, err := GetCertDir()
		if err != nil {
			t.Fatalf("GetCertDir failed: %v", err)
		}

		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("failed to stat cert directory: %v", err)
		}

		if !info.IsDir() {
			t.Error("cert path should be a directory")
		}

		mode := info.Mode().Perm()
		expected := os.FileMode(0700)
		if mode != expected {
			t.Errorf("cert directory permissions: expected %o, got %o", expected, mode)
		}
	})

	t.Run("is next to executable", func(t *testing.T) {
		dir, err := GetCertDir()
		if err != nil {
			t.Fatalf("GetCertDir failed: %v", err)
		}

		exePath, err := os.Executable()
		if err != nil {
			t.Fatalf("failed to get executable path: %v", err)
		}
		exePath, _ = filepath.EvalSymlinks(exePath)
		exeDir := filepath.Dir(exePath)

		expectedDir := filepath.Join(exeDir, "certs")
		if dir != expectedDir {
			t.Errorf("expected cert dir to be %q, got %q", expectedDir, dir)
		}
	})
}

// TestGetCertPaths tests the certificate file paths
func TestGetCertPaths(t *testing.T) {
	t.Run("returns correct filenames", func(t *testing.T) {
		privKey, cert, err := GetCertPaths()
		if err != nil {
			t.Fatalf("GetCertPaths failed: %v", err)
		}

		if filepath.Base(privKey) != "server.key.pem" {
			t.Errorf("expected private key filename 'server.key.pem', got %q", filepath.Base(privKey))
		}

		if filepath.Base(cert) != "server.crt" {
			t.Errorf("expected cert filename 'server.crt', got %q", filepath.Base(cert))
		}
	})

	t.Run("paths are in certs directory", func(t *testing.T) {
		privKey, cert, err := GetCertPaths()
		if err != nil {
			t.Fatalf("GetCertPaths failed: %v", err)
		}

		privKeyDir := filepath.Dir(privKey)
		certDir := filepath.Dir(cert)

		if filepath.Base(privKeyDir) != "certs" {
			t.Errorf("private key should be in 'certs' directory, got %q", privKeyDir)
		}

		if filepath.Base(certDir) != "certs" {
			t.Errorf("cert should be in 'certs' directory, got %q", certDir)
		}
	})

	t.Run("paths are NOT in tmp", func(t *testing.T) {
		// During `go test`, the test binary is compiled to /tmp/go-build.../
		// so GetCertPaths() will return paths in /tmp. This is expected
		// because it uses GetCertDir() which returns a path next to the executable.
		// In production, the binary will be in a persistent location.
		exePath, err := os.Executable()
		if err != nil {
			t.Fatalf("failed to get executable path: %v", err)
		}
		exePath, _ = filepath.EvalSymlinks(exePath)
		tmpDir := os.TempDir()
		if strings.HasPrefix(exePath, tmpDir) || strings.HasPrefix(exePath, "/tmp") {
			t.Skip("skipping: test binary is in temp directory (go test environment)")
		}

		privKey, cert, err := GetCertPaths()
		if err != nil {
			t.Fatalf("GetCertPaths failed: %v", err)
		}

		if strings.HasPrefix(privKey, tmpDir) || strings.HasPrefix(privKey, "/tmp") {
			t.Errorf("private key should NOT be in temp dir, got %q", privKey)
		}

		if strings.HasPrefix(cert, tmpDir) || strings.HasPrefix(cert, "/tmp") {
			t.Errorf("cert should NOT be in temp dir, got %q", cert)
		}
	})
}
