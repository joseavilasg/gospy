package session

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gospy/internal/ca"
	"gospy/internal/history"
)

// The replay server must ALWAYS emit responses with correct HTTP framing
// (a real Content-Length matching the body), never a close-delimited body.
//
// Regression: buildResponse left the ContentLength field at 0 while the
// recorded Content-Length header survived in the header map. goproxy's chunked
// normalization only fires when the header is absent, so http.Response.Write
// emitted a close-delimited response (no Content-Length, no Transfer-Encoding).
// Clients read the body and block on the missing EOF: gostream stalled ~2
// minutes per request and re-requested segments that had already been
// consumed, turning them into 404 "unrecoverable" failures. Fix A sets the
// field to the real body length.

// saveReplayEntry persists an entry (with optional binary body) into h.
func saveReplayEntry(t *testing.T, h *history.Store, id, method, rawURL string, status int, respHeaders map[string][]string, body []byte) {
	t.Helper()
	rec := &history.ResponseRecord{Status: status, Headers: respHeaders}
	if body != nil {
		if _, err := h.SaveBinaryBody(id, "resp", body); err != nil {
			t.Fatalf("SaveBinaryBody: %v", err)
		}
		rec.BodyFile = id + "-resp.bin"
	}
	entry := &history.Entry{
		ID:        id,
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: method, URL: rawURL, Host: rawURL},
		Response:  rec,
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// readBodyWithin reads r to EOF and fails the test if the read blocks past
// timeout. A close-delimited body never EOFs, so this is the direct detector
// of the regression.
func readBodyWithin(t *testing.T, r io.Reader, timeout time.Duration) []byte {
	t.Helper()
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read body: %v", res.err)
		}
		return res.b
	case <-time.After(timeout):
		t.Fatalf("body read blocked: response is close-delimited (no Content-Length), EOF never arrived within %v", timeout)
		return nil
	}
}

// buildEntryRequest returns a request and, for a recorded body, the expected
// wire content length.
func newBuildRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

