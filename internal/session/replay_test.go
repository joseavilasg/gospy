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
	"gospy/internal/rules"
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
	if miss.Unconsumed[0].ID != "s1" {
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

func replayWithRule(t *testing.T, rule *rules.Rule) (*ReplayServer, *history.Store) {
	t.Helper()
	rs, h := newReplayServer(t)
	engine := rules.NewEngine()
	engine.AddRule(rule)
	rs.SetRulesEngine(engine)
	return rs, h
}

func captureReplayEvents(t *testing.T, rs *ReplayServer) func() []ReplayEvent {
	t.Helper()
	var mu sync.Mutex
	var events []ReplayEvent
	rs.SetReplayNotifier(func(ev ReplayEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	return func() []ReplayEvent {
		mu.Lock()
		defer mu.Unlock()
		return events
	}
}

func assertServedResponse(t *testing.T, ev *ReplayEvent, wantStatus int, wantHeaders map[string]string, wantBody string) {
	t.Helper()
	if ev.ServedResponse == nil {
		t.Fatal("expected the rule-served response to be captured, got nil")
	}
	srv := ev.ServedResponse
	if srv.Status != wantStatus {
		t.Fatalf("servedResponse status: expected %d, got %d", wantStatus, srv.Status)
	}
	h := http.Header(srv.Headers)
	for k, v := range wantHeaders {
		if got := h.Get(k); got != v {
			t.Fatalf("servedResponse header %s: expected %q, got %q", k, v, got)
		}
	}
	if srv.Body != wantBody {
		t.Fatalf("servedResponse body: expected %q, got %q", wantBody, srv.Body)
	}
}

func TestReplayRulesMockOnHit(t *testing.T) {
	rs, h := replayWithRule(t, &rules.Rule{
		ID: "m1", Name: "fake manifest", Enabled: true,
		Match:    rules.MatchRule{Method: "GET", Host: "live.example.com", URLPattern: "/master.m3u8"},
		Action:   rules.ActionMock,
		MockResp: &rules.MockResponse{Status: 418, Headers: map[string][]string{"X-Test": {"yes"}}, Body: "fake body"},
	})
	if _, err := h.SaveBinaryBody("r1", "resp", []byte("recorded body")); err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}
	if err := h.Save(&history.Entry{
		ID:        "r1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200, Headers: map[string][]string{"Content-Type": {"application/vnd.apple.mpegurl"}}, BodyFile: "r1-resp.bin"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	events := captureReplayEvents(t, rs)

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp.StatusCode != 418 {
		t.Fatalf("expected mock status 418, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Test"); got != "yes" {
		t.Fatalf("expected X-Test: yes, got %q", got)
	}
	if resp.ContentLength != int64(len("fake body")) {
		t.Fatalf("expected ContentLength %d, got %d (#737)", len("fake body"), resp.ContentLength)
	}
	if body, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	} else if string(body) != "fake body" {
		t.Fatalf("expected 'fake body', got %q", body)
	}
	if got := resp.Header.Get("X-Gospy-Replay"); got != "hit" {
		t.Fatalf("expected X-Gospy-Replay: hit, got %q", got)
	}

	ev := events()
	if len(ev) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ev))
	}
	if ev[0].Result != "hit" || ev[0].EntryID != "r1" {
		t.Fatalf("unexpected match result: %+v", ev[0])
	}
	if ev[0].AppliedAction != string(rules.ActionMock) || ev[0].RuleName != "fake manifest" || ev[0].Status != 418 {
		t.Fatalf("unexpected rule metadata: %+v", ev[0])
	}
	assertServedResponse(t, &ev[0], 418, map[string]string{"X-Test": "yes", "X-Gospy-Replay": "hit"}, "fake body")
}

func TestReplayRulesDropOnHit(t *testing.T) {
	rs, h := replayWithRule(t, &rules.Rule{
		ID: "d1", Name: "block manifest", Enabled: true,
		Match:  rules.MatchRule{Method: "GET", Host: "live.example.com", URLPattern: "/master.m3u8"},
		Action: rules.ActionDrop,
	})
	if err := h.Save(&history.Entry{
		ID:        "d1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	events := captureReplayEvents(t, rs)

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected drop 504, got %d", resp.StatusCode)
	}
	if resp.ContentLength != 0 {
		t.Fatalf("expected empty body ContentLength 0, got %d", resp.ContentLength)
	}
	if body, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	} else if len(body) != 0 {
		t.Fatalf("expected empty drop body, got %q", body)
	}

	ev := events()
	if len(ev) != 1 || ev[0].Result != "hit" || ev[0].EntryID != "d1" || ev[0].AppliedAction != string(rules.ActionDrop) {
		t.Fatalf("unexpected events: %+v", ev)
	}
	assertServedResponse(t, &ev[0], http.StatusGatewayTimeout, map[string]string{"X-Gospy-Replay": "hit"}, "")

	// the hit was consumed: the queue is now exhausted, the rule still overrides
	resp2 := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp2.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected drop 504 on exhausted, got %d", resp2.StatusCode)
	}
	ev2 := events()
	if len(ev2) != 2 || ev2[1].Result != "exhausted" {
		t.Fatalf("expected exhausted event, got %+v", ev2)
	}
}

