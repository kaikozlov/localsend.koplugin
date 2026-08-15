// Command certgen pre-generates the LocalSend TLS identity certificate into
// the directory given by --dir (as certs/server.key.pem and certs/server.crt)
// and prints the uppercase-hex SHA-256 fingerprint of the DER certificate.
//
// The official-interop harness runs the Go binary from a read-only volume, so
// the receiver cannot create its certificate at runtime. This tool produces
// the exact files LoadOrGenTLScert would have generated, plus the fingerprint
// the Rust test needs to pin the Go receiver's certificate.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	lsutils "localsend-cli/internal/localsend/utils"
)

func main() {
	dir := flag.String("dir", ".", "directory that will contain the certs/ subdirectory")
	flag.Parse()

	certDir := filepath.Join(*dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		fatal(err)
	}
	privKeyFile := filepath.Join(certDir, "server.key.pem")
	certFile := filepath.Join(certDir, "server.crt")

	cert, err := lsutils.LoadOrGenTLScert(privKeyFile, certFile)
	if err != nil {
		fatal(err)
	}
	fingerprint, err := lsutils.CertificateFingerprint(cert)
	if err != nil {
		fatal(err)
	}
	fmt.Println(fingerprint)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "certgen:", err)
	os.Exit(1)
}
