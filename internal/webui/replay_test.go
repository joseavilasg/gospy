package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gospy/internal/history"
	"gospy/internal/proxy"
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

func TestListToggleScrollRestore(t *testing.T) {
	if !strings.Contains(appJS, "savedListScrollTop = list.scrollTop") ||
		!strings.Contains(appJS, "list.scrollTop = savedListScrollTop") ||
		!strings.Contains(appJS, "willHide") ||
		!strings.Contains(appJS, "onListScroll()") {
		t.Fatal("app.js: toggling the list must save the scroll position before hiding and restore it on re-open, re-rendering the virtual window so a hidden poll that rendered the wrong window (scrollTop reads 0 while display:none) never leaves the list empty")
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
		{"request-rule", httptest.NewRequest(http.MethodPost, "/api/request-rule", nil), s.replayReadOnly(s.handleRequestRule)},
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec = httptest.NewRecorder()
	s.handleProcessEvents(rec, httptest.NewRequest(http.MethodGet, "/api/process/events", nil).WithContext(ctx))
	assertStatus(t, "process events SSE in replay (signature results must reach the origin tab)", rec, http.StatusOK)

	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}
	if !strings.Contains(string(src), "sigCache.Snapshot()") ||
		!strings.Contains(string(src), "result.InFlight") {
		t.Fatal("server.go: the signature SSE must emit the cache snapshot on connect and the GET must not serve in-flight placeholders")
	}
}

