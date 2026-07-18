package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entries := store.List()
	if len(entries) != 0 {
		t.Errorf("List() = %d entries, want 0", len(entries))
	}
}

func TestStore_Save(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method:  "GET",
			URL:     "http://example.com/api",
			Host:    "example.com",
			Headers: map[string][]string{"Accept": {"application/json"}},
		},
		Action: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if entry.ID == "" {
		t.Error("Save() did not generate ID")
	}

	entries := store.List()
	if len(entries) != 1 {
		t.Errorf("List() = %d entries, want 1", len(entries))
	}
}

func TestStore_SaveWithResponse(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "POST",
			URL:    "http://example.com/data",
			Host:   "example.com",
		},
		Response: &ResponseRecord{
			Status:  201,
			Headers: map[string][]string{"Location": {"/data/123"}},
			Body:    `{"id": 123}`,
		},
		Action: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Response == nil {
		t.Fatal("Get() response is nil")
	}
	if retrieved.Response.Status != 201 {
		t.Errorf("Response.Status = %d, want 201", retrieved.Response.Status)
	}
	if retrieved.Response.Body != `{"id": 123}` {
		t.Errorf("Response.Body = %q, want %q", retrieved.Response.Body, `{"id": 123}`)
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	store1, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "GET",
			URL:    "http://example.com/",
			Host:   "example.com",
		},
		Action: "passthrough",
	}

	if err := store1.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	store2, err := New(dir)
	if err != nil {
		t.Fatalf("New() second load error = %v", err)
	}

	entries := store2.ListSummary()
	if len(entries) != 1 {
		t.Errorf("ListSummary() after reload = %d entries, want 1", len(entries))
	}
}

func TestStore_Get(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "GET",
			URL:    "http://test.com/",
			Host:   "test.com",
		},
		Action: "mock",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Request.Method != "GET" {
		t.Errorf("Request.Method = %q, want %q", retrieved.Request.Method, "GET")
	}
}

func TestStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = store.Get("nonexistent")
	if err == nil {
		t.Error("Get() nonexistent = nil, want error")
	}
}

func TestStore_Clear(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for i := 0; i < 5; i++ {
		entry := &Entry{
			Request: RequestRecord{
				Method: "GET",
				URL:    "http://example.com/",
				Host:   "example.com",
			},
			Action: "passthrough",
		}
		if err := store.Save(entry); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	entries := store.List()
	if len(entries) != 0 {
		t.Errorf("List() after Clear() = %d entries, want 0", len(entries))
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Errorf("Clear() left %d files, want 1 (index.json)", len(files))
	}
}

func TestStore_ListSortedByTime(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	times := []time.Time{
		time.Now().Add(-3 * time.Hour),
		time.Now().Add(-1 * time.Hour),
		time.Now().Add(-2 * time.Hour),
	}

	for i, ts := range times {
		entry := &Entry{
			Timestamp: ts,
			Request: RequestRecord{
				Method: "GET",
				URL:    "http://example.com/",
				Host:   "example.com",
			},
			Action: "passthrough",
		}
		entry.ID = ""
		if err := store.Save(entry); err != nil {
			t.Fatalf("Save() %d error = %v", i, err)
		}
	}

	summary := store.ListSummary()
	if len(summary) != 3 {
		t.Fatalf("ListSummary() = %d entries, want 3", len(summary))
	}

	for i := 0; i < len(summary)-1; i++ {
		if summary[i].Timestamp.Before(summary[i+1].Timestamp) {
			t.Errorf("entries[%d] (%v) before entries[%d] (%v)", i, summary[i].Timestamp, i+1, summary[i+1].Timestamp)
		}
	}
}

func TestStore_FilePerEntry(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "GET",
			URL:    "http://example.com/",
			Host:   "example.com",
		},
		Action: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	filePath := filepath.Join(dir, entry.ID+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Entry file %s not created", filePath)
	}
}

func TestStore_ListSummary(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method:  "POST",
			URL:     "http://example.com/api",
			Host:    "example.com",
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    `{"key":"value"}`,
		},
		Response: &ResponseRecord{
			Status:  200,
			Headers: map[string][]string{"X-Custom": {"yes"}},
			Body:    "response body",
		},
		Action: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	summary := store.ListSummary()
	if len(summary) != 1 {
		t.Fatalf("ListSummary() = %d entries, want 1", len(summary))
	}

	s := summary[0]
	if s.ID != entry.ID {
		t.Errorf("ListEntry.ID = %q, want %q", s.ID, entry.ID)
	}
	if s.Method != "POST" {
		t.Errorf("ListEntry.Method = %q, want %q", s.Method, "POST")
	}
	if s.URL != "http://example.com/api" {
		t.Errorf("ListEntry.URL = %q, want %q", s.URL, "http://example.com/api")
	}
	if s.Host != "example.com" {
		t.Errorf("ListEntry.Host = %q, want %q", s.Host, "example.com")
	}
	if s.Status == nil {
		t.Fatal("ListEntry.Status is nil, want 200")
	}
	if *s.Status != 200 {
		t.Errorf("ListEntry.Status = %d, want 200", *s.Status)
	}
}

func TestStore_ListSummaryNoResponse(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "GET",
			URL:    "http://example.com/",
			Host:   "example.com",
		},
		Action: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	summary := store.ListSummary()
	if len(summary) != 1 {
		t.Fatalf("ListSummary() = %d entries, want 1", len(summary))
	}

	if summary[0].Status != nil {
		t.Errorf("ListEntry.Status = %v, want nil", *summary[0].Status)
	}
}

func TestStore_ListSince(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	now := time.Now()
	times := []time.Time{
		now.Add(-3 * time.Hour),
		now.Add(-1 * time.Hour),
		now.Add(-2 * time.Hour),
	}

	for _, ts := range times {
		entry := &Entry{
			Timestamp: ts,
			Request: RequestRecord{
				Method: "GET",
				URL:    "http://example.com/",
				Host:   "example.com",
			},
			Action: "passthrough",
		}
		if err := store.Save(entry); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	result := store.ListSince(now.Add(-2*time.Hour - 30*time.Minute))
	if len(result) != 2 {
		t.Fatalf("ListSince() = %d entries, want 2", len(result))
	}

	for _, entry := range result {
		if !entry.Timestamp.After(now.Add(-2*time.Hour - 30*time.Minute)) {
			t.Errorf("ListSince() returned entry with timestamp %v before cutoff", entry.Timestamp)
		}
	}
}

func TestStore_ListSinceNone(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "GET",
			URL:    "http://example.com/",
			Host:   "example.com",
		},
		Action: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	result := store.ListSince(time.Now().Add(1 * time.Hour))
	if len(result) != 0 {
		t.Errorf("ListSince() future time = %d entries, want 0", len(result))
	}
}