func TestBuildResponseContentLengthMatchesBody(t *testing.T) {
	h, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	large := bytes.Repeat([]byte{0xAB}, 2*1024*1024)

	cases := []struct {
		name     string
		body     []byte
		recorded map[string][]string
	}{
		{name: "text body, conflicting stale recorded Content-Length", body: []byte("hello replay body"), recorded: map[string][]string{"Content-Length": {"999"}}},
		{name: "text body, matching recorded Content-Length", body: []byte("hello replay body"), recorded: map[string][]string{"Content-Length": {"17"}}},
		{name: "single byte", body: []byte("x"), recorded: map[string][]string{"Content-Length": {"1"}}},
		{name: "empty binary body", body: []byte{}, recorded: map[string][]string{"Content-Length": {"0"}}},
		{name: "large binary body", body: large, recorded: map[string][]string{"Content-Length": {"999"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "c_" + strings.ReplaceAll(tc.name, " ", "_")
			rec := &history.ResponseRecord{Status: 200, Headers: tc.recorded}
			if _, err := h.SaveBinaryBody(id, "resp", tc.body); err != nil {
				t.Fatalf("SaveBinaryBody: %v", err)
			}
			rec.BodyFile = id + "-resp.bin"
			entry := &history.Entry{
				ID:        id,
				Timestamp: time.Now(),
				Request:   history.RequestRecord{Method: "GET", URL: "https://example.com/x", Host: "example.com"},
				Response:  rec,
			}
			if err := h.Save(entry); err != nil {
				t.Fatalf("Save: %v", err)
			}

			resp, err := buildResponse(entry, newBuildRequest(t, "https://example.com/x"), h.Dir())
			if err != nil {
				t.Fatalf("buildResponse: %v", err)
			}
			// The ContentLength FIELD must be the actual body length, never the
			// recorded header value (which may be stale).
			if resp.ContentLength != int64(len(tc.body)) {
				t.Fatalf("ContentLength = %d, want %d (must be the real body length, not the recorded header)", resp.ContentLength, len(tc.body))
			}
		})
	}
}

func TestBuildResponseNoBodyContentLengthZero(t *testing.T) {
	entry := &history.Entry{
		ID:        "nbcl",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://example.com/nobody", Host: "example.com"},
		Response:  &history.ResponseRecord{Status: 204, Headers: map[string][]string{"Content-Length": {"0"}}},
	}
	resp, err := buildResponse(entry, newBuildRequest(t, "https://example.com/nobody"), t.TempDir())
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	if resp.ContentLength != 0 {
		t.Fatalf("ContentLength = %d, want 0 for a bodyless response", resp.ContentLength)
	}
}

// TestBuildResponseWireFraming serializes the built response exactly as
// goproxy's MITM path does (resp.Write) and parses it back with a real HTTP
// parser. This is the core regression mechanism: the wire must carry a
// Content-Length matching the body, never a close-delimited body.
func TestBuildResponseWireFraming(t *testing.T) {
	body1737 := bytes.Repeat([]byte("x"), 1737)
	large := bytes.Repeat([]byte{0x42}, 1024*1024)

	cases := []struct {
		name     string
		status   int
		body     []byte
		recorded map[string][]string
	}{
		{name: "recorded Content-Length matches body", status: 200, body: body1737, recorded: map[string][]string{"Content-Length": {"1737"}, "Connection": {"keep-alive"}}},
		{name: "recorded Content-Length stale", status: 200, body: []byte("hello"), recorded: map[string][]string{"Content-Length": {"999"}, "Connection": {"keep-alive"}}},
		{name: "no recorded Content-Length (chunked original)", status: 200, body: []byte("hello"), recorded: map[string][]string{"Content-Type": {"application/vnd.apple.mpegurl"}}},
		{name: "large binary body", status: 200, body: large, recorded: map[string][]string{"Content-Length": {"1048576"}, "Connection": {"keep-alive"}}},
		{name: "empty body", status: 204, body: []byte{}, recorded: map[string][]string{"Content-Length": {"0"}, "Connection": {"keep-alive"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := history.New(t.TempDir())
			if err != nil {
				t.Fatalf("history.New: %v", err)
			}
			id := "wf_" + strings.ReplaceAll(tc.name, " ", "_")
			saveReplayEntry(t, h, id, "GET", "https://example.com/x", tc.status, tc.recorded, tc.body)

			resp, err := buildResponse(mustGet(t, h, id), newBuildRequest(t, "https://example.com/x"), h.Dir())
			if err != nil {
				t.Fatalf("buildResponse: %v", err)
			}

			// goproxy normalizes the protocol version to HTTP/1.1 before
			// resp.Write in the MITM path.
			resp.Proto = "HTTP/1.1"
			resp.ProtoMajor = 1
			resp.ProtoMinor = 1

			var wire bytes.Buffer
			if err := resp.Write(&wire); err != nil {
				t.Fatalf("resp.Write: %v", err)
			}

			parsed, err := http.ReadResponse(bufio.NewReader(&wire), resp.Request)
			if err != nil {
				t.Fatalf("ReadResponse: %v (wire was: %q)", err, wire.String()[:min(len(wire.String()), 200)])
			}
			if parsed.ContentLength != int64(len(tc.body)) {
				t.Fatalf("wire ContentLength = %d, want %d (close-delimited responses parse as -1 and block clients)", parsed.ContentLength, len(tc.body))
			}
			if len(parsed.TransferEncoding) != 0 {
				t.Fatalf("unexpected Transfer-Encoding %v", parsed.TransferEncoding)
			}
			if parsed.Close {
				t.Fatalf("wire marks Connection: close (close-delimited framing)")
			}
			body := readBodyWithin(t, parsed.Body, 5*time.Second)
			if len(body) != len(tc.body) {
				t.Fatalf("body length %d, want %d", len(body), len(tc.body))
			}
		})
	}
}

func mustGet(t *testing.T, h *history.Store, id string) *history.Entry {
	t.Helper()
	e, err := h.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return e
}

// --- Integration: the full gostream path (CONNECT + MITM over HTTPS) ---

func newReplayServerWithCA(t *testing.T) (*ReplayServer, *history.Store, *ca.CA) {
	t.Helper()
	caCert, err := ca.New(t.TempDir())
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}
	h, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	return NewReplayServer("", caCert, NewReplayStore(h), nil), h, caCert
}

func startReplayProxy(t *testing.T, rs *ReplayServer) *url.URL {
	t.Helper()
	srv := httptest.NewServer(rs.proxy)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("proxy url: %v", err)
	}
	return u
}

// newTrustingHTTPSClient builds a client that trusts the replay CA and routes
// through the proxy, mirroring a real browser/HLS client pointed at gospy.
func newTrustingHTTPSClient(t *testing.T, caCert *ca.CA, proxyURL *url.URL) *http.Client {
	t.Helper()
	raw, err := x509.ParseCertificate(caCert.TLSCert().Certificate[0])
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(raw)
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: 10 * time.Second,
	}
}

