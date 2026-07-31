package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gospy/internal/history"
)

func saveTestEntry(t *testing.T, s *Server, host, method string) *history.Entry {
	t.Helper()
	entry := &history.Entry{
		Request: history.RequestRecord{
			Method:  method,
			URL:     "http://" + host + "/path",
			Host:    host,
			Headers: map[string][]string{},
		},
	}
	if err := s.history.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return entry
}

func getListResponse(t *testing.T, s *Server, url string) listResponse {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	s.handleListRequests(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp listResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestListRequests_FullNoFilters(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")
	saveTestEntry(t, s, "other.com", "POST")

	resp := getListResponse(t, s, "/api/requests")
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2", resp.Total)
	}
	if resp.Filters == nil {
		t.Fatal("expected filters in full response")
	}
	if resp.Version <= 0 {
		t.Errorf("version = %d, want > 0", resp.Version)
	}
}

func TestListRequests_HostFilter(t *testing.T) {
	s, _, _ := newTestServer(t)
	e1 := saveTestEntry(t, s, "api.example.com", "GET")
	saveTestEntry(t, s, "other.com", "POST")

	// Apply a host filter via PUT /api/filters and assert the response is the filtered list.
	body := `{"filters":{"host":["api.example.com"]},"focusEnabled":false}`
	req := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSaveFilters(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", w.Code)
	}
	var resp listResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ID != e1.ID {
		t.Fatalf("expected only %s, got %+v", e1.ID, resp.Entries)
	}
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2 (non-ignored)", resp.Total)
	}

	// GET now returns the filtered list too.
	resp2 := getListResponse(t, s, "/api/requests")
	if len(resp2.Entries) != 1 {
		t.Fatalf("expected 1 filtered entry, got %d", len(resp2.Entries))
	}
}

func TestListRequests_Focus(t *testing.T) {
	s, _, _ := newTestServer(t)
	eFocused := saveTestEntry(t, s, "api.example.com", "GET")
	saveTestEntry(t, s, "other.com", "POST")

	s.focusStore.(*mockFocusChecker).Add("api.example.com")

	body := `{"filters":{},"focusEnabled":true}`
	req := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSaveFilters(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", w.Code)
	}
	var resp listResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ID != eFocused.ID {
		t.Fatalf("expected only focused %s, got %+v", eFocused.ID, resp.Entries)
	}
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2 (non-ignored)", resp.Total)
	}

	// GET reflects the same focused scope.
	resp2 := getListResponse(t, s, "/api/requests")
	if len(resp2.Entries) != 1 {
		t.Fatalf("expected 1 focused entry, got %d", len(resp2.Entries))
	}
}

func TestListRequests_FocusWithFilters(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")
	saveTestEntry(t, s, "api.example.com", "POST")
	saveTestEntry(t, s, "other.com", "POST")

	s.focusStore.(*mockFocusChecker).Add("api.example.com")

	// Focus on api.example.com + host filter for other.com → filters act on the
	// focused subset, so nothing matches (other.com is not focused).
	body := `{"filters":{"host":["other.com"]},"focusEnabled":true}`
	req := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSaveFilters(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", w.Code)
	}
	var resp listResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Fatalf("expected 0 entries (filter outside focused subset), got %+v", resp.Entries)
	}

	// Focus + host filter within the focused set → the two api.example.com entries.
	body2 := `{"filters":{"host":["api.example.com"]},"focusEnabled":true}`
	req2 := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body2))
	w2 := httptest.NewRecorder()
	s.handleSaveFilters(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", w2.Code)
	}
	var resp2 listResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp2.Entries) != 2 {
		t.Fatalf("expected 2 focused entries matching filter, got %+v", resp2.Entries)
	}
}

func TestListRequests_DiffUpsert(t *testing.T) {
	s, _, _ := newTestServer(t)
	e1 := saveTestEntry(t, s, "api.example.com", "GET")

	resp := getListResponse(t, s, "/api/requests")
	baseVersion := resp.Version
	since := e1.Timestamp.Format(time.RFC3339Nano)

	// A new entry arrives after the full load → the next diff returns it as upsert.
	e2 := saveTestEntry(t, s, "other.com", "POST")
	diff := getListResponse(t, s, "/api/requests?since="+since+"&version="+strconv.Itoa(baseVersion))
	if len(diff.Upserts) != 1 || diff.Upserts[0].ID != e2.ID {
		t.Fatalf("expected 1 upsert (%s), got %+v", e2.ID, diff.Upserts)
	}
	if len(diff.Removed) != 0 {
		t.Fatalf("expected no removed, got %+v", diff.Removed)
	}
	if diff.Filters != nil {
		t.Fatal("diff response must not carry filters")
	}
}

