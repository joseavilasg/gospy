package ca

import "crypto/tls"

type CertStorage struct {
	ca *CA
}

func NewCertStorage(ca *CA) *CertStorage {
	return &CertStorage{ca: ca}
}

func (cs *CertStorage) Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	return cs.ca.GenerateHostCert(hostname)
}
