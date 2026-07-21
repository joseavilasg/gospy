package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"
	"testing"

	"gospy/internal/history"
	"gospy/internal/rules"

	"github.com/andybalholm/brotli"
	"github.com/elazarl/goproxy"
)

func newTestInterceptor(t *testing.T, engineRules []*rules.Rule) (*Interceptor, *history.Store) {
	t.Helper()
	store, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	ignoreStore := NewIgnoreStore(t.TempDir() + "/ignore.json")
	_ = ignoreStore.Load()
	engine := rules.NewEngine()
	engine.Load(engineRules)
	return NewInterceptor(store, ignoreStore, engine), store
}

func newRequest(method, url string) *http.Request {
	req, _ := http.NewRequest(method, url, nil)
	return req
}

func newProxyCtx(req *http.Request) *goproxy.ProxyCtx {
	return &goproxy.ProxyCtx{Req: req, UserData: nil}
}

func TestInterceptor_Passthrough_NoRule(t *testing.T) {
	ic, store := newTestInterceptor(t, nil)
	req := newRequest("GET", "http://example.com/api")
	ctx := newProxyCtx(req)

	returnedReq, resp := ic.HandleRequest(req, ctx)

	if returnedReq != req {
		t.Error("HandleRequest should return the same request")
	}
	if resp != nil {
		t.Error("HandleRequest should return nil response for passthrough")
	}

	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("List() = %d entries, want 1", len(entries))
	}
	if entries[0].AppliedAction != "" && entries[0].AppliedAction != "passthrough" {
		t.Errorf("AppliedAction = %q, want empty or passthrough", entries[0].AppliedAction)
	}
	if entries[0].RuleName != "" {
		t.Errorf("RuleName = %q, want empty", entries[0].RuleName)
	}
}

func TestInterceptor_Passthrough_ExplicitRule(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{ID: "1", Name: "pass", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionPassthrough, Enabled: true},
	})
	req := newRequest("GET", "http://example.com/api")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp != nil {
		t.Error("passthrough should return nil response")
	}
	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("List() = %d, want 1", len(entries))
	}
}

func TestInterceptor_Drop(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{ID: "1", Name: "block-telemetry", Match: rules.MatchRule{Host: "telemetry.example.com"}, Action: rules.ActionDrop, Enabled: true},
	})
	req := newRequest("GET", "http://telemetry.example.com/collect")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp == nil {
		t.Fatal("drop should return 504 response")
	}
	if resp.StatusCode != 504 {
		t.Errorf("drop StatusCode = %d, want 504", resp.StatusCode)
	}

	entries := store.ListSummary()
	if len(entries) != 1 {
		t.Fatalf("ListSummary() = %d, want 1", len(entries))
	}
	if entries[0].AppliedAction != "drop" {
		t.Errorf("AppliedAction = %q, want %q", entries[0].AppliedAction, "drop")
	}
	if entries[0].RuleName != "block-telemetry" {
		t.Errorf("RuleName = %q, want %q", entries[0].RuleName, "block-telemetry")
	}
}

func TestInterceptor_Mock(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{
			ID:      "1",
			Name:    "mock-api",
			Match:   rules.MatchRule{Method: "GET", URLPattern: ".*/api/mock$"},
			Action:  rules.ActionMock,
			Enabled: true,
			MockResp: &rules.MockResponse{
				Status:  201,
				Headers: map[string][]string{"X-Mocked": {"true"}},
				Body:    `{"mocked":true}`,
			},
		},
	})
	req := newRequest("GET", "http://example.com/api/mock")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp == nil {
		t.Fatal("mock should return a response")
	}
	if resp.StatusCode != 201 {
		t.Errorf("StatusCode = %d, want 201", resp.StatusCode)
	}
	if resp.Header.Get("X-Mocked") != "true" {
		t.Errorf("X-Mocked header = %q, want %q", resp.Header.Get("X-Mocked"), "true")
	}

	entries := store.ListSummary()
	if len(entries) != 1 {
		t.Fatalf("ListSummary() = %d, want 1", len(entries))
	}
	if entries[0].AppliedAction != "mock" {
		t.Errorf("AppliedAction = %q, want %q", entries[0].AppliedAction, "mock")
	}
	if entries[0].RuleName != "mock-api" {
		t.Errorf("RuleName = %q, want %q", entries[0].RuleName, "mock-api")
	}

	entry, _ := store.Get(entries[0].ID)
	if entry.Response == nil {
		t.Fatal("mock entry should have Response set")
	}
	if entry.Response.Status != 201 {
		t.Errorf("Response.Status = %d, want 201", entry.Response.Status)
	}
	if entry.Response.Body != `{"mocked":true}` {
		t.Errorf("Response.Body = %q, want %q", entry.Response.Body, `{"mocked":true}`)
	}
}