func TestListRequests_DiffVersionMismatchReturnsFull(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")

	// Front sends a stale version → server falls back to a full response.
	resp := getListResponse(t, s, "/api/requests?since="+time.Now().Add(-time.Hour).Format(time.RFC3339Nano)+"&version=999")
	if len(resp.Entries) == 0 || resp.Filters == nil {
		t.Fatal("expected full response on version mismatch")
	}
}

func TestListRequests_DiffRemovedOnContentTypeChange(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Filter: responseContentType = text/html.
	body := `{"filters":{"responseContentType":["text/html"]},"focusEnabled":false}`
	req := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSaveFilters(w, req)

	// Pending entry (no response) is not visible under a CT filter.
	e := saveTestEntry(t, s, "api.example.com", "GET")
	baseVersion := getListResponse(t, s, "/api/requests").Version

	// Response arrives with text/html → becomes visible (upsert).
	e.Response = &history.ResponseRecord{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"text/html; charset=utf-8"}},
	}
	if err := s.history.Update(e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	diff1 := getListResponse(t, s, "/api/requests?since="+time.Now().Add(-time.Hour).Format(time.RFC3339Nano)+"&version="+strconv.Itoa(baseVersion))
	if len(diff1.Upserts) != 1 || diff1.Upserts[0].ID != e.ID {
		t.Fatalf("expected upsert %s, got %+v", e.ID, diff1.Upserts)
	}

	// Response changes to application/json → entry leaves visibility (removed).
	e.Response = &history.ResponseRecord{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
	}
	if err := s.history.Update(e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	diff2 := getListResponse(t, s, "/api/requests?since="+time.Now().Add(-time.Hour).Format(time.RFC3339Nano)+"&version="+strconv.Itoa(baseVersion))
	if len(diff2.Upserts) != 0 {
		t.Fatalf("expected no upserts, got %+v", diff2.Upserts)
	}
	if len(diff2.Removed) != 1 || diff2.Removed[0] != e.ID {
		t.Fatalf("expected removed %s, got %+v", e.ID, diff2.Removed)
	}
}

func TestListRequests_EmptyResultIncludesEntries(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")

	// A filter matching nothing → the full response must carry "entries":[]
	// on the wire: the frontend's `if (data.entries)` truthiness check would
	// silently drop an absent key and leave the previous list rendered.
	body := `{"filters":{"host":["nope.com"]},"focusEnabled":false}`
	req := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSaveFilters(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Errorf("PUT body %s: want an explicit \"entries\":[] so JS truthiness applies it", w.Body.String())
	}

	// Same contract on a plain GET full load.
	w2 := httptest.NewRecorder()
	s.handleListRequests(w2, httptest.NewRequest("GET", "/api/requests", nil))
	if !strings.Contains(w2.Body.String(), `"entries":[]`) {
		t.Errorf("GET body %s: want an explicit \"entries\":[]", w2.Body.String())
	}
}

func TestListRequests_DiffUpsertsAlwaysPresent(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Filter: responseContentType = text/html.
	body := `{"filters":{"responseContentType":["text/html"]},"focusEnabled":false}`
	req := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSaveFilters(w, req)

	e := saveTestEntry(t, s, "api.example.com", "GET")
	baseVersion := getListResponse(t, s, "/api/requests").Version

	// Response arrives text/html → visible.
	e.Response = &history.ResponseRecord{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"text/html"}},
	}
	if err := s.history.Update(e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	getListResponse(t, s, "/api/requests?since=2000-01-01T00:00:00Z&version="+strconv.Itoa(baseVersion))

	// Response changes to application/json → entry leaves visibility. The diff
	// has zero upserts but one removal; the wire must still carry "upserts":[]
	// so the frontend's `if (data.upserts)` enters the diff branch and applies
	// the removal.
	e.Response = &history.ResponseRecord{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
	}
	if err := s.history.Update(e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	w2 := httptest.NewRecorder()
	s.handleListRequests(w2, httptest.NewRequest("GET",
		"/api/requests?since=2000-01-01T00:00:00Z&version="+strconv.Itoa(baseVersion), nil))
	raw := w2.Body.String()
	if !strings.Contains(raw, `"upserts":[]`) {
		t.Errorf("diff body %s: want an explicit \"upserts\":[] on the wire", raw)
	}
	if !strings.Contains(raw, `"removed":["`+e.ID+`"]`) {
		t.Errorf("diff body %s: want removed entry %s", raw, e.ID)
	}
}

func TestFilterOptions(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")
	saveTestEntry(t, s, "other.com", "POST")

	req := httptest.NewRequest("GET", "/api/filters/options?type=host", nil)
	w := httptest.NewRecorder()
	s.handleFilterOptions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Values []history.OptionCount `json:"values"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Values) != 2 {
		t.Fatalf("expected 2 host options, got %+v", resp.Values)
	}
	if resp.Values[0].Value != "api.example.com" || resp.Values[0].Count != 1 {
		t.Errorf("unexpected first option: %+v", resp.Values[0])
	}
}

func TestFilterOptions_EmptyResultWire(t *testing.T) {
	s, _, _ := newTestServer(t)

	// A valid type with no captured entries → the wire must carry "values":[]
	// (never null, never absent) so list-style truthiness consumers stay correct.
	req := httptest.NewRequest("GET", "/api/filters/options?type=host", nil)
	w := httptest.NewRecorder()
	s.handleFilterOptions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"values":[]`) {
		t.Errorf("options body %s: want an explicit \"values\":[] on the wire", w.Body.String())
	}
}

