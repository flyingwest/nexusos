package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureDevTLS writes a self-signed cert/key under dataDir if paths are empty.
// Returns the cert and key file paths to use. Only call when Dev is set.
func EnsureDevTLS(dataDir, certFile, keyFile string) (cert, key string, err error) {
	if certFile != "" && keyFile != "" {
		return certFile, keyFile, nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", "", err
	}
	cert = filepath.Join(dataDir, "dev-tls.crt")
	key = filepath.Join(dataDir, "dev-tls.key")
	if certFile != "" {
		cert = certFile
	}
	if keyFile != "" {
		key = keyFile
	}
	// Reuse existing pair if both present.
	if _, e1 := os.Stat(cert); e1 == nil {
		if _, e2 := os.Stat(key); e2 == nil {
			return cert, key, nil
		}
	}
	if err := generateSelfSigned(cert, key); err != nil {
		return "", "", err
	}
	return cert, key, nil
}

func generateSelfSigned(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"NexusOS Dev"}, CommonName: "nexusos-dev"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}
	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		certOut.Close()
		return err
	}
	certOut.Close()

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}