func TestInterceptor_Modify(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{
			ID:      "1",
			Name:    "modify-auth",
			Match:   rules.MatchRule{Host: "api.example.com"},
			Action:  rules.ActionModify,
			Enabled: true,
			ModifiedReq: &rules.ModifiedRequest{
				Headers: map[string][]string{"Authorization": {"Bearer new-token"}},
			},
		},
	})
	req := newRequest("POST", "http://api.example.com/data")
	req.Header.Set("Authorization", "Bearer old-token")
	ctx := newProxyCtx(req)

	returnedReq, resp := ic.HandleRequest(req, ctx)
	if resp != nil {
		t.Error("modify should return nil response (forward to server)")
	}
	if returnedReq.Header.Get("Authorization") != "Bearer new-token" {
		t.Errorf("Authorization = %q, want %q", returnedReq.Header.Get("Authorization"), "Bearer new-token")
	}

	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("List() = %d, want 1", len(entries))
	}
	if entries[0].AppliedAction != "modify" {
		t.Errorf("AppliedAction = %q, want %q", entries[0].AppliedAction, "modify")
	}

	entry, _ := store.Get(entries[0].ID)
	if entry.ServerRequest == nil {
		t.Fatal("modify entry should have ServerRequest set")
	}
	if entry.ServerRequest.Headers["Authorization"][0] != "Bearer new-token" {
		t.Errorf("ServerRequest.Headers[Authorization] = %q, want %q",
			entry.ServerRequest.Headers["Authorization"][0], "Bearer new-token")
	}
	if entry.Request.Headers["Authorization"][0] != "Bearer old-token" {
		t.Errorf("Original Request.Headers[Authorization] = %q, want %q",
			entry.Request.Headers["Authorization"][0], "Bearer old-token")
	}
}

func TestInterceptor_ResponseMock(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{
			ID:      "1",
			Name:    "resp-mock",
			Match:   rules.MatchRule{Method: "GET", Host: "api.example.com"},
			Action:  rules.ActionResponseMock,
			Enabled: true,
			MockResp: &rules.MockResponse{
				Status: 503,
				Body:   `{"error":"service unavailable"}`,
			},
		},
	})
	req := newRequest("GET", "http://api.example.com/status")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp != nil {
		t.Error("response_mock at request time should return nil response (request goes to server)")
	}

	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("List() = %d, want 1", len(entries))
	}
	if entries[0].AppliedAction != "response_mock" {
		t.Errorf("AppliedAction = %q, want %q", entries[0].AppliedAction, "response_mock")
	}

	if ctx.UserData == nil {
		t.Fatal("UserData should be set for response_mock")
	}
	ud, ok := ctx.UserData.(*requestUserData)
	if !ok {
		t.Fatal("UserData should be *requestUserData")
	}
	if ud.mockResponse == nil || ud.mockResponse.Status != 503 {
		t.Errorf("mockResponse.Status = %v, want 503", ud.mockResponse)
	}
	if ud.entryID != entries[0].ID {
		t.Errorf("entryID = %q, want %q", ud.entryID, entries[0].ID)
	}
}

func TestInterceptor_IgnoredHost(t *testing.T) {
	dir := t.TempDir()
	store, err := history.New(dir + "/history")
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	ignoreStore := NewIgnoreStore(dir + "/ignore.json")
	_ = ignoreStore.Load()
	_ = ignoreStore.Add("telemetry.googleapis.com")

	engine := rules.NewEngine()
	ic := NewInterceptor(store, ignoreStore, engine)

	req := newRequest("POST", "http://telemetry.googleapis.com/collect")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp != nil {
		t.Error("ignored host should return nil response")
	}

	entries := store.List()
	if len(entries) != 0 {
		t.Errorf("List() = %d, want 0 (ignored entries not saved)", len(entries))
	}
}

func TestInterceptor_DisabledRule(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{ID: "1", Name: "disabled", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionDrop, Enabled: false},
	})
	req := newRequest("GET", "http://example.com/api")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp != nil {
		t.Error("disabled rule should passthrough")
	}

	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("List() = %d, want 1", len(entries))
	}
	if entries[0].AppliedAction != "" {
		t.Errorf("AppliedAction = %q, want empty (passthrough)", entries[0].AppliedAction)
	}
}

