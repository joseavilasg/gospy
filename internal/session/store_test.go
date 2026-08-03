package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndMatch(t *testing.T) {
	dir := t.TempDir()

	s, err := NewOrLoad(dir)
	if err != nil {
		t.Fatalf("NewOrLoad: %v", err)
	}

	e := &Entry{
		ID:        "test-1",
		Timestamp: time.Now(),
		Request: ReqRecord{
			Method:  "GET",
			URL:     "https://example.com/api/v1/data",
			Host:    "example.com",
			Headers: map[string][]string{"Accept": {"application/json"}},
		},
		Response: RespRecord{
			Status:  200,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
		},
	}

	if err := s.SaveEntry(e); err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}

	s2, err := NewOrLoad(dir)
	if err != nil {
		t.Fatalf("NewOrLoad second: %v", err)
	}

	matched, _ := s2.Match("GET", "https://example.com/api/v1/data", nil)
	if matched == nil {
		t.Fatal("expected match, got nil")
	}
	if matched.ID != "test-1" {
		t.Fatalf("expected ID test-1, got %s", matched.ID)
	}
	if matched.Response.Status != 200 {
		t.Fatalf("expected status 200, got %d", matched.Response.Status)
	}

	miss, _ := s2.Match("POST", "https://example.com/api/v1/data", nil)
	if miss != nil {
		t.Fatalf("expected no match for POST, got entry %s", miss.ID)
	}
}

func TestMatchWithIgnoreQueryParams(t *testing.T) {
	dir := t.TempDir()

	s, _ := NewOrLoad(dir)
	s.SaveEntry(&Entry{
		ID:        "t1",
		Timestamp: time.Now(),
		Request: ReqRecord{
			Method: "GET",
			URL:    "https://api.example.com/endpoint?token=abc&id=123",
			Host:   "api.example.com",
		},
		Response: RespRecord{Status: 200},
	})

	cfg := &MatchConfig{IgnoreQueryParams: []string{"token"}}

	matched, _ := s.Match("GET", "https://api.example.com/endpoint?token=xyz&id=123", cfg)
	if matched == nil {
		t.Fatal("expected match with ignored token param")
	}

	miss, _ := s.Match("GET", "https://api.example.com/endpoint?token=abc&id=456", cfg)
	if miss != nil {
		t.Fatal("expected no match when non-ignored param differs")
	}
}

func TestSaveBin(t *testing.T) {
	dir := t.TempDir()

	s, _ := NewOrLoad(dir)
	data := []byte("hello world")
	filename, err := s.SaveBin("e1", "req", data)
	if err != nil {
		t.Fatalf("SaveBin: %v", err)
	}
	if filename != "e1-req.bin" {
		t.Fatalf("expected filename 'e1-req.bin', got %q", filename)
	}

	read, err := os.ReadFile(filepath.Join(dir, "entries", "e1-req.bin"))
	if err != nil {
		t.Fatalf("read bin: %v", err)
	}
	if string(read) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", read)
	}
}

func TestEmptyMatch(t *testing.T) {
	dir := t.TempDir()

	s, _ := NewOrLoad(dir)
	matched, _ := s.Match("GET", "http://example.com/", nil)
	if matched != nil {
		t.Fatal("expected nil for empty session")
	}
}

func TestMatchSequentialRepeatedURL(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewOrLoad(dir)
	base := time.Now()
	for i := 1; i <= 3; i++ {
		s.SaveEntry(&Entry{
			ID:        fmt.Sprintf("r%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Request: ReqRecord{
				Method: "GET",
				URL:    "https://live.example.com/master.m3u8",
				Host:   "live.example.com",
			},
			Response: RespRecord{Status: 200},
		})
	}
	for i := 1; i <= 3; i++ {
		e, exhausted := s.Match("GET", "https://live.example.com/master.m3u8", nil)
		if exhausted {
			t.Fatalf("poll %d: unexpected exhausted", i)
		}
		if e == nil || e.ID != fmt.Sprintf("r%d", i) {
			t.Fatalf("poll %d: expected entry r%d, got %+v", i, i, e)
		}
	}
	e, exhausted := s.Match("GET", "https://live.example.com/master.m3u8", nil)
	if e != nil || !exhausted {
		t.Fatalf("expected exhausted after consuming all, got entry=%v exhausted=%v", e, exhausted)
	}
}