// TestReplayRulesEnabled locks the rules-in-replay contract: the rule CRUD API
// is mutable in replay mode (the replay server consults the same engine live)
// and the engine picks up the stored rule.
func TestReplayRulesEnabled(t *testing.T) {
	s, hist, _ := newReplayServer(t)
	saveEntry(t, hist, "e1", "GET", "https://live.example.com/a")

	rule := `{"name":"fake 404","match":{"method":"GET","host":"live.example.com","url_pattern":"/a"},"action":"mock","mock_response":{"status":404,"body":"mocked"}}`
	rec := httptest.NewRecorder()
	s.handleRules(rec, httptest.NewRequest(http.MethodPost, "/api/rules", strings.NewReader(rule)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/rules in replay: expected 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleRules(rec, httptest.NewRequest(http.MethodGet, "/api/rules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/rules in replay: expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fake 404") {
		t.Fatalf("GET /api/rules must list the replay rule, got %q", rec.Body.String())
	}
	if got := len(s.engine.GetRules()); got != 1 {
		t.Fatalf("engine must hold the replay rule, got %d rules", got)
	}
}

// TestReplayRulesFrontend locks the rules-in-replay UI contract: the Rules
// button is visible in replay, the replay boot loads the rules, the modal only
// offers the actions the replay server honors (mock/drop), and applied events
// render their rule badge and banner.
func TestReplayRulesFrontend(t *testing.T) {
	src := strings.ReplaceAll(appJS, "\r\n", "\n")
	if strings.Contains(src, "id: 'rulesBtn',\n    hiddenIn: ['replay']") {
		t.Fatal("app.js: the Rules button must be visible in replay mode")
	}
	if !strings.Contains(src, "if (getReplayMode()) {\n    loadRules();\n    connectSSE();\n    connectRecordingEvents();\n    return;\n  }") {
		t.Fatal("app.js: the replay boot must load the rules and connect the signature SSE so the origin tab resolves its 'Analyzing...' state")
	}

	rnd := strings.ReplaceAll(renderJS, "\r\n", "\n")
	for _, probe := range []string{
		"if (replayMode) return (raw === 'mock' || raw === 'drop') ? raw : 'mock';",
		"for (const v of ['passthrough', 'modify'])",
		"function actionBannerHtml(record)",
		"actionBannerHtml(ev)",
		"replay-event-rule",
	} {
		if !strings.Contains(rnd, probe) {
			t.Fatalf("render.js: must %s", probe)
		}
	}
	if !strings.Contains(styleCSS, ".replay-event-rule-mock") || !strings.Contains(styleCSS, ".replay-event-rule-drop") {
		t.Fatal("style.css: the feed rule badges need mock/drop styles")
	}
	if !strings.Contains(rnd, "create-rule-from-replay-event") ||
		!strings.Contains(rnd, "function openRuleModalFromReplayEvent(ev)") {
		t.Fatal("render.js: the replay event detail must offer create-rule with a replay-specific prefill")
	}
	if !strings.Contains(src, "case 'create-rule-from-replay-event':") ||
		!strings.Contains(src, "openRuleModalFromReplayEvent(_lastReplayDetail && _lastReplayDetail.event)") {
		t.Fatal("app.js: the replay event detail create-rule must dispatch to the replay prefill")
	}
	if !strings.Contains(rnd, "const srv = ev.servedResponse;") ||
		!strings.Contains(rnd, `data-target="served"`) {
		t.Fatal("render.js: the response tab must render the rule-served response (and its body link) when present")
	}
	if !strings.Contains(src, `?target=${encodeURIComponent(target)}`) {
		t.Fatal("app.js: the replay-body fetch must pass the served target to the endpoint")
	}
	if !strings.Contains(rnd, "${bodyHtml ? `<div class=\"section-panel\">") ||
		strings.Contains(rnd, "Empty body") {
		t.Fatal("render.js: the response tab must omit the Body section when the response has no body, never print an 'Empty body' placeholder")
	}
}

// TestReplayServedBodyEndpoint locks the served-response body endpoint: a
// rule-served response (mock/drop) is captured in the event and its raw body
// is exposed via /api/replay/events/{run}/{seq}/body?target=served, distinct
// from the recorded body served on a plain hit.
func TestReplayServedBodyEndpoint(t *testing.T) {
	s, _, logRoot := newReplayServer(t)

	runDir, err := session.ReplayRunDir(logRoot, "mockrun")
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	l, err := session.OpenReplayLog(runDir, "mockrun")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}
	if err := l.Append(&session.ReplayEvent{
		Timestamp:     time.Now(),
		RunID:         "mockrun",
		Method:        "GET",
		URL:           "https://live.example.com/master.m3u8",
		Result:        "hit",
		Status:        418,
		AppliedAction: "mock",
		ServedResponse: &history.ResponseRecord{
			Status:  418,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
		},
	}, []byte("{}"), []byte("mocked body")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	getBody := func(qs string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleReplayBody(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/mockrun/1/body"+qs, nil), "mockrun", 1)
		return rec
	}

	rec := getBody("")
	if rec.Code != http.StatusOK || rec.Body.String() != "{}" {
		t.Fatalf("request target: expected 200 with the raw request body, got %d %q", rec.Code, rec.Body.String())
	}
	rec = getBody("?target=served")
	if rec.Code != http.StatusOK {
		t.Fatalf("served target: expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "mocked body" {
		t.Fatalf("served target: expected 'mocked body', got %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("served target: expected Content-Type text/plain, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("served target: expected CORS *, got %q", got)
	}
	rec = getBody("?target=evil")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid target: expected 400, got %d", rec.Code)
	}

	plainDir, err := session.ReplayRunDir(logRoot, "plainrun")
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	pl, err := session.OpenReplayLog(plainDir, "plainrun")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}
	if err := pl.Append(&session.ReplayEvent{
		Timestamp: time.Now(),
		RunID:     "plainrun",
		Method:    "GET",
		URL:       "https://live.example.com/master.m3u8",
		Result:    "hit",
		Status:    200,
	}, []byte("{}"), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := pl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rec = httptest.NewRecorder()
	s.handleReplayBody(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/plainrun/1/body?target=served", nil), "plainrun", 1)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("served target on a plain hit: expected 404, got %d", rec.Code)
	}
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

// TestReplayDetailEditActionsGated locks the replay read-only gate around the
// detail's edit affordances: canEdit (which drives the ✎ Edit kebab items and
// the body viewer edit kebab) must exclude replay mode in both renderDetail
// and buildResponseTab, and the header ↩ Revert must hide too - otherwise the
// read-only recorded-entry view still offers mutations that the server rejects
// (replayReadOnly 404s them) and the kebab lies about what is possible.
func TestReplayDetailEditActionsGated(t *testing.T) {
	render := strings.ReplaceAll(renderJS, "\r\n", "\n")
	for _, want := range []string{
		"const canEdit = !isModified && !isMocked && !isDropped && !getReplayMode()",
		"const canEdit = !isModified && !isMocked && !isDropped && !req?.response?.stream && !getReplayMode()",
		"reqHasEditedHeaders && !getReplayMode()",
	} {
		if !strings.Contains(render, want) {
			t.Fatalf("render.js: edit affordances must exclude replay mode (missing %q) - the read-only recorded-entry view must not offer mutations that the replayReadOnly guard 404s", want)
		}
	}
}

// TestReplayKeyboardShortcuts locks the Ctrl+B / Ctrl+J document shortcuts.
// Ctrl+B toggles the request list by reusing the exact toggleListBtn click
// handler (toggle + active class + localStorage persistence), Ctrl+J toggles
// the replay panel via the same replayChipClick the chip uses - but only when
// the chip exists (replay mode): outside replay the shortcut returns without
// preventDefault, so the browser keeps Ctrl+J as Downloads. The handler must
// skip auto-repeat and Monaco editors (the rule modal owns its own keys).
func TestReplayKeyboardShortcuts(t *testing.T) {
	app := strings.ReplaceAll(appJS, "\r\n", "\n")
	for _, want := range []string{
		"document.getElementById('toggleListBtn')?.click()",
		"if (!document.getElementById('replayChip')) return;",
		"replayChipClick()",
		"e.key.toLowerCase()",
		"e.repeat",
		"closest('.monaco-editor')",
		"Replay activity (Ctrl+J)",
	} {
		if !strings.Contains(app, want) {
			t.Fatalf("app.js: keyboard shortcuts lost %q - Ctrl+B must toggle the request list and Ctrl+J the replay panel, with the replay shortcut returning early (no preventDefault) when the replay chip is absent so browser Ctrl+J keeps opening Downloads", want)
		}
	}
	if !strings.Contains(indexHTML, "Toggle request list (Ctrl+B)") {
		t.Fatal("index.html: the toggleListBtn title must advertise the Ctrl+B shortcut")
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
	if !strings.Contains(renderJS, "sig.supported === false") {
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

type fakeSigCache struct {
	results map[string]*proxy.SignatureResult
}

func (f *fakeSigCache) Get(filePath string) *proxy.SignatureResult { return f.results[filePath] }
func (f *fakeSigCache) VerifyAsync(filePath string)                {}
func (f *fakeSigCache) OnUpdate(fn func(*proxy.SignatureResult))   {}
func (f *fakeSigCache) Snapshot() []*proxy.SignatureResult {
	out := make([]*proxy.SignatureResult, 0, len(f.results))
	for _, r := range f.results {
		out = append(out, r)
	}
	return out
}

func TestReplayDetailCarriesClientSignature(t *testing.T) {
	s, _, logRoot := newReplayServer(t)

	runDir, err := session.ReplayRunDir(logRoot, "sigrun")
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	l, err := session.OpenReplayLog(runDir, "sigrun")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}
	if err := l.Append(&session.ReplayEvent{
		Timestamp:  time.Now(),
		RunID:      "sigrun",
		Method:     "GET",
		URL:        "https://live.example.com/a",
		Result:     "hit",
		Status:     200,
		EntryID:    "e1",
		ClientPath: "/mnt/c/bin/streamer",
	}, []byte("{}"), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s.sigCache = &fakeSigCache{results: map[string]*proxy.SignatureResult{
		"/mnt/c/bin/streamer": {FilePath: "/mnt/c/bin/streamer", IsSigned: true, SignerName: "Gohulu Corp", Supported: true},
	}}

	rec := httptest.NewRecorder()
	s.handleReplayEventsSub(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/sigrun/1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d", rec.Code)
	}
	var detail replayDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ClientSignature == nil || !detail.ClientSignature.IsSigned || detail.ClientSignature.SignerName != "Gohulu Corp" {
		t.Fatalf("expected the cached client signature in the replay detail, got %+v", detail.ClientSignature)
	}
}

func TestRequestDetailCarriesClientSignature(t *testing.T) {
	s, h, _ := newReplayServer(t)

	entry := &history.Entry{
		ID:         "sigentry",
		Timestamp:  time.Now(),
		Request:    history.RequestRecord{Method: "GET", URL: "https://live.example.com/a", Host: "live.example.com"},
		Response:   &history.ResponseRecord{Status: 200},
		ClientPath: "/mnt/c/bin/streamer",
	}
	if err := h.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s.sigCache = &fakeSigCache{results: map[string]*proxy.SignatureResult{
		"/mnt/c/bin/streamer": {FilePath: "/mnt/c/bin/streamer", IsSigned: true, SignerName: "Gohulu Corp", Supported: true},
	}}

	rec := httptest.NewRecorder()
	s.handleGetRequest(rec, httptest.NewRequest(http.MethodGet, "/api/requests/sigentry", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d", rec.Code)
	}
	var resp struct {
		ID              string                 `json:"id"`
		ClientSignature *proxy.SignatureResult `json:"clientSignature"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if resp.ID != "sigentry" {
		t.Fatalf("embedded entry fields must stay at the top level, got id=%q", resp.ID)
	}
	if resp.ClientSignature == nil || !resp.ClientSignature.IsSigned {
		t.Fatalf("expected the cached client signature in the request detail, got %+v", resp.ClientSignature)
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
	if err := l.Append(&session.ReplayEvent{Timestamp: time.Now(), RunID: "pastrun", Method: "GET", URL: "https://live.example.com/a", Result: "hit", Status: 200, EntryID: "e1"}, []byte("{}"), nil); err != nil {
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

func TestReplayEventsListPaged(t *testing.T) {
	s, _, logRoot := newReplayServer(t)

	runDir, err := session.ReplayRunDir(logRoot, "bigrun")
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	l, err := session.OpenReplayLog(runDir, "bigrun")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}
	for i := 0; i < 500; i++ {
		ev := &session.ReplayEvent{
			Timestamp: time.Now(),
			RunID:     "bigrun",
			Method:    "GET",
			URL:       fmt.Sprintf("https://live.example.com/%d", i+1),
			Result:    "hit",
			Status:    200,
			EntryID:   fmt.Sprintf("e%d", i+1),
		}
		if err := l.Append(ev, []byte("{}"), nil); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	getPage := func(qs string) (events []session.ReplayEvent, total int, hasMore bool) {
		rec := httptest.NewRecorder()
		s.handleReplayEventsList(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events?run=bigrun&"+qs, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Events  []session.ReplayEvent `json:"events"`
			Total   int                   `json:"total"`
			HasMore bool                  `json:"hasMore"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.Events, resp.Total, resp.HasMore
	}

	first, total, hasMore := getPage("limit=200")
	if total != 500 || !hasMore || len(first) != 200 {
		t.Fatalf("first page: total=%d hasMore=%v len=%d, want 500/true/200", total, hasMore, len(first))
	}
	if first[0].Seq != 301 || first[len(first)-1].Seq != 500 {
		t.Fatalf("first page should be the newest 200 (seqs 301-500), got %d..%d", first[0].Seq, first[len(first)-1].Seq)
	}

	older, total, hasMore := getPage("limit=200&beforeSeq=301")
	if total != 500 || !hasMore || len(older) != 200 {
		t.Fatalf("older page: total=%d hasMore=%v len=%d, want 500/true/200", total, hasMore, len(older))
	}
	if older[0].Seq != 101 || older[len(older)-1].Seq != 300 {
		t.Fatalf("older page should be seqs 101-300, got %d..%d", older[0].Seq, older[len(older)-1].Seq)
	}

	oldest, _, hasMore := getPage("limit=200&beforeSeq=101")
	if hasMore || len(oldest) != 100 {
		t.Fatalf("oldest page: len=%d hasMore=%v, want 100/false", len(oldest), hasMore)
	}
	if oldest[0].Seq != 1 || oldest[len(oldest)-1].Seq != 100 {
		t.Fatalf("oldest page should be seqs 1-100, got %d..%d", oldest[0].Seq, oldest[len(oldest)-1].Seq)
	}

	all, total, hasMore := getPage("")
	if len(all) != 500 || total != 500 || hasMore {
		t.Fatalf("no params should serve the full run: len=%d total=%d hasMore=%v", len(all), total, hasMore)
	}
}

func TestReplayFeedVirtualization(t *testing.T) {
	if !strings.Contains(renderJS, "onReplayFeedScroll") ||
		!strings.Contains(renderJS, "slice().reverse()") {
		t.Fatal("render.js: the replay feed must render its virtual window with the newest events on top")
	}
	if !strings.Contains(renderJS, "setReplayFeed") ||
		!strings.Contains(renderJS, "prependReplayFeed") ||
		!strings.Contains(renderJS, "appendReplayFeedEvent") {
		t.Fatal("render.js: the feed needs its page load, older-page prepend and live append entry points")
	}
	if !strings.Contains(apiJS, "limit=") || !strings.Contains(apiJS, "beforeSeq") {
		t.Fatal("api.js: the feed must page with limit and load older pages via beforeSeq")
	}
	if !strings.Contains(appJS, "loadReplayFeedOlder") ||
		!strings.Contains(appJS, "onReplayFeedScroll") {
		t.Fatal("app.js: the feed scroll must be wired to the older-page loader")
	}
}

func TestFilterTypeListScroll(t *testing.T) {
	if !strings.Contains(indexHTML, "filter-type-list") {
		t.Fatal("index.html: the add-filter popover step 1 list needs the filter-type-list class so its height can be capped")
	}
	if !strings.Contains(styleCSS, ".filter-type-list") ||
		!strings.Contains(styleCSS, "max-height: var(--popover-list-max-h)") ||
		!strings.Contains(styleCSS, "overflow-y: auto") {
		t.Fatal("style.css: .filter-type-list must cap its height and scroll like .filter-option-list")
	}
}

func TestDateRangeNowClear(t *testing.T) {
	if !strings.Contains(filtersJS, "data-now") ||
		!strings.Contains(filtersJS, "data-clear") ||
		!strings.Contains(filtersJS, "getFullYear") {
		t.Fatal("filters.js: the date range inputs need now/clear actions that fill the current local datetime and empty an input")
	}
	if !strings.Contains(filtersJS, "clockSVG") ||
		!strings.Contains(filtersJS, "clearSVG") {
		t.Fatal("filters.js: the now/clear range buttons must render Lucide icons")
	}
	if !strings.Contains(styleCSS, ".filter-range-inputs") ||
		!strings.Contains(styleCSS, ".filter-range-btn") ||
		!strings.Contains(styleCSS, ".filter-range-clear") {
		t.Fatal("style.css: the range input row must lay out the input beside the now/clear buttons")
	}
}

func TestQueryParamsPrettyView(t *testing.T) {
	if !strings.Contains(renderJS, "parseQueryString") ||
		!strings.Contains(renderJS, "buildUrlBreakdown") ||
		!strings.Contains(renderJS, "buildQueryTable") ||
		!strings.Contains(renderJS, "renderUrlViewInner") ||
		!strings.Contains(renderJS, "buildRequestUrlBlock") {
		t.Fatal("render.js: the request section needs the query-params pretty view (parseQueryString/buildUrlBreakdown/buildQueryTable/renderUrlViewInner/buildRequestUrlBlock)")
	}
	if !strings.Contains(renderJS, "(hasQuery ? buildQueryTable(activeUrl) : '')") {
		t.Fatal("render.js: the pretty url view must be available for every URL, with the query table only when the active url has params")
	}
	if strings.Contains(renderJS, "hasQuery ? viewMode : 'raw'") {
		t.Fatal("render.js: the pretty url view must not be gated on the presence of query params")
	}
	if !strings.Contains(renderJS, "Protocol:</span>") ||
		!strings.Contains(renderJS, "protocol = u.protocol.slice(0, -1)") ||
		!strings.Contains(renderJS, "host = u.host") ||
		strings.Contains(renderJS, "host = u.origin") {
		t.Fatal("render.js: the url breakdown must split protocol and host (https + google.com), not show the origin combined")
	}
	if !strings.Contains(renderJS, "urlModified ? `<div class=\"divider-v\"></div><div class=\"body-tools-group\">") {
		t.Fatal("render.js: the url toolbar must separate the pretty/raw and original/modified groups with a divider, like the body toolbar")
	}
	if !strings.Contains(appJS, "case 'set-url-view'") ||
		!strings.Contains(appJS, "renderUrlViewInner(urlView.dataset") {
		t.Fatal("app.js: the url view needs a pretty/raw toggle that re-renders the block, and set-url-content must re-render too")
	}
	if !strings.Contains(styleCSS, ".url-breakdown") ||
		!strings.Contains(styleCSS, ".url-row") ||
		!strings.Contains(styleCSS, ".query-table") ||
		!strings.Contains(styleCSS, ".query-row") {
		t.Fatal("style.css: the pretty url view needs the breakdown rows and the separate query table styles")
	}
}

func TestReplayFeedSelection(t *testing.T) {
	if !strings.Contains(renderJS, "selectReplayFeedEvent") ||
		!strings.Contains(renderJS, "clearReplayFeedSelection") ||
		!strings.Contains(renderJS, "_feedSelectedSeq") {
		t.Fatal("render.js: the feed needs state-driven selection (selectReplayFeedEvent/clearReplayFeedSelection) that survives virtual window re-renders")
	}
	if !strings.Contains(appJS, "selectReplayFeedEvent(route.run, route.seq)") {
		t.Fatal("app.js: the router must select the feed event when reproducing a replay route (feed click and breadcrumb back both route through applyRoute)")
	}
	if !strings.Contains(styleCSS, ".replay-event.selected") {
		t.Fatal("style.css: the selected replay event needs the selected highlight")
	}
}

func TestReplayBrowserHistory(t *testing.T) {
	if !strings.Contains(routesJS, "export function parseRoute") ||
		!strings.Contains(routesJS, "export function buildHash") {
		t.Fatal("routes.js: must export parseRoute/buildHash for hash-based history")
	}
	for _, probe := range []string{"kind: 'entry'", "kind: 'replay'", "kind: 'replay-entry'", "'matching'", "'all'"} {
		if !strings.Contains(routesJS, probe) {
			t.Fatalf("routes.js: must model the %s view", probe)
		}
	}
	app := strings.ReplaceAll(appJS, "\r\n", "\n")
	if !strings.Contains(app, "addEventListener('hashchange'") ||
		!strings.Contains(app, "function applyRoute(") {
		t.Fatal("app.js: the router must react to hashchange (browser back/forward)")
	}
	for _, probe := range []string{
		"navigate({ kind: 'entry',",
		"navigate({ kind: 'replay',",
		"navigate({ kind: 'replay-entry',",
		"function sameIdentity(",
		"function applyRouteFull(",
		"function applyRouteDiff(",
		"selectMatchCandidate(route.candidate)",
		"renderReplayEventDetail(detail, activeTab)",
	} {
		if !strings.Contains(app, probe) {
			t.Fatalf("app.js: the router must %s", probe)
		}
	}
	if !strings.Contains(app, "showReplayDetail(detail, route.tab);\n        if (route.tab === 'origin') loadSignatureInfo();") ||
		!strings.Contains(app, "switchTabInPlace(route.tab);\n    if (route.tab === 'origin') loadSignatureInfo();") {
		t.Fatal("app.js: opening the origin tab (full apply or in-place diff) must request the signature so the replay event detail resolves its 'Analyzing...' state")
	}
	if !strings.Contains(app, "data.supported === false") {
		t.Fatal("app.js: the signature SSE handler must honor supported:false (N/A on platforms without signature verification)")
	}
	if !strings.Contains(renderJS, "renderReplayEventDetail(detail, activeTab") {
		t.Fatal("render.js: renderReplayEventDetail must accept the active tab from the route")
	}
	if !strings.Contains(renderJS, "renderOriginStatus(req.clientSignature)") ||
		!strings.Contains(renderJS, "renderOriginStatus(detail.clientSignature)") ||
		!strings.Contains(renderJS, "querySelector('.analyzing')") {
		t.Fatal("render.js: the origin verdict must come from the detail payload when available and loadSignatureInfo must skip the fetch once a verdict is already shown")
	}
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}
	if !strings.Contains(string(src), "//go:embed routes.js") ||
		!strings.Contains(string(src), `mux.HandleFunc("/routes.js"`) {
		t.Fatal("server.go: routes.js must be embedded and served so the browser can load the router module")
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
// TestReplayMatchTab locks the match tab into the replay event detail: four
// tabs (Request/Response/Origin/Match), the unified candidate list with state
// tags, the View full entry breadcrumb, and the removal of the legacy Show in
// list scope machinery.
func TestReplayMatchTab(t *testing.T) {
	if strings.Contains(indexHTML, "listScopeBanner") {
		t.Fatal("index.html: the list scope banner is removed; navigation lives in the Match tab")
	}
	for _, probe := range []string{`data-tab="request"`, `data-tab="response"`, `data-tab="origin"`, `data-tab="match"`} {
		if !strings.Contains(renderJS, probe) {
			t.Fatalf("render.js: the replay event detail must render a %s tab", probe)
		}
	}
	if !strings.Contains(renderJS, "renderReplayMatch") ||
		!strings.Contains(renderJS, "match-candidate-row") ||
		!strings.Contains(renderJS, "replay-candidate") ||
		!strings.Contains(renderJS, "replay-scope") ||
		!strings.Contains(renderJS, "match-search") ||
		!strings.Contains(renderJS, "match-scope-seg") ||
		!strings.Contains(renderJS, "match-scope-btn") ||
		!strings.Contains(renderJS, "match-candidate-url") ||
		!strings.Contains(renderJS, "shortUrl") {
		t.Fatal("render.js: the match tab needs the candidate list, the segmented scope control, the search input and the short-url helper")
	}
	if strings.Contains(renderJS, "match-chip") ||
		strings.Contains(renderJS, "match-scope-chips") {
		t.Fatal("render.js: the scope selector must be a segmented control, not standalone chips")
	}
	if !strings.Contains(renderJS, "replay-tag-served") ||
		!strings.Contains(renderJS, "consumed by seq") {
		t.Fatal("render.js: candidates must carry state tags (matched / consumed by seq / diffs)")
	}
	if !strings.Contains(renderJS, "replay-full-entry") ||
		!strings.Contains(renderJS, "replay-back-event") {
		t.Fatal("render.js: the detail must offer View full entry and a breadcrumb back to the event")
	}
	if !strings.Contains(renderJS, "Viewing recorded entry") ||
		!strings.Contains(renderJS, "read-only") {
		t.Fatal("render.js: the full-entry breadcrumb must read 'Viewing recorded entry · entry N · read-only'")
	}
	if strings.Contains(renderJS, "renderListScopeBanner") ||
		strings.Contains(renderJS, "scope-pending") {
		t.Fatal("render.js: the Show in list scope machinery is removed")
	}
	if !strings.Contains(appJS, "loadReplayCandidates") ||
		!strings.Contains(appJS, "loadReplayCandidateDiff") {
		t.Fatal("app.js: the match tab must load candidates and on-demand diffs")
	}
	if !strings.Contains(appJS, "_matchResp.selectedEntryId") {
		t.Fatal("app.js: View full entry must navigate to the candidate selected in the frontend")
	}
	if strings.Contains(appJS, "setListScope") ||
		strings.Contains(appJS, "'scope-back'") ||
		strings.Contains(appJS, "'scope-clear'") {
		t.Fatal("app.js: the list scope handlers are removed")
	}
	if strings.Contains(apiJS, "listScope") {
		t.Fatal("api.js: the ids list-scope parameter is removed")
	}
	if strings.Contains(renderJS, `title="${incoming}"`) ||
		strings.Contains(renderJS, `title="${recorded}"`) ||
		!strings.Contains(renderJS, "diff-empty") {
		t.Fatal("render.js: missing diff values must render a bare diff-empty placeholder, never HTML inside the title attribute")
	}
	for _, en := range []string{"Select a candidate to compare", "No candidate shares this host+path", "The entry ${resp.consumed.entry} was already consumed by", "out-of-order request", "Comparing against entry", "Select a candidate to see its diff"} {
		if !strings.Contains(renderJS, en) {
			t.Fatalf("render.js: the match tab text must be English, missing %q", en)
		}
	}
	matchRowWhite := regexp.MustCompile(`\.diff-row-match \.diff-side \{\r?\n  color: var\(--text-primary\);\r?\n\}`)
	if !matchRowWhite.MatchString(styleCSS) {
		t.Fatal("style.css: matched diff rows must use white letters (green does not contrast on the panel background)")
	}
	alignStatus := regexp.MustCompile(`\.diff-header-row\s*>\s*span:last-child \{\r?\n  text-align: right;\r?\n\}`)
	if !alignStatus.MatchString(styleCSS) {
		t.Fatal("style.css: the Status header must right-align with its values")
	}
	if got := strings.Count(styleCSS, "var(--bg-surface-alt)"); got != 1 {
		t.Fatalf("style.css: only the modal may use the neutral gray --bg-surface-alt, got %d uses", got)
	}
	if got := strings.Count(styleCSS, "var(--bg-header)"); got < 4 {
		t.Fatalf("style.css: the match tab headers (breadcrumb, chips, diff) must use --bg-header like the proto table, got %d uses", got)
	}
	diffSideWrap := regexp.MustCompile(`\.diff-side \{\r?\n  color: var\(--key\);\r?\n  white-space: normal;\r?\n  overflow-wrap: anywhere;\r?\n\}`)
	if !diffSideWrap.MatchString(styleCSS) {
		t.Fatal("style.css: diff param names must wrap, never truncate with ellipsis")
	}
	diffValWrap := regexp.MustCompile(`\.diff-incoming,\r?\n\.diff-recorded \{\r?\n  white-space: normal;\r?\n  overflow-wrap: anywhere;\r?\n  min-width: 0;\r?\n\}`)
	if !diffValWrap.MatchString(styleCSS) {
		t.Fatal("style.css: diff values must wrap (overflow-wrap: anywhere), never truncate with ellipsis")
	}
	if !strings.Contains(styleCSS, ".match-candidate-row") ||
		!strings.Contains(styleCSS, ".diff-row") ||
		!strings.Contains(styleCSS, ".diff-header-row") ||
		!strings.Contains(styleCSS, ".replay-warn-box") ||
		!strings.Contains(styleCSS, ".replay-breadcrumb") ||
		!strings.Contains(styleCSS, ".replay-tag-served") ||
		!strings.Contains(styleCSS, ".match-layout") ||
		!strings.Contains(styleCSS, ".match-list-col") ||
		!strings.Contains(styleCSS, ".replay-section-title") ||
		!strings.Contains(styleCSS, ".replay-badge-hit") ||
		!strings.Contains(styleCSS, ".replay-badge-miss") {
		t.Fatal("style.css: the match tab needs candidate rows, diff table, warn box, breadcrumb and badge styles")
	}
	if !strings.Contains(styleCSS, ".match-scope-seg") ||
		!strings.Contains(styleCSS, ".match-scope-btn") ||
		strings.Contains(styleCSS, ".match-scope-chips") {
		t.Fatal("style.css: the scope selector must be a segmented control, not standalone chips")
	}
	if !strings.Contains(styleCSS, ".match-candidate-row.served") ||
		!strings.Contains(styleCSS, ".match-candidate-row.consumed") ||
		!strings.Contains(styleCSS, "flex: 0 0 300px") {
		t.Fatal("style.css: candidate rows need a state left-border and the 300px rail")
	}
	servedBorder := regexp.MustCompile(`\.match-candidate-row\.served \{\r?\n  border-left-color: var\(--green\);\r?\n\}`)
	if !servedBorder.MatchString(styleCSS) {
		t.Fatal("style.css: the served row must carry a green left border")
	}
	consumedBorder := regexp.MustCompile(`\.match-candidate-row\.consumed \{\r?\n  border-left-color: var\(--orange\);\r?\n\}`)
	if !consumedBorder.MatchString(styleCSS) {
		t.Fatal("style.css: the consumed row must carry an amber left border")
	}
	if tagBase := regexp.MustCompile(`\.replay-tag \{\r?\n  align-self: center;\r?\n  font-size: var\(--fs-sm\);\r?\n  white-space: nowrap;\r?\n\}`); !tagBase.MatchString(styleCSS) {
		t.Fatal("style.css: state tags must hug their content (align-self: center), never stretch across the rail")
	}
	if urlSmall := regexp.MustCompile(`\.match-candidate-url \{\r?\n  color: var\(--text-dim\);\r?\n  font-size: var\(--fs-sm\);\r?\n  font-family: var\(--font-mono\)`); !urlSmall.MatchString(styleCSS) {
		t.Fatal("style.css: the pending-row URL line must run at 15px (--fs-sm), smaller than the entry name")
	}
	servedPill := regexp.MustCompile(`\.replay-tag-served \{\r?\n  color: var\(--green\);\r?\n  background: var\(--green-bg\);\r?\n  border: 1px solid var\(--green-dark\);\r?\n  border-radius: var\(--radius-md\);\r?\n  padding: var\(--sp-1\) var\(--sp-6\);\r?\n\}`)
	if !servedPill.MatchString(styleCSS) {
		t.Fatal("style.css: only the served tag keeps the pill (bg, radius, padding)")
	}
	consumedText := regexp.MustCompile(`\.replay-tag-consumed \{\r?\n  color: var\(--orange\);\r?\n\}`)
	if !consumedText.MatchString(styleCSS) {
		t.Fatal("style.css: the consumed tag must be plain amber text, no pill")
	}
	pendingText := regexp.MustCompile(`\.replay-tag-pending \{\r?\n  color: var\(--text-secondary\);\r?\n\}`)
	if !pendingText.MatchString(styleCSS) {
		t.Fatal("style.css: the pending tag must be plain gray text, no pill")
	}
	if sharedGray := regexp.MustCompile(`\.replay-tag-consumed,\s*\r?\n\s*\.replay-tag-pending`); sharedGray.MatchString(styleCSS) {
		t.Fatal("style.css: consumed and pending tags must not share a selector")
	}
	if matchStart := strings.Index(styleCSS, ".match-layout"); matchStart >= 0 {
		matchRegion := styleCSS[matchStart:]
		if strings.Contains(matchRegion, "font-size: var(--fs-xs)") {
			t.Fatal("style.css: the match tab must never drop below 15px (--fs-sm); the 14px fs-xs scale is not allowed in the match region")
		}
	}
	if !strings.Contains(renderJS, "function renderReplayMatch(resp, ctx, keepScroll)") ||
		!strings.Contains(renderJS, "scrollPos = keepScroll && listEl ? listEl.scrollTop : 0") ||
		!strings.Contains(renderJS, "newList.scrollTop = scrollPos") {
		t.Fatal("render.js: renderReplayMatch must preserve the candidate list scroll across selection re-renders (keepScroll)")
	}
	if !strings.Contains(appJS, "renderReplayMatch(resp, _matchEventCtx, true)") {
		t.Fatal("app.js: selecting a candidate must re-render with keepScroll so the list does not jump to the top")
	}
	if !strings.Contains(renderJS, "function buildCandidateRows(entries, scope, selectedId)") ||
		!strings.Contains(renderJS, "function renderMatchCandidates(resp, ctx)") ||
		!strings.Contains(renderJS, "match-candidate-top") {
		t.Fatal("render.js: the match tab needs a rows-only candidate renderer (buildCandidateRows/renderMatchCandidates) so search does not rebuild the input and lose its text and focus")
	}
	if !strings.Contains(renderJS, "const entries = resp.entries || []") || strings.Contains(renderJS, "!resp.entries") {
		t.Fatal("render.js: an empty candidate scope (entries null/absent) must render the full match tab chrome with an empty list; only a failed request (!resp) may show the 'No candidates available' fallback")
	}
	if !strings.Contains(appJS, "renderMatchCandidates(_matchResp, _matchEventCtx)") {
		t.Fatal("app.js: the search path must re-render only the candidate rows, not the whole match tab")
	}
	if !strings.Contains(appJS, "_matchResp = { ...resp, q: q || '' }") {
		t.Fatal("app.js: the applied query must be echoed into the response so full re-renders keep the search text in the input")
	}
	if !strings.Contains(styleCSS, ".match-candidate-top") {
		t.Fatal("style.css: .match-candidate-top is required for the unified row layout (entry + tag on one line, URL below)")
	}
	if !strings.Contains(renderJS, "match-search-wrap") ||
		!strings.Contains(renderJS, "match-search-clear") ||
		!strings.Contains(renderJS, "match-search-clear${resp.q ? '' : ' hidden'}") ||
		!strings.Contains(renderJS, "M18 6 6 18") {
		t.Fatal("render.js: the match search needs a custom clear button (always present, lucide x icon) toggled by the query")
	}
	if !strings.Contains(styleCSS, ".match-search::-webkit-search-cancel-button") ||
		!strings.Contains(styleCSS, ".match-search-clear.hidden") ||
		!strings.Contains(styleCSS, "padding: var(--sp-4) var(--sp-20) var(--sp-4) var(--sp-8);") {
		t.Fatal("style.css: the match search needs the native clear button hidden and a custom one with room to the right (sp-20) so long text does not run into the x")
	}
	if !strings.Contains(appJS, "classList.toggle('hidden', !e.target.value)") ||
		!strings.Contains(appJS, "case 'replay-search-clear'") ||
		!strings.Contains(appJS, "input.focus()") {
		t.Fatal("app.js: the match search needs a always-visible clear button toggled by value and a clear action that reloads with an empty query and refocuses")
	}
	if !strings.Contains(appJS, "let _matchQueries = {}") ||
		!strings.Contains(appJS, "_matchQueries[_matchState.scope] = e.target.value || ''") ||
		!strings.Contains(appJS, "_matchQueries[_matchState.scope] = input.value") ||
		!strings.Contains(appJS, "loadMatchTab(route.run, route.seq, route.scope, _matchQueries[route.scope] || '')") ||
		!strings.Contains(appJS, "_matchState.run !== run || _matchState.seq !== seq") ||
		!strings.Contains(appJS, "_matchQueries[_matchState.scope] = ''") ||
		!strings.Contains(appJS, "q: currentMatchQuery()") ||
		strings.Contains(appJS, "btn.dataset.scope, ''") {
		t.Fatal("app.js: each match scope must keep its own search query (per-scope _matchQueries, reset on event change) and selecting a candidate must keep the live query")
	}

	if strings.Contains(appJS, "resp.consumed") {
		t.Fatal("app.js: the consumed warning must be event-level - the selection path must not re-derive it from the clicked row (that toggles the banner and shifts the layout)")
	}
	if !strings.Contains(renderJS, `data-action="replay-warn-entry"`) || !strings.Contains(renderJS, "View that entry (seq ${resp.consumed.consumedBySeq})") {
		t.Fatal("render.js: the consumed banner must link to the consumed entry (replay-warn-entry), not to the selection")
	}
	if !strings.Contains(appJS, "case 'replay-warn-entry'") || !strings.Contains(appJS, "const ci = _matchResp && _matchResp.consumed") {
		t.Fatal("app.js: replay-warn-entry must open the consumed entry's full entry, not the selected candidate")
	}
	if !strings.Contains(styleCSS, ".replay-warn-link") {
		t.Fatal("style.css: the .replay-warn-link rule must style the banner's View that entry link")
	}
}

// TestReplayCandidatesEndpoint exercises the unified candidates + diff
// endpoints: a miss whose exact key was already consumed is selected by
// default and carries the already-consumed warning, the all-pending scope
// reflects the pending queue remaining after the event, and the on-demand diff
// endpoint serves per-entry diffs.
func TestReplayCandidatesEndpoint(t *testing.T) {
	s, hist, _ := newReplayServer(t)
	saveEntry(t, hist, "e1", "GET", "https://x.com/a?id=1")
	saveEntry(t, hist, "e2", "GET", "https://x.com/a?id=2")

	notify := s.ReplayNotifier()
	notify(session.ReplayEvent{Seq: 1, RunID: "run1", Method: "GET", URL: "https://x.com/a?id=1", Result: "hit", Status: 200, EntryID: "e1", Consumed: 1, Total: 2})
	notify(session.ReplayEvent{Seq: 2, RunID: "run1", Method: "GET", URL: "https://x.com/a?id=1", Result: "miss", Status: 404, Unconsumed: []session.UnconsumedEntry{{ID: "e2"}}, TotalPending: 1, Consumed: 1, Total: 2})
	notify(session.ReplayEvent{Seq: 3, RunID: "run1", Method: "GET", URL: "https://x.com/a?id=2", Result: "hit", Status: 200, EntryID: "e2", Consumed: 2, Total: 2})

	rec := httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=matching", nil), "run1", 2)
	if rec.Code != http.StatusOK {
		t.Fatalf("candidates: expected 200, got %d", rec.Code)
	}
	var resp replayCandidatesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 matching candidates, got %d", len(resp.Entries))
	}
	if resp.SelectedID != "e1" {
		t.Fatalf("miss with an already-consumed exact key must select it, got %q", resp.SelectedID)
	}
	if resp.Consumed == nil || resp.Consumed.ConsumedBySeq != 1 || resp.Consumed.Entry != 1 {
		t.Fatalf("expected consumed info for e1 by seq 1, got %+v", resp.Consumed)
	}
	if resp.Diff == nil || resp.Diff.DiffCount != 0 || !resp.Diff.HostPath.Match {
		t.Fatalf("the consumed e1 diff must be all-green, got %+v", resp.Diff)
	}
	if resp.Total["pending"] != 1 {
		t.Fatalf("expected 1 pending at the event's time, got %v", resp.Total)
	}
	byID := map[string]replayCandidate{}
	for _, c := range resp.Entries {
		byID[c.EntryID] = c
	}
	if byID["e1"].Tag != "consumed" || byID["e1"].ConsumedBySeq != 1 {
		t.Fatalf("e1 must be tagged consumed by seq 1, got %+v", byID["e1"])
	}
	if byID["e2"].Tag != "pending" || byID["e2"].DiffCount != 1 {
		t.Fatalf("e2 must be pending with 1 diff, got %+v", byID["e2"])
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=all", nil), "run1", 2)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode all: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].EntryID != "e2" {
		t.Fatalf("all pending must hold only the unconsumed e2, got %+v", resp.Entries)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/3/candidates?scope=matching", nil), "run1", 3)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode hit matching: %v", err)
	}
	if resp.SelectedID != "e2" || resp.Entries[0].Tag != "served" {
		t.Fatalf("the hit must select its served entry first, got selected=%q first=%+v", resp.SelectedID, resp.Entries)
	}
	if resp.Total["pending"] != 0 {
		t.Fatalf("after the last hit nothing remains pending, got %v", resp.Total)
	}
	if resp.Consumed != nil {
		t.Fatalf("a hit must never carry the already-consumed warning even with a consumed sibling (e1) in its pool, got %+v", resp.Consumed)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/3/candidates?scope=all", nil), "run1", 3)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode hit all: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Fatalf("all pending must exclude the entry just served by the hit, got %+v", resp.Entries)
	}
	// The JSON shape is the contract, not the decoded struct: a nil slice
	// unmarshals to len 0 too, but serializes as "entries":null, which blanks
	// the whole match tab on the frontend (it branches on the entries key).
	// An empty scope must always emit a real array.
	rawAll := rec.Body.String()
	if !strings.Contains(rawAll, `"entries":[]`) || strings.Contains(rawAll, `"entries":null`) {
		t.Fatalf("an empty candidate scope must serialize entries as [] (never null): %s", rawAll)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=matching&q=id%3D2", nil), "run1", 2)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode q: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].EntryID != "e2" || resp.SelectedID != "e2" {
		t.Fatalf("q filter must narrow to e2 and select it, got %+v", resp.Entries)
	}
	if resp.Consumed == nil || resp.Consumed.EntryID != "e1" {
		t.Fatalf("the consumed warning is event-level: it must survive a q filter that removes the consumed row, got %+v", resp.Consumed)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=matching&q=consumed", nil), "run1", 2)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode q=consumed: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].EntryID != "e1" {
		t.Fatalf("q=consumed must narrow to the consumed e1, got %+v", resp.Entries)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=matching&q=entry%202", nil), "run1", 2)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode q=entry 2: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].EntryID != "e2" {
		t.Fatalf("the entry N label must be searchable: q=entry 2 must narrow to e2, got %+v", resp.Entries)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=matching&q=2", nil), "run1", 2)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode q=2: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].EntryID != "e2" {
		t.Fatalf("a numeric query must match the entry number exactly: q=2 must narrow to e2, got %+v", resp.Entries)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidates(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates?scope=matching&q=9", nil), "run1", 2)
	resp = replayCandidatesResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode q=9: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Fatalf("q=9: no entry 9 exists and no visible surface contains 9, must fall back to an empty result, got %+v", resp.Entries)
	}

	rec = httptest.NewRecorder()
	s.handleReplayCandidateDiff(rec, httptest.NewRequest(http.MethodGet, "/api/replay/events/run1/2/candidates/e1", nil), "run1", 2, "e1")
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: expected 200, got %d", rec.Code)
	}
	var diffResp replayCandidateDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &diffResp); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if diffResp.Entry.EntryID != "e1" || diffResp.Diff == nil || diffResp.Diff.DiffCount != 0 {
		t.Fatalf("e1 diff must be all-green, got %+v", diffResp)
	}
}

// TestFilterReplayCandidates covers the match search predicate: a numeric
// query matches the entry number exactly (so "19" targets entry 19 and not the
// HLS segment timestamps that also contain "19"), falling back to the visible
// substring surface when no entry carries that number, and the surface never
// includes the scheme or host the candidate rows do not render.
func TestFilterReplayCandidates(t *testing.T) {
	seg19 := replayCandidate{EntryID: "e24", Entry: 24, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000_20260804T211902_779386.ts", Tag: "pending"}
	seg29 := replayCandidate{EntryID: "e28", Entry: 28, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000_20260804T212938_779411.ts", Tag: "pending"}
	entry19 := replayCandidate{EntryID: "e19", Entry: 19, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000.m3u8", Tag: "pending"}
	consumed := replayCandidate{EntryID: "e2", Entry: 2, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000.m3u8", Tag: "consumed", ConsumedBySeq: 7}
	pool := []replayCandidate{entry19, seg19, seg29, consumed}

	t.Run("numeric query matches the entry number exactly", func(t *testing.T) {
		got := filterReplayCandidates(pool, "19")
		if len(got) != 1 || got[0].Entry != 19 {
			t.Fatalf("q=19: want only entry 19, got %+v", got)
		}
	})

	t.Run("numeric query falls back to the URL surface without an entry hit", func(t *testing.T) {
		got := filterReplayCandidates(pool, "5000")
		if len(got) != 4 {
			t.Fatalf("q=5000: no entry 5000, must fall back to the media_5000 URL matches (all 4), got %d: %+v", len(got), got)
		}
	})

	t.Run("host and scheme are not part of the search surface", func(t *testing.T) {
		if got := filterReplayCandidates(pool, "mdstrm"); len(got) != 0 {
			t.Fatalf("q=mdstrm: the host must never match the visible-surface search, got %+v", got)
		}
		if got := filterReplayCandidates(pool, "https"); len(got) != 0 {
			t.Fatalf("q=https: the scheme must never match the visible-surface search, got %+v", got)
		}
	})

	t.Run("URL path fragments remain searchable", func(t *testing.T) {
		got := filterReplayCandidates(pool, "T2119")
		if len(got) != 1 || got[0].Entry != 24 {
			t.Fatalf("q=T2119: want only the segment carrying that timestamp, got %+v", got)
		}
	})

	t.Run("tag text is searchable", func(t *testing.T) {
		got := filterReplayCandidates(pool, "consumed by seq 7")
		if len(got) != 1 || got[0].Entry != 2 {
			t.Fatalf("q=consumed by seq 7: want the consumed entry, got %+v", got)
		}
	})

	t.Run("empty query returns the pool untouched", func(t *testing.T) {
		if got := filterReplayCandidates(pool, ""); len(got) != len(pool) {
			t.Fatalf("q=\"\": want the whole pool back, got %+v", got)
		}
	})
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
