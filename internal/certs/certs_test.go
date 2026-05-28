package certs

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSignedCreatesCertificateAndKey(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	if err := EnsureSelfSigned(Request{CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("EnsureSelfSigned returned error: %v", err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if cert.Subject.CommonName != "docker-vm-runner" {
		t.Fatalf("CommonName = %q", cert.Subject.CommonName)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions = %v", keyInfo.Mode().Perm())
	}
}

func TestEnsureSelfSignedReusesExistingPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, []byte("existing cert"), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("existing key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	if err := EnsureSelfSigned(Request{CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("EnsureSelfSigned returned error: %v", err)
	}
	certBytes, _ := os.ReadFile(certPath)
	keyBytes, _ := os.ReadFile(keyPath)
	if string(certBytes) != "existing cert" || string(keyBytes) != "existing key" {
		t.Fatalf("existing certificate pair was overwritten")
	}
}
