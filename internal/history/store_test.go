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
		AppliedAction: "passthrough",
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
		AppliedAction: "passthrough",
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
		AppliedAction: "passthrough",
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
		AppliedAction: "mock",
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
			AppliedAction: "passthrough",
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
			AppliedAction: "passthrough",
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
		AppliedAction: "passthrough",
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
		AppliedAction: "passthrough",
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
		AppliedAction: "passthrough",
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
			AppliedAction: "passthrough",
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
		AppliedAction: "passthrough",
	}

	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	result := store.ListSince(time.Now().Add(1 * time.Hour))
	if len(result) != 0 {
		t.Errorf("ListSince() future time = %d entries, want 0", len(result))
	}
}

func TestStore_SaveEditedBody(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "POST",
			URL:    "http://example.com/api",
			Host:   "example.com",
			Body:   `{"key":"original"}`,
		},
		AppliedAction: "passthrough",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.SaveEditedBody(entry.ID, "request", `{"key":"edited"}`); err != nil {
		t.Fatalf("SaveEditedBody() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Request.EditedBody != `{"key":"edited"}` {
		t.Errorf("EditedBody = %q, want %q", retrieved.Request.EditedBody, `{"key":"edited"}`)
	}
	if retrieved.Request.Body != `{"key":"original"}` {
		t.Errorf("Body = %q, want %q (original preserved)", retrieved.Request.Body, `{"key":"original"}`)
	}
}

func TestStore_RevertBody(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method:     "POST",
			URL:        "http://example.com/api",
			Host:       "example.com",
			Body:       `{"key":"original"}`,
			EditedBody: `{"key":"edited"}`,
		},
		AppliedAction: "passthrough",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.RevertBody(entry.ID, "request"); err != nil {
		t.Fatalf("RevertBody() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Request.EditedBody != "" {
		t.Errorf("EditedBody after revert = %q, want empty", retrieved.Request.EditedBody)
	}
	if retrieved.Request.Body != `{"key":"original"}` {
		t.Errorf("Body = %q, want original", retrieved.Request.Body)
	}
}

