package ca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	rsaBits     = 2048
	certFile    = "ca.crt"
	keyFile     = "ca.key"
	validYears  = 10
	hostCertTTL = 365 * 24 * time.Hour // 1 year for host certs
)

type CA struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	tlsCert tls.Certificate
	dir     string

	mu    sync.RWMutex
	certs map[string]*tls.Certificate
}

func New(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create ca dir: %w", err)
	}

	ca := &CA{
		dir:   dir,
		certs: make(map[string]*tls.Certificate),
	}

	if err := ca.loadOrGenerate(); err != nil {
		return nil, err
	}

	return ca, nil
}

func (ca *CA) loadOrGenerate() error {
	certPath := filepath.Join(ca.dir, certFile)
	keyPath := filepath.Join(ca.dir, keyFile)

	certPEM, err1 := os.ReadFile(certPath)
	keyPEM, err2 := os.ReadFile(keyPath)

	if err1 == nil && err2 == nil {
		return ca.loadFromPEM(certPEM, keyPEM)
	}

	return ca.generate()
}

func (ca *CA) loadFromPEM(certPEM, keyPEM []byte) error {
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("load CA key pair: %w", err)
	}

	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse CA cert: %w", err)
	}

	ca.tlsCert = tlsCert
	ca.cert = cert

	key, ok := tlsCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("CA private key is not RSA")
	}
	ca.key = key

	return nil
}

func (ca *CA) generate() error {
	key, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Gospy MITM Proxy"},
			CommonName:   "Gospy CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(validYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("parse generated cert: %w", err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}

	ca.cert = cert
	ca.key = key
	ca.tlsCert = tlsCert

	return ca.save()
}

func (ca *CA) save() error {
	certPath := filepath.Join(ca.dir, certFile)
	keyPath := filepath.Join(ca.dir, keyFile)

	certFile, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("create cert file: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	defer keyFile.Close()

	keyDER := x509.MarshalPKCS1PrivateKey(ca.key)
	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER}); err != nil {
		return fmt.Errorf("write key: %w", err)
	}

	return nil
}

func (ca *CA) CertPath() string {
	return filepath.Join(ca.dir, certFile)
}

func (ca *CA) TLSCert() tls.Certificate {
	return ca.tlsCert
}

func (ca *CA) GenerateHostCert(host string) (*tls.Certificate, error) {
	ca.mu.RLock()
	if cert, ok := ca.certs[host]; ok {
		ca.mu.RUnlock()
		return cert, nil
	}
	ca.mu.RUnlock()

	ca.mu.Lock()
	defer ca.mu.Unlock()

	// double check after acquiring write lock
	if cert, ok := ca.certs[host]; ok {
		return cert, nil
	}

	cert, err := ca.generateCertForHost(host)
	if err != nil {
		return nil, err
	}

	ca.certs[host] = cert
	return cert, nil
}

func (ca *CA) generateCertForHost(host string) (*tls.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Gospy MITM Proxy"},
			CommonName:   host,
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(hostCertTTL),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	if ip := parseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	hostKey, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &hostKey.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("create host cert: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  hostKey,
	}, nil
}

func (ca *CA) InstallInstructions() string {
	return fmt.Sprintf(`--- GOSPY CA INSTALLATION ---

To intercept HTTPS traffic, install the CA certificate:

  Windows (PowerShell as Admin):
    certutil -addstore "Root" "%s"

  macOS:
    sudo security add-trusted-cert -d -r trustRoot \
      -k /Library/Keychains/System.keychain \
      "%s"

  Linux (Debian/Ubuntu):
    sudo cp "%s" /usr/local/share/ca-certificates/gospy.crt
    sudo update-ca-certificates

  Firefox (separate trust store):
    Settings → Privacy & Security → Certificates →
    View Certificates → Authorities → Import
`, ca.CertPath(), ca.CertPath(), ca.CertPath())
}
