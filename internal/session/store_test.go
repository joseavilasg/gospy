package session

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"gospy/internal/history"
)

func saveTestEntry(t *testing.T, h *history.Store, id, method, rawURL string, status int, ts time.Time) {
	t.Helper()
	u, _ := url.Parse(rawURL)
	host := ""
	if u != nil {
		host = u.Host
	}
	var resp *history.ResponseRecord
	if status != 0 {
		resp = &history.ResponseRecord{Status: status}
	} else {
		resp = &history.ResponseRecord{}
	}
	if err := h.Save(&history.Entry{
		ID:        id,
		Timestamp: ts,
		Request:   history.RequestRecord{Method: method, URL: rawURL, Host: host},
		Response:  resp,
	}); err != nil {
		t.Fatalf("save entry %s: %v", id, err)
	}
}

func newReplay(t *testing.T) (*ReplayStore, *history.Store) {
	t.Helper()
	h, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	return NewReplayStore(h), h
}

func TestMatch(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "test-1", "GET", "https://example.com/api/v1/data", 200, base)
	saveTestEntry(t, h, "test-2", "GET", "https://example.com/api/v2/data", 200, base.Add(time.Second))

	matched, result := rs.Match("GET", "https://example.com/api/v1/data", nil)
	if result != ResultHit {
		t.Fatalf("expected hit, got %v", result)
	}
	if matched == nil {
		t.Fatal("expected match, got nil")
	}
	if matched.ID != "test-1" {
		t.Fatalf("expected ID test-1, got %s", matched.ID)
	}
	if matched.Response.Status != 200 {
		t.Fatalf("expected status 200, got %d", matched.Response.Status)
	}

	miss, result := rs.Match("POST", "https://example.com/api/v1/data", nil)
	if miss != nil || result != ResultMiss {
		t.Fatalf("expected miss for POST, got entry=%v result=%v", miss, result)
	}
}

func TestReloadFromDisk(t *testing.T) {
	h, err := history.New(t.TempDir())
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	base := time.Now()
	saveTestEntry(t, h, "test-1", "GET", "https://example.com/api/v1/data", 200, base)

	h2, err := history.New(h.Dir())
	if err != nil {
		t.Fatalf("reload history: %v", err)
	}
	rs := NewReplayStore(h2)
	matched, result := rs.Match("GET", "https://example.com/api/v1/data", nil)
	if matched == nil || matched.ID != "test-1" || result != ResultHit {
		t.Fatalf("expected hit after reload, got entry=%+v result=%v", matched, result)
	}
}

func TestMatchWithIgnoreQueryParams(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "t1", "GET", "https://api.example.com/endpoint?token=abc&id=123", 200, time.Now())
	saveTestEntry(t, h, "t2", "GET", "https://api.example.com/endpoint?token=def&id=999", 200, time.Now().Add(time.Second))

	cfg := &MatchConfig{IgnoreQueryParams: []string{"token"}}

	matched, result := rs.Match("GET", "https://api.example.com/endpoint?token=xyz&id=123", cfg)
	if matched == nil || result != ResultHit {
		t.Fatalf("expected hit with ignored token param, got entry=%v result=%v", matched, result)
	}

	miss, result := rs.Match("GET", "https://api.example.com/endpoint?token=abc&id=456", cfg)
	if miss != nil || result != ResultMiss {
		t.Fatalf("expected miss when non-ignored param differs, got entry=%v result=%v", miss, result)
	}
}

func TestEmptyMatch(t *testing.T) {
	rs, _ := newReplay(t)
	matched, result := rs.Match("GET", "http://example.com/", nil)
	if matched != nil {
		t.Fatal("expected nil for empty session")
	}
	if result != ResultExhausted {
		t.Fatalf("empty session should be exhausted, got %v", result)
	}
}

func TestMatchSequentialRepeatedURL(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	for i := 1; i <= 3; i++ {
		saveTestEntry(t, h, fmt.Sprintf("r%d", i), "GET", "https://live.example.com/master.m3u8", 200, base.Add(time.Duration(i)*time.Second))
	}
	for i := 1; i <= 3; i++ {
		e, result := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
		if result != ResultHit {
			t.Fatalf("poll %d: expected hit, got %v", i, result)
		}
		if e == nil || e.ID != fmt.Sprintf("r%d", i) {
			t.Fatalf("poll %d: expected entry r%d, got %+v", i, i, e)
		}
	}
	e, result := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
	if e != nil || result != ResultExhausted {
		t.Fatalf("expected exhausted after consuming all, got entry=%v result=%v", e, result)
	}
}

