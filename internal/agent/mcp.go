package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"gospy/internal/history"
	"gospy/internal/replay"
	"gospy/internal/session"
)

// Server exposes the agent scope as an MCP server with tools over
// streamable HTTP on a single /mcp endpoint.
type Server struct {
	scope   *Scope
	histVal atomic.Pointer[history.Store]
	fwd     *Forwarder
	mcp     *server.MCPServer
	replay  ReplayAnalyzer // nil in record mode; set for replay
}

// ReplayAnalyzer is implemented by the webui.Server and provides the MCP agent
// with replay event analysis. Nil in record mode.
type ReplayAnalyzer interface {
	ActiveRunID() string
	ListReplayRuns() ([]session.RunSummary, error)
	ReplayEvents(runID string) ([]session.ReplayEvent, error)
	ReplayEventDetail(runID string, seq int) (*session.ReplayEvent, error)
	ReplayCandidates(runID string, seq int, filter replay.CandidateFilter) ([]replay.Candidate, map[string]int, error)
	ReplayDiff(runID string, seq int, entryID string) (*ReplayDiffResult, error)
}

// ReplayCandidateResult is the MCP response for list_replay_candidates.
type ReplayCandidateResult struct {
	Filters replay.CandidateFilter `json:"filters"`
	Total   map[string]int         `json:"total"`
	Entries []replay.Candidate     `json:"entries"`
}

// ReplayDiffResult is the MCP response for replay_diff.
type ReplayDiffResult struct {
	RunID   string             `json:"runId"`
	Seq     int                `json:"id"`
	EntryID string             `json:"entryId"`
	Diff    session.DiffResult `json:"diff"`
}

// SetReplayAnalyzer wires the replay analysis backend. When set, the replay
// tools (list_replay_events, get_replay_event, list_replay_candidates,
// list_replay_filter_values, replay_diff) become available and send_request
// is blocked.
func (s *Server) SetReplayAnalyzer(a ReplayAnalyzer) {
	s.replay = a
}

// requireGate returns an error if the agent gate is off. Every MCP tool
// calls this before doing any work - the gate is the single hard stop
// that controls agent access in both record and replay modes.
func (s *Server) requireGate() error {
	if !s.scope.GateEnabled() {
		return fmt.Errorf("the agent gate is disabled, request the user to enable it")
	}
	return nil
}

