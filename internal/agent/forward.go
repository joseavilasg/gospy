package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// agentHeader marks a request as originating from the agent; the interceptor
// strips it and records the entry with Origin=agent.
const agentHeader = "X-Gospy-Agent"

// Forwarder issues requests through the gospy proxy itself so they are captured
// as agent-origin entries and the rule engine applies exactly as for the browser.
type Forwarder struct {
	client *http.Client
}

// NewForwarder builds the forwarding client: the local proxy at proxyURL
// (e.g. "http://127.0.0.1:8080") as the HTTP proxy, and RootCAs seeded from
// the local CA so the MITM certificates presented by the proxy are trusted.
func NewForwarder(proxyURL string, caCert tls.Certificate) (*Forwarder, error) {
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy address: %w", err)
	}
	pool := x509.NewCertPool()
	for _, der := range caCert.Certificate {
		if cert, err := x509.ParseCertificate(der); err == nil {
			pool.AddCert(cert)
		}
	}
	tr := &http.Transport{
		Proxy:           http.ProxyURL(pu),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	return &Forwarder{client: &http.Client{Transport: tr, Timeout: 60 * time.Second}}, nil
}

// Do forwards a request through the proxy and returns the sanitized response
// plus the correlation ID the proxy stored on the captured entry (AgentCallID),
// which the caller uses to resolve the new entry ID via the history store.
func (f *Forwarder) Do(ctx context.Context, method, rawURL string, headers map[string][]string, body []byte) (*ForwardResponse, string, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	callID := uuid.New().String()
	req.Header.Set(agentHeader, callID)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("forward request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}
	return &ForwardResponse{
		Status:  resp.StatusCode,
		Headers: SanitizeHeaders(resp.Header),
		Body:    string(data),
	}, callID, nil
}
