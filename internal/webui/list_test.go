package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if err := s.hist().Save(entry); err != nil {
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

func TestListRequests_Pagination(t *testing.T) {
	s, _, _ := newTestServer(t)
	// 15 entries with explicit timestamps so the newest-first index order is deterministic.
	for i := 0; i < 15; i++ {
		entry := &history.Entry{
			Request: history.RequestRecord{
				Method:  "GET",
				URL:     fmt.Sprintf("http://host%d.example.com/path", i),
				Host:    fmt.Sprintf("host%d.example.com", i),
				Headers: map[string][]string{},
			},
			Timestamp: time.Unix(0, int64(i+1)), // i=14 is the newest
		}
		if err := s.hist().Save(entry); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	page0 := getListResponse(t, s, "/api/requests?offset=0&limit=5")
	if len(page0.Entries) != 5 {
		t.Fatalf("page 0 len = %d, want 5", len(page0.Entries))
	}
	if page0.VisibleCount != 15 {
		t.Errorf("visibleCount = %d, want 15", page0.VisibleCount)
	}
	if page0.Offset != 0 {
		t.Errorf("offset = %d, want 0", page0.Offset)
	}
	if page0.Entries[0].Host != "host14.example.com" {
		t.Errorf("page 0 must start with newest entry, got %s", page0.Entries[0].Host)
	}

	page1 := getListResponse(t, s, "/api/requests?offset=5&limit=5")
	if len(page1.Entries) != 5 || page1.Entries[0].Host != "host9.example.com" {
		t.Fatalf("page 1 = %+v, want host9..host5", page1.Entries)
	}
	if page1.Offset != 5 || page1.VisibleCount != 15 {
		t.Errorf("page 1 offset=%d visibleCount=%d, want 5/15", page1.Offset, page1.VisibleCount)
	}

	page2 := getListResponse(t, s, "/api/requests?offset=10&limit=5")
	if len(page2.Entries) != 5 || page2.Entries[4].Host != "host0.example.com" {
		t.Fatalf("page 2 = %+v, want host4..host0", page2.Entries)
	}

	// Past the end: empty page but the metadata still present in the wire.
	page3 := getListResponse(t, s, "/api/requests?offset=15&limit=5")
	if len(page3.Entries) != 0 {
		t.Fatalf("page past end len = %d, want 0", len(page3.Entries))
	}
	if page3.VisibleCount != 15 || page3.Offset != 15 {
		t.Errorf("page past end offset=%d visibleCount=%d, want 15/15", page3.Offset, page3.VisibleCount)
	}

	// Raw wire: visibleCount and offset always present, never omitted.
	req := httptest.NewRequest("GET", "/api/requests?offset=3&limit=2", nil)
	w := httptest.NewRecorder()
	s.handleListRequests(w, req)
	raw := w.Body.String()
	for _, want := range []string{`"visibleCount":15`, `"offset":3`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("full response must carry %s, got %s", want, raw)
		}
	}
}

func TestPageLimits(t *testing.T) {
	cases := []struct {
		offset, limit, wantO, wantL int
	}{
		{0, 0, 0, defaultPageSize},
		{0, -1, 0, defaultPageSize},
		{-5, 10, 0, 10},
		{3, 5000, 3, maxPageSize},
		{0, defaultPageSize, 0, defaultPageSize},
		{0, maxPageSize + 1, 0, maxPageSize},
	}
	for _, c := range cases {
		o, l := pageLimits(c.offset, c.limit)
		if o != c.wantO || l != c.wantL {
			t.Errorf("pageLimits(%d, %d) = (%d, %d), want (%d, %d)", c.offset, c.limit, o, l, c.wantO, c.wantL)
		}
	}
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

// TestListScopeRemoved locks the replay scope removal: the ids list-scope
// parameter no longer exists end-to-end - /api/requests ignores it and the
// frontend dropped the Show in list machinery in favor of the Match tab.
func TestListScopeRemoved(t *testing.T) {
	s, _, _ := newTestServer(t)
	a := saveTestEntry(t, s, "a.example.com", "GET")
	b := saveTestEntry(t, s, "b.example.com", "GET")
	saveTestEntry(t, s, "c.example.com", "GET")

	resp := getListResponse(t, s, "/api/requests?ids="+a.ID+","+b.ID)
	if len(resp.Entries) != 3 {
		t.Fatalf("the ids list-scope parameter must be ignored; entries = %d, want 3", len(resp.Entries))
	}

	if strings.Contains(apiJS, "listScope") {
		t.Fatal("api.js: the ids list-scope parameter is removed from loadRequests")
	}
	if strings.Contains(appJS, "setListScope") {
		t.Fatal("app.js: the list scope handler is removed")
	}
	if strings.Contains(renderJS, "renderListScopeBanner") {
		t.Fatal("render.js: the list scope banner is removed")
	}
	if strings.Contains(indexHTML, "listScopeBanner") {
		t.Fatal("index.html: the list scope banner element is removed")
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
	if err := s.hist().Update(e); err != nil {
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
	if err := s.hist().Update(e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	diff2 := getListResponse(t, s, "/api/requests?since="+time.Now().Add(-time.Hour).Format(time.RFC3339Nano)+"&version="+strconv.Itoa(baseVersion))
	if len(diff2.Upserts) != 0 {
		t.Fatalf("expected no upserts, got %+v", diff2.Upserts)
	}
	if len(diff2.Removed) != 1 || diff2.Removed[0] != e.ID {
		t.Fatalf("expected removed %s, got %+v", e.ID, diff2.Removed)
	}
	if diff2.VisibleCount != 0 {
		t.Errorf("visibleCount after removal = %d, want 0 (entry left the visible set)", diff2.VisibleCount)
	}
}

func TestListRequests_DiffCarriesVisibleCount(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")
	saveTestEntry(t, s, "other.com", "POST")

	baseVersion := getListResponse(t, s, "/api/requests").Version

	req := httptest.NewRequest("GET",
		"/api/requests?since="+time.Now().Add(-time.Hour).Format(time.RFC3339Nano)+
			"&version="+strconv.Itoa(baseVersion), nil)
	w := httptest.NewRecorder()
	s.handleListRequests(w, req)
	raw := w.Body.String()
	for _, want := range []string{`"visibleCount":2`, `"upserts":`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("diff response must carry %s, got %s", want, raw)
		}
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
	if err := s.hist().Update(e); err != nil {
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
	if err := s.hist().Update(e); err != nil {
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
		if err := s.hist().Save(e); err != nil {
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

func TestListRequests_AgentViewSwitchesActiveProfile(t *testing.T) {
	s, _, _ := newTestServer(t)
	agent := &history.Entry{
		Request: history.RequestRecord{
			Method:  "GET",
			URL:     "http://api.example.com/path",
			Host:    "api.example.com",
			Headers: map[string][]string{},
		},
		Origin: "agent",
	}
	if err := s.hist().Save(agent); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saveTestEntry(t, s, "other.com", "POST")

	// Normal profile: host filter active.
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
	if resp.AgentPreview {
		t.Fatal("agentPreview must be false while off")
	}
	if resp.AgentEnabled {
		t.Fatal("agentEnabled gate must be false while off")
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ID != agent.ID {
		t.Fatalf("expected the host-filtered api.example.com entry, got %+v", resp.Entries)
	}

	// Enable agent preview: response is the agent profile (empty criteria) - full list.
	req2 := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":true}`))
	w2 := httptest.NewRecorder()
	s.handleAgentView(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200", w2.Code)
	}
	// Raw wire check: all agent fields always present (no omitempty).
	raw := w2.Body.String()
	for _, want := range []string{`"agentPreview":true`, `"agentEnabled":false`, `"agentExposed":false`, `"visibleCount":2`, `"offset":0`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("toggle response must carry %s, got %s", want, raw)
		}
	}
	var resp2 listResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp2.AgentPreview {
		t.Fatal("agentPreview must be true after toggle")
	}
	if resp2.VisibleCount != 2 || resp2.Offset != 0 {
		t.Fatalf("toggle must serve page 0, got visibleCount=%d offset=%d", resp2.VisibleCount, resp2.Offset)
	}
	if len(resp2.Entries) != 2 {
		t.Fatalf("expected all 2 entries in empty-criteria agent profile, got %d", len(resp2.Entries))
	}

	// Now filters are written to the AGENT profile.
	body3 := `{"filters":{"origin":["agent"]},"focusEnabled":false}`
	req3 := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body3))
	w3 := httptest.NewRecorder()
	s.handleSaveFilters(w3, req3)
	var resp3 listResponse
	if err := json.NewDecoder(w3.Body).Decode(&resp3); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp3.Entries) != 1 || resp3.Entries[0].ID != agent.ID {
		t.Fatalf("expected only the agent-origin entry, got %+v", resp3.Entries)
	}

	// Toggling back restores the normal profile (host filter) untouched.
	req4 := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":false}`))
	w4 := httptest.NewRecorder()
	s.handleAgentView(w4, req4)
	var resp4 listResponse
	if err := json.NewDecoder(w4.Body).Decode(&resp4); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp4.AgentPreview {
		t.Fatal("agentPreview must be false after toggle off")
	}
	if len(resp4.Entries) != 1 || resp4.Entries[0].ID != agent.ID {
		t.Fatalf("expected normal profile host filter restored (1 entry), got %+v", resp4.Entries)
	}
}

func TestListRequests_AgentViewFiltersAgentOrigin(t *testing.T) {
	s, _, _ := newTestServer(t)
	agent := &history.Entry{
		Request: history.RequestRecord{
			Method:  "GET",
			URL:     "http://api.example.com/agent",
			Host:    "api.example.com",
			Headers: map[string][]string{},
		},
		Origin: "agent",
	}
	if err := s.hist().Save(agent); err != nil {
		t.Fatalf("Save agent: %v", err)
	}
	browser := &history.Entry{
		Request: history.RequestRecord{
			Method:  "GET",
			URL:     "http://api.example.com/browser",
			Host:    "api.example.com",
			Headers: map[string][]string{},
		},
	}
	if err := s.hist().Save(browser); err != nil {
		t.Fatalf("Save browser: %v", err)
	}

	// Agent view ON with a host filter that excludes the entries: the agent's
	// own traffic is bounded by the same filter scope as browser traffic
	// (github/sonarqube scenario) - there is no origin bypass.
	req := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":true}`))
	w := httptest.NewRecorder()
	s.handleAgentView(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200", w.Code)
	}

	body := `{"filters":{"host":["other.com"]},"focusEnabled":false}`
	req2 := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w2 := httptest.NewRecorder()
	s.handleSaveFilters(w2, req2)
	var resp listResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Fatalf("expected the agent-origin entry filtered out, got %+v", resp.Entries)
	}
	if resp.VisibleCount != 0 {
		t.Errorf("visibleCount = %d, want 0", resp.VisibleCount)
	}

	// A filter matching the entries' host makes them visible again.
	bodyMatching := `{"filters":{"host":["api.example.com"]},"focusEnabled":false}`
	reqMatching := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(bodyMatching))
	wMatching := httptest.NewRecorder()
	s.handleSaveFilters(wMatching, reqMatching)
	var respMatching listResponse
	if err := json.NewDecoder(wMatching.Body).Decode(&respMatching); err != nil {
		t.Fatalf("decode matching: %v", err)
	}
	if len(respMatching.Entries) != 2 || respMatching.VisibleCount != 2 {
		t.Fatalf("expected both entries back, got %d entries / %d visible", len(respMatching.Entries), respMatching.VisibleCount)
	}

	// The focus gate applies to agent-origin entries exactly like any other.
	s.focusStore.(*mockFocusChecker).Add("other.com")
	bodyFocus := `{"filters":{"host":["api.example.com"]},"focusEnabled":true}`
	reqFocus := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(bodyFocus))
	wFocus := httptest.NewRecorder()
	s.handleSaveFilters(wFocus, reqFocus)
	var respFocus listResponse
	if err := json.NewDecoder(wFocus.Body).Decode(&respFocus); err != nil {
		t.Fatalf("decode focus: %v", err)
	}
	if len(respFocus.Entries) != 0 {
		t.Fatalf("expected agent-origin entries hidden by focus, got %+v", respFocus.Entries)
	}

	// Preview OFF restores the normal profile (empty criteria).
	req4 := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":false}`))
	w4 := httptest.NewRecorder()
	s.handleAgentView(w4, req4)
	var resp3 listResponse
	if err := json.NewDecoder(w4.Body).Decode(&resp3); err != nil {
		t.Fatalf("decode off: %v", err)
	}
	if len(resp3.Entries) != 2 {
		t.Fatalf("expected both entries back in the normal profile, got %+v", resp3.Entries)
	}
}

func TestAgentView_ToggleClearsActiveProfileBody(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")

	// Body IDs committed to the normal profile (active).
	s.filterStore.SetBodyIDs([]string{"x1"}, 1)
	f, _, _ := s.filterStore.Snapshot()
	if len(f.Body) != 1 {
		t.Fatalf("setup: expected 1 body ID in normal, got %+v", f.Body)
	}

	// Toggle ON: clears the OLD active profile's body and switches.
	req := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":true}`))
	w := httptest.NewRecorder()
	s.handleAgentView(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	af, _, _ := s.filterStore.SnapshotAgent()
	if !s.filterStore.AgentPreview() {
		t.Fatal("agent preview must be on")
	}
	if len(af.Body) != 0 {
		t.Fatalf("agent profile must start with empty body, got %+v", af.Body)
	}

	// Toggle OFF: clears the (now active) agent profile's body.
	s.filterStore.SetBodyIDs([]string{"a1"}, 2)
	req2 := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":false}`))
	w2 := httptest.NewRecorder()
	s.handleAgentView(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w2.Code)
	}
	f2, _, _ := s.filterStore.Snapshot()
	if len(f2.Body) != 0 {
		t.Fatalf("normal profile body must be empty after toggle, got %+v", f2.Body)
	}
	af2, _, _ := s.filterStore.SnapshotAgent()
	if len(af2.Body) != 0 {
		t.Fatalf("agent body must be empty after toggle OFF, got %+v", af2.Body)
	}
}

func TestListRequests_AgentEnabledGate(t *testing.T) {
	s, _, _ := newTestServer(t)
	saveTestEntry(t, s, "api.example.com", "GET")

	// Full list with the gate off carries the three agent fields.
	req := httptest.NewRequest("GET", "/api/requests", nil)
	w := httptest.NewRecorder()
	s.handleListRequests(w, req)
	raw := w.Body.String()
	for _, want := range []string{`"agentPreview":false`, `"agentEnabled":false`, `"agentExposed":false`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("list must carry %s, got %s", want, raw)
		}
	}

	// Enabling the gate with an empty agent profile exposes everything.
	req2 := httptest.NewRequest("PUT", "/api/agent/enabled", strings.NewReader(`{"enabled":true}`))
	w2 := httptest.NewRecorder()
	s.handleAgentEnabled(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w2.Code)
	}
	var gateResp map[string]bool
	if err := json.NewDecoder(w2.Body).Decode(&gateResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gateResp["enabled"] || !gateResp["exposed"] {
		t.Fatalf("expected enabled+exposed true, got %+v", gateResp)
	}

	// A filter written to the AGENT profile removes the exposure.
	req2b := httptest.NewRequest("PUT", "/api/agent/view", strings.NewReader(`{"preview":true}`))
	w2b := httptest.NewRecorder()
	s.handleAgentView(w2b, req2b)
	if w2b.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200", w2b.Code)
	}
	body := `{"filters":{"host":["api.example.com"]},"focusEnabled":false}`
	req3 := httptest.NewRequest("PUT", "/api/filters", strings.NewReader(body))
	w3 := httptest.NewRecorder()
	s.handleSaveFilters(w3, req3)
	raw3 := w3.Body.String()
	if !strings.Contains(raw3, `"agentPreview":true`) || !strings.Contains(raw3, `"agentEnabled":true`) || !strings.Contains(raw3, `"agentExposed":false`) {
		t.Fatalf("filtered agent profile must keep gate on but exposure off, got %s", raw3)
	}

	// Disabling the gate clears the exposure.
	req4 := httptest.NewRequest("PUT", "/api/agent/enabled", strings.NewReader(`{"enabled":false}`))
	w4 := httptest.NewRecorder()
	s.handleAgentEnabled(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w4.Code)
	}
	if err := json.NewDecoder(w4.Body).Decode(&gateResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gateResp["enabled"] || gateResp["exposed"] {
		t.Fatalf("expected enabled+exposed false, got %+v", gateResp)
	}
}

func TestRecordingStoppedPayload(t *testing.T) {
	s, _, hist := newTestServer(t)
	saveEntry(t, hist, "a", "GET", "http://example.com/a")

	resp := getListResponse(t, s, "/api/requests")
	if resp.RecordingStopped {
		t.Fatal("recordingStopped must be false initially")
	}
	if resp.RecordingMax != "" {
		t.Fatalf("recordingMax = %q, want empty", resp.RecordingMax)
	}

	s.SetRecordingStopped("60s")
	resp = getListResponse(t, s, "/api/requests")
	if !resp.RecordingStopped {
		t.Fatal("recordingStopped must be true after SetRecordingStopped")
	}
	if resp.RecordingMax != "60s" {
		t.Fatalf("recordingMax = %q, want 60s", resp.RecordingMax)
	}
}

func TestRecordingEventsSSE(t *testing.T) {
	s, _, _ := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	s.handleRecordingEvents(rec, httptest.NewRequest(http.MethodGet, "/api/recording/events", nil).WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("recording events SSE: expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"stopped":false`) {
		t.Fatalf("snapshot must report stopped=false initially, got %q", rec.Body.String())
	}

	s.SetRecordingStopped("60s")
	rec = httptest.NewRecorder()
	s.handleRecordingEvents(rec, httptest.NewRequest(http.MethodGet, "/api/recording/events", nil).WithContext(ctx))
	if !strings.Contains(rec.Body.String(), `"stopped":true`) {
		t.Fatalf("snapshot must report stopped=true after the cut, got %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"max":"60s"`) {
		t.Fatalf("snapshot must carry the max label, got %q", rec.Body.String())
	}
}

func TestRecordingHubBroadcast(t *testing.T) {
	hub := newRecordingHub()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	hub.publish(recordingEvent{Stopped: true, Max: "60s"})
	select {
	case ev := <-ch:
		if !ev.Stopped || ev.Max != "60s" {
			t.Fatalf("broadcast = %+v, want stopped=true max=60s", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("broadcast never delivered")
	}
}

func TestRecordingStoppedFrontend(t *testing.T) {
	api := strings.ReplaceAll(apiJS, "\r\n", "\n")
	for _, probe := range []string{
		"onRecordingStoppedUpdate(data.recordingStopped",
		"setOnRecordingStoppedUpdate",
	} {
		if !strings.Contains(api, probe) {
			t.Fatalf("api.js: must %s", probe)
		}
	}
	app := strings.ReplaceAll(appJS, "\r\n", "\n")
	for _, probe := range []string{
		"function syncRecordingStopped(stopped, max)",
		"setOnRecordingStoppedUpdate(syncRecordingStopped)",
		"recordingStoppedBanner",
		"connectRecordingEvents",
		"new EventSource('/api/recording/events')",
	} {
		if !strings.Contains(app, probe) {
			t.Fatalf("app.js: must %s", probe)
		}
	}
	if !strings.Contains(styleCSS, ".recording-stopped-banner") {
		t.Fatal("style.css: must style the recording-stopped banner")
	}
	if !strings.Contains(indexHTML, "recordingStoppedBanner") {
		t.Fatal("index.html: must render the recording-stopped banner")
	}
}