// NewServer wires the tools. The caller mounts Handler() at exactly /mcp.
func NewServer(scope *Scope, hist *history.Store, fwd *Forwarder) *Server {
	s := &Server{scope: scope, fwd: fwd}
	s.histVal.Store(hist)
	ms := server.NewMCPServer("gospy-agent", "1.0.0", server.WithToolCapabilities(true), server.WithInputSchemaValidation())

	ms.AddTool(mcp.NewTool("list_entries",
		mcp.WithDescription("Lists the entries currently visible to the agent: the agent gate must be enabled and the agent filter profile applies. Optional criteria narrow the visible set - they combine with AND and can never expand the profile scope. Fields that require exact values (host, referer, process, origin, requestContentType, responseContentType, method) accept comma-separated lists (OR) and should be discovered with list_entry_filter_values; path and text are free text; status is an exact HTTP status code; from/to bound the entry timestamp (inclusive). Pagination is mandatory (max 200 entries per page)."),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Min(0)),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Min(1), mcp.Max(200)),
		mcp.WithString("host", mcp.Description("Exact host match. Discover valid values with list_entry_filter_values('host'). Comma-separated for multiple (OR).")),
		mcp.WithString("path", mcp.Description("Free text: case-insensitive substring over the URL path+query, e.g. '/api/issues/'.")),
		mcp.WithString("method", mcp.Description("Exact HTTP method (GET, POST, ...). Discover valid values with list_entry_filter_values('method'). Comma-separated for multiple (OR).")),
		mcp.WithString("status", mcp.Description("Exact HTTP response status, e.g. '200' or '404'. Comma-separated for multiple (OR).")),
		mcp.WithString("referer", mcp.Description("Exact referer host match. Discover valid values with list_entry_filter_values('referer'). Comma-separated for multiple (OR).")),
		mcp.WithString("process", mcp.Description("Exact client process name match. Discover valid values with list_entry_filter_values('process'). Comma-separated for multiple (OR).")),
		mcp.WithString("origin", mcp.Description("Exact origin match ('browser' or 'agent'). Comma-separated for multiple (OR).")),
		mcp.WithString("requestContentType", mcp.Description("Exact request Content-Type match. Discover valid values with list_entry_filter_values('requestContentType'). Comma-separated for multiple (OR).")),
		mcp.WithString("responseContentType", mcp.Description("Exact response Content-Type match. Discover valid values with list_entry_filter_values('responseContentType'). Comma-separated for multiple (OR).")),
		mcp.WithString("text", mcp.Description("Free text: matches the URL, method, status, client process and entry id.")),
		mcp.WithString("from", mcp.Description("Inclusive lower bound on the entry timestamp. Format: RFC3339 instant ('2026-08-02T14:30:00Z') or local wall-clock ('2026-08-02T14:30', system time zone).")),
		mcp.WithString("to", mcp.Description("Inclusive upper bound on the entry timestamp. Same formats as 'from'.")),
	), s.handleListEntries)

	ms.AddTool(mcp.NewTool("get_entry",
		mcp.WithDescription("Returns a single captured entry in full: headers are sanitized (raw in replay mode for debugging), bodies are included (hex dump for binary). Only entries in the agent-visible set can be read."),
		mcp.WithString("id", mcp.Required()),
	), s.handleGetEntry)

	ms.AddTool(mcp.NewTool("send_request",
		mcp.WithDescription("Sends a request through the gospy proxy using a captured entry as template. The template is REQUIRED and must be in the agent-visible set: the host (scheme+host+port) is fixed to the template's, and sensitive headers (Authorization, Cookie, ...) come exclusively from the captured request and are never exposed. Every omitted field is inherited from the template: method, path, query, headers and body. 'headers' only adds or replaces NON-sensitive headers (overriding a sensitive one is rejected). A 'body' string replaces the body with UTF-8 text (Content-Encoding is dropped); when omitted, the template's raw captured body is replayed byte-for-byte (binary and compressed included). The request is captured as agent-origin traffic and the active rules apply. Response headers are sanitized. Not available in replay mode."),
		mcp.WithString("template", mcp.Required(), mcp.Description("ID of a captured entry in the agent-visible set to use as the request template.")),
		mcp.WithString("method", mcp.Description("HTTP method (uppercased). Omitted: the template's method.")),
		mcp.WithString("path", mcp.Description("Request path, e.g. /api/issues/search. Omitted: the template's path.")),
		mcp.WithString("query", mcp.Description("Query parameters as a URL query string, e.g. `a=1&b=2`. Omitted or empty: the template's query is inherited.")),
		mcp.WithString("headers", mcp.Description("Headers to add or replace as a JSON object, e.g. {\"X-Trace\": \"abc\"}. Sensitive headers cannot be overridden and always come from the template. Omitted: no changes.")),
		mcp.WithString("body", mcp.Description("Request body as UTF-8 text. Omitted: the template's raw captured body is replayed byte-for-byte.")),
	), s.handleSendRequest)

	ms.AddTool(mcp.NewTool("list_visible_hosts",
		mcp.WithDescription("Lists the distinct hosts currently in the agent-visible set (your scope: what you can see), sorted."),
	), s.handleListVisibleHosts)

	ms.AddTool(mcp.NewTool("list_entry_filter_values",
		mcp.WithDescription("Lists the exact values available for a list-type filter field (host, referer, process, origin, requestContentType, responseContentType, method), scoped to the agent's visible set. Use the returned values verbatim in list_entries - these fields require exact matches, not free text. This answers 'what can I filter by', while list_visible_hosts answers 'what is my scope'."),
		mcp.WithString("type", mcp.Required(), mcp.Description("Filter field to enumerate: host, referer, process, origin, requestContentType, responseContentType or method.")),
	), s.handleListEntryFilterValues)

	ms.AddTool(mcp.NewTool("list_replay_runs",
		mcp.WithDescription("Lists all replay runs for the session, newest first. Each run includes hit/miss/exhausted counts, duration, and an active flag indicating whether it is the currently running replay."),
	), s.handleListReplayRuns)

	ms.AddTool(mcp.NewTool("list_replay_events",
		mcp.WithDescription("Lists replay events for a run: each event is one request as it passed through the replay proxy, with the match outcome (hit/miss/exhausted). If runId is omitted, uses the active (live) run - returns an error if no run is active. Pagination is mandatory (max 200 events per page). Use list_replay_filter_values to discover valid filter values."),
		mcp.WithString("runId", mcp.Description("Replay run ID (from list_replay_runs). Omitted: the active run.")),
		mcp.WithString("result", mcp.Description("Filter by match result: 'hit', 'miss', or 'exhausted'. Use list_replay_filter_values('result') to discover values.")),
		mcp.WithString("host", mcp.Description("Filter by host in the event URL. Use list_replay_filter_values('host') to discover values.")),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Min(0)),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Min(1), mcp.Max(200)),
	), s.handleListReplayEvents)

	ms.AddTool(mcp.NewTool("get_replay_event",
		mcp.WithDescription("Returns the full detail of a replay event by its event ID within a run. If runId is omitted, uses the active run. Includes the incoming request metadata, match result, and for hits the served response from the matched recorded entry."),
		mcp.WithNumber("eventId", mcp.Required(), mcp.Description("Event ID (from list_replay_events). Identifies the replay event, not a recorded entry.")),
		mcp.WithString("runId", mcp.Description("Replay run ID (from list_replay_runs). Omitted: the active run.")),
	), s.handleGetReplayEvent)

	ms.AddTool(mcp.NewTool("list_replay_candidates",
		mcp.WithDescription("Lists candidate recorded entries of a replay run for an event, selected by optional per-entry attribute filters. Each entry carries its tag (served = this HIT served it, consumed = served by an earlier event, pending = still available) and potentialMatch (true when it shares the event's host+path and is thus a viable comparison). Both filters combine with AND and may be combined; leaving both unset returns the full run universe. To reproduce the WebUI's views: matching sends potentialMatch=true (entries sharing host+path, ranked by diff count), pending sends tag=pending (the unconsumed queue). The response echoes the filters applied, per-view totals (matching/pending counts over the whole universe) and the selected entries."),
		mcp.WithNumber("eventId", mcp.Required(), mcp.Description("Event ID of the replay event to find candidates for (from list_replay_events).")),
		mcp.WithBoolean("potentialMatch", mcp.Description("Optional. true (matching view: entries sharing host+path, ranked) or false (everything else). Omit for either.")),
		mcp.WithString("tag", mcp.Description("Optional. Entry state to keep: 'served', 'consumed' or 'pending'. Omit for all states.")),
		mcp.WithString("runId", mcp.Description("Replay run ID (from list_replay_runs). Omitted: the active run.")),
	), s.handleListReplayCandidates)

	ms.AddTool(mcp.NewTool("replay_diff",
		mcp.WithDescription("Shows the detailed diff between a replay event's incoming request and a specific recorded entry. Use list_replay_candidates to find candidate entry IDs, then this tool to inspect the exact parameter differences."),
		mcp.WithNumber("eventId", mcp.Required(), mcp.Description("Event ID of the replay event (from list_replay_events).")),
		mcp.WithString("entryId", mcp.Required(), mcp.Description("ID of the recorded entry to diff against (from list_replay_candidates).")),
		mcp.WithString("runId", mcp.Description("Replay run ID (from list_replay_runs). Omitted: the active run.")),
	), s.handleReplayDiff)

	ms.AddTool(mcp.NewTool("list_replay_filter_values",
		mcp.WithDescription("Lists the distinct values available for a replay event filter field (result, host, method), scoped to a run. Use the returned values verbatim in list_replay_events. If runId is omitted, uses the active run."),
		mcp.WithString("type", mcp.Required(), mcp.Description("Filter field to enumerate: 'result' (hit/miss/exhausted), 'host', or 'method'.")),
		mcp.WithString("runId", mcp.Description("Replay run ID (from list_replay_runs). Omitted: the active run.")),
	), s.handleListReplayFilterValues)

	s.mcp = ms
	return s
}