func TestMatchGlobalOrder(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "a1", "GET", "https://live.example.com/master.m3u8", 200, base)
	saveTestEntry(t, h, "b1", "GET", "https://live.example.com/seg-1.ts", 200, base.Add(time.Second))
	saveTestEntry(t, h, "a2", "GET", "https://live.example.com/master.m3u8", 200, base.Add(2*time.Second))
	saveTestEntry(t, h, "b2", "GET", "https://live.example.com/seg-1.ts", 200, base.Add(3*time.Second))

	expected := []struct {
		url string
		id  string
	}{
		{"https://live.example.com/master.m3u8", "a1"},
		{"https://live.example.com/seg-1.ts", "b1"},
		{"https://live.example.com/master.m3u8", "a2"},
		{"https://live.example.com/seg-1.ts", "b2"},
	}
	for _, want := range expected {
		e, result := rs.Match("GET", want.url, nil)
		if result != ResultHit {
			t.Fatalf("expected hit for %s, got %v", want.url, result)
		}
		if e == nil || e.ID != want.id {
			t.Fatalf("expected %s for %s, got %+v", want.id, want.url, e)
		}
	}

	_, result := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
	if result != ResultExhausted {
		t.Fatalf("expected exhausted after full session, got %v", result)
	}
}

func TestMatchNotExhaustedWhileUnconsumedRemain(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "a1", "GET", "https://live.example.com/master.m3u8", 200, base)
	saveTestEntry(t, h, "b1", "GET", "https://live.example.com/seg-1.ts", 200, base.Add(time.Second))

	if _, result := rs.Match("GET", "https://live.example.com/master.m3u8", nil); result != ResultHit {
		t.Fatalf("expected hit for a1, got %v", result)
	}
	e, result := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
	if e != nil || result != ResultMiss {
		t.Fatalf("exhaustion must be global: b1 remains, got entry=%v result=%v", e, result)
	}
	if _, result := rs.Match("GET", "https://live.example.com/seg-1.ts", nil); result != ResultHit {
		t.Fatalf("expected hit for b1, got %v", result)
	}
	_, result = rs.Match("GET", "https://live.example.com/seg-1.ts", nil)
	if result != ResultExhausted {
		t.Fatalf("expected exhausted after whole session consumed, got %v", result)
	}
}

func TestMatchRetrySequential(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "e403", "GET", "https://s.example.com/stream", 403, base)
	saveTestEntry(t, h, "e200", "GET", "https://s.example.com/stream", 200, base.Add(time.Second))

	first, result := rs.Match("GET", "https://s.example.com/stream", nil)
	if result != ResultHit || first.Response.Status != 403 {
		t.Fatalf("first attempt should get the recorded 403, got result=%v entry=%+v", result, first)
	}
	retry, result := rs.Match("GET", "https://s.example.com/stream", nil)
	if result != ResultHit || retry.Response.Status != 200 {
		t.Fatalf("retry should get the recorded 200, got result=%v entry=%+v", result, retry)
	}
}

func TestMatchSkipsNoStatus(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "nos", "GET", "https://s.example.com/x", 0, base)
	saveTestEntry(t, h, "ok", "GET", "https://s.example.com/x", 200, base.Add(time.Second))

	e, result := rs.Match("GET", "https://s.example.com/x", nil)
	if result != ResultHit || e.ID != "ok" {
		t.Fatalf("expected the 200 entry, got result=%v entry=%+v", result, e)
	}
	_, result = rs.Match("GET", "https://s.example.com/x", nil)
	if result != ResultExhausted {
		t.Fatalf("Status 0 entry must not consume a slot; after the 200 the session is exhausted, got %v", result)
	}
}

func TestMatchRebuildAfterSave(t *testing.T) {
	rs, h := newReplay(t)

	e, result := rs.Match("GET", "https://s.example.com/x", nil)
	if e != nil || result != ResultExhausted {
		t.Fatalf("expected exhausted on empty store, got entry=%v result=%v", e, result)
	}
	saveTestEntry(t, h, "a1", "GET", "https://s.example.com/x", 200, time.Now())
	e, result = rs.Match("GET", "https://s.example.com/x", nil)
	if e == nil || result != ResultHit {
		t.Fatalf("expected hit after Save, got entry=%v result=%v", e, result)
	}
}

