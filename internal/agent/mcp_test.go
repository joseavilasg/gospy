package agent

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gospy/internal/history"
	"gospy/internal/session"
)

func emptyCert() tls.Certificate { return tls.Certificate{} }

type rpcContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	IsError bool   `json:"isError"`
}

type rpcResult struct {
	Content []rpcContent `json:"content"`
	IsError bool         `json:"isError"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string     `json:"jsonrpc"`
	Result  *rpcResult `json:"result"`
	Error   *rpcErr    `json:"error"`
}

func newTestMCPServer(t *testing.T, fs *mockFilterStore) (http.Handler, *history.Store) {
	t.Helper()
	hist := newTestHistory(t)
	fwd, err := NewForwarder("http://127.0.0.1:9", emptyCert())
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	srv := NewServer(NewScope(hist, fs, nil, nil), hist, fwd)
	return srv.Handler(), hist
}

func callTool(t *testing.T, h http.Handler, name string, args map[string]any) rpcResponse {
	t.Helper()
	// Real client flow: initialize first, then carry the session id on the
	// tool call (the default session manager rejects header-less requests).
	session := newTestSession(t, h)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", session)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp rpcResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// newTestSession performs the initialize handshake and returns the
// Mcp-Session-Id the server issued.
func newTestSession(t *testing.T, h http.Handler) string {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.1"},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Mcp-Session-Id"); got == "" {
		t.Fatal("initialize must return Mcp-Session-Id for session-aware clients")
	}
	return rr.Header().Get("Mcp-Session-Id")
}

// TestMCP_InitializeReturnsSessionID guards the wire contract session-aware
// clients (MCP Inspector web UI) depend on: initialize must return a
// Mcp-Session-Id even though the server keeps no session state.
func TestMCP_InitializeReturnsSessionID(t *testing.T) {
	h, _ := newTestMCPServer(t, &mockFilterStore{gate: false})
	if got := newTestSession(t, h); got == "" {
		t.Fatal("initialize must return Mcp-Session-Id for session-aware clients")
	}
}

// resultText returns the tool's text content, asserting it is not an error.
func resultText(t *testing.T, resp rpcResponse) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %d %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil || len(resp.Result.Content) == 0 {
		t.Fatalf("no result content: %+v", resp)
	}
	if resp.Result.Content[0].IsError {
		t.Fatalf("tool execution error: %s", resp.Result.Content[0].Text)
	}
	return resp.Result.Content[0].Text
}

func isErrorText(t *testing.T, resp rpcResponse) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %d %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil || len(resp.Result.Content) == 0 || !resp.Result.IsError {
		t.Fatalf("expected a tool execution error, got %+v", resp.Result)
	}
	return resp.Result.Content[0].Text
}

func TestMCP_ListEntries(t *testing.T) {
	fs := &mockFilterStore{gate: false}
	h, hist := newTestMCPServer(t, fs)

	// Gate off: tool returns explicit error.
	resp := callTool(t, h, "list_entries", map[string]any{})
	errText := isErrorText(t, resp)
	if !strings.Contains(errText, "the agent gate is disabled") {
		t.Fatalf("gate off error = %q", errText)
	}

	// Gate on: data flows.
	fs.gate = true
	saveTestEntry(t, hist, "a.com", "", 200)
	saveTestEntry(t, hist, "b.com", "", 200)
	fs.filters = history.Filters{Host: []string{"a.com"}, MatchMode: "all"}

	resp = callTool(t, h, "list_entries", map[string]any{})
	text := resultText(t, resp)
	var page ListPage
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Host != "a.com" {
		t.Fatalf("filtered list = %+v", page.Entries)
	}
	if page.VisibleCount != 1 || page.Total != 2 {
		t.Fatalf("counts = visible %d total %d", page.VisibleCount, page.Total)
	}
}

func TestMCP_GetEntry(t *testing.T) {
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	h, hist := newTestMCPServer(t, fs)

	e := &history.Entry{
		Request: history.RequestRecord{
			Method:  "GET",
			URL:     "http://a.com/x",
			Host:    "a.com",
			Headers: map[string][]string{"Authorization": {"Bearer tok"}},
		},
		Response: &history.ResponseRecord{
			Status:  200,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
			Body:    "hello",
		},
	}
	if err := hist.Save(e); err != nil {
		t.Fatalf("save: %v", err)
	}
	hidden := saveTestEntry(t, hist, "b.com", "", 200)

	resp := callTool(t, h, "get_entry", map[string]any{"id": e.ID})
	text := resultText(t, resp)
	var got history.Entry
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Request.Headers["Authorization"][0] != "<redacted len=10>" {
		t.Errorf("Authorization not redacted: %v", got.Request.Headers["Authorization"])
	}
	if got.Response.Headers["Content-Type"][0] != "text/plain" || got.Response.Body != "hello" {
		t.Errorf("response content lost: %+v", got.Response)
	}

	// Entries outside the visible set must be rejected.
	errText := isErrorText(t, callTool(t, h, "get_entry", map[string]any{"id": hidden.ID}))
	if !strings.Contains(errText, "not in the agent-visible set") {
		t.Errorf("hidden entry error = %q", errText)
	}

	// Gate off rejects everything.
	fs.gate = false
	errText = isErrorText(t, callTool(t, h, "get_entry", map[string]any{"id": e.ID}))
	if !strings.Contains(errText, "the agent gate is disabled") {
		t.Errorf("gate off error = %q", errText)
	}
}

func TestMCP_SendRequestGate(t *testing.T) {
	fs := &mockFilterStore{gate: false}
	h, _ := newTestMCPServer(t, fs)

	// A well-formed template reaches the handler, which rejects on the gate.
	errText := isErrorText(t, callTool(t, h, "send_request", map[string]any{"template": "x"}))
	if !strings.Contains(errText, "gate is disabled") {
		t.Errorf("gate off error = %q", errText)
	}
}

func TestMCP_GateBlocksAllTools(t *testing.T) {
	gateErr := "the agent gate is disabled, request the user to enable it"

	t.Run("record_tools", func(t *testing.T) {
		fs := &mockFilterStore{gate: false}
		h, hist := newTestMCPServer(t, fs)
		_ = hist

		cases := []struct {
			name string
			args map[string]any
		}{
			{"list_entries", map[string]any{}},
			{"list_visible_hosts", map[string]any{}},
			{"list_entry_filter_values", map[string]any{"type": "host"}},
			{"send_request", map[string]any{"template": "x"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp := callTool(t, h, tc.name, tc.args)
				msg := isErrorText(t, resp)
				if !strings.Contains(msg, gateErr) {
					t.Errorf("%s with gate off: expected gate error, got %q", tc.name, msg)
				}
			})
		}

		// get_entry needs an entry id - create one with gate on, then flip off.
		fs.gate = true
		saveTestEntry(t, hist, "a.com", "", 200)
		fs.gate = false
		resp := callTool(t, h, "get_entry", map[string]any{"id": "1"})
		msg := isErrorText(t, resp)
		if !strings.Contains(msg, gateErr) {
			t.Errorf("get_entry with gate off: expected gate error, got %q", msg)
		}
	})

	t.Run("replay_tools", func(t *testing.T) {
		hist := newTestHistory(t)
		fs := &mockFilterStore{gate: false}
		srv := NewServer(NewScope(hist, fs, nil, nil), hist, nil)
		srv.SetReplayAnalyzer(&mockReplayAnalyzer{
			events: []session.ReplayEvent{{Seq: 1, Result: "miss", Method: "GET", URL: "http://a.com/", Status: 0, Timestamp: time.Now()}},
			runID:  "test-run",
		})
		h := srv.Handler()

		cases := []struct {
			name string
			args map[string]any
		}{
			{"list_replay_runs", map[string]any{}},
			{"list_replay_events", map[string]any{}},
			{"get_replay_event", map[string]any{"eventId": float64(1)}},
			{"list_replay_candidates", map[string]any{"eventId": float64(1)}},
			{"replay_diff", map[string]any{"eventId": float64(1), "entryId": "1"}},
			{"list_replay_filter_values", map[string]any{"type": "result"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp := callTool(t, h, tc.name, tc.args)
				msg := isErrorText(t, resp)
				if !strings.Contains(msg, gateErr) {
					t.Errorf("%s with gate off: expected gate error, got %q", tc.name, msg)
				}
			})
		}
	})
}

func TestMCP_SendRequest_RequiresTemplate(t *testing.T) {
	fs := &mockFilterStore{gate: true}
	h, _ := newTestMCPServer(t, fs)

	// The tool schema enforces template as required at the API edge.
	errText := isErrorText(t, callTool(t, h, "send_request", map[string]any{}))
	if !strings.Contains(errText, "Missing") {
		t.Errorf("missing template error = %q", errText)
	}
}

func TestMCP_SendRequest_TemplateNotVisible(t *testing.T) {
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	h, hist := newTestMCPServer(t, fs)
	hidden := saveTestEntry(t, hist, "b.com", "", 200)

	errText := isErrorText(t, callTool(t, h, "send_request", map[string]any{"template": hidden.ID}))
	if !strings.Contains(errText, "not in the agent's visible set") {
		t.Errorf("hidden template error = %q", errText)
	}
}

// TestMCP_SendRequest exercises the vaulted template path end-to-end: the proxy
// simulator captures the forwarded request (reading the X-Gospy-Agent call ID,
// like the real interceptor) into the history store, so the response carries the
// new entry ID and get_entry can read it right back.
func TestMCP_SendRequest(t *testing.T) {
	hist := newTestHistory(t)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}

	tmpl := &history.Entry{
		Request: history.RequestRecord{
			Method: "POST",
			URL:    "http://a.com/api/original",
			Host:   "a.com",
			Headers: map[string][]string{
				"Authorization": {"Bearer vault-secret"},
				"Content-Type":  {"application/json"},
				"X-Business":    {"keep-me"},
			},
			Body: `{"orig":true}`,
		},
	}
	if err := hist.Save(tmpl); err != nil {
		t.Fatalf("save template: %v", err)
	}

	var capturedCallID, capturedURL, capturedMethod, capturedAuth, capturedOverride string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCallID = r.Header.Get(agentHeader)
		capturedURL = r.URL.String()
		capturedMethod = r.Method
		capturedAuth = r.Header.Get("Authorization")
		capturedOverride = r.Header.Get("X-Override")
		entry := &history.Entry{
			Request: history.RequestRecord{
				Method:  r.Method,
				URL:     "http://" + r.Host + r.URL.String(),
				Host:    r.Host,
				Headers: r.Header.Clone(),
			},
			Response: &history.ResponseRecord{
				Status:  200,
				Headers: map[string][]string{"Content-Type": {"application/json"}},
				Body:    `{"from":"origin"}`,
			},
			Origin:      "agent",
			AgentCallID: capturedCallID,
		}
		if err := hist.Save(entry); err != nil {
			t.Errorf("save captured entry: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"from":"origin"}`)
	}))
	defer proxy.Close()

	fwd, err := NewForwarder(proxy.URL, emptyCert())
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	srv := NewServer(NewScope(hist, fs, nil, nil), hist, fwd)
	h := srv.Handler()

	resp := callTool(t, h, "send_request", map[string]any{
		"template": tmpl.ID,
		"path":     "/api/new",
		"headers":  `{"X-Override": "yes"}`,
		"body":     `{"new":true}`,
	})
	text := resultText(t, resp)
	var out ForwardResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The vaulted auth reached the wire without ever being exposed.
	if capturedAuth != "Bearer vault-secret" {
		t.Errorf("vault auth did not reach the origin: %q", capturedAuth)
	}
	if capturedOverride != "yes" {
		t.Errorf("override header not applied: %q", capturedOverride)
	}
	if capturedMethod != "POST" || capturedURL != "http://a.com/api/new" {
		t.Errorf("forwarded = %s %s", capturedMethod, capturedURL)
	}
	if out.EntryID == "" {
		t.Fatal("send_request must return the captured entry id")
	}
	if out.Request.Method != "POST" || out.Request.URL != "http://a.com/api/new" || out.Request.BodySource != "override" {
		t.Errorf("echo = %+v", out.Request)
	}
	if out.Status != 200 || out.Body != `{"from":"origin"}` {
		t.Errorf("response = %d %q", out.Status, out.Body)
	}

	// The captured entry resolves and get_entry reads it back (the agent-origin
	// entry matches the same host filter - no origin bypass).
	entryResp := callTool(t, h, "get_entry", map[string]any{"id": out.EntryID})
	text = resultText(t, entryResp)
	var got history.Entry
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	if got.Request.Headers["Authorization"][0] != "<redacted len=19>" {
		t.Errorf("captured entry auth not redacted: %v", got.Request.Headers["Authorization"])
	}
	if got.Response.Body != `{"from":"origin"}` {
		t.Errorf("captured response lost: %q", got.Response.Body)
	}
}