// hist returns the current history store. Swapped atomically on session
// rotation so get_entry and send_request templates target the active session.
func (s *Server) hist() *history.Store {
	return s.histVal.Load()
}

// SetHistoryStore rotates the session store the MCP reads, keeping the scope
// in sync so list_entries and get_entry serve the same active session.
func (s *Server) SetHistoryStore(h *history.Store) {
	s.histVal.Store(h)
	s.scope.SetHistoryStore(h)
}

// Handler returns the streamable HTTP handler; mount it at exactly /mcp.
//
// The default session manager is used: it stays stateless (IDs are generated
// but never validated for existence) while still returning a Mcp-Session-Id on
// initialize, which session-aware clients like MCP Inspector require.
func (s *Server) Handler() http.Handler {
	return server.NewStreamableHTTPServer(s.mcp, server.WithEndpointPath("/mcp"))
}

func (s *Server) handleListEntries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	query := history.Filters{
		Host:                parseListArg(req.GetString("host", "")),
		Path:                parseListArg(req.GetString("path", "")),
		Method:              parseListArg(req.GetString("method", "")),
		Status:              parseListArg(req.GetString("status", "")),
		Referer:             parseListArg(req.GetString("referer", "")),
		Process:             parseListArg(req.GetString("process", "")),
		Origin:              parseListArg(req.GetString("origin", "")),
		RequestContentType:  parseListArg(req.GetString("requestContentType", "")),
		ResponseContentType: parseListArg(req.GetString("responseContentType", "")),
		Text:                req.GetString("text", ""),
		From:                req.GetString("from", ""),
		To:                  req.GetString("to", ""),
	}
	for _, v := range query.Status {
		if _, err := strconv.Atoi(v); err != nil {
			return mcp.NewToolResultErrorf("status values must be integer HTTP status codes, got %q", v), nil
		}
	}
	for name, v := range map[string]string{"from": query.From, "to": query.To} {
		if v != "" {
			if _, err := history.ParseFilterTime(v); err != nil {
				return mcp.NewToolResultErrorf("%s must be an RFC3339 instant or a local wall-clock 'YYYY-MM-DDTHH:MM', got %q", name, v), nil
			}
		}
	}
	page := s.scope.ListEntries(query, req.GetInt("offset", 0), req.GetInt("limit", 50))
	return mcp.NewToolResultJSON(page)
}

