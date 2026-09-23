package bambulab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"fmt"
	"net"
	"time"
)

// defaultPrinterCAPEM is the public printer CA bundle shipped with Bambu Studio.
//
//go:embed certs/printer.cer
var defaultPrinterCAPEM []byte

// DefaultPrinterTLSConfig verifies a printer using the bundled Bambu CA bundle.
func DefaultPrinterTLSConfig(serial string) (*tls.Config, error) {
	return PrinterTLSConfig(serial, defaultPrinterCAPEM)
}

// PrinterTLSConfig trusts only the supplied CA and verifies the printer identity.
// Older printer certificates use a legacy Common Name without SAN extensions.
func PrinterTLSConfig(serial string, caPEM []byte) (*tls.Config, error) {
	if serial == "" {
		return nil, fmt.Errorf("bambulab: printer serial is required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("bambulab: no CA certificates found")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12, ServerName: serial, RootCAs: roots,
		ClientSessionCache: tls.NewLRUClientSessionCache(8),
		// Built-in hostname verification rejects legacy CN-only certificates.
		// VerifyConnection performs BOTH chain and identity verification instead.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return fmt.Errorf("bambulab: missing printer certificate")
			}
			leaf := state.PeerCertificates[0]
			intermediates := x509.NewCertPool()
			for _, cert := range state.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return err
			}
			for _, ext := range leaf.Extensions {
				if ext.Id.Equal([]int{2, 5, 29, 17}) {
					return leaf.VerifyHostname(serial)
				}
			}
			if leaf.Subject.CommonName != serial {
				return fmt.Errorf("bambulab: printer certificate identity does not match serial")
			}
			return nil
		},
	}, nil
}

// connectionContext interrupts blocked network I/O and waits for the cancellation
// hook before clearing deadlines, so cancellation cannot poison the next read.
func connectionContext(ctx context.Context, conn net.Conn) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()); close(finished) })
	return func() {
		if !stop() {
			<-finished
		}
		_ = conn.SetDeadline(time.Time{})
	}
}
