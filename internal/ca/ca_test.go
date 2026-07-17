package ca

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCA_GeneratesCert(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	certPath := filepath.Join(dir, certFile)
	keyPath := filepath.Join(dir, keyFile)

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Error("CA cert file not created")
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Error("CA key file not created")
	}

	tlsCert := ca.TLSCert()
	if len(tlsCert.Certificate) == 0 {
		t.Error("CA TLS cert has no certificate chain")
	}

	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	if !cert.IsCA {
		t.Error("Certificate is not a CA")
	}
	if cert.Subject.CommonName != "Gospy CA" {
		t.Errorf("Certificate CN = %q, want %q", cert.Subject.CommonName, "Gospy CA")
	}
}

func TestNewCA_LoadsExisting(t *testing.T) {
	dir := t.TempDir()

	ca1, err := New(dir)
	if err != nil {
		t.Fatalf("New() first call error = %v", err)
	}

	ca2, err := New(dir)
	if err != nil {
		t.Fatalf("New() second call error = %v", err)
	}

	cert1 := ca1.TLSCert().Certificate[0]
	cert2 := ca2.TLSCert().Certificate[0]

	if len(cert1) != len(cert2) {
		t.Error("CA did not reload existing certificate")
	}
	for i := range cert1 {
		if cert1[i] != cert2[i] {
			t.Error("CA certificate mismatch on reload")
			break
		}
	}
}

func TestCA_GenerateHostCert(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	hostCert, err := ca.GenerateHostCert("example.com")
	if err != nil {
		t.Fatalf("GenerateHostCert() error = %v", err)
	}

	if hostCert == nil {
		t.Fatal("GenerateHostCert() returned nil")
	}

	cert, err := x509.ParseCertificate(hostCert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	if cert.Subject.CommonName != "example.com" {
		t.Errorf("Host cert CN = %q, want %q", cert.Subject.CommonName, "example.com")
	}

	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "example.com" {
		t.Errorf("Host cert DNSNames = %v, want [example.com]", cert.DNSNames)
	}

	if cert.IsCA {
		t.Error("Host cert should not be a CA")
	}
}

func TestCA_GenerateHostCert_IP(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	hostCert, err := ca.GenerateHostCert("192.168.1.1")
	if err != nil {
		t.Fatalf("GenerateHostCert() error = %v", err)
	}

	cert, err := x509.ParseCertificate(hostCert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	if len(cert.IPAddresses) != 1 {
		t.Errorf("Host cert IPAddresses = %v, want [192.168.1.1]", cert.IPAddresses)
	}
}

func TestCA_HostCertCaching(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	cert1, err := ca.GenerateHostCert("cached.com")
	if err != nil {
		t.Fatalf("GenerateHostCert() first call error = %v", err)
	}

	cert2, err := ca.GenerateHostCert("cached.com")
	if err != nil {
		t.Fatalf("GenerateHostCert() second call error = %v", err)
	}

	if len(cert1.Certificate) != len(cert2.Certificate) {
		t.Error("Certificate caching failed: length mismatch")
	}
	for i := range cert1.Certificate {
		if !bytes.Equal(cert1.Certificate[i], cert2.Certificate[i]) {
			t.Error("Certificate caching failed: cert content mismatch")
			break
		}
	}
}

func TestCertStorage_Fetch(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	storage := NewCertStorage(ca)

	cert, err := storage.Fetch("test.example.com", func() (*tls.Certificate, error) {
		return ca.GenerateHostCert("test.example.com")
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if cert == nil {
		t.Fatal("Fetch() returned nil")
	}

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	if parsed.Subject.CommonName != "test.example.com" {
		t.Errorf("Fetched cert CN = %q, want %q", parsed.Subject.CommonName, "test.example.com")
	}
}

func TestCA_CertPath(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	expected := filepath.Join(dir, certFile)
	if ca.CertPath() != expected {
		t.Errorf("CertPath() = %q, want %q", ca.CertPath(), expected)
	}
}

func TestCA_InstallInstructions(t *testing.T) {
	dir := t.TempDir()

	ca, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	instructions := ca.InstallInstructions()
	if instructions == "" {
		t.Error("InstallInstructions() returned empty string")
	}

	if !contains(instructions, "certutil") {
		t.Error("InstallInstructions() missing Windows instructions")
	}
	if !contains(instructions, "security add-trusted-cert") {
		t.Error("InstallInstructions() missing macOS instructions")
	}
	if !contains(instructions, "update-ca-certificates") {
		t.Error("InstallInstructions() missing Linux instructions")
	}

	certPath := ca.CertPath()
	if !contains(instructions, certPath) {
		t.Errorf("InstallInstructions() missing cert path %q", certPath)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