func assertFramedReplayResponse(t *testing.T, resp *http.Response, wantStatus int, wantBody string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	if resp.ContentLength != int64(len(wantBody)) {
		t.Fatalf("client saw ContentLength = %d, want %d (close-delimited responses parse as -1 and block)", resp.ContentLength, len(wantBody))
	}
	if len(resp.TransferEncoding) != 0 {
		t.Fatalf("unexpected Transfer-Encoding %v", resp.TransferEncoding)
	}
	if resp.Close {
		t.Fatalf("client will not reuse the connection: close-delimited framing")
	}
	body := readBodyWithin(t, resp.Body, 5*time.Second)
	if string(body) != wantBody {
		t.Fatalf("body = %q, want %q", body, wantBody)
	}
}

// TestReplayServeFramedOverHTTPS drives real requests through the actual
// ReplayServer over CONNECT+MITM — the exact route gostream takes. Every
// response (hits, misses and exhausted alike) must be properly framed so the
// client never blocks on a missing EOF.
func TestReplayServeFramedOverHTTPS(t *testing.T) {
	const masterURL = "https://live.example.com/master.m3u8"
	const mediaURL = "https://cdn.example.com/publish/media_5000.m3u8"
	const segURL = "https://cdn.example.com/media_5000_20260804T063232_770521.ts?aid=1&sid=2"
	const missURL = "https://cdn.example.com/not-recorded.ts"

	segBody := bytes.Repeat([]byte{0x47}, 2*1024*1024)

	cases := []struct {
		name       string
		setup      func(t *testing.T, h *history.Store)
		reqURL     string
		wantBody   string
		wantStatus int
	}{
		{
			name: "master playlist with recorded Content-Length (the 2-minute stall case)",
			setup: func(t *testing.T, h *history.Store) {
				saveReplayEntry(t, h, "m1", "GET", masterURL, 200, map[string][]string{
					"Content-Type":   {"application/x-mpegurl; charset=utf-8"},
					"Content-Length": {"1737"},
					"Connection":     {"keep-alive"},
				}, bytes.Repeat([]byte("x"), 1737))
			},
			reqURL:   masterURL,
			wantBody: strings.Repeat("x", 1737),
		},
		{
			name: "media playlist without recorded Content-Length (chunked original)",
			setup: func(t *testing.T, h *history.Store) {
				saveReplayEntry(t, h, "p1", "GET", mediaURL, 200, map[string][]string{
					"Content-Type": {"application/vnd.apple.mpegurl"},
				}, []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:770519\n"))
			},
			reqURL:   mediaURL,
			wantBody: "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:770519\n",
		},
		{
			name: "large binary segment with recorded Content-Length (the demuxer-block case)",
			setup: func(t *testing.T, h *history.Store) {
				saveReplayEntry(t, h, "s1", "GET", segURL, 200, map[string][]string{
					"Content-Length": {"2097152"},
					"Connection":     {"keep-alive"},
				}, segBody)
			},
			reqURL:   segURL,
			wantBody: string(segBody),
		},
		{
			name: "stale recorded Content-Length header",
			setup: func(t *testing.T, h *history.Store) {
				saveReplayEntry(t, h, "s2", "GET", segURL, 200, map[string][]string{
					"Content-Length": {"999"},
					"Connection":     {"keep-alive"},
				}, []byte("ok"))
			},
			reqURL:   segURL,
			wantBody: "ok",
		},
		{
			name: "empty body response",
			setup: func(t *testing.T, h *history.Store) {
				saveReplayEntry(t, h, "e1", "GET", missURL, 204, map[string][]string{
					"Content-Length": {"0"},
				}, nil)
			},
			reqURL:     missURL,
			wantBody:   "",
			wantStatus: http.StatusNoContent,
		},
		{
			name: "synthetic miss (404) must also be framed",
			setup: func(t *testing.T, h *history.Store) {
				saveReplayEntry(t, h, "m2", "GET", masterURL, 200, nil, []byte("#EXTM3U"))
			},
			reqURL:     missURL,
			wantBody:   "no recording for GET " + missURL,
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs, h, caCert := newReplayServerWithCA(t)
			tc.setup(t, h)
			proxyURL := startReplayProxy(t, rs)
			client := newTrustingHTTPSClient(t, caCert, proxyURL)

			resp, err := client.Get(tc.reqURL)
			if err != nil {
				// Pre-fix the first request stalled until the client timeout.
				t.Fatalf("GET through replay: %v", err)
			}
			defer resp.Body.Close()
			wantStatus := tc.wantStatus
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			assertFramedReplayResponse(t, resp, wantStatus, tc.wantBody)
		})
	}
}