// parseListArg splits a comma-separated MCP argument into the engine's list of
// exact-match values (within-field OR), mirroring the UI multi-select. Empty
// input yields nil so the field stays unconstrained.
func parseListArg(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var filterValueTypes = map[string]bool{
	"host": true, "referer": true, "process": true, "origin": true,
	"requestContentType": true, "responseContentType": true, "method": true,
}

func (s *Server) handleListEntryFilterValues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	typ := req.GetString("type", "")
	if !filterValueTypes[typ] {
		return mcp.NewToolResultErrorf("unknown filter type %q; valid: host, referer, process, origin, requestContentType, responseContentType, method", typ), nil
	}
	values := s.scope.FilterValues(typ)
	if values == nil {
		values = []history.OptionCount{}
	}
	return mcp.NewToolResultJSON(map[string]interface{}{
		"type":   typ,
		"values": values,
	})
}

func (s *Server) handleGetEntry(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id := req.GetString("id", "")
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	if s.hist() == nil {
		return mcp.NewToolResultError("no session recording active"), nil
	}
	if !s.scope.IsVisible(id) {
		return mcp.NewToolResultErrorf("entry %s is not in the agent-visible set", id), nil
	}
	entry, err := s.hist().Get(id)
	if err != nil {
		return mcp.NewToolResultErrorf("entry %s not found", id), nil
	}
	return mcp.NewToolResultJSON(EntryDetail(s.hist().Dir(), entry, s.replay != nil))
}

func (s *Server) handleSendRequest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.replay != nil {
		return mcp.NewToolResultError("send_request is not available in replay mode"), nil
	}
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	spec, err := parseRequestSpec(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	built, err := s.scope.buildTemplateRequest(spec)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, callID, err := s.fwd.Do(ctx, built.method, built.url, built.headers, built.body)
	if err != nil {
		return mcp.NewToolResultErrorf("forward failed: %v", err), nil
	}
	resp.Request = ResolvedRequest{Method: built.method, URL: built.url, BodySource: built.bodySource}
	if st := s.hist(); st != nil {
		if le, err := st.GetByAgentCallID(callID); err == nil {
			resp.EntryID = le.ID
		}
	}
	return mcp.NewToolResultJSON(resp)
}

