package session

import (
	"io"
	"net/http"
	"testing"
	"time"

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