func TestReplayRulesMockOnMiss(t *testing.T) {
	rs, h := replayWithRule(t, &rules.Rule{
		ID: "m2", Name: "fake missing", Enabled: true,
		Match:    rules.MatchRule{Method: "GET", Host: "example.com", URLPattern: "/not-in-queue"},
		Action:   rules.ActionMock,
		MockResp: &rules.MockResponse{Status: 202, Body: "mocked miss"},
	})
	if err := h.Save(&history.Entry{
		ID:        "keep",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	events := captureReplayEvents(t, rs)

	resp := callHandleRequest(t, rs, "GET", "https://example.com/not-in-queue")
	if resp.StatusCode != 202 {
		t.Fatalf("expected mock status 202, got %d", resp.StatusCode)
	}
	if body, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	} else if string(body) != "mocked miss" {
		t.Fatalf("expected 'mocked miss', got %q", body)
	}

	ev := events()
	if len(ev) != 1 || ev[0].Result != "miss" || ev[0].AppliedAction != string(rules.ActionMock) || ev[0].Status != 202 {
		t.Fatalf("unexpected events: %+v", ev)
	}
	assertServedResponse(t, &ev[0], 202, map[string]string{"X-Gospy-Replay": "miss"}, "mocked miss")
}

func TestReplayRulesMockOnExhausted(t *testing.T) {
	rs, h := replayWithRule(t, &rules.Rule{
		ID: "m3", Name: "fake exhausted", Enabled: true,
		Match:    rules.MatchRule{Method: "GET", Host: "live.example.com", URLPattern: "/master.m3u8"},
		Action:   rules.ActionMock,
		MockResp: &rules.MockResponse{Status: 203, Body: "mocked exhausted"},
	})
	if err := h.Save(&history.Entry{
		ID:        "x2",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	events := captureReplayEvents(t, rs)

	callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp.StatusCode != 203 {
		t.Fatalf("expected mock status 203 on exhausted, got %d", resp.StatusCode)
	}
	if body, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	} else if string(body) != "mocked exhausted" {
		t.Fatalf("expected 'mocked exhausted', got %q", body)
	}

	ev := events()
	if len(ev) != 2 || ev[1].Result != "exhausted" || ev[1].AppliedAction != string(rules.ActionMock) || ev[1].Status != 203 {
		t.Fatalf("unexpected events: %+v", ev)
	}
	assertServedResponse(t, &ev[1], 203, map[string]string{"X-Gospy-Replay": "exhausted"}, "mocked exhausted")
}

func TestReplayRulesResponseMockCollapsesToMock(t *testing.T) {
	rs, h := replayWithRule(t, &rules.Rule{
		ID: "rm1", Name: "fake response", Enabled: true,
		Match:    rules.MatchRule{Method: "GET", Host: "live.example.com", URLPattern: "/master.m3u8"},
		Action:   rules.ActionResponseMock,
		MockResp: &rules.MockResponse{Status: 204, Body: "collapsed"},
	})
	if err := h.Save(&history.Entry{
		ID:        "r2",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	events := captureReplayEvents(t, rs)

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if body, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	} else if string(body) != "collapsed" {
		t.Fatalf("expected 'collapsed', got %q", body)
	}

	ev := events()
	if len(ev) != 1 || ev[0].AppliedAction != string(rules.ActionResponseMock) {
		t.Fatalf("unexpected events: %+v", ev)
	}
	assertServedResponse(t, &ev[0], 204, map[string]string{"X-Gospy-Replay": "hit"}, "collapsed")
}

func TestReplayRulesPassthroughNoOverride(t *testing.T) {
	rs, h := replayWithRule(t, &rules.Rule{
		ID: "p1", Name: "pass through", Enabled: true,
		Match:  rules.MatchRule{Method: "GET", Host: "live.example.com", URLPattern: "/master.m3u8"},
		Action: rules.ActionPassthrough,
	})
	if _, err := h.SaveBinaryBody("p1", "resp", []byte("recorded body")); err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}
	if err := h.Save(&history.Entry{
		ID:        "p1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200, BodyFile: "p1-resp.bin"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	events := captureReplayEvents(t, rs)

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/master.m3u8")
	if resp.StatusCode != 200 {
		t.Fatalf("expected recorded 200, got %d", resp.StatusCode)
	}
	if body, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	} else if string(body) != "recorded body" {
		t.Fatalf("expected 'recorded body', got %q", body)
	}

	ev := events()
	if len(ev) != 1 || ev[0].AppliedAction != "" || ev[0].RuleName != "" {
		t.Fatalf("passthrough must not mark the event: %+v", ev)
	}
	if ev[0].ServedResponse != nil {
		t.Fatal("passthrough must not capture a served response: the recorded response is served as-is")
	}
}

func TestReplayRepeatOnMiss(t *testing.T) {
	rs, h, logRoot := newLoggingReplayServer(t)

	// One entry recorded without query params (normalized form).
	entry := &history.Entry{
		ID:        "vtype1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://pcw-api.iq.com/api/vtype", Host: "pcw-api.iq.com"},
		Response:  &history.ResponseRecord{Status: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, BodyFile: "vtype1-resp.bin"},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := h.SaveBinaryBody("vtype1", "resp", []byte(`{"tm":"t","vf":"v","authKey":"k"}`)); err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}

	// Config: repeat_on_miss + ignore_query_params.
	cfg := MatchConfig{
		{
			Match:             MatchFields{Host: &Match{kind: matchExact, val: "pcw-api.iq.com"}, Path: &Match{kind: matchPrefix, val: "/api/vtype"}},
			IgnoreQueryParams: []string{"callback", "deviceId"},
			RepeatOnMiss:      true,
		},
	}
	rs.StartNewRun(&cfg)

	// First call: ignored params stripped → normalizes to same key as entry → hit.
	resp1 := callHandleRequest(t, rs, "GET", "https://pcw-api.iq.com/api/vtype?callback=jQuery&deviceId=abc123")
	if resp1.StatusCode != 200 {
		t.Fatalf("first call: expected 200, got %d", resp1.StatusCode)
	}
	if got := resp1.Header.Get("X-Gospy-Replay"); got != "hit" {
		t.Fatalf("first call: expected X-Gospy-Replay: hit, got %q", got)
	}

	// Second call: same URL, queue exhausted → repeat serves cached response.
	resp2 := callHandleRequest(t, rs, "GET", "https://pcw-api.iq.com/api/vtype?callback=jQuery&deviceId=abc123")
	if resp2.StatusCode != 200 {
		t.Fatalf("second call: expected 200, got %d", resp2.StatusCode)
	}
	if got := resp2.Header.Get("X-Gospy-Replay"); got != "repeat" {
		t.Fatalf("second call: expected X-Gospy-Replay: repeat, got %q", got)
	}
	body, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"tm":"t","vf":"v","authKey":"k"}` {
		t.Fatalf("second call: unexpected body %q", body)
	}

	// Third call: still repeat (cache persists within the run).
	resp3 := callHandleRequest(t, rs, "GET", "https://pcw-api.iq.com/api/vtype?callback=X")
	if resp3.StatusCode != 200 {
		t.Fatalf("third call: expected 200, got %d", resp3.StatusCode)
	}
	if got := resp3.Header.Get("X-Gospy-Replay"); got != "repeat" {
		t.Fatalf("third call: expected X-Gospy-Replay: repeat, got %q", got)
	}

	// Verify events: hit, repeat, repeat.
	summaries, _ := ListReplayRuns(logRoot)
	dir, err := ReplayRunDir(logRoot, summaries[0].RunID)
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	events, err := LoadReplayRun(dir)
	if err != nil {
		t.Fatalf("LoadReplayRun: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Result != "hit" {
		t.Errorf("event 0: expected hit, got %s", events[0].Result)
	}
	if events[1].Result != "repeat" {
		t.Errorf("event 1: expected repeat, got %s", events[1].Result)
	}
	if events[2].Result != "repeat" {
		t.Errorf("event 2: expected repeat, got %s", events[2].Result)
	}
}

func TestReplayRepeatOnMiss_NoCache(t *testing.T) {
	rs, _, _ := newLoggingReplayServer(t)

	// Config with repeat_on_miss, no entries in the store at all.
	// Request for a URL that has no matching queue entry → exhausted.
	cfg := MatchConfig{
		{
			Match:        MatchFields{Host: &Match{kind: matchExact, val: "api.example.com"}},
			RepeatOnMiss: true,
		},
	}
	rs.StartNewRun(&cfg)

	// Call with no cached entry and no queue entry → exhausted (queue is empty).
	resp := callHandleRequest(t, rs, "GET", "https://api.example.com/data")
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expected 410, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Gospy-Replay"); got != "exhausted" {
		t.Fatalf("expected X-Gospy-Replay: exhausted, got %q", got)
	}
}

func TestReplayRepeatOnMiss_CacheClearedOnNewRun(t *testing.T) {
	rs, h, _ := newLoggingReplayServer(t)

	entry := &history.Entry{
		ID:        "r1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://api.example.com/data", Host: "api.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// First run: hit + repeat.
	cfg1 := MatchConfig{
		{Match: MatchFields{Host: &Match{kind: matchExact, val: "api.example.com"}}, RepeatOnMiss: true},
	}
	rs.StartNewRun(&cfg1)
	callHandleRequest(t, rs, "GET", "https://api.example.com/data")
	resp := callHandleRequest(t, rs, "GET", "https://api.example.com/data")
	if resp.StatusCode != 200 || resp.Header.Get("X-Gospy-Replay") != "repeat" {
		t.Fatalf("expected repeat, got status=%d replay=%s", resp.StatusCode, resp.Header.Get("X-Gospy-Replay"))
	}

	// New run with fresh config → queue rebuilds, entry is unconsumed again.
	cfg2 := MatchConfig{
		{Match: MatchFields{Host: &Match{kind: matchExact, val: "api.example.com"}}, RepeatOnMiss: true},
	}
	rs.StartNewRun(&cfg2)
	resp2 := callHandleRequest(t, rs, "GET", "https://api.example.com/data")
	if resp2.StatusCode != 200 || resp2.Header.Get("X-Gospy-Replay") != "hit" {
		t.Fatalf("expected hit after new run, got status=%d replay=%s", resp2.StatusCode, resp2.Header.Get("X-Gospy-Replay"))
	}
}

func TestReplayServer_ActiveMirror(t *testing.T) {
	rs, h, _ := newLoggingReplayServer(t)
	for _, e := range []*history.Entry{
		{ID: "a1", Timestamp: time.Now(), Request: history.RequestRecord{Method: "GET", URL: "https://live.example.com/a", Host: "live.example.com"}, Response: &history.ResponseRecord{Status: 200}},
		{ID: "a2", Timestamp: time.Now().Add(time.Second), Request: history.RequestRecord{Method: "GET", URL: "https://live.example.com/b", Host: "live.example.com"}, Response: &history.ResponseRecord{Status: 200}},
	} {
		if err := h.Save(e); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if got := rs.ActiveRunID(); got != "" {
		t.Fatalf("expected empty ActiveRunID before first request, got %q", got)
	}
	if got := rs.ActiveEvents(); len(got) != 0 {
		t.Fatalf("expected 0 active events before first request, got %d", len(got))
	}
	callHandleRequest(t, rs, "GET", "https://live.example.com/a")
	if got := rs.ActiveRunID(); got == "" {
		t.Fatal("expected ActiveRunID set after first request")
	}
	evs := rs.ActiveEvents()
	if len(evs) != 1 || evs[0].Result != "hit" || evs[0].EntryID != "a1" {
		t.Fatalf("unexpected active events after hit: %+v", evs)
	}
	callHandleRequest(t, rs, "GET", "https://example.com/miss")
	evs = rs.ActiveEvents()
	if len(evs) != 2 || evs[1].Result != "miss" {
		t.Fatalf("expected 2 active events after miss: %+v", evs)
	}
	// EventsFor on the active run returns the mirror copy.
	activeID := rs.ActiveRunID()
	got, err := rs.EventsFor(activeID)
	if err != nil {
		t.Fatalf("EventsFor active: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events from EventsFor, got %d", len(got))
	}
	// StartNewRun clears the active mirror.
	rs.StartNewRun(nil)
	if got := rs.ActiveRunID(); got != "" {
		t.Fatalf("expected empty ActiveRunID after StartNewRun, got %q", got)
	}
	if got := rs.ActiveEvents(); len(got) != 0 {
		t.Fatalf("expected 0 active events after StartNewRun, got %d", len(got))
	}
}

func TestReplayServer_Subscribe(t *testing.T) {
	rs, h := newReplayServer(t)
	if err := h.Save(&history.Entry{ID: "s1", Timestamp: time.Now(), Request: history.RequestRecord{Method: "GET", URL: "https://live.example.com/a", Host: "live.example.com"}, Response: &history.ResponseRecord{Status: 200}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ch, cancel := rs.Subscribe()
	defer cancel()
	callHandleRequest(t, rs, "GET", "https://live.example.com/a")
	select {
	case ev := <-ch:
		if ev.Result != "hit" || ev.EntryID != "s1" {
			t.Fatalf("unexpected subscribed event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscribed event")
	}
	cancel()
	callHandleRequest(t, rs, "GET", "https://example.com/miss2")
	select {
	case ev := <-ch:
		t.Fatalf("expected no event after cancel, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