func (s *Server) handleListVisibleHosts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	hosts := s.scope.VisibleHosts()
	return mcp.NewToolResultJSON(map[string]any{"hosts": hosts, "count": len(hosts)})
}

func (s *Server) handleListReplayRuns(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.replay == nil {
		return mcp.NewToolResultError("replay tools are only available in replay mode"), nil
	}
	runs, err := s.replay.ListReplayRuns()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if runs == nil {
		runs = []session.RunSummary{}
	}
	return mcp.NewToolResultJSON(map[string]any{"runs": runs, "count": len(runs)})
}

func (s *Server) handleListReplayEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.replay == nil {
		return mcp.NewToolResultError("replay tools are only available in replay mode"), nil
	}
	runID, err := s.resolveRunID(req.GetString("runId", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	events, err := s.replay.ReplayEvents(runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Apply filters.
	resultFilter := strings.ToLower(strings.TrimSpace(req.GetString("result", "")))
	hostFilter := strings.ToLower(strings.TrimSpace(req.GetString("host", "")))
	var filtered []session.ReplayEvent
	for _, ev := range events {
		if resultFilter != "" && ev.Result != resultFilter {
			continue
		}
		if hostFilter != "" {
			u, err := url.Parse(ev.URL)
			if err != nil || !strings.EqualFold(u.Hostname(), hostFilter) {
				continue
			}
		}
		filtered = append(filtered, ev)
	}

	total := len(filtered)
	offset := int(req.GetFloat("offset", 0))
	limit := int(req.GetFloat("limit", 50))
	if limit < 1 {
		limit = 1
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := filtered[offset:end]

	type eventSummary struct {
		ID        int       `json:"id"`
		Result    string    `json:"result"`
		Method    string    `json:"method"`
		URL       string    `json:"url"`
		Status    int       `json:"status"`
		Exhausted bool      `json:"exhausted"`
		Ts        time.Time `json:"ts"`
		EntryID   string    `json:"entryId,omitempty"`
	}
	out := make([]eventSummary, len(page))
	for i, ev := range page {
		out[i] = eventSummary{
			ID: ev.Seq, Result: ev.Result, Method: ev.Method, URL: ev.URL,
			Status: ev.Status, Exhausted: ev.Exhausted, Ts: ev.Timestamp, EntryID: ev.EntryID,
		}
	}
	return mcp.NewToolResultJSON(map[string]any{
		"runId":   runID,
		"events":  out,
		"total":   total,
		"hasMore": offset+limit < total,
	})
}

func (s *Server) handleGetReplayEvent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.replay == nil {
		return mcp.NewToolResultError("replay tools are only available in replay mode"), nil
	}
	seq := req.GetInt("eventId", 0)
	if seq <= 0 {
		return mcp.NewToolResultError("eventId is required and must be > 0"), nil
	}
	runID, err := s.resolveRunID(req.GetString("runId", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ev, err := s.replay.ReplayEventDetail(runID, seq)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result := map[string]any{
		"id":        ev.Seq,
		"result":    ev.Result,
		"method":    ev.Method,
		"url":       ev.URL,
		"status":    ev.Status,
		"exhausted": ev.Exhausted,
		"ts":        ev.Timestamp,
	}
	if ev.EntryID != "" {
		result["entryId"] = ev.EntryID
	}
	if ev.MatchedURL != "" {
		result["matchedUrl"] = ev.MatchedURL
	}
	if ev.ServedResponse != nil {
		result["servedResponse"] = ev.ServedResponse
	}
	return mcp.NewToolResultJSON(result)
}

func (s *Server) handleListReplayCandidates(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.replay == nil {
		return mcp.NewToolResultError("replay tools are only available in replay mode"), nil
	}
	seq := req.GetInt("eventId", 0)
	if seq <= 0 {
		return mcp.NewToolResultError("eventId is required and must be > 0"), nil
	}
	var filter replay.CandidateFilter
	if args := req.GetArguments(); args != nil {
		if raw, ok := args["potentialMatch"]; ok {
			b, _ := raw.(bool)
			filter.PotentialMatch = &b
		}
	}
	if tag := req.GetString("tag", ""); tag != "" {
		filter.Tag = tag
	}
	runID, err := s.resolveRunID(req.GetString("runId", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	pool, total, err := s.replay.ReplayCandidates(runID, seq, filter)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultJSON(ReplayCandidateResult{
		Filters: filter,
		Total:   total,
		Entries: pool,
	})
}

func (s *Server) handleReplayDiff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.replay == nil {
		return mcp.NewToolResultError("replay tools are only available in replay mode"), nil
	}
	seq := req.GetInt("eventId", 0)
	if seq <= 0 {
		return mcp.NewToolResultError("eventId is required and must be > 0"), nil
	}
	entryID := req.GetString("entryId", "")
	if entryID == "" {
		return mcp.NewToolResultError("entryId is required"), nil
	}
	runID, err := s.resolveRunID(req.GetString("runId", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	diff, err := s.replay.ReplayDiff(runID, seq, entryID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultJSON(diff)
}

var replayFilterValueTypes = map[string]bool{"result": true, "host": true, "method": true}

func (s *Server) handleListReplayFilterValues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireGate(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.replay == nil {
		return mcp.NewToolResultError("replay tools are only available in replay mode"), nil
	}
	typ := req.GetString("type", "")
	if !replayFilterValueTypes[typ] {
		return mcp.NewToolResultErrorf("unknown filter type %q; valid: result, host, method", typ), nil
	}
	runID, err := s.resolveRunID(req.GetString("runId", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	events, err := s.replay.ReplayEvents(runID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	type optionCount struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	}
	counts := map[string]int{}
	for _, ev := range events {
		var val string
		switch typ {
		case "result":
			val = ev.Result
		case "method":
			val = ev.Method
		case "host":
			u, err := url.Parse(ev.URL)
			if err != nil {
				continue
			}
			val = u.Hostname()
		}
		if val != "" {
			counts[val]++
		}
	}
	out := make([]optionCount, 0, len(counts))
	for v, c := range counts {
		out = append(out, optionCount{Value: v, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return mcp.NewToolResultJSON(map[string]any{
		"type":   typ,
		"values": out,
	})
}

// resolveRunID resolves an optional runId parameter: empty → active run, non-empty → passed through.
func (s *Server) resolveRunID(runID string) (string, error) {
	if runID != "" {
		return runID, nil
	}
	active := s.replay.ActiveRunID()
	if active == "" {
		return "", fmt.Errorf("no active replay run - specify runId")
	}
	return active, nil
}

func parseRequestSpec(req mcp.CallToolRequest) (RequestSpec, error) {
	spec := RequestSpec{
		Template: req.GetString("template", ""),
		Method:   strings.ToUpper(req.GetString("method", "")),
		Path:     req.GetString("path", ""),
		Body:     req.GetString("body", ""),
	}
	if spec.Template == "" {
		return spec, fmt.Errorf("template is required: reference a captured entry in the agent-visible set")
	}
	var err error
	if spec.Headers, err = parseHeadersArg(req.GetArguments()); err != nil {
		return spec, fmt.Errorf("headers: %w", err)
	}
	if spec.Query, err = parseQueryArg(req.GetArguments()); err != nil {
		return spec, fmt.Errorf("query: %w", err)
	}
	return spec, nil
}

// parseQueryArg decodes the send_request query argument: a URL query string
// such as "a=1&b=2". An absent or empty value yields nil so the template's
// query is inherited unchanged.
func parseQueryArg(args map[string]any) (map[string][]string, error) {
	raw, ok := args["query"]
	if !ok {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("must be a query string")
	}
	if s == "" {
		return nil, nil
	}
	return url.ParseQuery(s)
}

// parseHeadersArg decodes the send_request headers argument: a JSON object with
// string or string-array values. An absent or empty value yields nil so no
// headers are overridden.
func parseHeadersArg(args map[string]any) (map[string][]string, error) {
	raw, ok := args["headers"]
	if !ok {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("must be a JSON string")
	}
	if s == "" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		var sval string
		if err := json.Unmarshal(v, &sval); err == nil {
			out[k] = []string{sval}
			continue
		}
		var aval []string
		if err := json.Unmarshal(v, &aval); err != nil {
			return nil, fmt.Errorf("value for %q must be a string or string array", k)
		}
		out[k] = aval
	}
	return out, nil
}
