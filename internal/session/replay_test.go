package session

import (
	"io"
	"net/http"
	"testing"
	"time"

	"gospy/internal/ca"
	"gospy/internal/history"
)

func TestBuildResponseBody(t *testing.T) {
	h, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}

	id := "resp1"
	if _, err := h.SaveBinaryBody(id, "resp", []byte("hello replay body")); err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}
	entry := &history.Entry{
		ID:        id,
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://example.com/body", Host: "example.com"},
		Response: &history.ResponseRecord{
			Status:   200,
			Headers:  map[string][]string{"Content-Type": {"text/plain"}},
			BodyFile: id + "-resp.bin",
		},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req, err := http.NewRequest("GET", "https://example.com/body", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := buildResponse(entry, req, h.Dir())
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "hello replay body" {
		t.Fatalf("expected 'hello replay body', got %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("expected Content-Type text/plain, got %q", ct)
	}
}

func TestBuildResponseNoBody(t *testing.T) {
	entry := &history.Entry{
		ID:        "nb1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://example.com/nobody", Host: "example.com"},
		Response:  &history.ResponseRecord{Status: 204},
	}
	req, err := http.NewRequest("GET", "https://example.com/nobody", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := buildResponse(entry, req, t.TempDir())
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("expected status 204, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty body, got %q", body)
	}
}

func newReplayServer(t *testing.T) (*ReplayServer, *history.Store) {
	t.Helper()
	caCert, err := ca.New(t.TempDir())
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}
	h, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	return NewReplayServer("", caCert, NewReplayStore(h), nil), h
}

func callHandleRequest(t *testing.T, rs *ReplayServer, method, rawURL string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, resp := rs.handleRequest(req, nil)
	if resp == nil {
		t.Fatalf("handleRequest returned nil response for %s %s", method, rawURL)
	}
	return resp
}

func TestReplayHandleHit(t *testing.T) {
	rs, h := newReplayServer(t)
	if _, err := h.SaveBinaryBody("h1", "resp", []byte("manifest body")); err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}
	entry := &history.Entry{
		ID:        "h1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response: &history.ResponseRecord{
			Status:   200,
			Headers:  map[string][]string{"Content-Type": {"application/vnd.apple.mpegurl"}},
			BodyFile: "h1-resp.bin",
		},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Gospy-Replay"); got != "hit" {
		t.Fatalf("expected X-Gospy-Replay: hit, got %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "manifest body" {
		t.Fatalf("expected 'manifest body', got %q", body)
	}
}

func TestReplayHandleMiss(t *testing.T) {
	rs, h := newReplayServer(t)
	entry := &history.Entry{
		ID:        "m1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	resp := callHandleRequest(t, rs, "GET", "https://example.com/not-recorded")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Gospy-Replay"); got != "miss" {
		t.Fatalf("expected X-Gospy-Replay: miss, got %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := string(body); got != "no recording for GET https://example.com/not-recorded" {
		t.Fatalf("unexpected miss body %q", got)
	}
}

func TestReplayHandleExhausted(t *testing.T) {
	rs, h := newReplayServer(t)
	entry := &history.Entry{
		ID:        "x1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8"); resp.StatusCode != 200 {
		t.Fatalf("expected 200 on first request, got %d", resp.StatusCode)
	}

	const exhaustedBody = "replay exhausted: all recorded requests have been served"

	for _, url := range []string{
		"https://live.example.com/master.m3u8",
		"https://example.com/anything-else",
	} {
		resp := callHandleRequest(t, rs, "GET", url)
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("expected 410 for %s, got %d", url, resp.StatusCode)
		}
		if got := resp.Header.Get("X-Gospy-Replay"); got != "exhausted" {
			t.Fatalf("expected X-Gospy-Replay: exhausted, got %q", got)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := string(body); got != exhaustedBody {
			t.Fatalf("exhausted body must be constant, got %q", got)
		}
	}
}
