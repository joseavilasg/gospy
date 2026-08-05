package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestReplayDetailToolbarGated locks the replay gate around the whole
// .detail-toolbar in the detail panel. If only the buttons were gated, replay
// mode would still render the (empty) toolbar div - and its margin leaves a
// dead band above the tabs (regression: in replay the detail panel showed an
// empty strip where Ignore host / Add to focus / Replay / Rule used to be).
func TestReplayDetailToolbarGated(t *testing.T) {
	render := strings.ReplaceAll(renderJS, "\r\n", "\n")
	const marker = "getReplayMode() ? '' : `\n        <div class=\"detail-toolbar\">"
	if !strings.Contains(render, marker) {
		t.Fatal("render.js: detail toolbar must be wrapped in the replay gate (getReplayMode() ? '' : `...<div class=\"detail-toolbar\">...`) so replay mode omits the empty toolbar div instead of rendering its margin as dead space")
	}
}

// TestReplayDetailPanelTopPadding locks the replay-specific top padding of the
// detail panel. With the action toolbar absent in replay, the panel must fall
// back to the toolbar's own margin gap (--sp-12) instead of the full panel
// padding (--sp-20) - otherwise the tabs content, which already has its own
// padding, stacks two large paddings and looks overly spacious.
func TestReplayDetailPanelTopPadding(t *testing.T) {
	if !strings.Contains(styleCSS, ".detail-panel:not(:has(.detail-toolbar))") {
		t.Fatal("style.css: detail panel lost its toolbarless top-padding rule - .detail-panel:not(:has(.detail-toolbar)) { padding-top: var(--sp-12) } is required so replay mode doesn't stack the full panel padding on top of the tab content's own padding")
	}
}

// TestHeaderActionsDataDriven locks the header refactor: action items live in
// the header.js config and separators are derived from the visible units at
// render time, so replay mode cannot leave orphaned palotes or hardcoded
// header-sep ids behind in the markup.
func TestHeaderActionsDataDriven(t *testing.T) {
	if !strings.Contains(indexHTML, `<div class="header-actions" id="headerActions"></div>`) {
		t.Fatal("index.html: header actions must be rendered into #headerActions by header.js, not hardcoded in the markup")
	}
	if !strings.Contains(styleCSS, ".header-actions") {
		t.Fatal("style.css: .header-actions flex layout rule is required to lay out the rendered header items")
	}
	for _, hardcoded := range []string{`class="header-sep"`, `id="sepIgnored"`, `id="sepRules"`, `id="focusBtn"`, `id="replayChip"`} {
		if strings.Contains(indexHTML, hardcoded) {
			t.Fatalf("index.html: %s must live in the header.js item config, not in the markup (hardcoded separators are what left orphaned palotes in replay)", hardcoded)
		}
	}
}

// TestHeaderModuleSepsFromVisible locks header.js's separator logic: the
// module must interleave .header-sep between adjacent visible items and honor
// per-item mode hiding (hiddenIn), which is what collapses separators around
// hidden items in replay.
func TestHeaderModuleSepsFromVisible(t *testing.T) {
	header := strings.ReplaceAll(headerJS, "\r\n", "\n")
	if !strings.Contains(header, "header-sep") {
		t.Fatal("header.js: separator rendering (.header-sep) must live in the header module")
	}
	if !strings.Contains(header, "hiddenIn") {
		t.Fatal("header.js: per-item mode hiding (hiddenIn) must live in the header module")
	}
	if !strings.Contains(header, "sep !== false") {
		t.Fatal("header.js: items with sep:false must be glued to the previous unit (e.g. a button and its own checkbox)")
	}
}