func TestInterceptor_FirstMatchWins(t *testing.T) {
	ic, store := newTestInterceptor(t, []*rules.Rule{
		{ID: "1", Name: "first", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionDrop, Enabled: true},
		{ID: "2", Name: "second", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionMock, Enabled: true, MockResp: &rules.MockResponse{Status: 200}},
	})
	req := newRequest("GET", "http://example.com/api")
	ctx := newProxyCtx(req)

	_, resp := ic.HandleRequest(req, ctx)
	if resp == nil {
		t.Fatal("first match (drop) should return 504 response")
	}
	if resp.StatusCode != 504 {
		t.Errorf("first match (drop) StatusCode = %d, want 504", resp.StatusCode)
	}

	entries := store.ListSummary()
	if entries[0].RuleName != "first" {
		t.Errorf("RuleName = %q, want %q (first match wins)", entries[0].RuleName, "first")
	}
}

func TestInterceptor_ModifyWithBody(t *testing.T) {
	ic, _ := newTestInterceptor(t, []*rules.Rule{
		{
			ID:     "1",
			Name:   "inject-body",
			Match:  rules.MatchRule{Host: "api.example.com"},
			Action: rules.ActionModify,
			ModifiedReq: &rules.ModifiedRequest{
				Body: `{"injected":true}`,
			},
			Enabled: true,
		},
	})
	req := newRequest("POST", "http://api.example.com/data")
	req.Body = io.NopCloser(strings.NewReader(`{"original":true}`))
	ctx := newProxyCtx(req)

	returnedReq, _ := ic.HandleRequest(req, ctx)
	if returnedReq == nil {
		t.Fatal("returned request should not be nil")
	}
}

func TestDecompressBody_Gzip(t *testing.T) {
	original := `{"key":"value","method":"POST"}`
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte(original))
	w.Close()

	result := decompressBody(buf.Bytes(), "gzip")
	if result.Decoded != original {
		t.Errorf("gzip decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "gzip" {
		t.Errorf("gzip compression: got %q, want %q", result.Compression, "gzip")
	}
	if result.Raw == "" {
		t.Error("gzip: raw should not be empty")
	}
}

func TestDecompressBody_Zlib(t *testing.T) {
	original := `{"status":200,"body":"hello world"}`
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
	w.Write([]byte(original))
	w.Close()

	result := decompressBody(buf.Bytes(), "deflate")
	if result.Decoded != original {
		t.Errorf("zlib decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "zlib" {
		t.Errorf("zlib compression: got %q, want %q", result.Compression, "zlib")
	}
}

func TestDecompressBody_Deflate(t *testing.T) {
	original := `{"host":"example.com","path":"/api"}`

	flatBuf, err := flatten([]byte(original))
	if err != nil {
		t.Fatalf("flate compress: %v", err)
	}

	result := decompressBody(flatBuf, "deflate")
	if result.Decoded != original {
		t.Errorf("deflate decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "deflate" {
		t.Errorf("deflate compression: got %q, want %q", result.Compression, "deflate")
	}
}

func TestDecompressBody_Brotli(t *testing.T) {
	original := `{"content":"brotli compressed data"}`
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	w.Write([]byte(original))
	w.Close()

	result := decompressBody(buf.Bytes(), "br")
	if result.Decoded != original {
		t.Errorf("brotli decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "brotli" {
		t.Errorf("brotli compression: got %q, want %q", result.Compression, "brotli")
	}
}

func TestDecompressBody_PlainText(t *testing.T) {
	original := `{"plain":"no compression"}`
	result := decompressBody([]byte(original), "")
	if result.Decoded != original {
		t.Errorf("plain decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "" {
		t.Errorf("plain compression: got %q, want empty", result.Compression)
	}
}

func TestDecompressBody_Empty(t *testing.T) {
	result := decompressBody([]byte{}, "")
	if result.Decoded != "" {
		t.Errorf("empty decoded: got %q, want empty string", result.Decoded)
	}
}

func TestDecompressBody_DeflateWithoutHeader(t *testing.T) {
	original := `{"host":"example.com","path":"/api"}`

	flatBuf, err := flatten([]byte(original))
	if err != nil {
		t.Fatalf("flate compress: %v", err)
	}

	result := decompressBody(flatBuf, "")
	if result.Decoded == original {
		t.Error("deflate without header should not decompress")
	}
	if result.Compression != "" {
		t.Errorf("deflate without header: compression should be empty, got %q", result.Compression)
	}
}

func flatten(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
