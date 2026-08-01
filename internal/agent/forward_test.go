package agent

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestCA generates a CA and a leaf certificate valid for 127.0.0.1, signed
// by the CA. The leaf is what a TLS target presents; the CA seeds the client's
// RootCAs (mirroring gospy's own MITM CA).
func newTestCA(t *testing.T) (caCert, leafCert tls.Certificate) {
	t.Helper()
	now := time.Now()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gospy test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCert = tls.Certificate{Certificate: [][]byte{caDER}, PrivateKey: caKey}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	leafCert = tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}
	return caCert, leafCert
}

func TestForwarder_InjectsAgentHeaderAndSanitizes(t *testing.T) {
	var gotAgentHeader string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAgentHeader = r.Header.Get(agentHeader)
		w.Header().Set("Set-Cookie", "session=secret")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer origin.Close()

	fwd, err := NewForwarder(origin.URL, tls.Certificate{})
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	resp, callID, err := fwd.Do(context.Background(), "GET", origin.URL, map[string][]string{}, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAgentHeader == "" {
		t.Error("X-Gospy-Agent header was not injected")
	}
	if callID == "" || callID != gotAgentHeader {
		t.Errorf("callID = %q, header value = %q; the correlation id must travel in the header", callID, gotAgentHeader)
	}
	if resp.Status != 200 || resp.Body != `{"ok":true}` {
		t.Errorf("response = status %d body %q", resp.Status, resp.Body)
	}
	if resp.Headers["Set-Cookie"][0] != "<redacted len=14>" {
		t.Errorf("Set-Cookie not redacted: %v", resp.Headers["Set-Cookie"])
	}
	if resp.Headers["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type must survive: %v", resp.Headers["Content-Type"])
	}
}

func TestForwarder_TrustsLocalCA(t *testing.T) {
	caCert, leafCert := newTestCA(t)

	var sawAgentHeader string
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAgentHeader = r.Header.Get(agentHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{leafCert}}
	target.StartTLS()
	defer target.Close()

	// The httptest proxy answers CONNECT by tunneling to the TLS target, so the
	// TLS handshake happens end-to-end against the CA-signed leaf.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upstream, err := net.Dial("tcp", target.Listener.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijacker", http.StatusInternalServerError)
			return
		}
		client, _, err := hj.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		fmt.Fprint(client, "HTTP/1.1 200 Connection established\r\n\r\n")
		go io.Copy(client, upstream)
		io.Copy(upstream, client)
	}))
	defer proxy.Close()

	fwd, err := NewForwarder(proxy.URL, caCert)
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	resp, _, err := fwd.Do(context.Background(), "GET", target.URL, map[string][]string{}, nil)
	if err != nil {
		t.Fatalf("Do through CONNECT tunnel: %v", err)
	}
	if resp.Status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.Status)
	}
	if sawAgentHeader == "" {
		t.Error("X-Gospy-Agent header did not reach the target through the tunnel")
	}
}
