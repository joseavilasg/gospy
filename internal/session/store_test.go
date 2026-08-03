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

	matched, exhausted := rs.Match("GET", "https://example.com/api/v1/data", nil)
	if exhausted {
		t.Fatal("unexpected exhausted")
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

	miss, _ := rs.Match("POST", "https://example.com/api/v1/data", nil)
	if miss != nil {
		t.Fatalf("expected no match for POST, got entry %s", miss.ID)
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
	matched, _ := rs.Match("GET", "https://example.com/api/v1/data", nil)
	if matched == nil || matched.ID != "test-1" {
		t.Fatalf("expected match after reload, got %+v", matched)
	}
}

func TestMatchWithIgnoreQueryParams(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "t1", "GET", "https://api.example.com/endpoint?token=abc&id=123", 200, time.Now())

	cfg := &MatchConfig{IgnoreQueryParams: []string{"token"}}

	matched, _ := rs.Match("GET", "https://api.example.com/endpoint?token=xyz&id=123", cfg)
	if matched == nil {
		t.Fatal("expected match with ignored token param")
	}

	miss, _ := rs.Match("GET", "https://api.example.com/endpoint?token=abc&id=456", cfg)
	if miss != nil {
		t.Fatal("expected no match when non-ignored param differs")
	}
}

func TestEmptyMatch(t *testing.T) {
	rs, _ := newReplay(t)
	matched, exhausted := rs.Match("GET", "http://example.com/", nil)
	if matched != nil {
		t.Fatal("expected nil for empty session")
	}
	if exhausted {
		t.Fatal("empty session should not report exhausted")
	}
}

func TestMatchSequentialRepeatedURL(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	for i := 1; i <= 3; i++ {
		saveTestEntry(t, h, fmt.Sprintf("r%d", i), "GET", "https://live.example.com/master.m3u8", 200, base.Add(time.Duration(i)*time.Second))
	}
	for i := 1; i <= 3; i++ {
		e, exhausted := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
		if exhausted {
			t.Fatalf("poll %d: unexpected exhausted", i)
		}
		if e == nil || e.ID != fmt.Sprintf("r%d", i) {
			t.Fatalf("poll %d: expected entry r%d, got %+v", i, i, e)
		}
	}
	e, exhausted := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
	if e != nil || !exhausted {
		t.Fatalf("expected exhausted after consuming all, got entry=%v exhausted=%v", e, exhausted)
	}
}

func TestMatchSequentialMixed(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "seg1", "GET", "https://live.example.com/seg-1.ts", 200, base)
	saveTestEntry(t, h, "m1", "GET", "https://live.example.com/master.m3u8", 200, base.Add(time.Second))
	saveTestEntry(t, h, "m2", "GET", "https://live.example.com/master.m3u8", 200, base.Add(2*time.Second))

	seg, _ := rs.Match("GET", "https://live.example.com/seg-1.ts", nil)
	if seg == nil || seg.ID != "seg1" {
		t.Fatalf("expected seg1, got %+v", seg)
	}
	seg2, ex := rs.Match("GET", "https://live.example.com/seg-1.ts", nil)
	if seg2 != nil || !ex {
		t.Fatalf("unique segment should be exhausted on second request, got entry=%v exhausted=%v", seg2, ex)
	}
	m, _ := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
	if m == nil || m.ID != "m1" {
		t.Fatalf("expected m1, got %+v", m)
	}
	m2, _ := rs.Match("GET", "https://live.example.com/master.m3u8", nil)
	if m2 == nil || m2.ID != "m2" {
		t.Fatalf("expected m2, got %+v", m2)
	}
}

func TestMatchRetrySequential(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "e403", "GET", "https://s.example.com/stream", 403, base)
	saveTestEntry(t, h, "e200", "GET", "https://s.example.com/stream", 200, base.Add(time.Second))

	first, _ := rs.Match("GET", "https://s.example.com/stream", nil)
	if first == nil || first.Response.Status != 403 {
		t.Fatalf("first attempt should get the recorded 403, got %+v", first)
	}
	retry, _ := rs.Match("GET", "https://s.example.com/stream", nil)
	if retry == nil || retry.Response.Status != 200 {
		t.Fatalf("retry should get the recorded 200, got %+v", retry)
	}
}

func TestMatchSkipsNoStatus(t *testing.T) {
	rs, h := newReplay(t)
	base := time.Now()
	saveTestEntry(t, h, "nos", "GET", "https://s.example.com/x", 0, base)
	saveTestEntry(t, h, "ok", "GET", "https://s.example.com/x", 200, base.Add(time.Second))

	e, _ := rs.Match("GET", "https://s.example.com/x", nil)
	if e == nil || e.ID != "ok" {
		t.Fatalf("expected the 200 entry, got %+v", e)
	}
	_, ex := rs.Match("GET", "https://s.example.com/x", nil)
	if !ex {
		t.Fatal("after serving the 200, group should be exhausted (Status 0 must not consume a slot)")
	}
}

func TestMatchRebuildAfterSave(t *testing.T) {
	rs, h := newReplay(t)

	e, _ := rs.Match("GET", "https://s.example.com/x", nil)
	if e != nil {
		t.Fatal("expected nil on empty store")
	}
	saveTestEntry(t, h, "a1", "GET", "https://s.example.com/x", 200, time.Now())
	e, ex := rs.Match("GET", "https://s.example.com/x", nil)
	if e == nil || ex {
		t.Fatalf("expected match after Save, got entry=%v exhausted=%v", e, ex)
	}
}

func TestMatchMethodCaseInsensitive(t *testing.T) {
	rs, h := newReplay(t)
	saveTestEntry(t, h, "g1", "get", "https://s.example.com/x", 200, time.Now())
	e, _ := rs.Match("GET", "https://s.example.com/x", nil)
	if e == nil || e.ID != "g1" {
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
