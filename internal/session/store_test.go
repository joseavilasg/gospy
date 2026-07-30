package session

import (
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

	matched := s2.Match("GET", "https://example.com/api/v1/data", nil, nil)
	if matched == nil {
		t.Fatal("expected match, got nil")
	}
	if matched.ID != "test-1" {
		t.Fatalf("expected ID test-1, got %s", matched.ID)
	}
	if matched.Response.Status != 200 {
		t.Fatalf("expected status 200, got %d", matched.Response.Status)
	}

	miss := s2.Match("POST", "https://example.com/api/v1/data", nil, nil)
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

	matched := s.Match("GET", "https://api.example.com/endpoint?token=xyz&id=123", nil, cfg)
	if matched == nil {
		t.Fatal("expected match with ignored token param")
	}

	miss := s.Match("GET", "https://api.example.com/endpoint?token=abc&id=456", nil, cfg)
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
	matched := s.Match("GET", "http://example.com/", nil, nil)
	if matched != nil {
		t.Fatal("expected nil for empty session")
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
