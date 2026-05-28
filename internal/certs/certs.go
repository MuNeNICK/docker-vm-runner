package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

type Request struct {
	CertPath     string
	KeyPath      string
	CommonName   string
	Organization string
	ValidFor     time.Duration
}

func EnsureSelfSigned(req Request) error {
	if req.CertPath == "" || req.KeyPath == "" {
		return fmt.Errorf("certificate and key paths are required")
	}
	if fileExists(req.CertPath) && fileExists(req.KeyPath) {
		return nil
	}
	if req.CommonName == "" {
		req.CommonName = "docker-vm-runner"
	}
	if req.Organization == "" {
		req.Organization = "docker-vm-runner"
	}
	if req.ValidFor == 0 {
		req.ValidFor = 365 * 24 * time.Hour
	}
	if err := os.MkdirAll(filepath.Dir(req.CertPath), 0o755); err != nil {
		return fmt.Errorf("create certificate directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(req.KeyPath), 0o755); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate private key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate certificate serial: %w", err)
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   req.CommonName,
			Organization: []string{req.Organization},
		},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(req.ValidFor),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}
	if err := writePEM(req.CertPath, 0o644, "CERTIFICATE", der); err != nil {
		return err
	}
	keyDER := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := writePEM(req.KeyPath, 0o600, "RSA PRIVATE KEY", keyDER); err != nil {
		return err
	}
	return nil
}

func writePEM(path string, perm os.FileMode, typ string, bytes []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	if err := pem.Encode(file, &pem.Block{Type: typ, Bytes: bytes}); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
