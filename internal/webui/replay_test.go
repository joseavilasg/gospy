package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gospy/internal/history"
	"gospy/internal/session"
)

func newReplayServer(t *testing.T) (*Server, *history.Store, string) {
	t.Helper()
	s, _, hist := newTestServer(t)
	logRoot := t.TempDir()
	s.SetReplayMode(true)
	s.SetReplayLogDir(logRoot)
	return s, hist, logRoot
}

func saveEntry(t *testing.T, h *history.Store, id, method, url string) {
	t.Helper()
	entry := &history.Entry{
		ID:        id,
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: method, URL: url, Host: strings.SplitN(strings.TrimPrefix(url, "https://"), "/", 2)[0]},
		Response:  &history.ResponseRecord{Status: 200},
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestReplaySessionStartConflict(t *testing.T) {
	s, _, _ := newReplayServer(t)

	rec := httptest.NewRecorder()
	s.handleSessionStart(rec, httptest.NewRequest(http.MethodPost, "/api/session/start", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "replay mode") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleSessionStart(rec, httptest.NewRequest(http.MethodGet, "/api/session/start", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", rec.Code)
	}
}

func assertStatus(t *testing.T, name string, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("%s: expected %d, got %d", name, rec.Code, want)
	}
}

func TestReplayModeReadOnlyGuards(t *testing.T) {
	s, hist, _ := newReplayServer(t)
	saveEntry(t, hist, "e1", "GET", "https://live.example.com/a")

	cases := []struct {
		name string
		req  *http.Request
		h    http.HandlerFunc
	}{
		{"agent view", httptest.NewRequest(http.MethodPut, "/api/agent/view", nil), s.replayReadOnly(s.handleAgentView)},
		{"agent enabled", httptest.NewRequest(http.MethodPut, "/api/agent/enabled", nil), s.replayReadOnly(s.handleAgentEnabled)},
		{"ignored", httptest.NewRequest(http.MethodPut, "/api/ignored", nil), s.replayReadOnly(s.handleIgnored)},
		{"focused", httptest.NewRequest(http.MethodPut, "/api/focused", nil), s.replayReadOnly(s.handleFocused)},
		{"rules", httptest.NewRequest(http.MethodPut, "/api/rules", nil), s.replayReadOnly(s.handleRules)},
		{"request-rule", httptest.NewRequest(http.MethodPost, "/api/request-rule", nil), s.replayReadOnly(s.handleRequestRule)},
		{"process events", httptest.NewRequest(http.MethodGet, "/api/process/events", nil), s.replayReadOnly(s.handleProcessEvents)},
		{"save body", httptest.NewRequest(http.MethodPut, "/api/requests/e1/body", nil), s.handleGetRequest},
		{"revert body", httptest.NewRequest(http.MethodDelete, "/api/requests/e1/body", nil), s.handleGetRequest},
		{"save headers", httptest.NewRequest(http.MethodPut, "/api/requests/e1/headers", nil), s.handleGetRequest},
		{"revert headers", httptest.NewRequest(http.MethodDelete, "/api/requests/e1/headers", nil), s.handleGetRequest},
		{"replay entry", httptest.NewRequest(http.MethodPost, "/api/requests/e1/replay", nil), s.handleGetRequest},
		{"save multipart", httptest.NewRequest(http.MethodPut, "/api/requests/e1/body-multipart", nil), s.handleGetRequest},
		{"streams", httptest.NewRequest(http.MethodGet, "/api/streams/e1/events", nil), s.handleStreamEvents},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.h(rec, c.req)
		assertStatus(t, c.name, rec, http.StatusNotFound)
	}

	rec := httptest.NewRecorder()
	s.handleGetRequest(rec, httptest.NewRequest(http.MethodGet, "/api/requests/e1", nil))
	assertStatus(t, "detail GET in replay", rec, http.StatusOK)

	rec = httptest.NewRecorder()
	s.handleGetRequest(rec, httptest.NewRequest(http.MethodGet, "/api/requests/e1/body-bin", nil))
	assertStatus(t, "body-bin GET in replay (no body -> 400, guard must not 404)", rec, http.StatusBadRequest)
}

// TestReplayFilterSaveAllowed locks the replay filter contract: criteria saves
// (PUT /api/filters, DELETE /api/filters/body) are UI state over the session
// store, so they must work in replay mode for the search box to filter the
// read-only list. Session data stays untouched.
func TestReplayFilterSaveAllowed(t *testing.T) {
	s, hist, _ := newReplayServer(t)
	saveEntry(t, hist, "e1", "GET", "https://live.example.com/segment_a.ts")
	saveEntry(t, hist, "e2", "GET", "https://live.example.com/media_b.m3u8")

	rec := httptest.NewRecorder()
	s.handleSaveFilters(rec, httptest.NewRequest(http.MethodPut, "/api/filters",
		strings.NewReader(`{"filters":{"text":"segment_a"},"focusEnabled":false}`)))
	assertStatus(t, "save filters in replay", rec, http.StatusOK)

	var list listResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list.Replay == nil {
		t.Fatalf("expected replay field in save response, got nil")
	}
	if len(list.Entries) != 1 || list.Entries[0].ID != "e1" {
		t.Fatalf("expected only e1 after text filter, got %+v", list.Entries)
	}

	rec = httptest.NewRecorder()
	s.handleClearBodyFilter(rec, httptest.NewRequest(http.MethodDelete, "/api/filters/body", nil))
	assertStatus(t, "clear body filter in replay", rec, http.StatusNoContent)
}

// TestReplayPanelVisibility locks the panel open/close mechanism: visibility is
// class-driven (.side-panel.open), so #replayPanel must NOT carry an inline
// style - an inline display:none overrides the class rule, leaving the panel
// invisible when the REPLAY chip toggles 'open' (regression: the panel was
// fully wired but clicking the chip did nothing).
func TestReplayPanelVisibility(t *testing.T) {
	const marker = `id="replayPanel"`
	i := strings.Index(indexHTML, marker)
	if i < 0 {
		t.Fatalf("index.html: #replayPanel not found")
	}
	start := strings.LastIndex(indexHTML[:i], "<div")
	end := strings.Index(indexHTML[i:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("index.html: cannot delimit the replayPanel div tag")
	}
	tag := indexHTML[start : i+end]
	if strings.Contains(tag, "style=") {
		t.Fatalf("index.html: #replayPanel carries an inline style %q - inline display:none beats .side-panel.open, so the panel stays hidden on chip click; visibility must be class-driven", tag)
	}
}

// TestReplayOpenRendersSelectedRun locks the chip open handler's fallback
// chain. The auto-load of the first run only runs on the very first open (when
// the run select is still empty); on reopen the select already holds a run but
// _pickedRun/_activeRunId are both null (the replay server has not served
// requests in this process), so renderFeedFor must fall back to the selected
// value - otherwise the feed resets to "No replay activity yet" on every reopen
// (regression: data showed on first open, then vanished on close/reopen).
func TestReplayOpenRendersSelectedRun(t *testing.T) {
	if !strings.Contains(appJS, "_activeRunId || sel.value") {
		t.Fatal("app.js: chip open handler lost the selected-run fallback - renderFeedFor(_pickedRun === null ? (_activeRunId || sel.value) : _pickedRun) is required so reopening the replay panel re-renders the chosen run")
	}
}

func TestReplayFieldInListResponse(t *testing.T) {
	s, _, _ := newTestServer(t)
	full := s.fullList(0, 10)
	if full.Replay != nil {
		t.Fatal("expected nil replay field in live mode")
	}

	s.SetReplayMode(true)
	full = s.fullList(0, 10)
	if full.Replay == nil {
		t.Fatal("expected replay field in replay mode")
	}
	if full.Replay.Active || full.Replay.RunID != "" || full.Replay.Total != 0 {
		t.Fatalf("unexpected empty replay state %+v", full.Replay)
	}
}

func TestReplayListAndDetail(t *testing.T) {
	s, hist, _ := newReplayServer(t)
	saveEntry(t, hist, "e1", "GET", "https://live.example.com/a")

	notify := s.ReplayNotifier()
	notify(session.ReplayEvent{Seq: 1, RunID: "run1", Method: "GET", URL: "https://live.example.com/a", Result: "hit", Status: 200, EntryID: "e1", Consumed: 1, Total: 2})
	notify(session.ReplayEvent{Seq: 2, RunID: "run1", Method: "GET", URL: "https://example.com/miss", Result: "miss", Status: 404, Unconsumed: []session.UnconsumedEntry{{Method: "GET", URL: "https://live.example.com/b"}}, TotalPending: 1, Consumed: 1, Total: 2})

	full := s.fullList(0, 10)
	if full.Replay == nil || !full.Replay.Active || full.Replay.RunID != "run1" {
		t.Fatalf("unexpected replay state %+v", full.Replay)
	}
	if full.Replay.Consumed != 1 || full.Replay.Total != 2 || full.Replay.Exhausted {
		t.Fatalf("unexpected progress %+v", full.Replay)
	}
	if len(full.Replay.Served) != 1 || full.Replay.Served[0] != "e1" {
		t.Fatalf("unexpected served %v", full.Replay.Served)
	}

	rec := httptest.NewRecorder()
	s.handleReplayEventsList(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}
	var listResp struct {
		RunID  string                `json:"runId"`
		Events []session.ReplayEvent `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(listResp.Events))
	}

	rec = httptest.NewRecorder()
	s.handleReplayEventsSub(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d", rec.Code)
	}
	var detail replayDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Event.Result != "hit" || detail.Event.EntryID != "e1" {
		t.Fatalf("unexpected event %+v", detail.Event)
	}
	if detail.MatchedEntry == nil || detail.MatchedEntry.ID != "e1" {
		t.Fatalf("expected matched entry e1, got %+v", detail.MatchedEntry)
	}

	rec = httptest.NewRecorder()
	s.handleReplayEventsSub(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2", nil))
	var miss replayDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &miss); err != nil {
		t.Fatalf("decode miss detail: %v", err)
	}
	if miss.Event.Result != "miss" || miss.MatchedEntry != nil {
		t.Fatalf("expected miss without matched entry, got %+v", miss)
	}
	if len(miss.Event.Unconsumed) != 1 || miss.Event.Unconsumed[0].URL != "https://live.example.com/b" {
		t.Fatalf("unexpected unconsumed %+v", miss.Event.Unconsumed)
	}
}

func TestReplayRunsEndpointFromDisk(t *testing.T) {
	s, _, logRoot := newReplayServer(t)

	runDir, err := session.ReplayRunDir(logRoot, "pastrun")
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	l, err := session.OpenReplayLog(runDir, "pastrun")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}
	if err := l.Append(&session.ReplayEvent{Timestamp: time.Now(), RunID: "pastrun", Method: "GET", URL: "https://live.example.com/a", Result: "hit", Status: 200, EntryID: "e1"}, []byte("{}")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handleReplayRuns(rec, httptest.NewRequest(http.MethodGet, "/api/replay/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("runs: expected 200, got %d", rec.Code)
	}
	var runsResp struct {
		Runs []session.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runsResp.Runs) != 1 || runsResp.Runs[0].RunID != "pastrun" || runsResp.Runs[0].Hits != 1 {
		t.Fatalf("unexpected runs %+v", runsResp.Runs)
	}

	rec = httptest.NewRecorder()
	s.handleReplayEventsList(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events?run=pastrun", nil))
	var past struct {
		Events []session.ReplayEvent `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &past); err != nil {
		t.Fatalf("decode past run: %v", err)
	}
	if len(past.Events) != 1 {
		t.Fatalf("expected 1 event from disk, got %d", len(past.Events))
	}
	if _, err := os.Stat(runDir); err != nil {
		t.Fatalf("bin dir not created: %v", err)
	}
	if _, err := os.Stat(runDir + "/bin/1.req.bin"); err != nil {
		t.Fatalf("body bin not persisted: %v", err)
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}

func TestReplayStreamSnapshotAndLive(t *testing.T) {
	s, hist, _ := newReplayServer(t)
	saveEntry(t, hist, "e1", "GET", "https://live.example.com/a")

	notify := s.ReplayNotifier()
	notify(session.ReplayEvent{Seq: 1, RunID: "run1", Method: "GET", URL: "https://live.example.com/a", Result: "hit", Status: 200, EntryID: "e1", Consumed: 1, Total: 1, Exhausted: true})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleReplayStream(rec, req, "run1")
	}()

	notify(session.ReplayEvent{Seq: 2, RunID: "run1", Method: "GET", URL: "https://example.com/x", Result: "exhausted", Status: 410, Consumed: 1, Total: 1, Exhausted: true})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, `"seq":1`) || !strings.Contains(body, `"seq":2`) {
		t.Fatalf("SSE body missing events: %s", body)
	}
}

func TestReplayStreamRejectsInactiveRun(t *testing.T) {
	s, _, _ := newReplayServer(t)
	rec := httptest.NewRecorder()
	s.handleReplayStream(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/other/stream", nil), "other")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for inactive run, got %d", rec.Code)
	}
}