func TestStore_Replay(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	original := &Entry{
		Request: RequestRecord{
			Method:  "POST",
			URL:     "http://example.com/api",
			Host:    "example.com",
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    `{"key":"original"}`,
		},
		Response: &ResponseRecord{
			Status: 200,
			Body:   `{"result":"ok"}`,
		},
		AppliedAction: "passthrough",
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	replayed, err := store.Replay(original.ID, `{"key":"modified"}`)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if replayed.ID == original.ID {
		t.Error("Replay() returned same ID as original")
	}
	if replayed.ReplayedFrom != original.ID {
		t.Errorf("ReplayedFrom = %q, want %q", replayed.ReplayedFrom, original.ID)
	}
	if replayed.Request.Body != `{"key":"modified"}` {
		t.Errorf("Body = %q, want modified body", replayed.Request.Body)
	}
	if replayed.Request.Method != "POST" {
		t.Errorf("Method = %q, want POST", replayed.Request.Method)
	}
	if replayed.Request.URL != "http://example.com/api" {
		t.Errorf("URL = %q, want original URL", replayed.Request.URL)
	}
	if replayed.Response != nil {
		t.Error("Replay() should not carry over response")
	}

	index := store.ListSummary()
	if len(index) != 2 {
		t.Fatalf("ListSummary() = %d entries, want 2", len(index))
	}

	var foundOriginal, foundReplay bool
	for _, le := range index {
		if le.ID == original.ID {
			foundOriginal = true
		}
		if le.ID == replayed.ID {
			foundReplay = true
			if le.ReplayedFrom != original.ID {
				t.Errorf("Index ReplayedFrom = %q, want %q", le.ReplayedFrom, original.ID)
			}
		}
	}
	if !foundOriginal {
		t.Error("Original not found in index")
	}
	if !foundReplay {
		t.Error("Replayed entry not found in index")
	}
}

func TestStore_SaveEditedHeaders(t *testing.T) {
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
			Headers: map[string][]string{"Authorization": {"Bearer old-token"}},
		},
		AppliedAction: "passthrough",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	newHeaders := map[string][]string{
		"Authorization": {"Bearer new-token"},
		"X-Custom":      {"value1", "value2"},
	}
	if err := store.SaveEditedHeaders(entry.ID, newHeaders); err != nil {
		t.Fatalf("SaveEditedHeaders() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Request.EditedHeaders == nil {
		t.Fatal("EditedHeaders is nil, want non-nil")
	}
	if retrieved.Request.EditedHeaders["Authorization"][0] != "Bearer new-token" {
		t.Errorf("EditedHeaders[Authorization] = %v, want [Bearer new-token]", retrieved.Request.EditedHeaders["Authorization"])
	}
	if len(retrieved.Request.EditedHeaders["X-Custom"]) != 2 {
		t.Errorf("EditedHeaders[X-Custom] has %d values, want 2", len(retrieved.Request.EditedHeaders["X-Custom"]))
	}
	if retrieved.Request.Headers["Authorization"][0] != "Bearer old-token" {
		t.Errorf("Original Headers modified: Headers[Authorization] = %v, want [Bearer old-token]", retrieved.Request.Headers["Authorization"])
	}
}

func TestStore_RevertHeaders(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method:        "GET",
			URL:           "http://example.com/api",
			Host:          "example.com",
			Headers:       map[string][]string{"Authorization": {"Bearer old-token"}},
			EditedHeaders: map[string][]string{"Authorization": {"Bearer new-token"}},
		},
		AppliedAction: "passthrough",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.RevertHeaders(entry.ID); err != nil {
		t.Fatalf("RevertHeaders() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Request.EditedHeaders != nil {
		t.Errorf("EditedHeaders = %v, want nil after revert", retrieved.Request.EditedHeaders)
	}
	if retrieved.Request.Headers["Authorization"][0] != "Bearer old-token" {
		t.Errorf("Original Headers lost: Headers[Authorization] = %v, want [Bearer old-token]", retrieved.Request.Headers["Authorization"])
	}
}

func TestStore_ReplayWithEditedHeaders(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	original := &Entry{
		Request: RequestRecord{
			Method:        "GET",
			URL:           "http://example.com/api",
			Host:          "example.com",
			Headers:       map[string][]string{"Authorization": {"Bearer old-token"}},
			EditedHeaders: map[string][]string{"Authorization": {"Bearer new-token"}},
		},
		Response:      &ResponseRecord{Status: 200, Body: `{"ok":true}`},
		AppliedAction: "passthrough",
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	replayed, err := store.Replay(original.ID, "")
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if replayed.Request.Headers["Authorization"][0] != "Bearer new-token" {
		t.Errorf("Replay should use EditedHeaders: Headers[Authorization] = %v, want [Bearer new-token]", replayed.Request.Headers["Authorization"])
	}
}

func TestStore_AppliedActionAndRuleName(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method: "GET",
			URL:    "http://example.com/api",
			Host:   "example.com",
		},
		AppliedAction: "mock",
		RuleName:      "mock-api",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify ListEntry has the fields
	summary := store.ListSummary()
	if len(summary) != 1 {
		t.Fatalf("ListSummary() = %d, want 1", len(summary))
	}
	if summary[0].AppliedAction != "mock" {
		t.Errorf("ListEntry.AppliedAction = %q, want %q", summary[0].AppliedAction, "mock")
	}
	if summary[0].RuleName != "mock-api" {
		t.Errorf("ListEntry.RuleName = %q, want %q", summary[0].RuleName, "mock-api")
	}

	// Verify full entry has the fields
	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if retrieved.AppliedAction != "mock" {
		t.Errorf("Entry.AppliedAction = %q, want %q", retrieved.AppliedAction, "mock")
	}
	if retrieved.RuleName != "mock-api" {
		t.Errorf("Entry.RuleName = %q, want %q", retrieved.RuleName, "mock-api")
	}
}

func TestStore_ServerRequestAndServerResponse(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	entry := &Entry{
		Request: RequestRecord{
			Method:  "POST",
			URL:     "http://api.example.com/data",
			Host:    "api.example.com",
			Headers: map[string][]string{"Authorization": {"Bearer original"}},
			Body:    `{"original":true}`,
		},
		ServerRequest: &RequestRecord{
			Method:  "POST",
			URL:     "http://api.example.com/data",
			Host:    "api.example.com",
			Headers: map[string][]string{"Authorization": {"Bearer modified"}},
			Body:    `{"modified":true}`,
		},
		ServerResponse: &ResponseRecord{
			Status:  200,
			Body:    `{"server":"response"}`,
			Headers: map[string][]string{"X-Real": {"true"}},
		},
		Response: &ResponseRecord{
			Status: 503,
			Body:   `{"mocked":true}`,
		},
		AppliedAction: "response_mock",
		RuleName:      "resp-mock",
	}
	if err := store.Save(entry); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	retrieved, err := store.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Verify original request preserved
	if retrieved.Request.Headers["Authorization"][0] != "Bearer original" {
		t.Errorf("Request.Headers = %v, want [Bearer original]", retrieved.Request.Headers["Authorization"])
	}

	// Verify ServerRequest
	if retrieved.ServerRequest == nil {
		t.Fatal("ServerRequest is nil, want non-nil")
	}
	if retrieved.ServerRequest.Headers["Authorization"][0] != "Bearer modified" {
		t.Errorf("ServerRequest.Headers = %v, want [Bearer modified]", retrieved.ServerRequest.Headers["Authorization"])
	}
	if retrieved.ServerRequest.Body != `{"modified":true}` {
		t.Errorf("ServerRequest.Body = %q, want %q", retrieved.ServerRequest.Body, `{"modified":true}`)
	}

	// Verify ServerResponse
	if retrieved.ServerResponse == nil {
		t.Fatal("ServerResponse is nil, want non-nil")
	}
	if retrieved.ServerResponse.Status != 200 {
		t.Errorf("ServerResponse.Status = %d, want 200", retrieved.ServerResponse.Status)
	}
	if retrieved.ServerResponse.Body != `{"server":"response"}` {
		t.Errorf("ServerResponse.Body = %q, want %q", retrieved.ServerResponse.Body, `{"server":"response"}`)
	}

	// Verify Response (what browser sees)
	if retrieved.Response == nil {
		t.Fatal("Response is nil, want non-nil")
	}
	if retrieved.Response.Status != 503 {
		t.Errorf("Response.Status = %d, want 503", retrieved.Response.Status)
	}
}

func TestStore_AppliedActionPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	actions := []struct {
		action string
		rule   string
	}{
		{"passthrough", ""},
		{"mock", "mock-api"},
		{"drop", "block-telemetry"},
		{"modify", "inject-headers"},
		{"response_mock", "resp-mock"},
	}

	for _, a := range actions {
		entry := &Entry{
			Request: RequestRecord{
				Method: "GET",
				URL:    "http://example.com/api",
				Host:   "example.com",
			},
			AppliedAction: a.action,
			RuleName:      a.rule,
		}
		if err := store.Save(entry); err != nil {
			t.Fatalf("Save(%s) error = %v", a.action, err)
		}
	}

	entries := store.ListSummary()
	if len(entries) != len(actions) {
		t.Fatalf("ListSummary() = %d, want %d", len(entries), len(actions))
	}

	// Verify persistence across re-open
	store2, err := New(dir)
	if err != nil {
		t.Fatalf("New() for re-open error = %v", err)
	}

	reloaded := store2.ListSummary()
	if len(reloaded) != len(actions) {
		t.Fatalf("ListSummary() after re-open = %d, want %d", len(reloaded), len(actions))
	}
}
