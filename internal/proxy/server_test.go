package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
)

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
