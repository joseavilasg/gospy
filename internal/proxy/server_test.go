package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/elazarl/goproxy"

	"gospy/internal/ca"
	"gospy/internal/history"
	"gospy/internal/rules"
)

// TestNewServer_InvalidBindIface verifies that a bad --bind-iface fails startup
// with a clear error instead of silently falling back to unbound dials.
func TestNewServer_InvalidBindIface(t *testing.T) {
	dir := t.TempDir()
	caCert, err := ca.New(filepath.Join(dir, "ca"))
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}
	hist, err := history.New(filepath.Join(dir, "history"))
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}

	_, err = NewServer(":0", ":0", caCert, hist, rules.NewEngine(), NewIgnoreStore(filepath.Join(dir, "ignore.json")), dir, "gospy-no-such-interface", "")
	if err == nil {
		t.Fatal("NewServer with invalid bind iface = nil error, want error")
	}
}

// TestNewServer_CustomDNS verifies the flags reach the outbound transport:
// a custom DNS server alone (no iface) must succeed on any platform.
func TestNewServer_CustomDNS(t *testing.T) {
	dir := t.TempDir()
	caCert, err := ca.New(filepath.Join(dir, "ca"))
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}
	hist, err := history.New(filepath.Join(dir, "history"))
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}

	srv, err := NewServer(":0", ":0", caCert, hist, rules.NewEngine(), NewIgnoreStore(filepath.Join(dir, "ignore.json")), dir, "", "8.8.8.8")
	if err != nil {
		t.Fatalf("NewServer with custom DNS error: %v", err)
	}
	if srv == nil {
		t.Fatal("NewServer with custom DNS = nil server")
	}
}

// TestStreamingHeadersFlushedImmediately reproduces the SSE failure seen with
// the MCP Inspector: an upstream stream that sends headers but holds the body
// open without emitting any data. The client must still receive the response
// headers promptly; without the WriteHeader flush they stay in the proxy's
// connection buffer and the client hangs.
func TestStreamingHeadersFlushedImmediately(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr = &http.Transport{}
	psrv := httptest.NewServer(flushStreamingHeaders(proxy))
	defer psrv.Close()

	pu, err := url.Parse("http://" + psrv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("proxy url: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}

	done := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/events")
		if err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()

	select {
	case resp := <-done:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	case err := <-errCh:
		t.Fatalf("request failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("response headers were not flushed: client blocked while the SSE stream holds no body data")
	}
}