func TestMCP_ListVisibleHosts(t *testing.T) {
	fs := &mockFilterStore{gate: false}
	h, hist := newTestMCPServer(t, fs)

	// Gate off: explicit error.
	resp := callTool(t, h, "list_visible_hosts", map[string]any{})
	errText := isErrorText(t, resp)
	if !strings.Contains(errText, "the agent gate is disabled") {
		t.Fatalf("gate off error = %q", errText)
	}

	// Gate on: data flows.
	fs.gate = true
	saveTestEntry(t, hist, "b.com", "", 200)
	saveTestEntry(t, hist, "a.com", "", 200)

	resp = callTool(t, h, "list_visible_hosts", map[string]any{})
	text := resultText(t, resp)
	var out struct {
		Hosts []string `json:"hosts"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || out.Hosts[0] != "a.com" || out.Hosts[1] != "b.com" {
		t.Fatalf("hosts = %v (count %d)", out.Hosts, out.Count)
	}
}

func TestMCP_PaginationIsNonBypassable(t *testing.T) {
	fs := &mockFilterStore{gate: true}
	h, hist := newTestMCPServer(t, fs)
	for i := 0; i < 3; i++ {
		saveTestEntry(t, hist, "a.com", "", 200)
	}

	// The tool schema enforces the cap (max 200, min offset 0) at the API edge.
	for _, args := range []map[string]any{{"limit": 500}, {"limit": 201}, {"offset": -1}} {
		errText := isErrorText(t, callTool(t, h, "list_entries", args))
		if !strings.Contains(errText, "failed") {
			t.Errorf("args %v: schema rejection = %q", args, errText)
		}
	}

	// The scope clamps server-side as the backstop (covered by pageLimits), so
	// a well-formed call pages correctly.
	resp := callTool(t, h, "list_entries", map[string]any{"limit": 2})
	text := resultText(t, resp)
	var page ListPage
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Entries) != 2 || page.VisibleCount != 3 || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
}

// TestMCP_SendRequest_InheritsTemplateQuery is the regression for the
// empty-query bug: parseRequestSpec used to turn an absent query argument into
// an empty map, which cleared the template's query instead of inheriting it.
func TestMCP_SendRequest_InheritsTemplateQuery(t *testing.T) {
	hist := newTestHistory(t)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}

	tmpl := &history.Entry{
		Request: history.RequestRecord{
			Method: "GET",
			URL:    "http://a.com/api?token=abc&n=1",
			Host:   "a.com",
		},
	}
	if err := hist.Save(tmpl); err != nil {
		t.Fatalf("save template: %v", err)
	}

	var capturedURL, capturedMethod string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		capturedMethod = r.Method
		w.WriteHeader(200)
	}))
	defer proxy.Close()

	fwd, err := NewForwarder(proxy.URL, emptyCert())
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	srv := NewServer(NewScope(hist, fs, nil, nil), hist, fwd)
	h := srv.Handler()

	resp := callTool(t, h, "send_request", map[string]any{"template": tmpl.ID})
	text := resultText(t, resp)
	var out ForwardResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if capturedMethod != "GET" || capturedURL != "http://a.com/api?token=abc&n=1" {
		t.Errorf("forwarded = %s %s, want GET http://a.com/api?token=abc&n=1", capturedMethod, capturedURL)
	}
	if out.Request.BodySource != "template" {
		t.Errorf("bodySource = %q, want template", out.Request.BodySource)
	}
}

// TestMCP_SendRequest_QueryOverride checks that a provided query replaces the
// template's, and that empty and missing are equivalent (both inherit).
func TestMCP_SendRequest_QueryOverride(t *testing.T) {
	hist := newTestHistory(t)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}

	tmpl := &history.Entry{
		Request: history.RequestRecord{
			Method: "GET",
			URL:    "http://a.com/api?token=abc",
			Host:   "a.com",
		},
	}
	if err := hist.Save(tmpl); err != nil {
		t.Fatalf("save template: %v", err)
	}

	var capturedURLs []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURLs = append(capturedURLs, r.URL.String())
		w.WriteHeader(200)
	}))
	defer proxy.Close()

	fwd, err := NewForwarder(proxy.URL, emptyCert())
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	srv := NewServer(NewScope(hist, fs, nil, nil), hist, fwd)
	h := srv.Handler()

	for _, args := range []map[string]any{
		{"template": tmpl.ID},
		{"template": tmpl.ID, "query": ""},
		{"template": tmpl.ID, "query": "x=2&y=3"},
	} {
		resp := callTool(t, h, "send_request", args)
		if resp.Result == nil || resp.Error != nil {
			t.Fatalf("call %v failed: %+v", args, resp)
		}
	}

	want := []string{
		"http://a.com/api?token=abc",
		"http://a.com/api?token=abc",
		"http://a.com/api?x=2&y=3",
	}
	if !reflect.DeepEqual(capturedURLs, want) {
		t.Errorf("captured = %v, want %v", capturedURLs, want)
	}
}

func TestParseQueryArg(t *testing.T) {
	if got, err := parseQueryArg(map[string]any{}); err != nil || got != nil {
		t.Errorf("absent: got=%v err=%v, want nil", got, err)
	}
	if got, err := parseQueryArg(map[string]any{"query": ""}); err != nil || got != nil {
		t.Errorf("empty: got=%v err=%v, want nil", got, err)
	}
	got, err := parseQueryArg(map[string]any{"query": "a=1&b=2&a=3"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string][]string{"a": {"1", "3"}, "b": {"2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
	if _, err := parseQueryArg(map[string]any{"query": 42}); err == nil {
		t.Error("non-string query accepted")
	}
}

func TestParseHeadersArg(t *testing.T) {
	if got, err := parseHeadersArg(map[string]any{}); err != nil || got != nil {
		t.Errorf("absent: got=%v err=%v, want nil", got, err)
	}
	if got, err := parseHeadersArg(map[string]any{"headers": ""}); err != nil || got != nil {
		t.Errorf("empty: got=%v err=%v, want nil", got, err)
	}
	got, err := parseHeadersArg(map[string]any{"headers": `{"X-One":"a","X-Two":["1","2"]}`})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string][]string{"X-One": {"a"}, "X-Two": {"1", "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("headers = %v, want %v", got, want)
	}
	if _, err := parseHeadersArg(map[string]any{"headers": "{invalid"}); err == nil {
		t.Error("invalid JSON accepted")
	}
	if _, err := parseHeadersArg(map[string]any{"headers": `{"X":"a", "Y": 5}`}); err == nil {
		t.Error("non-string value accepted")
	}
}

func TestMCP_ListEntriesFilterArgs(t *testing.T) {
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"sonarcloud.io"}, MatchMode: "all"}}
	h, hist := newTestMCPServer(t, fs)
	saveTestEntryFull(t, hist, "GET", "sonarcloud.io", "/api/issues/search", 200)
	saveTestEntryFull(t, hist, "GET", "sonarcloud.io", "/api/rules/show", 200)
	saveTestEntryFull(t, hist, "POST", "sonarcloud.io", "/api/projects/create", 500)
	saveTestEntryFull(t, hist, "GET", "github.com", "/api/pulls", 200)

	decode := func(resp rpcResponse) ListPage {
		t.Helper()
		var page ListPage
		if err := json.Unmarshal([]byte(resultText(t, resp)), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return page
	}

	// Free-text path substring + exact method narrow within the profile.
	page := decode(callTool(t, h, "list_entries", map[string]any{"path": "/api/issues/", "method": "GET", "limit": 50}))
	if len(page.Entries) != 1 || page.Entries[0].Host != "sonarcloud.io" {
		t.Fatalf("expected the sonarcloud.io /api/issues/ entry, got %+v", page.Entries)
	}

	// Comma-separated exact values OR within the field.
	page = decode(callTool(t, h, "list_entries", map[string]any{"method": "GET,POST", "limit": 50}))
	if len(page.Entries) != 3 {
		t.Fatalf("GET,POST OR expected 3 entries, got %+v", page.Entries)
	}

	// Status narrows to the 500 entry.
	page = decode(callTool(t, h, "list_entries", map[string]any{"status": "500", "limit": 50}))
	if len(page.Entries) != 1 || page.Entries[0].Status == nil || *page.Entries[0].Status != 500 {
		t.Fatalf("status 500 filter expected the POST entry, got %+v", page.Entries)
	}

	// A host outside the profile must never widen the scope.
	page = decode(callTool(t, h, "list_entries", map[string]any{"host": "github.com", "limit": 50}))
	if len(page.Entries) != 0 {
		t.Fatalf("host outside profile leaked: %+v", page.Entries)
	}
}

func TestMCP_ListEntriesInvalidStatus(t *testing.T) {
	fs := &mockFilterStore{gate: true}
	h, _ := newTestMCPServer(t, fs)
	resp := callTool(t, h, "list_entries", map[string]any{"status": "abc"})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "status values must be integer") {
		t.Fatalf("expected a status validation error, got %q", msg)
	}
}

func TestMCP_ListFilterValues(t *testing.T) {
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"sonarcloud.io"}, MatchMode: "all"}}
	h, hist := newTestMCPServer(t, fs)
	saveTestEntryFull(t, hist, "GET", "sonarcloud.io", "/api/issues", 200)
	saveTestEntryFull(t, hist, "POST", "sonarcloud.io", "/api/projects", 500)
	saveTestEntryFull(t, hist, "GET", "github.com", "/api/pulls", 200)

	var payload struct {
		Type   string                `json:"type"`
		Values []history.OptionCount `json:"values"`
	}
	unmarshal := func(resp rpcResponse) {
		t.Helper()
		if err := json.Unmarshal([]byte(resultText(t, resp)), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}

	// Scoped to the visible set: github.com must not leak.
	unmarshal(callTool(t, h, "list_entry_filter_values", map[string]any{"type": "method"}))
	if payload.Type != "method" || len(payload.Values) != 2 {
		t.Fatalf("list_entry_filter_values(method) = %+v, want 2 scoped values", payload)
	}
	if payload.Values[0].Value != "GET" || payload.Values[0].Count != 1 || payload.Values[1].Value != "POST" || payload.Values[1].Count != 1 {
		t.Fatalf("unexpected method values: %+v", payload.Values)
	}

	// Unknown type is a clear error.
	msg := isErrorText(t, callTool(t, h, "list_entry_filter_values", map[string]any{"type": "bogus"}))
	if !strings.Contains(msg, "unknown filter type") {
		t.Fatalf("expected an unknown-type error, got %q", msg)
	}

	// Gate off: explicit error.
	fs.gate = false
	errText := isErrorText(t, callTool(t, h, "list_entry_filter_values", map[string]any{"type": "host"}))
	if !strings.Contains(errText, "the agent gate is disabled") {
		t.Fatalf("gate off error = %q", errText)
	}
}

func TestMCP_ListEntriesTimeRange(t *testing.T) {
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"sonarcloud.io"}, MatchMode: "all"}}
	h, hist := newTestMCPServer(t, fs)

	now := time.Now()
	saveAt := func(path string, ts time.Time) {
		t.Helper()
		e := &history.Entry{
			Request: history.RequestRecord{
				Method:  "GET",
				URL:     "http://sonarcloud.io" + path,
				Host:    "sonarcloud.io",
				Headers: map[string][]string{},
			},
			Response:  &history.ResponseRecord{Status: 200},
			Timestamp: ts,
		}
		if err := hist.Save(e); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	saveAt("/old", now.Add(-2*time.Hour))
	saveAt("/recent", now)
	saveAt("/future", now.Add(2*time.Hour))

	decode := func(resp rpcResponse) ListPage {
		t.Helper()
		var page ListPage
		if err := json.Unmarshal([]byte(resultText(t, resp)), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return page
	}

	// From narrows: the old entry drops out.
	page := decode(callTool(t, h, "list_entries", map[string]any{"from": now.Add(-1 * time.Hour).Format(time.RFC3339), "limit": 50}))
	if len(page.Entries) != 2 {
		t.Fatalf("from bound expected 2 entries, got %+v", page.Entries)
	}

	// To narrows: the future entry drops out.
	page = decode(callTool(t, h, "list_entries", map[string]any{"to": now.Add(1 * time.Hour).Format(time.RFC3339), "limit": 50}))
	if len(page.Entries) != 2 {
		t.Fatalf("to bound expected 2 entries, got %+v", page.Entries)
	}

	// Combined window keeps only the recent entry.
	page = decode(callTool(t, h, "list_entries", map[string]any{"from": now.Add(-1 * time.Hour).Format(time.RFC3339), "to": now.Add(1 * time.Hour).Format(time.RFC3339), "limit": 50}))
	if len(page.Entries) != 1 {
		t.Fatalf("window expected 1 entry, got %+v", page.Entries)
	}
}

func TestMCP_ListEntriesInvalidTimeBound(t *testing.T) {
	fs := &mockFilterStore{gate: true}
	h, _ := newTestMCPServer(t, fs)
	resp := callTool(t, h, "list_entries", map[string]any{"from": "not-a-time"})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "must be an RFC3339 instant or a local wall-clock") {
		t.Fatalf("expected a time validation error, got %q", msg)
	}
}

// --- replay tool tests ---

type mockReplayAnalyzer struct {
	events []session.ReplayEvent
	runID  string
}

func (m *mockReplayAnalyzer) ActiveRunID() string { return m.runID }
func (m *mockReplayAnalyzer) ListReplayRuns() ([]session.RunSummary, error) {
	return []session.RunSummary{{RunID: m.runID, Active: true}}, nil
}
func (m *mockReplayAnalyzer) ReplayEvents(runID string) ([]session.ReplayEvent, error) {
	return m.events, nil
}
func (m *mockReplayAnalyzer) ReplayEventDetail(runID string, seq int) (*session.ReplayEvent, error) {
	for i := range m.events {
		if m.events[i].Seq == seq {
			return &m.events[i], nil
		}
	}
	return nil, fmt.Errorf("event with seq %d not found in run %s", seq, runID)
}
func (m *mockReplayAnalyzer) MatchConfigFor(runID string) (*session.MatchConfig, error) {
	return &session.MatchConfig{}, nil
}

func newTestReplayMCPServer(t *testing.T, events []session.ReplayEvent, runID string) http.Handler {
	t.Helper()
	hist := newTestHistory(t)
	srv := NewServer(NewScope(hist, &mockFilterStore{gate: true}, nil, nil), hist, nil)
	srv.SetReplayAnalyzer(&mockReplayAnalyzer{events: events, runID: runID})
	return srv.Handler()
}

func TestMCP_ListReplayEvents(t *testing.T) {
	now := time.Now()
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", Method: "GET", URL: "http://example.com/a", Status: 200, Timestamp: now},
		{Seq: 2, Result: "miss", Method: "POST", URL: "http://example.com/b", Status: 0, Timestamp: now},
		{Seq: 3, Result: "exhausted", Method: "GET", URL: "http://cdn.example.com/c", Status: 0, Exhausted: true, Timestamp: now},
	}
	h := newTestReplayMCPServer(t, events, "run-42")

	// No filters - all events.
	resp := callTool(t, h, "list_replay_events", map[string]any{})
	text := resultText(t, resp)
	if !strings.Contains(text, "\"runId\":\"run-42\"") {
		t.Fatalf("expected runId in response, got %s", text)
	}
	if !strings.Contains(text, "\"total\":3") {
		t.Fatalf("expected total=3, got %s", text)
	}
	if strings.Contains(text, "\"hasMore\":true") {
		t.Fatalf("should not have more with 3 events and default limit, got %s", text)
	}

	// Filter by result=miss.
	resp = callTool(t, h, "list_replay_events", map[string]any{"result": "miss"})
	text = resultText(t, resp)
	if !strings.Contains(text, "\"total\":1") {
		t.Fatalf("expected total=1 for miss filter, got %s", text)
	}
	if !strings.Contains(text, "\"result\":\"miss\"") {
		t.Fatalf("expected miss event, got %s", text)
	}

	// Filter by host.
	resp = callTool(t, h, "list_replay_events", map[string]any{"host": "cdn.example.com"})
	text = resultText(t, resp)
	if !strings.Contains(text, "\"total\":1") {
		t.Fatalf("expected total=1 for host filter, got %s", text)
	}
	if !strings.Contains(text, "cdn.example.com") {
		t.Fatalf("expected cdn event, got %s", text)
	}

	// Pagination: limit=2.
	resp = callTool(t, h, "list_replay_events", map[string]any{"limit": float64(2)})
	text = resultText(t, resp)
	if !strings.Contains(text, "\"events\"") {
		t.Fatalf("expected events array, got %s", text)
	}
	if !strings.Contains(text, "\"total\":3") {
		t.Fatalf("expected total=3, got %s", text)
	}
	if !strings.Contains(text, "\"hasMore\":true") {
		t.Fatalf("expected hasMore=true, got %s", text)
	}

	// Pagination: offset=2, limit=2 → 1 event.
	resp = callTool(t, h, "list_replay_events", map[string]any{"offset": float64(2), "limit": float64(2)})
	text = resultText(t, resp)
	if strings.Contains(text, "\"hasMore\":true") {
		t.Fatalf("expected hasMore=false, got %s", text)
	}
}

func TestMCP_ListReplayEvents_NoReplay(t *testing.T) {
	h, _ := newTestMCPServer(t, &mockFilterStore{gate: true})
	resp := callTool(t, h, "list_replay_events", map[string]any{})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "replay tools are only available in replay mode") {
		t.Fatalf("expected replay-mode error, got %q", msg)
	}
}

func TestMCP_GetReplayEvent(t *testing.T) {
	now := time.Now()
	events := []session.ReplayEvent{
		{Seq: 1, Result: "miss", Method: "POST", URL: "http://example.com/b?id=abc&quality=high", Status: 0, Timestamp: now},
	}
	h := newTestReplayMCPServer(t, events, "run-42")
	resp := callTool(t, h, "get_replay_event", map[string]any{"eventId": float64(1)})
	text := resultText(t, resp)
	if !strings.Contains(text, "\"result\":\"miss\"") {
		t.Fatalf("expected miss, got %s", text)
	}
	if !strings.Contains(text, "\"method\":\"POST\"") {
		t.Fatalf("expected method POST, got %s", text)
	}
	if !strings.Contains(text, "\"id\":1") {
		t.Fatalf("expected id:1, got %s", text)
	}
	// consumed/total must not appear in the detail response.
	if strings.Contains(text, "\"consumed\"") || strings.Contains(text, "\"total\"") {
		t.Fatalf("consumed/total must not appear in event detail, got %s", text)
	}
}

func TestMCP_GetReplayEvent_NotFound(t *testing.T) {
	h := newTestReplayMCPServer(t, nil, "run-42")
	resp := callTool(t, h, "get_replay_event", map[string]any{"eventId": float64(99)})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not-found error, got %q", msg)
	}
}

func TestMCP_GetReplayEvent_MissingEventId(t *testing.T) {
	h := newTestReplayMCPServer(t, nil, "run-42")
	resp := callTool(t, h, "get_replay_event", map[string]any{})
	// MCP schema validation catches missing required 'eventId' before the handler runs.
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "Missing") || !strings.Contains(msg, "eventId") {
		t.Fatalf("expected missing-eventId validation error, got %q", msg)
	}
}

func TestMCP_ListReplayCandidates(t *testing.T) {
	now := time.Now()
	events := []session.ReplayEvent{
		{Seq: 1, Result: "miss", Method: "GET", URL: "http://example.com/a", Status: 0, Consumed: 1, Total: 1, Timestamp: now},
	}
	h := newTestReplayMCPServer(t, events, "run-42")
	resp := callTool(t, h, "list_replay_candidates", map[string]any{"eventId": float64(1), "tag": "pending"})
	text := resultText(t, resp)
	if !strings.Contains(text, "\"tag\":\"pending\"") {
		t.Fatalf("expected the tag filter echoed, got %s", text)
	}
	if strings.Contains(text, "scope") {
		t.Fatalf("the scope param is gone; expected no scope key in %s", text)
	}
}

func TestMCP_ListReplayCandidates_NoReplay(t *testing.T) {
	h, _ := newTestMCPServer(t, &mockFilterStore{gate: true})
	resp := callTool(t, h, "list_replay_candidates", map[string]any{"eventId": float64(1)})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "replay tools are only available in replay mode") {
		t.Fatalf("expected replay-mode error, got %q", msg)
	}
}

func TestMCP_ListReplayFilterValues(t *testing.T) {
	now := time.Now()
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", Method: "GET", URL: "http://example.com/a", Status: 200, Timestamp: now},
		{Seq: 2, Result: "miss", Method: "POST", URL: "http://example.com/b", Status: 0, Timestamp: now},
		{Seq: 3, Result: "exhausted", Method: "GET", URL: "http://cdn.example.com/c", Status: 0, Exhausted: true, Timestamp: now},
		{Seq: 4, Result: "miss", Method: "GET", URL: "http://cdn.example.com/d", Status: 0, Timestamp: now},
	}
	h := newTestReplayMCPServer(t, events, "run-42")

	// result: 1 hit, 2 miss, 1 exhausted.
	resp := callTool(t, h, "list_replay_filter_values", map[string]any{"type": "result"})
	text := resultText(t, resp)
	if !strings.Contains(text, "\"type\":\"result\"") {
		t.Fatalf("expected type=result, got %s", text)
	}
	if !strings.Contains(text, "\"value\":\"miss\"") || !strings.Contains(text, "\"count\":2") {
		t.Fatalf("expected miss count=2, got %s", text)
	}
	if !strings.Contains(text, "\"value\":\"hit\"") || !strings.Contains(text, "\"count\":1") {
		t.Fatalf("expected hit count=1, got %s", text)
	}

	// host: example.com (2), cdn.example.com (2).
	resp = callTool(t, h, "list_replay_filter_values", map[string]any{"type": "host"})
	text = resultText(t, resp)
	if !strings.Contains(text, "\"value\":\"example.com\"") {
		t.Fatalf("expected example.com, got %s", text)
	}
	if !strings.Contains(text, "\"value\":\"cdn.example.com\"") {
		t.Fatalf("expected cdn.example.com, got %s", text)
	}

	// method: GET (3), POST (1).
	resp = callTool(t, h, "list_replay_filter_values", map[string]any{"type": "method"})
	text = resultText(t, resp)
	if !strings.Contains(text, "\"value\":\"GET\"") || !strings.Contains(text, "\"count\":3") {
		t.Fatalf("expected GET count=3, got %s", text)
	}

	// Unknown type.
	resp = callTool(t, h, "list_replay_filter_values", map[string]any{"type": "bogus"})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "unknown filter type") {
		t.Fatalf("expected unknown-type error, got %q", msg)
	}
}

func TestMCP_ListReplayFilterValues_NoReplay(t *testing.T) {
	h, _ := newTestMCPServer(t, &mockFilterStore{gate: true})
	resp := callTool(t, h, "list_replay_filter_values", map[string]any{"type": "result"})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "replay tools are only available in replay mode") {
		t.Fatalf("expected replay-mode error, got %q", msg)
	}
}

func TestMCP_SendRequestBlockedInReplay(t *testing.T) {
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", Method: "GET", URL: "http://example.com/", Status: 200, Timestamp: time.Now()},
	}
	h := newTestReplayMCPServer(t, events, "run-42")
	resp := callTool(t, h, "send_request", map[string]any{
		"template": "http://example.com/",
		"method":   "GET",
	})
	msg := isErrorText(t, resp)
	if !strings.Contains(msg, "send_request is not available in replay mode") {
		t.Fatalf("expected replay-mode block, got %q", msg)
	}
}

func TestMCP_ListReplayCandidates_WithHistory(t *testing.T) {
	now := time.Now()
	events := []session.ReplayEvent{
		{Seq: 1, Result: "miss", Method: "GET", URL: "http://example.com/a?x=1", Status: 404, Timestamp: now, RunID: "run-42"},
	}
	hist := newTestHistory(t)
	// Recorded entry sharing host+path with the miss → potentialMatch.
	saveTestEntryFull(t, hist, "GET", "example.com", "/a?x=1", 200)
	saveTestEntryFull(t, hist, "GET", "example.com", "/b", 200)
	fs := &mockFilterStore{gate: true}
	srv := NewServer(NewScope(hist, fs, nil, nil), hist, nil)
	srv.SetReplayAnalyzer(&mockReplayAnalyzer{events: events, runID: "run-42"})
	h := srv.Handler()

	resp := callTool(t, h, "list_replay_candidates", map[string]any{"eventId": float64(1), "potentialMatch": true})
	text := resultText(t, resp)
	if !strings.Contains(text, "\"potentialMatch\":true") {
		t.Fatalf("expected potentialMatch filter echoed, got %s", text)
	}
	// The potentialMatch universe should contain /a but not /b.
	if !strings.Contains(text, "/a") {
		t.Fatalf("expected candidate /a in response, got %s", text)
	}
	if strings.Contains(text, "\"url\":\"http://example.com/b\"") {
		t.Fatalf("potentialMatch=true must not include /b, got %s", text)
	}
}

func TestMCP_ReplayDiff_WithHistory(t *testing.T) {
	now := time.Now()
	events := []session.ReplayEvent{
		{Seq: 1, Result: "miss", Method: "GET", URL: "http://example.com/a?x=1&y=2", Status: 404, Timestamp: now, RunID: "run-42"},
	}
	hist := newTestHistory(t)
	e := saveTestEntryFull(t, hist, "GET", "example.com", "/a?x=1&y=9", 200)
	fs := &mockFilterStore{gate: true}
	srv := NewServer(NewScope(hist, fs, nil, nil), hist, nil)
	srv.SetReplayAnalyzer(&mockReplayAnalyzer{events: events, runID: "run-42"})
	h := srv.Handler()

	resp := callTool(t, h, "replay_diff", map[string]any{"eventId": float64(1), "entryId": e.ID})
	text := resultText(t, resp)
	if !strings.Contains(text, "\"entryId\":\""+e.ID+"\"") {
		t.Fatalf("expected entryId echoed, got %s", text)
	}
	if !strings.Contains(text, "\"diff\"") {
		t.Fatalf("expected diff in response, got %s", text)
	}
}
