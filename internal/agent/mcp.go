package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"gospy/internal/history"
)

// Server exposes the agent scope as an MCP server with 4 synchronous tools over
// streamable HTTP on a single /mcp endpoint.
type Server struct {
	scope *Scope
	hist  HistoryStore
	fwd   *Forwarder
	mcp   *server.MCPServer
}

// NewServer wires the tools. The caller mounts Handler() at exactly /mcp.
func NewServer(scope *Scope, hist HistoryStore, fwd *Forwarder) *Server {
	s := &Server{scope: scope, hist: hist, fwd: fwd}
	ms := server.NewMCPServer("gospy-agent", "1.0.0", server.WithToolCapabilities(true), server.WithInputSchemaValidation())

	ms.AddTool(mcp.NewTool("list_entries",
		mcp.WithDescription("Lists the entries currently visible to the agent: the agent gate must be enabled and the agent filter profile applies. Optional criteria narrow the visible set - they combine with AND and can never expand the profile scope. Fields that require exact values (host, referer, process, origin, requestContentType, responseContentType, method) accept comma-separated lists (OR) and should be discovered with list_filter_values; path and text are free text; status is an exact HTTP status code; from/to bound the entry timestamp (inclusive). Pagination is mandatory (max 200 entries per page)."),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Min(0)),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Min(1), mcp.Max(200)),
		mcp.WithString("host", mcp.Description("Exact host match. Discover valid values with list_filter_values('host'). Comma-separated for multiple (OR).")),
		mcp.WithString("path", mcp.Description("Free text: case-insensitive substring over the URL path+query, e.g. '/api/issues/'.")),
		mcp.WithString("method", mcp.Description("Exact HTTP method (GET, POST, ...). Discover valid values with list_filter_values('method'). Comma-separated for multiple (OR).")),
		mcp.WithString("status", mcp.Description("Exact HTTP response status, e.g. '200' or '404'. Comma-separated for multiple (OR).")),
		mcp.WithString("referer", mcp.Description("Exact referer host match. Discover valid values with list_filter_values('referer'). Comma-separated for multiple (OR).")),
		mcp.WithString("process", mcp.Description("Exact client process name match. Discover valid values with list_filter_values('process'). Comma-separated for multiple (OR).")),
		mcp.WithString("origin", mcp.Description("Exact origin match ('browser' or 'agent'). Comma-separated for multiple (OR).")),
		mcp.WithString("requestContentType", mcp.Description("Exact request Content-Type match. Discover valid values with list_filter_values('requestContentType'). Comma-separated for multiple (OR).")),
		mcp.WithString("responseContentType", mcp.Description("Exact response Content-Type match. Discover valid values with list_filter_values('responseContentType'). Comma-separated for multiple (OR).")),
		mcp.WithString("text", mcp.Description("Free text: matches the URL, method, status, client process and entry id.")),
		mcp.WithString("from", mcp.Description("Inclusive lower bound on the entry timestamp. Format: RFC3339 instant ('2026-08-02T14:30:00Z') or local wall-clock ('2026-08-02T14:30', system time zone).")),
		mcp.WithString("to", mcp.Description("Inclusive upper bound on the entry timestamp. Same formats as 'from'.")),
	), s.handleListEntries)

	ms.AddTool(mcp.NewTool("get_entry",
		mcp.WithDescription("Returns a single captured entry in full: headers are sanitized, bodies are included (hex dump for binary). Only entries in the agent-visible set can be read."),
		mcp.WithString("id", mcp.Required()),
	), s.handleGetEntry)

	ms.AddTool(mcp.NewTool("send_request",
		mcp.WithDescription("Sends a request through the gospy proxy using a captured entry as template. The template is REQUIRED and must be in the agent-visible set: the host (scheme+host+port) is fixed to the template's, and sensitive headers (Authorization, Cookie, ...) come exclusively from the captured request and are never exposed. Every omitted field is inherited from the template: method, path, query, headers and body. 'headers' only adds or replaces NON-sensitive headers (overriding a sensitive one is rejected). A 'body' string replaces the body with UTF-8 text (Content-Encoding is dropped); when omitted, the template's raw captured body is replayed byte-for-byte (binary and compressed included). The request is captured as agent-origin traffic and the active rules apply. Response headers are sanitized."),
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

	ms.AddTool(mcp.NewTool("list_filter_values",
		mcp.WithDescription("Lists the exact values available for a list-type filter field (host, referer, process, origin, requestContentType, responseContentType, method), scoped to the agent's visible set. Use the returned values verbatim in list_entries - these fields require exact matches, not free text. This answers 'what can I filter by', while list_visible_hosts answers 'what is my scope'."),
		mcp.WithString("type", mcp.Required(), mcp.Description("Filter field to enumerate: host, referer, process, origin, requestContentType, responseContentType or method.")),
	), s.handleListFilterValues)

	s.mcp = ms
	return s
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

func (s *Server) handleListFilterValues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	id := req.GetString("id", "")
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	if !s.scope.IsVisible(id) {
		return mcp.NewToolResultErrorf("entry %s is not in the agent-visible set", id), nil
	}
	entry, err := s.hist.Get(id)
	if err != nil {
		return mcp.NewToolResultErrorf("entry %s not found", id), nil
	}
	return mcp.NewToolResultJSON(EntryDetail(s.hist.Dir(), entry))
}

func (s *Server) handleSendRequest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.scope.GateEnabled() {
		return mcp.NewToolResultError("the agent gate is disabled"), nil
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
	if le, err := s.hist.GetByAgentCallID(callID); err == nil {
		resp.EntryID = le.ID
	}
	return mcp.NewToolResultJSON(resp)
}

func (s *Server) handleListVisibleHosts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	hosts := s.scope.VisibleHosts()
	return mcp.NewToolResultJSON(map[string]any{"hosts": hosts, "count": len(hosts)})
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