// TestOriginSignedUnsupportedHandled locks the signature row on platforms
// without binary signing: the UI must render "N/A" when the server reports
// supported:false instead of a misleading "Unsigned" (Linux has no
// Authenticode equivalent).
func TestOriginSignedUnsupportedHandled(t *testing.T) {
	if !strings.Contains(renderJS, "data.supported === false") {
		t.Fatal("render.js: signature loading must handle supported:false so Linux shows N/A instead of Unsigned")
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
	notify(session.ReplayEvent{Seq: 2, RunID: "run1", Method: "GET", URL: "https://example.com/miss", Result: "miss", Status: 404, Unconsumed: []session.UnconsumedEntry{{ID: "e1"}}, TotalPending: 1, Consumed: 1, Total: 2})

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
	if len(miss.Event.Unconsumed) != 1 {
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
		Session string               `json:"session"`
		Runs    []session.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if runsResp.Session != filepath.Base(logRoot) {
		t.Fatalf("session: expected %q, got %q", filepath.Base(logRoot), runsResp.Session)
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

// TestReplayActiveStream exercises the active stream (/api/replay/events/stream,
// runID==""). It must subscribe with no run active, announce the first run via
// runChanged, and keep streaming across a run switch instead of closing like the
// per-run path - that is what keeps the panel live without reconnects.
func TestReplayActiveStream(t *testing.T) {
	s, _, _ := newReplayServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/events/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleReplayStream(rec, req, "")
	}()

	notify := s.ReplayNotifier()
	notify(session.ReplayEvent{Seq: 1, RunID: "runA", Method: "GET", URL: "https://a.example.com/x", Result: "hit", Status: 200, Consumed: 1, Total: 2})
	time.Sleep(20 * time.Millisecond)
	notify(session.ReplayEvent{Seq: 2, RunID: "runB", Method: "GET", URL: "https://b.example.com/y", Result: "miss", Status: 404, Consumed: 1, Total: 1, Exhausted: true})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"runChanged"`) || !strings.Contains(body, `"runId":"runA"`) {
		t.Fatalf("SSE missing the leading runChanged for the first run: %s", body)
	}
	if !strings.Contains(body, `"seq":1`) || !strings.Contains(body, `"seq":2`) {
		t.Fatalf("SSE missing events: %s", body)
	}
	if !strings.Contains(body, `"type":"runChanged"`) || !strings.Contains(body, `"runId":"runB"`) {
		t.Fatalf("SSE missing runChanged for the run switch (must keep streaming): %s", body)
	}
}

// TestReplayActiveStreamSnapshot locks the snapshot semantics: connecting when a
// run is already active must send a leading runChanged for that run followed by
// its buffered events.
func TestReplayActiveStreamSnapshot(t *testing.T) {
	s, _, _ := newReplayServer(t)
	notify := s.ReplayNotifier()
	notify(session.ReplayEvent{Seq: 1, RunID: "runX", Method: "GET", URL: "https://x.example.com/", Result: "hit", Status: 200, Consumed: 1, Total: 1, Exhausted: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/events/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleReplayStream(rec, req, "")
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"runChanged"`) || !strings.Contains(body, `"runId":"runX"`) {
		t.Fatalf("SSE missing the leading runChanged for the active run: %s", body)
	}
	if !strings.Contains(body, `"seq":1`) {
		t.Fatalf("SSE missing the snapshot event: %s", body)
	}
}

// TestReplayPerRunStreamClosesOnSwitch locks the legacy per-run path behavior:
// a <runID>/stream connection must terminate when the run switches.
func TestReplayPerRunStreamClosesOnSwitch(t *testing.T) {
	s, _, _ := newReplayServer(t)
	notify := s.ReplayNotifier()
	notify(session.ReplayEvent{Seq: 1, RunID: "run1", Method: "GET", URL: "https://a.example.com/", Result: "hit", Status: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleReplayStream(rec, req, "run1")
	}()

	notify(session.ReplayEvent{Seq: 2, RunID: "run2", Method: "GET", URL: "https://b.example.com/", Result: "miss", Status: 404})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("per-run stream did not close on run switch")
	}
}

// TestReplayActiveStreamFrontend locks the panel's live channel wiring: the
// frontend must connect the dedicated active stream (independent of the
// auto-refresh poll), reconnect it when it closes, and must not self-kill it in
// onerror (EventSource auto-reconnects; a closed stream is recreated by the
// ensureReplayStream self-heal). The old run-scoped stream URL is gone.
func TestReplayActiveStreamFrontend(t *testing.T) {
	if !strings.Contains(appJS, "new EventSource('/api/replay/events/stream')") {
		t.Fatal("app.js: the replay panel must connect the dedicated active stream (/api/replay/events/stream) so live events push to the feed regardless of the auto-refresh poll")
	}
	if !strings.Contains(appJS, "ensureReplayStream") {
		t.Fatal("app.js: syncReplay/chip-open must call ensureReplayStream to (re)connect the active replay stream")
	}
	if strings.Contains(appJS, "src.onerror") {
		t.Fatal("app.js: connectReplayStream must not self-kill the stream in onerror - EventSource auto-reconnects, and the ensureReplayStream self-heal recreates a closed stream")
	}
	if strings.Contains(appJS, "/api/replay/events/${encodeURIComponent(runId)}/stream") {
		t.Fatal("app.js: the run-scoped stream URL must be gone - the panel uses the dedicated active stream that survives run switches")
	}
}

// TestReplayFeedTimestamps locks the per-event timestamp in the replay feed
// rows: the same toLocaleTimeString used by the request list, styled muted.
func TestReplayFeedTimestamps(t *testing.T) {
	render := strings.ReplaceAll(renderJS, "\r\n", "\n")
	if !strings.Contains(render, "replay-event-time") || !strings.Contains(render, "toLocaleTimeString") {
		t.Fatal("render.js: replay feed rows must carry a per-event timestamp (replay-event-time) so the panel shows when each request was served")
	}
	if !strings.Contains(styleCSS, ".replay-event-time") {
		t.Fatal("style.css: .replay-event-time rule is required for the feed timestamp")
	}
}

// TestReplaySessionLabelShown locks the session name into the replay panel
// header: the runs endpoint exposes it, the drawer carries a dedicated label,
// and the frontend renders it so the user always knows which session the runs
// belong to.
func TestReplayPendingScope(t *testing.T) {
	if !strings.Contains(indexHTML, `id="listScopeBanner"`) {
		t.Fatal("index.html: the list column must carry a scope banner (listScopeBanner)")
	}
	if !strings.Contains(renderJS, `data-action="scope-pending"`) ||
		!strings.Contains(renderJS, "Show in list") ||
		!strings.Contains(renderJS, "scope-link") {
		t.Fatal("render.js: replay event details must offer Show in list as a text link scoping the pending set")
	}
	if strings.Contains(renderJS, "replay-event-pending") {
		t.Fatal("render.js: the inline pending rows are removed in favor of the scoped list")
	}
	if !strings.Contains(renderJS, "renderListScopeBanner") {
		t.Fatal("render.js: renderListScopeBanner must render the active scope into listScopeBanner")
	}
	if !strings.Contains(renderJS, "export function clearListSelection") ||
		!strings.Contains(renderJS, "clearListSelection();") {
		t.Fatal("render.js: opening a replay event detail must clear the list selection so no stale row stays highlighted")
	}
	if !strings.Contains(renderJS, "scope.url") ||
		strings.Contains(renderJS, "scope.ids.length") {
		t.Fatal("render.js: the scope banner anchor must show the anchored event request line without a redundant entries count")
	}
	if !strings.Contains(appJS, "setListScope") ||
		!strings.Contains(appJS, "'scope-back'") ||
		!strings.Contains(appJS, "'scope-clear'") {
		t.Fatal("app.js: the scope banner needs its anchor (scope-back) and Clear (scope-clear) wired")
	}
	if !strings.Contains(appJS, "ev.unconsumed.map") {
		t.Fatal("app.js: the pending scope must be built from the event's unconsumed ids, with no extra fetch")
	}
	if !strings.Contains(apiJS, "listScope") ||
		!strings.Contains(apiJS, "params.set('ids', listScope.ids.join(','))") {
		t.Fatal("api.js: the active scope must travel to /api/requests as the ids parameter")
	}
}

func TestReplaySessionLabelShown(t *testing.T) {
	if !strings.Contains(indexHTML, `id="replaySessionLabel"`) {
		t.Fatal("index.html: the replay drawer header must carry a session label next to the run select")
	}
	if !strings.Contains(styleCSS, ".replay-session-label") {
		t.Fatal("style.css: .replay-session-label rule is required for the session label")
	}
	if !strings.Contains(apiJS, "session: data.session") {
		t.Fatal("api.js: loadReplayRuns must surface the session name from /api/replay/runs")
	}
	if !strings.Contains(appJS, "replaySessionLabel") {
		t.Fatal("app.js: populateReplayRuns must render the session name into replaySessionLabel")
	}
}

func TestReplayHeaderStatsPluralization(t *testing.T) {
	if !strings.Contains(appJS, "'es' : 's'") {
		t.Fatal("app.js: pluralLabel must use the English sibilant plural rule (-es) so miss becomes misses, not misss")
	}
	if strings.Contains(appJS, "word + 's'") {
		t.Fatal("app.js: pluralLabel must not blindly append 's' to words ending in a sibilant")
	}
}