func TestMatchDetailedMissUnconsumed(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "u1", "GET", "https://example.com/a", 200, base)
	saveTestEntry(t, h, "u2", "GET", "https://example.com/b", 200, base.Add(time.Second))
	saveTestEntry(t, h, "u3", "GET", "https://example.com/c", 200, base.Add(2*time.Second))

	if _, res, _, _ := rs.MatchDetailed("GET", "https://example.com/a", nil); res != ResultHit {
		t.Fatalf("expected hit, got %v", res)
	}

	entry, result, pending, total := rs.MatchDetailed("GET", "https://example.com/not-recorded", nil)
	if entry != nil || result != ResultMiss {
		t.Fatalf("expected miss, got entry=%v result=%v", entry, result)
	}
	if total != 2 {
		t.Fatalf("expected 2 pending, got %d", total)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 unconsumed entries, got %d", len(pending))
	}
	if pending[0].ID != "u2" {
		t.Fatalf("unexpected pending[0]: %+v", pending[0])
	}
	if pending[1].ID != "u3" {
		t.Fatalf("unexpected pending[1]: %+v", pending[1])
	}
}

func TestMatchDetailedMissUnconsumedNoCap(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("u%d", i+1)
		saveTestEntry(t, h, id, "GET", fmt.Sprintf("https://example.com/%d", i+1), 200, base.Add(time.Duration(i)*time.Second))
	}

	_, result, pending, total := rs.MatchDetailed("GET", "https://example.com/not-recorded", nil)
	if result != ResultMiss {
		t.Fatalf("expected miss, got %v", result)
	}
	if total != 60 {
		t.Fatalf("expected 60 pending, got %d", total)
	}
	if len(pending) != 60 {
		t.Fatalf("expected all 60 unconsumed entries (no 50 cap), got %d", len(pending))
	}
}

func TestMatchDetailedHitNoUnconsumed(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "h1", "GET", "https://example.com/a", 200, time.Now())
	saveTestEntry(t, h, "h2", "GET", "https://example.com/b", 200, time.Now().Add(time.Second))

	entry, result, pending, total := rs.MatchDetailed("GET", "https://example.com/a", nil)
	if result != ResultHit || entry == nil {
		t.Fatalf("expected hit, got entry=%v result=%v", entry, result)
	}
	if pending != nil || total != 1 {
		t.Fatalf("expected no unconsumed on hit, got pending=%v total=%d", pending, total)
	}
}

func TestMatchDetailedExhaustedNoPending(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "x1", "GET", "https://example.com/a", 200, time.Now())

	if _, res, _, _ := rs.MatchDetailed("GET", "https://example.com/a", nil); res != ResultHit {
		t.Fatalf("expected hit, got %v", res)
	}
	entry, result, pending, total := rs.MatchDetailed("GET", "https://example.com/anything", nil)
	if entry != nil || result != ResultExhausted {
		t.Fatalf("expected exhausted, got entry=%v result=%v", entry, result)
	}
	if pending != nil || total != 0 {
		t.Fatalf("expected no pending when exhausted, got pending=%v total=%d", pending, total)
	}
}

func TestProgress(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "p1", "GET", "https://example.com/a", 200, time.Now())
	saveTestEntry(t, h, "p2", "GET", "https://example.com/b", 200, time.Now().Add(time.Second))

	if consumed, total, exhausted := rs.Progress(nil); consumed != 0 || total != 2 || exhausted {
		t.Fatalf("expected 0/2 not exhausted, got %d/%d exhausted=%v", consumed, total, exhausted)
	}

	rs.Match("GET", "https://example.com/a", nil)
	if consumed, total, exhausted := rs.Progress(nil); consumed != 1 || total != 2 || exhausted {
		t.Fatalf("expected 1/2 not exhausted, got %d/%d exhausted=%v", consumed, total, exhausted)
	}

	rs.Match("GET", "https://example.com/b", nil)
	if consumed, total, exhausted := rs.Progress(nil); consumed != 2 || total != 2 || !exhausted {
		t.Fatalf("expected 2/2 exhausted, got %d/%d exhausted=%v", consumed, total, exhausted)
	}
}

func TestMatchMethodCaseInsensitive(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "g1", "get", "https://s.example.com/x", 200, time.Now())
	e, result := rs.Match("GET", "https://s.example.com/x", nil)
	if e == nil || e.ID != "g1" || result != ResultHit {
		t.Fatal("method matching should be case-insensitive")
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		raw  string
		cfg  *MatchConfig
		want string
	}{
		{
			raw:  "https://example.com/path?q=1&token=a",
			cfg:  &MatchConfig{IgnoreQueryParams: []string{"token"}},
			want: "https://example.com/path?q=1",
		},
		{
			raw:  "https://example.com/path",
			cfg:  nil,
			want: "https://example.com/path",
		},
		{
			raw:  "http://a.com:8080/x?y=z",
			cfg:  &MatchConfig{IgnoreQueryParams: []string{"token"}},
			want: "http://a.com:8080/x?y=z",
		},
	}
	for _, tc := range tests {
		got := normalizeURL(tc.raw, tc.cfg)
		if got != tc.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