func TestSearchCommitsBodyIDs(t *testing.T) {
	s, _, _ := newTestServer(t)

	mk := func(host, reqBody, respBody string) *history.Entry {
		e := &history.Entry{
			Request: history.RequestRecord{
				Method:  "GET",
				URL:     "http://" + host + "/path",
				Host:    host,
				Headers: map[string][]string{},
				Body:    reqBody,
			},
			Response: &history.ResponseRecord{
				Status:  200,
				Headers: map[string][]string{},
				Body:    respBody,
			},
		}
		if err := s.history.Save(e); err != nil {
			t.Fatalf("Save: %v", err)
		}
		return e
	}

	eMatchReq := mk("a.com", "hello needle world", "nope")
	eMatchResp := mk("b.com", "nope", "the needle is here")
	mk("c.com", "nothing", "nothing")

	req := httptest.NewRequest("POST", "/api/requests/search", bytes.NewBufferString(`{"q":"needle"}`))
	w := httptest.NewRecorder()
	s.handleSearch(w, req)

	var doneLine bool
	var matchCount int
	for _, line := range strings.Split(w.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("bad ndjson line %q: %v", line, err)
		}
		if _, ok := msg["scanned"]; ok {
			continue
		}
		if _, ok := msg["done"].(bool); ok {
			doneLine = true
			matchCount = int(msg["matchCount"].(float64))
		}
	}
	if !doneLine {
		t.Fatal("missing done line")
	}
	if matchCount != 2 {
		t.Fatalf("matchCount = %d, want 2", matchCount)
	}

	// The matched IDs must be committed server-side in the criteria body.
	f, _, _ := s.filterStore.Snapshot()
	if len(f.Body) != 2 {
		t.Fatalf("expected 2 committed body IDs, got %+v", f.Body)
	}
	want := map[string]bool{eMatchReq.ID: true, eMatchResp.ID: true}
	for _, id := range f.Body {
		if !want[id] {
			t.Errorf("unexpected committed id %s", id)
		}
	}

	// The body filter now drives the list.
	resp := getListResponse(t, s, "/api/requests")
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries in list via body filter, got %d", len(resp.Entries))
	}
}

func TestClearBodyFilter(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.filterStore.SetBodyIDs([]string{"e1"}, 1)

	req := httptest.NewRequest("DELETE", "/api/filters/body", nil)
	w := httptest.NewRecorder()
	s.handleClearBodyFilter(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	f, _, _ := s.filterStore.Snapshot()
	if len(f.Body) != 0 {
		t.Fatalf("expected body cleared, got %+v", f.Body)
	}
}