func TestMatchSequentialMixed(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewOrLoad(dir)
	base := time.Now()
	s.SaveEntry(&Entry{ID: "seg1", Timestamp: base, Request: ReqRecord{Method: "GET", URL: "https://live.example.com/seg-1.ts", Host: "live.example.com"}, Response: RespRecord{Status: 200}})
	s.SaveEntry(&Entry{ID: "m1", Timestamp: base.Add(time.Second), Request: ReqRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"}, Response: RespRecord{Status: 200}})
	s.SaveEntry(&Entry{ID: "m2", Timestamp: base.Add(2 * time.Second), Request: ReqRecord{Method: "GET", URL: "https://live.example.com/master.m3u8", Host: "live.example.com"}, Response: RespRecord{Status: 200}})

	seg, _ := s.Match("GET", "https://live.example.com/seg-1.ts", nil)
	if seg == nil || seg.ID != "seg1" {
		t.Fatalf("expected seg1, got %+v", seg)
	}
	seg2, ex := s.Match("GET", "https://live.example.com/seg-1.ts", nil)
	if seg2 != nil || !ex {
		t.Fatalf("unique segment should be exhausted on second request, got entry=%v exhausted=%v", seg2, ex)
	}
	m, _ := s.Match("GET", "https://live.example.com/master.m3u8", nil)
	if m == nil || m.ID != "m1" {
		t.Fatalf("expected m1, got %+v", m)
	}
	m2, _ := s.Match("GET", "https://live.example.com/master.m3u8", nil)
	if m2 == nil || m2.ID != "m2" {
		t.Fatalf("expected m2, got %+v", m2)
	}
}

func TestMatchRetrySequential(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewOrLoad(dir)
	base := time.Now()
	s.SaveEntry(&Entry{ID: "e403", Timestamp: base, Request: ReqRecord{Method: "GET", URL: "https://s.example.com/stream", Host: "s.example.com"}, Response: RespRecord{Status: 403}})
	s.SaveEntry(&Entry{ID: "e200", Timestamp: base.Add(time.Second), Request: ReqRecord{Method: "GET", URL: "https://s.example.com/stream", Host: "s.example.com"}, Response: RespRecord{Status: 200}})

	first, _ := s.Match("GET", "https://s.example.com/stream", nil)
	if first == nil || first.Response.Status != 403 {
		t.Fatalf("first attempt should get the recorded 403, got %+v", first)
	}
	retry, _ := s.Match("GET", "https://s.example.com/stream", nil)
	if retry == nil || retry.Response.Status != 200 {
		t.Fatalf("retry should get the recorded 200, got %+v", retry)
	}
}

func TestMatchSkipsNoStatus(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewOrLoad(dir)
	base := time.Now()
	s.SaveEntry(&Entry{ID: "nos", Timestamp: base, Request: ReqRecord{Method: "GET", URL: "https://s.example.com/x", Host: "s.example.com"}, Response: RespRecord{}})
	s.SaveEntry(&Entry{ID: "ok", Timestamp: base.Add(time.Second), Request: ReqRecord{Method: "GET", URL: "https://s.example.com/x", Host: "s.example.com"}, Response: RespRecord{Status: 200}})

	e, _ := s.Match("GET", "https://s.example.com/x", nil)
	if e == nil || e.ID != "ok" {
		t.Fatalf("expected the 200 entry, got %+v", e)
	}
	_, ex := s.Match("GET", "https://s.example.com/x", nil)
	if !ex {
		t.Fatal("after serving the 200, group should be exhausted (Status 0 must not consume a slot)")
	}
}

func TestMatchRebuildAfterSave(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewOrLoad(dir)

	e, _ := s.Match("GET", "https://s.example.com/x", nil)
	if e != nil {
		t.Fatal("expected nil on empty store")
	}
	s.SaveEntry(&Entry{ID: "a1", Timestamp: time.Now(), Request: ReqRecord{Method: "GET", URL: "https://s.example.com/x", Host: "s.example.com"}, Response: RespRecord{Status: 200}})
	e, ex := s.Match("GET", "https://s.example.com/x", nil)
	if e == nil || ex {
		t.Fatalf("expected match after SaveEntry, got entry=%v exhausted=%v", e, ex)
	}
}

func TestMatchMethodCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewOrLoad(dir)
	s.SaveEntry(&Entry{ID: "g1", Timestamp: time.Now(), Request: ReqRecord{Method: "get", URL: "https://s.example.com/x", Host: "s.example.com"}, Response: RespRecord{Status: 200}})
	e, _ := s.Match("GET", "https://s.example.com/x", nil)
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
