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

	entries := store2.List()
	if len(entries) != 1 {
		t.Errorf("List() after reload = %d entries, want 1", len(entries))
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
	if len(files) != 0 {
		t.Errorf("Clear() left %d files, want 0", len(files))
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

	entries := store.List()
	if len(entries) != 3 {
		t.Fatalf("List() = %d entries, want 3", len(entries))
	}

	for i := 0; i < len(entries)-1; i++ {
		if entries[i].Timestamp.Before(entries[i+1].Timestamp) {
			t.Errorf("entries[%d] (%v) before entries[%d] (%v)", i, entries[i].Timestamp, i+1, entries[i+1].Timestamp)
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
