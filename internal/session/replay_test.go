package session

import (
	"io"
	"net/http"
	"strings"
	"sync"
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

func newLoggingReplayServer(t *testing.T) (*ReplayServer, *history.Store, string) {
	t.Helper()
	logRoot := t.TempDir()
	rs, h := newReplayServer(t)
	rs.SetReplayLogRoot(logRoot)
	t.Cleanup(func() {
		if err := rs.Close(); err != nil {
			t.Errorf("ReplayServer.Close: %v", err)
		}
	})
	return rs, h, logRoot
}

func TestReplayWritesRunLog(t *testing.T) {
	rs, h, logRoot := newLoggingReplayServer(t)
	base := time.Now()
	entries := []*history.Entry{
		{
			ID:        "h1",
			Timestamp: base,
			Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
			Response:  &history.ResponseRecord{Status: 200},
		},
		{
			ID:        "s1",
			Timestamp: base.Add(time.Second),
			Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/seg-1.ts", Host: "live.example.com"},
			Response:  &history.ResponseRecord{Status: 200},
		},
	}
	for _, e := range entries {
		if err := h.Save(e); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	if resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8"); resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	callHandleRequest(t, rs, "GET", "https://example.com/not-recorded")
	callHandleRequest(t, rs, "GET", "https://live.example.com/seg-1.ts")
	callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")

	summaries, err := ListReplayRuns(logRoot)
	if err != nil {
		t.Fatalf("ListReplayRuns: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 run, got %d", len(summaries))
	}
	s := summaries[0]
	if s.Hits != 2 || s.Misses != 1 || s.Exhausted != 1 {
		t.Fatalf("unexpected summary %+v", s)
	}

	dir, err := ReplayRunDir(logRoot, s.RunID)
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	events, err := LoadReplayRun(dir)
	if err != nil {
		t.Fatalf("LoadReplayRun: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	hit := events[0]
	if hit.Result != "hit" || hit.EntryID != "h1" || hit.Status != 200 {
		t.Fatalf("unexpected hit event %+v", hit)
	}
	if hit.Request.Method != "GET" || hit.Request.URL != "https://live.example.com/master.m3u8" {
		t.Fatalf("unexpected captured request %+v", hit.Request)
	}
	if hit.Consumed != 1 || hit.Total != 2 || hit.Exhausted {
		t.Fatalf("unexpected progress on hit %+v", hit)
	}
	miss := events[1]
	if miss.Result != "miss" || miss.Status != 404 {
		t.Fatalf("unexpected miss event %+v", miss)
	}
	if miss.TotalPending != 1 || len(miss.Unconsumed) != 1 {
		t.Fatalf("expected 1 pending entry, got total=%d list=%d", miss.TotalPending, len(miss.Unconsumed))
	}
	if miss.Unconsumed[0].URL != "https://live.example.com/seg-1.ts" {
		t.Fatalf("unexpected unconsumed %+v", miss.Unconsumed[0])
	}
	exhausted := events[3]
	if exhausted.Result != "exhausted" || exhausted.Status != 410 {
		t.Fatalf("unexpected exhausted event %+v", exhausted)
	}
	if !exhausted.Exhausted || exhausted.Consumed != 2 || exhausted.Total != 2 {
		t.Fatalf("unexpected progress on exhausted %+v", exhausted)
	}
}

func TestReplayCapturesRequestBody(t *testing.T) {
	rs, h, logRoot := newLoggingReplayServer(t)
	entry := &history.Entry{
		ID:        "p1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "POST", URL: "https://live.example.com/api", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req, err := http.NewRequest("POST", "https://live.example.com/api", strings.NewReader(`{"report":true}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	_, resp := rs.handleRequest(req, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	summaries, _ := ListReplayRuns(logRoot)
	dir, err := ReplayRunDir(logRoot, summaries[0].RunID)
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	events, err := LoadReplayRun(dir)
	if err != nil {
		t.Fatalf("LoadReplayRun: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	rec := events[0].Request
	if rec.Body != `{"report":true}` {
		t.Fatalf("expected decoded body, got %q", rec.Body)
	}
	if rec.IsBinaryBody {
		t.Fatal("json body should not be binary")
	}
	if rec.BodyFile == "" || rec.BodySize == 0 {
		t.Fatalf("expected raw body stored, got file=%q size=%d", rec.BodyFile, rec.BodySize)
	}
	raw, err := ReadReplayBody(dir, rec.BodyFile)
	if err != nil {
		t.Fatalf("ReadReplayBody: %v", err)
	}
	if string(raw) != `{"report":true}` {
		t.Fatalf("unexpected raw body %q", raw)
	}
}

func TestReplayNotifier(t *testing.T) {
	rs, h := newReplayServer(t)
	base := time.Now()
	for _, e := range []*history.Entry{
		{
			ID:        "n1",
			Timestamp: base,
			Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/a", Host: "live.example.com"},
			Response:  &history.ResponseRecord{Status: 200},
		},
		{
			ID:        "n2",
			Timestamp: base.Add(time.Second),
			Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/b", Host: "live.example.com"},
			Response:  &history.ResponseRecord{Status: 200},
		},
	} {
		if err := h.Save(e); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	var mu sync.Mutex
	var got []ReplayEvent
	notify := func(ev ReplayEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	rs.SetReplayNotifier(notify)

	callHandleRequest(t, rs, "GET", "https://live.example.com/a")
	callHandleRequest(t, rs, "GET", "https://example.com/unknown")

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(got))
	}
	if got[0].Result != "hit" || got[0].EntryID != "n1" {
		t.Fatalf("unexpected event[0] %+v", got[0])
	}
	if got[1].Result != "miss" || got[1].Status != 404 {
		t.Fatalf("unexpected event[1] %+v", got[1])
	}
	if got[1].TotalPending != 1 {
		t.Fatalf("expected 1 pending, got %d", got[1].TotalPending)
	}
}

func TestReplayNotifierWithLog(t *testing.T) {
	rs, h, logRoot := newLoggingReplayServer(t)
	entry := &history.Entry{
		ID:        "r1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/a", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var got ReplayEvent
	rs.SetReplayNotifier(func(ev ReplayEvent) { got = ev })
	callHandleRequest(t, rs, "GET", "https://live.example.com/a")

	if got.RunID == "" {
		t.Fatal("expected RunID set on notified event")
	}
	if got.Seq != 1 {
		t.Fatalf("expected seq 1, got %d", got.Seq)
	}

	summaries, err := ListReplayRuns(logRoot)
	if err != nil {
		t.Fatalf("ListReplayRuns: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 run in log, got %d", len(summaries))
	}
}