// TestReplaySyntheticMissAndExhaustedFramed checks that the miss and exhausted
// error responses are themselves framed (a client must be able to read the
// error body to EOF without hanging).
func TestReplaySyntheticMissAndExhaustedFramed(t *testing.T) {
	const entryURL = "https://live.example.com/a"
	const missURL = "https://live.example.com/unknown"

	rs, h, caCert := newReplayServerWithCA(t)
	saveReplayEntry(t, h, "one", "GET", entryURL, 200, map[string][]string{"Content-Type": {"text/plain"}}, []byte("body-a"))
	proxyURL := startReplayProxy(t, rs)
	client := newTrustingHTTPSClient(t, caCert, proxyURL)

	// miss (a pending entry exists, so this is a genuine 404)
	resp, err := client.Get(missURL)
	if err != nil {
		t.Fatalf("miss GET: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("miss status = %d, want 404", resp.StatusCode)
	}
	wantMiss := "no recording for GET " + missURL
	if resp.ContentLength != int64(len(wantMiss)) {
		t.Fatalf("miss ContentLength = %d, want %d (framed)", resp.ContentLength, len(wantMiss))
	}
	if b := readBodyWithin(t, resp.Body, 5*time.Second); string(b) != wantMiss {
		t.Fatalf("miss body = %q, want %q", b, wantMiss)
	}
	resp.Body.Close()

	// consume the queue, then request anything -> 410 exhausted
	if _, err := client.Get(entryURL); err != nil {
		t.Fatalf("hit GET: %v", err)
	}
	resp, err = client.Get(missURL)
	if err != nil {
		t.Fatalf("exhausted GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("exhausted status = %d, want 410", resp.StatusCode)
	}
	const wantExhausted = "replay exhausted: all recorded requests have been served"
	if resp.ContentLength != int64(len(wantExhausted)) {
		t.Fatalf("exhausted ContentLength = %d, want %d (framed)", resp.ContentLength, len(wantExhausted))
	}
	if b := readBodyWithin(t, resp.Body, 5*time.Second); string(b) != wantExhausted {
		t.Fatalf("exhausted body = %q, want %q", b, wantExhausted)
	}
}

// TestReplayMasterThenMediaKeepAlive mirrors the gostream bootstrap flow: the
// master playlist is fetched first, then the media playlist on the same client.
// Pre-fix the master stalled for minutes; post-fix both complete instantly.
func TestReplayMasterThenMediaKeepAlive(t *testing.T) {
	const masterURL = "https://live.example.com/master.m3u8"
	const mediaURL = "https://cdn.example.com/publish/media_5000.m3u8"

	rs, h, caCert := newReplayServerWithCA(t)
	saveReplayEntry(t, h, "master", "GET", masterURL, 200, map[string][]string{
		"Content-Type":   {"application/x-mpegurl; charset=utf-8"},
		"Content-Length": {"1737"},
		"Connection":     {"keep-alive"},
	}, bytes.Repeat([]byte("x"), 1737))
	saveReplayEntry(t, h, "media", "GET", mediaURL, 200, map[string][]string{
		"Content-Type": {"application/vnd.apple.mpegurl"},
	}, []byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nseg-770519.ts\n"))
	proxyURL := startReplayProxy(t, rs)
	client := newTrustingHTTPSClient(t, caCert, proxyURL)

	// The regression made the FIRST request block ~2 minutes. Enforce a tight
	// wall-clock budget so any close-delimited regression fails fast.
	deadline := time.Now().Add(8 * time.Second)

	start := time.Now()
	resp, err := client.Get(masterURL)
	if err != nil {
		t.Fatalf("master GET: %v", err)
	}
	assertFramedReplayResponse(t, resp, http.StatusOK, strings.Repeat("x", 1737))
	resp.Body.Close()
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("master request took %v, want < 2s (pre-fix it stalled ~2 minutes)", el)
	}

	start = time.Now()
	resp, err = client.Get(mediaURL)
	if err != nil {
		t.Fatalf("media GET: %v", err)
	}
	assertFramedReplayResponse(t, resp, http.StatusOK, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nseg-770519.ts\n")
	resp.Body.Close()
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("media request took %v, want < 2s", el)
	}

	if time.Now().After(deadline) {
		t.Fatalf("master+media flow exceeded the 8s budget")
	}
}
